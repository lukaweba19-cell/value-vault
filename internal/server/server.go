// Package server is the backend of SOLO Checker: run-state snapshots, the
// checker engine glue and the HTTP API the Flutter client (client/) talks to.
// It contains no UI at all — the interface is the Flutter app.
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	stdhttp "net/http"

	"xbox-checker/internal/checker"
	"xbox-checker/internal/core"
	"xbox-checker/internal/machine"
	"xbox-checker/internal/proxy"
)

type (
	Config        = checker.Config
	Session       = checker.Session
	ProgressEvent = checker.ProgressEvent
)

type SessionCard struct {
	File     string `json:"file"`
	Combo    string `json:"combo"`
	Proxy    string `json:"proxy"`
	Keywords string `json:"keywords"`
	Workers  int    `json:"workers"`
	Retries  int    `json:"retries"`
	Updated  string `json:"updated"`
	Started  string `json:"started"`
}

type Snap struct {
	Running bool     `json:"running"`
	Paused  bool     `json:"paused"`
	Status  string   `json:"status"`
	Valid   int      `json:"valid"`
	Invalid int      `json:"invalid"`
	Retries int      `json:"retries"`
	Checked int      `json:"checked"`
	Total   int      `json:"total"`
	Daily   int      `json:"daily"`
	Limit   int      `json:"limit"`
	UID     string   `json:"uid"`
	Logs    []string `json:"logs,omitempty"`
	Hits    []string `json:"hits,omitempty"`
	// Delta fields: with ?log_from=&hit_from= the server sends only the
	// lines the client has not seen yet, so the log/hit slices are copied
	// once when they change instead of on every poll.
	LogTotal int           `json:"log_total"`
	HitTotal int           `json:"hit_total"`
	NewLogs  []string      `json:"new_logs,omitempty"`
	NewHits  []string      `json:"new_hits,omitempty"`
	LogReset bool          `json:"log_reset,omitempty"`
	HitReset bool          `json:"hit_reset,omitempty"`
	Free     int           `json:"free"`
	Private  int           `json:"private"`
	Combo    string        `json:"combo"`
	Proxy    string        `json:"proxy"`
	Keywords string        `json:"keywords"`
	Workers  string        `json:"workers"`
	Sessions []SessionCard `json:"sessions"`
	Elapsed  string        `json:"elapsed"`
	ETA      string        `json:"eta"`
	RateCPM  float64       `json:"rate_cpm"`
}

var (
	mu           sync.Mutex
	running      int32
	paused       int32
	status       = "Ready"
	valid        int
	invalid      int
	retries      int
	checked      int
	total        int
	logs         []string
	hits         []string
	free         int
	private      int
	engineAlive  int32
	stopFlag     int32
	startedAt    time.Time
	pauseStarted time.Time
	pausedAccum  time.Duration
	// rolling samples of checked counts for smoother ETA (unix ms, checked)
	rateSamples [][2]int64
	// Absolute counts of log/hit lines ever produced this run. The logs/hits
	// windows keep only the last 80; these sequences let a client ask for
	// just the lines after the ones it already holds.
	logSeq int
	hitSeq int
	// runGen increments on every start (under mu). A consumer goroutine from
	// an older run checks it under the same lock and discards its events, so
	// a stale "Done" from a previous run can never clobber the current run's
	// status or running flag.
	runGen int64
	// endedAt freezes the elapsed clock once a run finishes or is stopped.
	endedAt time.Time
)

func formatDurationShort(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d.Seconds())
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func activeElapsed() time.Duration {
	if startedAt.IsZero() {
		return 0
	}
	// Once the run is over, stop the clock: a finished run used to keep
	// counting elapsed seconds, which made the UI look like it was still
	// running.
	end := time.Now()
	if !endedAt.IsZero() && endedAt.Before(end) {
		end = endedAt
	}
	elapsed := end.Sub(startedAt) - pausedAccum
	if atomic.LoadInt32(&paused) == 1 && !pauseStarted.IsZero() {
		elapsed -= end.Sub(pauseStarted)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed
}

func computeETA(checked, total int) (elapsedStr, etaStr string, rateCPM float64) {
	elapsed := activeElapsed()
	elapsedStr = formatDurationShort(elapsed)
	if atomic.LoadInt32(&running) != 1 && checked == 0 {
		return elapsedStr, "—", 0
	}
	// Prefer recent window (~last 30s of samples) for adaptive ETA
	nowMs := time.Now().UnixMilli()
	samples := rateSamples
	var ratePerSec float64
	if len(samples) >= 2 {
		// drop samples older than 45s
		cut := nowMs - 45000
		i0 := 0
		for i0 < len(samples) && samples[i0][0] < cut {
			i0++
		}
		if i0 > 0 && i0 < len(samples) {
			samples = samples[i0:]
		}
		if len(samples) >= 2 {
			dt := float64(samples[len(samples)-1][0]-samples[0][0]) / 1000.0
			dc := float64(samples[len(samples)-1][1] - samples[0][1])
			if dt > 0.5 && dc >= 0 {
				ratePerSec = dc / dt
			}
		}
	}
	// Fallback: overall session rate (exclude pause)
	if ratePerSec <= 0 && elapsed.Seconds() >= 2 && checked > 0 {
		ratePerSec = float64(checked) / elapsed.Seconds()
	}
	rateCPM = ratePerSec * 60
	// Not running anymore: freeze the ETA ("done" or "—") instead of
	// keeping a live countdown ticking from stale rate samples.
	if atomic.LoadInt32(&running) != 1 {
		if total > 0 && checked >= total {
			return elapsedStr, "done", rateCPM
		}
		return elapsedStr, "—", rateCPM
	}
	remain := total - checked
	if remain < 0 {
		remain = 0
	}
	if ratePerSec <= 0.01 || checked < 3 {
		if atomic.LoadInt32(&running) == 1 {
			etaStr = "calculating…"
		} else {
			etaStr = "—"
		}
		return elapsedStr, etaStr, rateCPM
	}
	if remain == 0 {
		return elapsedStr, "done", rateCPM
	}
	secs := float64(remain) / ratePerSec
	etaStr = formatDurationShort(time.Duration(secs * float64(time.Second)))
	return elapsedStr, etaStr, rateCPM
}

// windowDelta returns the entries with absolute index >= from. The window
// holds only the last N lines, so total is the absolute number of lines
// produced and win[i] has absolute index total-len(win)+i. truncated tells
// the client to replace its list with what came back (its position fell out
// of the window or the counters reset for a new run).
func windowDelta(win []string, total, from int) (out []string, truncated bool) {
	if from > total {
		return append([]string{}, win...), true
	}
	if from == total {
		return nil, false
	}
	base := total - len(win)
	if from < base {
		return append([]string{}, win...), true
	}
	return append([]string{}, win[from-base:]...), false
}

func parseSeq(v string) (int, bool) {
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err != nil || strings.TrimSpace(v) == "" {
		return 0, false
	}
	return n, true
}

func HandleState(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	q := r.URL.Query()
	// lite=1: counters only — the shell poll needs just running/uid and
	// should not force the log/hit windows to be copied at all.
	lite := q.Get("lite") == "1"
	logFrom, hasLogFrom := parseSeq(q.Get("log_from"))
	hitFrom, hasHitFrom := parseSeq(q.Get("hit_from"))
	mu.Lock()
	elapsedStr, etaStr, rateCPM := computeETA(checked, total)
	s := Snap{
		Running:  atomic.LoadInt32(&running) == 1,
		Paused:   atomic.LoadInt32(&paused) == 1,
		Status:   status,
		Valid:    valid,
		Invalid:  invalid,
		Retries:  retries,
		Checked:  checked,
		Total:    total,
		Daily:    core.DailyUsed(),
		Limit:    core.DailyLimit,
		UID:      machine.ID(),
		LogTotal: logSeq,
		HitTotal: hitSeq,
		Free:     free,
		Private:  private,
		Elapsed:  elapsedStr,
		ETA:      etaStr,
		RateCPM:  rateCPM,
	}
	if !lite {
		if hasLogFrom {
			s.NewLogs, s.LogReset = windowDelta(logs, logSeq, logFrom)
		} else {
			s.Logs = append([]string{}, logs...)
		}
		if hasHitFrom {
			s.NewHits, s.HitReset = windowDelta(hits, hitSeq, hitFrom)
		} else {
			s.Hits = append([]string{}, hits...)
		}
	}
	mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

func HandleStart(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodPost {
		stdhttp.Error(w, "POST only", 405)
		return
	}
	var req struct {
		Combo       string   `json:"combo"`
		Proxy       string   `json:"proxy"`
		ProxyMode   string   `json:"proxy_mode"`
		ProxyURLs   []string `json:"proxy_urls"`
		Keywords    string   `json:"keywords"`
		Workers     string   `json:"workers"`
		Resume      bool     `json:"resume"`
		SessionFile string   `json:"session_file"`
		KeywordsOn  *bool    `json:"keywords_on"`
		PaymentsOn  *bool    `json:"payments_on"`
		PromosOn    *bool    `json:"promos_on"`
		FortniteOn  *bool    `json:"fortnite_on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	if atomic.LoadInt32(&running) == 1 || atomic.LoadInt32(&engineAlive) == 1 {
		stopEngine()
	}
	atomic.StoreInt32(&stopFlag, 0)
	atomic.StoreInt32(&paused, 0)
	checker.SetStopFlag(0)
	checker.SetPauseFlag(0)
	// New run generation: a consumer goroutine still draining an older run's
	// channel sees the mismatch (under mu, same as here) and discards its
	// events, so the previous run can never overwrite this run's status.
	mu.Lock()
	gen := atomic.AddInt64(&runGen, 1)
	mu.Unlock()
	atomic.StoreInt32(&running, 1)
	checker.Hooks.OnTotal = func() {
		mu.Lock()
		total++
		mu.Unlock()
	}
	combo := core.ResolveLocalFile(strings.TrimSpace(req.Combo))
	if combo == "" || !core.FileExists(combo) {
		atomic.StoreInt32(&running, 0)
		stdhttp.Error(w, "combo file not found: "+strings.TrimSpace(req.Combo), 400)
		return
	}
	if core.AtLimit() {
		atomic.StoreInt32(&running, 0)
		stdhttp.Error(w, core.LimitMessage(), 429)
		return
	}
	wc := 20
	fmt.Sscanf(strings.TrimSpace(req.Workers), "%d", &wc)
	if wc <= 0 {
		wc = 20
	}
	proxyIn := strings.TrimSpace(req.Proxy)
	var proxies []string
	var err error
	if strings.EqualFold(strings.TrimSpace(req.ProxyMode), "server") {
		proxies, err = proxy.FetchFromURLs(req.ProxyURLs)
		proxyIn = strings.Join(req.ProxyURLs, ",")
	} else {
		if proxyIn != "" && (strings.HasSuffix(strings.ToLower(proxyIn), ".txt") || strings.Contains(proxyIn, `\`) || strings.Contains(proxyIn, `/`)) {
			if p := core.ResolveLocalFile(proxyIn); p != "" {
				proxyIn = p
			}
		}
		proxies, err = proxy.Load(proxyIn)
	}
	if err != nil {
		atomic.StoreInt32(&running, 0)
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	cfg := Config{
		ComboPath:     combo,
		ProxyInput:    proxyIn,
		KeywordsInput: strings.TrimSpace(req.Keywords),
		Proxies:       proxies,
		Workers:       wc,
		Keywords:      core.ParseKeywords(req.Keywords),
		CapKeywords:   req.KeywordsOn == nil || *req.KeywordsOn,
		CapPayments:   req.PaymentsOn == nil || *req.PaymentsOn,
		CapPromos:     req.PromosOn == nil || *req.PromosOn,
		CapFortnite:   req.FortniteOn == nil || *req.FortniteOn,
	}
	if req.Resume {
		if req.SessionFile != "" {
			if b, err := os.ReadFile(req.SessionFile); err == nil {
				var s Session
				if json.Unmarshal(b, &s) == nil {
					s.File = req.SessionFile
					cfg.Session = &s
					if cfg.ComboPath == "" {
						cfg.ComboPath = s.ComboPath
						combo = s.ComboPath
					}
					if cfg.ProxyInput == "" {
						cfg.ProxyInput = s.ProxyInput
					}
					if cfg.KeywordsInput == "" {
						cfg.KeywordsInput = s.KeywordsInput
						cfg.Keywords = core.ParseKeywords(s.KeywordsInput)
					}
					if cfg.Workers == 20 && s.Workers > 0 {
						cfg.Workers = s.Workers
					}
				}
			}
		} else if s, err := checker.Load(); err == nil && s != nil {
			cfg.Session = s
		}
	}
	checker.AttachSession(&cfg, req.Resume)

	mu.Lock()
	valid, invalid, retries, checked, total, free, private = 0, 0, 0, 0, 0, 0, 0
	logs, hits = nil, nil
	logSeq, hitSeq = 0, 0
	startedAt = time.Now()
	pauseStarted = time.Time{}
	pausedAccum = 0
	rateSamples = nil
	endedAt = time.Time{}
	proxyDesc := "direct (no proxy)"
	if len(proxies) > 0 {
		proxyDesc = fmt.Sprintf("%d proxies", len(proxies))
	}
	status = fmt.Sprintf("Running — %d workers · %s · %s", wc, filepath.Base(combo), proxyDesc)
	mu.Unlock()

	ch := make(chan ProgressEvent, 50000)
	atomic.StoreInt32(&engineAlive, 1)
	go checker.RunEngine(cfg, ch)
	go func(gen int64) {
		for ev := range ch {
			mu.Lock()
			if atomic.LoadInt64(&runGen) != gen {
				// A newer run started — drop this stale event so it
				// cannot overwrite the new run's status/running flag.
				mu.Unlock()
				continue
			}
			if ev.IsRetry {
				retries += ev.RetryDelta
				if retries < 0 {
					retries = 0
				}
			}
			if ev.IsValid {
				valid++
				if strings.Contains(ev.LogMsg, "[Free Hit]") {
					free++
				} else if strings.Contains(ev.LogMsg, "[Private Hit]") {
					private++
				}
				checked++
				rateSamples = append(rateSamples, [2]int64{time.Now().UnixMilli(), int64(checked)})
				if len(rateSamples) > 120 {
					// Reallocate: plain reslicing keeps the old backing array
					// (and every dropped sample) alive for the whole run.
					rateSamples = append([][2]int64(nil), rateSamples[len(rateSamples)-120:]...)
				}
				if ev.Hit != "" {
					hits = append(hits, ev.Hit)
					hitSeq++
					if len(hits) > 80 {
						hits = append([]string(nil), hits[len(hits)-80:]...)
					}
				}
			} else if !ev.IsRetry && !ev.IsDone && !ev.LimitHit {
				invalid++
				checked++
				rateSamples = append(rateSamples, [2]int64{time.Now().UnixMilli(), int64(checked)})
				if len(rateSamples) > 120 {
					rateSamples = append([][2]int64(nil), rateSamples[len(rateSamples)-120:]...)
				}
			}
			if ev.LogMsg != "" {
				logs = append(logs, ev.LogMsg)
				logSeq++
				if len(logs) > 80 {
					logs = append([]string(nil), logs[len(logs)-80:]...)
				}
			}
			if ev.LimitHit {
				status = core.LimitMessage()
			}
			if ev.IsDone {
				if atomic.LoadInt32(&stopFlag) == 1 {
					status = "Stopped — session saved. Return to menu."
				} else if core.AtLimit() {
					status = core.LimitMessage()
				} else {
					status = "Done — saved to valid.txt. Return to menu."
				}
				endedAt = time.Now() // freeze the elapsed clock
				atomic.StoreInt32(&running, 0)
				atomic.StoreInt32(&engineAlive, 0)
			}
			mu.Unlock()
		}
		mu.Lock()
		stale := atomic.LoadInt64(&runGen) != gen
		mu.Unlock()
		if !stale {
			atomic.StoreInt32(&running, 0)
			atomic.StoreInt32(&engineAlive, 0)
		}
	}(gen)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "total": 0})
}

func HandleSessions(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var cards []SessionCard
	for _, sess := range checker.List() {
		cards = append(cards, SessionCard{
			File:     sess.File,
			Combo:    sess.ComboPath,
			Proxy:    sess.ProxyInput,
			Keywords: sess.KeywordsInput,
			Workers:  sess.Workers,
			Retries:  len(sess.RetryLines),
			Updated:  sess.UpdatedAt,
			Started:  sess.StartedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sessions": cards})
}

func stopEngine() {
	atomic.StoreInt32(&paused, 0)
	atomic.StoreInt32(&stopFlag, 1)
	checker.SetStopFlag(1)
	checker.SetPauseFlag(0)
	for i := 0; i < 40 && atomic.LoadInt32(&engineAlive) == 1; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	atomic.StoreInt32(&running, 0)
	atomic.StoreInt32(&engineAlive, 0)
	mu.Lock()
	status = "Stopped — session saved"
	endedAt = time.Now() // freeze the elapsed clock
	mu.Unlock()
}

func HandleStop(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	stopEngine()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "stopped": true})
}

func HandlePause(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if atomic.LoadInt32(&running) != 1 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not running"})
		return
	}
	mu.Lock()
	if atomic.LoadInt32(&paused) == 1 {
		atomic.StoreInt32(&paused, 0)
		if !pauseStarted.IsZero() {
			pausedAccum += time.Since(pauseStarted)
			pauseStarted = time.Time{}
		}
		status = "Running — resumed"
	} else {
		atomic.StoreInt32(&paused, 1)
		pauseStarted = time.Now()
		status = "Paused — workers are waiting"
	}
	paused := atomic.LoadInt32(&paused) == 1
	checker.SetPauseFlag(flag32(paused))
	mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "paused": paused})
}

func HandlePick(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	title := "Select file"
	if r.URL.Query().Get("kind") == "proxy" {
		title = "Select proxy list"
	} else {
		title = "Select combo file"
	}
	path := WindowsOpenFile(title)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

func HandlePickSave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "combos-export.txt"
	}
	path := WindowsSaveFile("Export combos", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

// HandleRoot answers the bare / route on both listeners. There is no web UI
// anymore — the interface is the Flutter app — so this only returns a short
// status line for stray requests.
func HandleRoot(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("SOLO Checker backend running — UI: xbox-checker.exe (Flutter)\n"))
}

func WindowsSaveFile(title string, names ...string) string {
	def := "combos-export.txt"
	if len(names) > 0 && strings.TrimSpace(names[0]) != "" {
		def = strings.TrimSpace(names[0])
	}
	if runtime.GOOS != "windows" {
		return def
	}
	ps := `Add-Type -AssemblyName System.Windows.Forms; $form = New-Object System.Windows.Forms.Form; $form.TopMost = $true; $form.WindowState = 'Minimized'; $form.ShowInTaskbar = $false; $null = $form.Handle; $f = New-Object System.Windows.Forms.SaveFileDialog; $f.Filter = 'Text (*.txt)|*.txt|All files (*.*)|*.*'; $f.Title = '` + strings.ReplaceAll(title, "'", "") + `'; $f.FileName = '` + strings.ReplaceAll(def, "'", "") + `'; if ($f.ShowDialog($form) -eq 'OK') { $f.FileName }; $form.Close()`
	out, err := runHiddenPowerShell(ps)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runHiddenPowerShell(ps string) ([]byte, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-STA", "-Command", ps)
	cmd.Stdin = nil
	return cmd.Output()
}

func WindowsOpenFile(title string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	ps := `Add-Type -AssemblyName System.Windows.Forms; $form = New-Object System.Windows.Forms.Form; $form.TopMost = $true; $form.ShowInTaskbar = $false; $null = $form.Handle; $f = New-Object System.Windows.Forms.OpenFileDialog; $f.Filter = 'Text (*.txt)|*.txt|All files (*.*)|*.*'; $f.Title = '` + strings.ReplaceAll(title, "'", "") + `'; $f.InitialDirectory = [Environment]::GetFolderPath('Desktop'); $f.RestoreDirectory = $true; $f.CheckFileExists = $true; if ($f.ShowDialog($form) -eq 'OK') { $f.FileName }; $form.Close()`
	out, err := runHiddenPowerShell(ps)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func HandleOpenLink(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	u := strings.TrimSpace(r.URL.Query().Get("u"))
	if !strings.HasPrefix(u, "https://t.me/") && !strings.HasPrefix(u, "https://telegram.me/") {
		stdhttp.Error(w, "blocked", 400)
		return
	}
	openBrowserPortable(u)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func flag32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func openBrowserPortable(u string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		// macOS has(1); on iOS the binary is absent and the exec harmlessly
		// fails — URL opening there belongs to the Flutter layer.
		_ = exec.Command("open", u).Start()
	default:
		_ = exec.Command("xdg-open", u).Start()
	}
}
