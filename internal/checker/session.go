// Package checker runs the combo-checking engine: session persistence,
// worker pool, retry queue and progress events.
package checker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Session file writes go through a single background writer goroutine. The
// engine used to marshal + write the (growing) session JSON inline on the
// worker that just finished a line — every 2s that worker blocked on disk
// while holding SessMu. Producers hand the writer the newest bytes and move
// on; force writes wait until the bytes are on disk.
type sessWrite struct {
	path   string
	data   []byte
	remove bool
	done   chan struct{}
}

var (
	sessWriteCh    = make(chan sessWrite, 1)
	sessWriterOnce sync.Once
)

func enqueueSessWrite(w sessWrite) {
	sessWriterOnce.Do(func() {
		go func() {
			for sw := range sessWriteCh {
				if sw.remove {
					_ = os.Remove(sw.path)
				} else {
					_ = os.MkdirAll(filepath.Dir(sw.path), 0755)
					_ = os.WriteFile(sw.path, sw.data, 0644)
				}
				if sw.done != nil {
					close(sw.done)
				}
			}
		}()
	})
	select {
	case sessWriteCh <- w:
		return
	default:
	}
	// Channel holds one stale write: drop it (its bytes are older than ours)
	// and put ours in its place. Producers are effectively single-threaded
	// (SaveAt runs under SessMu), so one retry suffices.
	select {
	case old := <-sessWriteCh:
		if old.done != nil {
			close(old.done)
		}
	default:
	}
	select {
	case sessWriteCh <- w:
	default:
		if w.remove {
			_ = os.Remove(w.path)
		} else {
			_ = os.WriteFile(w.path, w.data, 0644)
		}
		if w.done != nil {
			close(w.done)
		}
	}
}

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
	// Compact marshal: indented JSON of a large session made every flush more
	// expensive as DoneRanges/TriedProxies grew.
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	w := sessWrite{path: s.File, data: b}
	if force {
		w.done = make(chan struct{})
	}
	enqueueSessWrite(w)
	if w.done != nil {
		<-w.done
	}
}

func Delete(s *Session) {
	if s == nil || s.File == "" {
		return
	}
	// Removal is queued behind any pending write so a still-buffered flush
	// cannot re-create the file after Delete.
	w := sessWrite{path: s.File, remove: true, done: make(chan struct{})}
	enqueueSessWrite(w)
	<-w.done
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

// RangeHas reports whether n falls in any done range. DoneRanges is kept
// sorted and disjoint by addDoneRange, so a binary search replaces the old
// linear walk — on a large resume that walk ran for every combo line before
// it was even queued.
func RangeHas(ranges []LineRange, n int) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].To >= n })
	return i < len(ranges) && n >= ranges[i].From
}

// addDoneRange inserts n into the sorted, merged range list in O(log k)
// instead of the previous bubble-sort of the whole list on every done line
// (quadratic on a big resume).
func addDoneRange(ranges []LineRange, n int) []LineRange {
	if n <= 0 {
		return ranges
	}
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].From > n })
	// Already covered by the range before the insertion point.
	if i > 0 && ranges[i-1].To >= n {
		return ranges
	}
	// Extends the left neighbour; absorb the right neighbour if now adjacent.
	if i > 0 && ranges[i-1].To == n-1 {
		ranges[i-1].To = n
		if i < len(ranges) && ranges[i].From <= n+1 {
			if ranges[i].To > ranges[i-1].To {
				ranges[i-1].To = ranges[i].To
			}
			ranges = append(ranges[:i], ranges[i+1:]...)
		}
		return ranges
	}
	// Absorbed by the right neighbour.
	if i < len(ranges) && ranges[i].From == n+1 {
		ranges[i].From = n
		return ranges
	}
	ranges = append(ranges, LineRange{})
	copy(ranges[i+1:], ranges[i:])
	ranges[i] = LineRange{From: n, To: n}
	return ranges
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
