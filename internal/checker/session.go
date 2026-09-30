// Package checker runs the combo-checking engine: session persistence,
// worker pool, retry queue and progress events.
package checker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xbox-checker/internal/checkercfg"
	"xbox-checker/internal/core"
)

type (
	LineRange     = checkercfg.LineRange
	Session       = checkercfg.Session
	Config        = checkercfg.Config
	ProgressEvent = checkercfg.ProgressEvent
)

// Hooks lets the GUI layer observe engine work counters without the engine
// depending on the GUI package.
var Hooks struct {
	// OnTotal is called (under no lock) every time the feeder queues a line.
	OnTotal func()
}

func Load() (*Session, error) {
	list := List()
	if len(list) == 0 {
		return nil, os.ErrNotExist
	}
	best := list[0]
	for _, s := range list[1:] {
		if s.UpdatedAt > best.UpdatedAt {
			best = s
		}
	}
	return &best, nil
}

var lastSessionFlush time.Time

func Save(s *Session) {
	SaveAt(s, false)
}

func SaveAt(s *Session, force bool) {
	if s == nil {
		return
	}
	if !force && time.Since(lastSessionFlush) < 2*time.Second {
		return
	}
	lastSessionFlush = time.Now()
	s.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if s.File == "" || !strings.HasPrefix(s.File, core.SessionsDir) {
		_ = os.MkdirAll(core.SessionsDir, 0755)
		base := filepath.Base(s.ComboPath)
		if base == "" || base == "." {
			base = "session"
		}
		s.File = filepath.Join(core.SessionsDir, fmt.Sprintf("%s-%s.json", strings.TrimSuffix(base, filepath.Ext(base)), time.Now().Format("20060102-150405")))
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(core.SessionsDir, 0755)
	_ = os.WriteFile(s.File, b, 0644)
}

func Delete(s *Session) {
	if s == nil || s.File == "" {
		return
	}
	_ = os.Remove(s.File)
}

func List() []Session {
	var out []Session
	seen := map[string]bool{}
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var s Session
		if json.Unmarshal(b, &s) != nil {
			return
		}
		if s.ComboPath == "" {
			return
		}
		s.File = path
		key := s.ComboPath + "|" + s.StartedAt + "|" + s.UpdatedAt
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}
	if entries, err := os.ReadDir(core.SessionsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				continue
			}
			add(filepath.Join(core.SessionsDir, e.Name()))
		}
	}
	return out
}

func LineKey(n int) string { return fmt.Sprintf("%d", n) }

func RangeHas(ranges []LineRange, n int) bool {
	for _, r := range ranges {
		if n >= r.From && n <= r.To {
			return true
		}
	}
	return false
}

func addDoneRange(ranges []LineRange, n int) []LineRange {
	if n <= 0 || RangeHas(ranges, n) {
		return ranges
	}
	ranges = append(ranges, LineRange{From: n, To: n})
	// sort + merge
	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[j].From < ranges[i].From {
				ranges[i], ranges[j] = ranges[j], ranges[i]
			}
		}
	}
	out := make([]LineRange, 0, len(ranges))
	for _, r := range ranges {
		if len(out) == 0 {
			out = append(out, r)
			continue
		}
		last := &out[len(out)-1]
		if r.From <= last.To+1 {
			if r.To > last.To {
				last.To = r.To
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func removeRetryLine(lines []int, n int) []int {
	out := lines[:0]
	for _, l := range lines {
		if l != n {
			out = append(out, l)
		}
	}
	return out
}

func MarkLineDone(cfg *Config, line int) {
	if cfg == nil || cfg.Session == nil || cfg.SessMu == nil || line <= 0 {
		return
	}
	cfg.SessMu.Lock()
	defer cfg.SessMu.Unlock()
	cfg.Session.DoneRanges = addDoneRange(cfg.Session.DoneRanges, line)
	cfg.Session.RetryLines = removeRetryLine(cfg.Session.RetryLines, line)
	k := LineKey(line)
	if cfg.Session.RetryCounts != nil {
		delete(cfg.Session.RetryCounts, k)
	}
	if cfg.Session.TriedProxies != nil {
		delete(cfg.Session.TriedProxies, k)
	}
	Save(cfg.Session)
}

func MarkLinePending(cfg *Config, line int, tries int, used map[string]struct{}) {
	if cfg == nil || cfg.Session == nil || cfg.SessMu == nil || line <= 0 {
		return
	}
	cfg.SessMu.Lock()
	defer cfg.SessMu.Unlock()
	found := false
	for _, l := range cfg.Session.RetryLines {
		if l == line {
			found = true
			break
		}
	}
	if !found {
		cfg.Session.RetryLines = append(cfg.Session.RetryLines, line)
	}
	if cfg.Session.RetryCounts == nil {
		cfg.Session.RetryCounts = map[string]int{}
	}
	cfg.Session.RetryCounts[LineKey(line)] = tries
	if cfg.Session.TriedProxies == nil {
		cfg.Session.TriedProxies = map[string][]string{}
	}
	var plist []string
	for p := range used {
		plist = append(plist, p)
	}
	cfg.Session.TriedProxies[LineKey(line)] = plist
	Save(cfg.Session)
}

func PackWork(line int, combo string) string {
	return fmt.Sprintf("%d\x00%s", line, combo)
}

func UnpackWork(s string) (int, string) {
	if i := strings.IndexByte(s, 0); i > 0 {
		var n int
		fmt.Sscanf(s[:i], "%d", &n)
		return n, s[i+1:]
	}
	return 0, s
}

func AttachSession(cfg *Config, resume bool) {
	cfg.SessMu = &sync.Mutex{}
	now := time.Now().Format("2006-01-02 15:04:05")
	if resume && cfg.Session != nil {
		if cfg.Session.RetryCounts == nil {
			cfg.Session.RetryCounts = map[string]int{}
		}
		if cfg.Session.TriedProxies == nil {
			cfg.Session.TriedProxies = map[string][]string{}
		}
		return
	}
	if resume {
		if s, err := Load(); err == nil && s != nil {
			cfg.Session = s
			if s.RetryCounts == nil {
				s.RetryCounts = map[string]int{}
			}
			if s.TriedProxies == nil {
				s.TriedProxies = map[string][]string{}
			}
			return
		}
	}
	cfg.Session = &Session{
		ComboPath:     cfg.ComboPath,
		ProxyInput:    cfg.ProxyInput,
		KeywordsInput: cfg.KeywordsInput,
		Workers:       cfg.Workers,
		CapKeywords:   cfg.CapKeywords,
		CapPayments:   cfg.CapPayments,
		CapPromos:     cfg.CapPromos,
		CapFortnite:   cfg.CapFortnite,
		CapturesSet:   true,
		DoneRanges:    []LineRange{},
		RetryLines:    []int{},
		RetryCounts:   map[string]int{},
		TriedProxies:  map[string][]string{},
		StartedAt:     now,
	}
	Save(cfg.Session)
}
