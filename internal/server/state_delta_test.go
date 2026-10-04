package server

import (
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestWindowDelta(t *testing.T) {
	// Window keeps the last 3 lines of a 10-line stream.
	win := []string{"l8", "l9", "l10"}
	total := 10

	// Client up to date.
	out, trunc := windowDelta(win, total, 10)
	if out != nil || trunc {
		t.Errorf("up to date: got (%v, %v), want (nil, false)", out, trunc)
	}
	// Window base is 7 (total 10, len 3): a client holding 9 lines needs
	// only the last entry, one holding 8 needs the last two, and one at the
	// base needs the whole window.
	out, trunc = windowDelta(win, total, 9)
	if len(out) != 1 || out[0] != "l10" || trunc {
		t.Errorf("from=9: got (%v, %v), want ([l10], false)", out, trunc)
	}
	out, trunc = windowDelta(win, total, 8)
	if len(out) != 2 || out[0] != "l9" || out[1] != "l10" || trunc {
		t.Errorf("from=8: got (%v, %v), want ([l9 l10], false)", out, trunc)
	}
	out, trunc = windowDelta(win, total, 7)
	if len(out) != 3 || out[0] != "l8" || trunc {
		t.Errorf("from=7: got (%v, %v), want full window, false", out, trunc)
	}
	// Client missed entries that fell out of the window: full replace.
	out, trunc = windowDelta(win, total, 2)
	if len(out) != 3 || !trunc {
		t.Errorf("from=2: got (%v, %v), want full window + trunc", out, trunc)
	}
	// Counters reset (new run): client's position is ahead of total.
	out, trunc = windowDelta(win, 2, 40)
	if len(out) != 3 || !trunc {
		t.Errorf("reset: got (%v, %v), want full window + trunc", out, trunc)
	}
	// Empty stream with a stale client.
	out, trunc = windowDelta(nil, 0, 40)
	if out == nil || !trunc {
		t.Errorf("empty reset: got (%v, %v), want ([] non-nil, true)", out, trunc)
	}
}

func decodeSnap(t *testing.T, target string) Snap {
	t.Helper()
	req := httptest.NewRequest(stdhttp.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	HandleState(rec, req)
	var s Snap
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode %s: %v", target, err)
	}
	return s
}

func TestHandleStateDeltaAndLite(t *testing.T) {
	mu.Lock()
	logs, hits = []string{"l1", "l2"}, []string{"h1"}
	logSeq, hitSeq = 2, 1
	mu.Unlock()
	defer func() {
		mu.Lock()
		logs, hits = nil, nil
		logSeq, hitSeq = 0, 0
		mu.Unlock()
	}()

	// No from params: full payload (compatibility with the boot check).
	full := decodeSnap(t, "/api/state")
	if len(full.Logs) != 2 || full.LogTotal != 2 || len(full.Hits) != 1 {
		t.Fatalf("full payload: logs=%v total=%d hits=%v", full.Logs, full.LogTotal, full.Hits)
	}

	// Delta: only unseen lines, no full window copied.
	delta := decodeSnap(t, "/api/state?log_from=1&hit_from=1")
	if len(delta.NewLogs) != 1 || delta.NewLogs[0] != "l2" {
		t.Fatalf("delta logs: %v", delta.NewLogs)
	}
	if len(delta.NewHits) != 0 {
		t.Fatalf("delta hits: %v", delta.NewHits)
	}
	if delta.Logs != nil || delta.Hits != nil {
		t.Fatalf("delta response should omit full windows: %v %v", delta.Logs, delta.Hits)
	}

	// lite: counters only, nothing else copied.
	lite := decodeSnap(t, "/api/state?lite=1&log_from=0&hit_from=0")
	if lite.Logs != nil || lite.Hits != nil || lite.NewLogs != nil || lite.NewHits != nil {
		t.Fatalf("lite response leaked windows: %v %v %v %v", lite.Logs, lite.Hits, lite.NewLogs, lite.NewHits)
	}
	if lite.LogTotal != 2 || lite.HitTotal != 1 {
		t.Fatalf("lite totals: log=%d hit=%d", lite.LogTotal, lite.HitTotal)
	}
}
