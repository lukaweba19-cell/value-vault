//go:build cgo

package main

// dart:ffi bridge: the Flutter app loads this module as a shared library and
// calls the engine in-process — no sockets, no HTTP, no second process.
//
//   - SoloInit   one-time bootstrap (same work main() does in exe mode)
//   - SoloCall   run one route to completion, return {"s":status,"b":body}
//   - SoloStream run a streaming route (SSE / NDJSON / events); chunks are
//     pushed to the Dart callback until it is called with data==NULL (done)
//   - SoloCancel cancel a running stream by id
//   - SoloFree   free a string returned by SoloCall

/*
#include <stdlib.h>
typedef void (*solo_cb)(long long id, const char* data, int len);
static void solo_emit(solo_cb cb, long long id, const char* data, int len) {
    if (cb) cb(id, data, len);
}
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	stdhttp "net/http"
	"runtime/debug"
	"strings"
	"sync"
	"unsafe"
	"xbox-checker/internal/core"
	"xbox-checker/internal/r2store"
)

var (
	ffiMux       = newFFIMux()
	streamMu     sync.Mutex
	activeStream = map[int64]context.CancelFunc{}
)

/* ---------------- request runner ---------------- */

type bridgeBase struct {
	header stdhttp.Header
	code   int
}

func (b *bridgeBase) Header() stdhttp.Header {
	if b.header == nil {
		b.header = stdhttp.Header{}
	}
	return b.header
}

func (b *bridgeBase) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}

// bufferWriter collects the whole response (SoloCall).
type bufferWriter struct {
	bridgeBase
	buf bytes.Buffer
}

func (w *bufferWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.buf.Write(p)
}

// streamWriter forwards every write to the Dart callback as it happens.
type streamWriter struct {
	bridgeBase
	ctx context.Context
	id  int64
	cb  C.solo_cb
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.code == 0 {
		w.code = 200
	}
	s := string(p)
	cs := C.CString(s)
	C.solo_emit(w.cb, C.longlong(w.id), cs, C.int(len(s)))
	C.free(unsafe.Pointer(cs))
	return len(p), nil
}

func (w *streamWriter) Flush() {}

func newBridgeRequest(method, path, body string) (*stdhttp.Request, error) {
	req, err := stdhttp.NewRequest(method, "http://solo.local"+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func serveSafe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			bootLogf("panic serving %s: %v\n%s", r.URL.Path, rec, debug.Stack())
			if rb, ok := w.(*bufferWriter); ok {
				rb.code = 500
				rb.buf.WriteString(`{"error":"internal error"}`)
			} else if sw, ok := w.(*streamWriter); ok && sw.ctx.Err() == nil {
				// A streaming route must always end with a terminal frame,
				// otherwise the UI reports a misleading generic error
				// ("connection ended unexpectedly") and the real cause is lost.
				line, _ := json.Marshal(map[string]any{
					"type": "done", "ok": false, "error": "internal error — see solo-launch.log",
				})
				_, _ = w.Write(append(line, '\n'))
			}
		}
	}()
	ffiMux.ServeHTTP(w, r)
}

/* ---------------- exports ---------------- */

// SoloInit runs the one-time backend bootstrap (quota pull, update cleanup).
//
//export SoloInit
func SoloInit() {
	r2store.AfterUpdateCleanup()
	go core.PullQuotaFromCloud()
}

// SoloCall runs one route to completion and returns {"s":status,"b":body}.
// The result must be released with SoloFree.
//
//export SoloCall
func SoloCall(method, path, body *C.char) *C.char {
	m := C.GoString(method)
	p := C.GoString(path)
	b := C.GoString(body)
	out := func(status int, payload string) *C.char {
		raw, _ := json.Marshal(map[string]any{"s": status, "b": payload})
		return C.CString(string(raw))
	}
	req, err := newBridgeRequest(m, p, b)
	if err != nil {
		return out(400, err.Error())
	}
	w := &bufferWriter{}
	serveSafe(w, req)
	if w.code == 0 {
		w.code = 200
	}
	return out(w.code, w.buf.String())
}

// SoloStream runs a streaming route in the background; chunks arrive through
// cb until cb is invoked with data == NULL (stream finished or cancelled).
//
//export SoloStream
func SoloStream(id C.longlong, method, path, body *C.char, cb C.solo_cb) {
	m := C.GoString(method)
	p := C.GoString(path)
	b := C.GoString(body)
	sid := int64(id)

	ctx, cancel := context.WithCancel(context.Background())
	streamMu.Lock()
	activeStream[sid] = cancel
	streamMu.Unlock()

	go func() {
		defer func() {
			_ = recover()
			streamMu.Lock()
			delete(activeStream, sid)
			streamMu.Unlock()
			cancel()
			C.solo_emit(cb, C.longlong(sid), nil, 0)
		}()
		req, err := newBridgeRequest(m, p, b)
		if err != nil {
			return
		}
		req = req.WithContext(ctx)
		w := &streamWriter{ctx: ctx, id: sid, cb: cb}
		serveSafe(w, req)
	}()
}

// SoloCancel stops a running stream.
//
//export SoloCancel
func SoloCancel(id C.longlong) {
	streamMu.Lock()
	if cancel, ok := activeStream[int64(id)]; ok {
		cancel()
	}
	streamMu.Unlock()
}

// SoloFree releases a string returned by SoloCall.
//
//export SoloFree
func SoloFree(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}
