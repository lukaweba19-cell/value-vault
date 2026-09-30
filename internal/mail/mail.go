// Package mail implements Outlook/Office mailbox access: token capture,
// inbox search, REST folder listing, message HTML/deletion and the HTTP
// handlers behind /api/mail/*, plus the Thunderbird consent revoke CLI flow.
package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	stdhttp "net/http"

	"xbox-checker/internal/nettls"
	"xbox-checker/internal/proxy"
)

const (
	sessionsDir = "sessions"

	OutlookTokenURL  = "https://login.live.com/oauth20_token.srf"
	OutlookSearchURL = "https://outlook.live.com/search/api/v2/query?n=124&cv=tNZ1DVP5NhDwG%2FDUCelaIu.124"
	OutlookClientID  = "0000000048170EF2"
	OutlookScope     = "https://substrate.office.com/User-Internal.ReadWrite"
	OutlookRedirect  = "https://login.live.com/oauth20_desktop.srf"
)

type Tok struct {
	Email        string `json:"email"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	CID          string `json:"cid"`
	When         int64  `json:"when"`
}

func tokFile() string {
	dir := filepath.Join(sessionsDir, "mail")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "tokens.json")
}

func loadTokMap() map[string]Tok {
	out := map[string]Tok{}
	b, err := os.ReadFile(tokFile())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func saveToken(email, atk, rtk, cid string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || atk == "" {
		return
	}
	m := loadTokMap()
	m[email] = Tok{Email: email, AccessToken: atk, RefreshToken: rtk, CID: cid, When: time.Now().Unix()}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(tokFile(), b, 0644)
}

func loadToken(email string) (Tok, bool) {
	t, ok := loadTokMap()[strings.ToLower(strings.TrimSpace(email))]
	return t, ok && t.AccessToken != ""
}

func CaptureOutlookSession(client, noRedir tls_client.HttpClient, ua, email string) {
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
	resp, err := nettls.Do(noRedir, req)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	combined := resp.Header.Get("Location") + "\n" + string(body)
	atk := nettls.ExtractParam(combined, "access_token=")
	rtk := nettls.ExtractParam(combined, "refresh_token=")
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
		if resp4, err := nettls.Do(client, req4); err == nil {
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
		for _, c := range jar.Cookies(nettls.MustParseURL("https://login.live.com")) {
			if c.Name == "MSPCID" {
				cid = strings.ToUpper(c.Value)
				break
			}
		}
	}
	if atk != "" {
		saveToken(email, atk, rtk, cid)
	}
}

// inbox search scopes) then searches for each keyword.
// Returns keyword -> match count (0 if search failed for that kw).
func SearchInboxForKeywords(email, password, proxyStr string, keywords []string) map[string]int {
	results := make(map[string]int)
	for _, kw := range keywords {
		results[kw] = 0
	}
	if len(keywords) == 0 {
		return results
	}

	preset := nettls.NextPreset()
	client, noRedir := nettls.NewPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.Profile)
	if client == nil || noRedir == nil {
		return results
	}
	jar := client.GetCookieJar()

	ua := preset.UA

	// 1) GET login.live.com for PPFT
	req, err := http.NewRequest("GET", "https://login.live.com/", nil)
	if err != nil {
		return results
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := nettls.Do(client, req)
	if err != nil {
		return results
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := nettls.ExtractPPFT(string(body))
	if ppft == "" {
		return results
	}

	uaid := ""
	for _, c := range jar.Cookies(nettls.MustParseURL("https://login.live.com")) {
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
	resp2, err := nettls.Do(client, req2)
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
	resp3, err := nettls.Do(noRedir, req3)
	if err != nil {
		return results
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	combined := loc + "\n" + string(body3)

	// Tokens live in the URL fragment (#access_token=...)
	atk := nettls.ExtractParam(combined, "access_token=")
	rtk := nettls.ExtractParam(combined, "refresh_token=")
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
			if resp4, err := nettls.Do(client, req4); err == nil {
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
	for _, c := range jar.Cookies(nettls.MustParseURL("https://login.live.com")) {
		if c.Name == "MSPCID" {
			cid = strings.ToUpper(c.Value)
			break
		}
	}
	RememberSess(email, token, cid)
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
	for _, c := range jar.Cookies(nettls.MustParseURL("https://login.live.com")) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
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

		resp, err := nettls.Do(client, req)
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

type Msg struct {
	ID        string   `json:"id,omitempty"`
	ChangeKey string   `json:"change_key,omitempty"`
	Subject   string   `json:"subject"`
	From      string   `json:"from"`
	Date      string   `json:"date"`
	Preview   string   `json:"preview"`
	HTML      string   `json:"html,omitempty"`
	Images    []string `json:"images,omitempty"`
}

type Sess struct {
	Token string
	CID   string
	Exp   time.Time
}

var (
	mailSessMu    sync.Mutex
	mailSessMap   = map[string]Sess{}
	mailLastTotal int
)

func tokenFile() string {
	dir := filepath.Join(sessionsDir, "mail")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "tokens.json")
}

func loadSessFile() {
	b, err := os.ReadFile(tokenFile())
	if err != nil {
		return
	}
	var m map[string]Sess
	if json.Unmarshal(b, &m) != nil {
		return
	}
	mailSessMu.Lock()
	if mailSessMap == nil {
		mailSessMap = map[string]Sess{}
	}
	for k, v := range m {
		mailSessMap[strings.ToLower(k)] = v
	}
	mailSessMu.Unlock()
}

func saveSessFile() {
	mailSessMu.Lock()
	cp := make(map[string]Sess, len(mailSessMap))
	for k, v := range mailSessMap {
		cp[k] = v
	}
	mailSessMu.Unlock()
	_ = os.MkdirAll(filepath.Join(sessionsDir, "mail"), 0o755)
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(tokenFile(), b, 0o600)
}

var mailSessLoaded int32

func RememberSess(email, token, cid string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || token == "" {
		return
	}
	if atomic.LoadInt32(&mailSessLoaded) == 0 {
		loadSessFile()
		atomic.StoreInt32(&mailSessLoaded, 1)
	}
	mailSessMu.Lock()
	mailSessMap[email] = Sess{Token: token, CID: cid, Exp: time.Now().Add(12 * time.Hour)}
	mailSessMu.Unlock()
	saveSessFile()
}

func CachedSess(email string) (Sess, bool) {
	if atomic.LoadInt32(&mailSessLoaded) == 0 {
		loadSessFile()
		atomic.StoreInt32(&mailSessLoaded, 1)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	mailSessMu.Lock()
	defer mailSessMu.Unlock()
	s, ok := mailSessMap[email]
	if !ok || s.Token == "" {
		return Sess{}, false
	}
	return s, true
}

func DropSess(email string) {
	mailSessMu.Lock()
	delete(mailSessMap, strings.ToLower(strings.TrimSpace(email)))
	mailSessMu.Unlock()
	saveSessFile()
}

func HandleProbe(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	sess, ok := CachedSess(req.Email)
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "need_login": true})
		return
	}
	msgs, alive, total := QueryWithTokenOK(sess.Token, sess.CID, strings.TrimSpace(req.Email), 0, 100, "kind:email", "inbox")
	if !alive {
		DropSess(req.Email)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "need_login": true})
		return
	}
	more := len(msgs) >= 10
	if total > 0 {
		more = len(msgs) < total
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs, "count": len(msgs), "total": total, "more": more, "reused": true})
}

func HandleList(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
		if px, err := proxy.FetchFromURLs(req.ProxyURLs); err == nil {
			tryPx = px
		}
	} else if p := strings.TrimSpace(req.Proxy); p != "" {
		if px, err := proxy.Load(p); err == nil && len(px) > 0 {
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
	var msgs []Msg
	var err error
	if sess, ok := CachedSess(email); ok {
		msgs, alive, total := QueryWithTokenOK(sess.Token, sess.CID, email, req.From, req.Size, q, req.Folder)
		if alive {
			more := (total > 0 && req.From+len(msgs) < total) || (total == 0 && len(msgs) >= 10)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": req.Email, "messages": msgs, "count": len(msgs), "total": total, "from": req.From, "size": req.Size, "more": more, "reused": true})
			return
		}
		DropSess(email)
	}
	for _, p := range tryPx {
		msgs, err = ListMailOnce(email, req.Password, p, req.From, req.Size, q, req.Folder)
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

// HandleConnect streams the sign-in as NDJSON progress lines so the UI can
// animate proxies being tried and token steps. Request body is the same
// {email,password,proxy,proxy_mode,proxy_urls} shape as /api/mail/list.
// Lines: {"type":"stage","stage":"login_page"|"credentials"|"token"|"mailbox"}
// and {"type":"try","proxy":...,"stage":...,"ok":false,"err":...} per attempt,
// then a final {"type":"done","ok":true,"email":...} or
// {"type":"done","ok":false,"error":...}.
func HandleConnect(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != stdhttp.MethodPost {
		writeNDJSON(w, map[string]any{"type": "done", "ok": false, "error": "POST only"})
		return
	}
	var req struct {
		Email     string   `json:"email"`
		Password  string   `json:"password"`
		Proxy     string   `json:"proxy"`
		ProxyMode string   `json:"proxy_mode"`
		ProxyURLs []string `json:"proxy_urls"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Email) == "" {
		writeNDJSON(w, map[string]any{"type": "done", "ok": false, "error": "email required"})
		return
	}
	email := strings.TrimSpace(req.Email)
	var tryPx []string
	if strings.EqualFold(req.ProxyMode, "server") && len(req.ProxyURLs) > 0 {
		if px, err := proxy.FetchFromURLs(req.ProxyURLs); err == nil {
			tryPx = px
		}
	} else if p := strings.TrimSpace(req.Proxy); p != "" {
		if px, err := proxy.Load(p); err == nil && len(px) > 0 {
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
	// writeNDJSON flushes after every line, so the UI sees each step live.
	writeNDJSON(w, map[string]any{"type": "start", "proxies": len(tryPx)})
	msgs, err := ConnectMailbox(email, req.Password, tryPx,
		func(stage string) {
			writeNDJSON(w, map[string]any{"type": "stage", "stage": stage})
		},
		func(res ConnResult) {
			writeNDJSON(w, map[string]any{"type": "try", "proxy": res.Proxy, "stage": res.Stage, "ok": res.Ok, "err": res.Err})
		})
	if err != nil {
		writeNDJSON(w, map[string]any{"type": "done", "ok": false, "error": err.Error()})
		return
	}
	// Success: hand the UI the first page so it doesn't need a second round-trip.
	mailSessMu.Lock()
	total := mailLastTotal
	mailSessMu.Unlock()
	more := len(msgs) >= 10
	if total > 0 {
		more = len(msgs) < total
	}
	writeNDJSON(w, map[string]any{"type": "done", "ok": true, "email": email, "messages": msgs, "count": len(msgs), "total": total, "more": more})
}

func writeNDJSON(w stdhttp.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	w.Write(append(b, '\n'))
	if f, ok := w.(stdhttp.Flusher); ok {
		f.Flush()
	}
}

func countTotal(v interface{}) int {
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
			if n := countTotal(child); n > best {
				best = n
			}
		}
		return best
	case []interface{}:
		best := 0
		for _, child := range val {
			if n := countTotal(child); n > best {
				best = n
			}
		}
		return best
	}
	return 0
}

func FolderName(folder string) string {
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

func QueryWithToken(token, cid, mailbox string, from, size int, query string) []Msg {
	msgs, _, _ := QueryWithTokenOK(token, cid, mailbox, from, size, query, "inbox")
	return msgs
}

func listFolderREST(token, cid, mailbox, folder string, from, size int) ([]Msg, bool, int) {
	if size <= 0 {
		size = 80
	}
	if from < 0 {
		from = 0
	}
	name := FolderName(folder)
	cli := nettls.NewClient("", 25*time.Second, true)
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
	var msgs []Msg
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
		resp, err := nettls.Do(cli, req)
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
			msgs = append(msgs, Msg{
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

func QueryWithTokenOK(token, cid, mailbox string, from, size int, query, folder string) ([]Msg, bool, int) {
	if rest, alive, total := listFolderREST(token, cid, mailbox, folder, from, size); alive && (len(rest) > 0 || folder != "") {
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
	searchBody := fmt.Sprintf(`{"Cvid":"7ef2720e-6e59-ee2b-a217-3a4f427ab0f7","Scenario":{"Name":"owa.react"},"TimeZone":"UTC","TextDecorations":"Off","EntityRequests":[{"EntityType":"Conversation","ContentSources":["Exchange"],"Filter":{"Term":{"DistinguishedFolderName":%q}},"From":%d,"Query":{"QueryString":%s},"Size":%d,"Sort":[{"Field":"Time","SortDirection":"Desc"}],"EnableTopResults":false}],"LogicalId":"446c567a-02d9-b739-b9ca-616e0d45905c"}`, FolderName(folder), from, string(qJSON), size)
	cli := nettls.NewClient("", 25*time.Second, true)
	if cli == nil {
		return nil, false, 0
	}
	var msgs []Msg
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
		respS, err := nettls.Do(cli, reqS)
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
		if n := countTotal(raw); n > total {
			total = n
		}
		collectMsgs(raw, &msgs)
		if len(msgs) > 0 {
			break
		}
	}
	return msgs, alive, total
}

func DeleteMessage(token, cid, mailbox, itemID, changeKey string, purge bool) error {
	itemID = strings.TrimSpace(itemID)
	if token == "" || itemID == "" {
		return fmt.Errorf("missing token or message id")
	}
	cli := nettls.NewClient("", 25*time.Second, true)
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
		resp, err := nettls.Do(cli, req)
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
			"__type":               "DeleteItemRequest:#Exchange",
			"ItemIds":              []itemRef{ref},
			"DeleteType":           map[bool]string{true: "HardDelete", false: "MoveToDeletedItems"}[purge],
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
		resp, err := nettls.Do(cli, req)
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

func fetchItemHTML(token, cid, mailbox, itemID string) string {
	itemID = strings.TrimSpace(itemID)
	if token == "" || itemID == "" {
		return ""
	}
	cli := nettls.NewClient("", 25*time.Second, true)
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
		resp, err := nettls.Do(cli, req)
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
			return inlineCIDs(html, raw)
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
				"__type":       "ItemResponseShape:#Exchange",
				"BaseShape":    "AllProperties",
				"BodyType":     "HTML",
				"WantHtmlBody": true,
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
		resp, err := nettls.Do(cli, req)
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
			return inlineCIDs(html, raw)
		}
	}
	return ""
}

func inlineCIDs(html string, raw interface{}) string {
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

func HandleBody(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
	sess, ok := CachedSess(req.Email)
	if !ok || sess.Token == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no mail session"})
		return
	}
	html := fetchItemHTML(sess.Token, sess.CID, req.Email, req.ID)
	if html == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no html body"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "html": html})
}

func HandleView(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if email == "" || id == "" {
		stdhttp.Error(w, "email and id required", 400)
		return
	}
	sess, ok := CachedSess(email)
	if !ok || sess.Token == "" {
		stdhttp.Error(w, "no mail session", 401)
		return
	}
	html := fetchItemHTML(sess.Token, sess.CID, email, id)
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

func HandleDelete(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
	sess, ok := CachedSess(req.Email)
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
		if err := DeleteMessage(sess.Token, sess.CID, req.Email, id, req.ChangeKey, req.Purge); err != nil {
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

func ListMail(email, password, proxyStr string, from, size int, query string) ([]Msg, error) {
	proxies := []string{""}
	if strings.TrimSpace(proxyStr) != "" {
		if px, err := proxy.Load(proxyStr); err == nil && len(px) > 0 {
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
		msgs, err := ListMailOnce(email, password, p, from, size, query, "inbox")
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

// StageFn receives human-meaningful sign-in progress steps while a mailbox
// login runs: "login_page" → "credentials" → "token" → "mailbox". It may be nil.
type StageFn func(stage string)

func emitStage(onStage StageFn, stage string) {
	if onStage != nil {
		onStage(stage)
	}
}

// ListMailOnce signs in through proxyStr and fetches one page (no progress callbacks).
func ListMailOnce(email, password, proxyStr string, from, size int, query, folder string) ([]Msg, error) {
	return listMailOnceStaged(email, password, proxyStr, from, size, query, folder, nil)
}

// ConnResult reports what happened while trying one proxy during connect.
// Stage is the last sign-in step reached; Err explains the failure, if any.
type ConnResult struct {
	Proxy string `json:"proxy"`
	Stage string `json:"stage"`
	Ok    bool   `json:"ok"`
	Err   string `json:"err,omitempty"`
}

func maskProxy(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "direct (no proxy)"
	}
	if at := strings.LastIndex(p, "@"); at >= 0 {
		return p[at+1:]
	}
	return p
}

// ConnectMailbox signs in to the mailbox, trying each candidate proxy in
// order until one works. It reports every attempt through onTry and every
// sign-in step through onStage so the UI can animate the process.
func ConnectMailbox(email, password string, tryPx []string, onStage StageFn, onTry func(ConnResult)) ([]Msg, error) {
	var msgs []Msg
	var last error
	for _, p := range tryPx {
		stage := "connecting" // how far this attempt got, for diagnostics
		staged := func(s string) {
			stage = s
			if onStage != nil {
				onStage(s)
			}
		}
		if onTry != nil {
			onTry(ConnResult{Proxy: maskProxy(p), Stage: stage})
		}
		ms, err := listMailOnceStaged(email, password, p, 0, 100, "kind:email", "inbox", staged)
		res := ConnResult{Proxy: maskProxy(p), Ok: err == nil, Stage: stage}
		if err != nil {
			res.Err = err.Error()
		} else {
			res.Stage = "mailbox"
			msgs = ms
		}
		if onTry != nil {
			onTry(res)
		}
		if err == nil {
			return msgs, nil
		}
		last = err
		onStage = nil // progress steps are only meaningful for the first attempt
	}
	if last == nil {
		last = fmt.Errorf("couldn't create client / bad proxies")
	} else {
		last = fmt.Errorf("couldn't create client / bad proxies: %v", last)
	}
	return nil, last
}

func listMailOnceStaged(email, password, proxyStr string, from, size int, query, folder string, onStage StageFn) ([]Msg, error) {
	preset := nettls.NextPreset()
	client, noRedir := nettls.NewPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.Profile)
	if client == nil || noRedir == nil {
		return nil, fmt.Errorf("could not create client")
	}
	jar := client.GetCookieJar()
	ua := preset.UA
	emitStage(onStage, "login_page")
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
	resp, err := nettls.Do(client, req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	ppft := nettls.ExtractPPFT(page)
	if ppft == "" {
		return nil, fmt.Errorf("login page failed")
	}
	emitStage(onStage, "credentials")
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
	resp2, err := nettls.Do(noRedir, req2)
	if err != nil {
		resp2, err = nettls.Do(client, req2)
	}
	if err != nil {
		return nil, err
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	emitStage(onStage, "token")
	combined := resp2.Header.Get("Location") + "\n" + string(body2)
	req3, _ := http.NewRequest("GET", authStart+"&prompt=none", nil)
	req3.Header.Set("User-Agent", ua)
	if resp3, err := nettls.Do(noRedir, req3); err == nil {
		body3, _ := io.ReadAll(resp3.Body)
		resp3.Body.Close()
		combined += "\n" + resp3.Header.Get("Location") + "\n" + string(body3)
	}
	atk := nettls.ExtractParam(combined, "access_token=")
	rtk := nettls.ExtractParam(combined, "refresh_token=")
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
		if resp4, err := nettls.Do(client, req4); err == nil {
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
	for _, c := range jar.Cookies(nettls.MustParseURL("https://login.live.com")) {
		if c.Name == "MSPCID" {
			cid = strings.ToUpper(c.Value)
			break
		}
	}
	RememberSess(email, token, cid)
	emitStage(onStage, "mailbox")
	if rest, alive, _ := listFolderREST(token, cid, email, folder, from, size); alive {
		return rest, nil
	}
	if query == "*" || query == "" {
		query = "kind:email"
	}
	qJSON, _ := json.Marshal(query)
	searchBody := fmt.Sprintf(`{"Cvid":"7ef2720e-6e59-ee2b-a217-3a4f427ab0f7","Scenario":{"Name":"owa.react"},"TimeZone":"UTC","TextDecorations":"Off","EntityRequests":[{"EntityType":"Conversation","ContentSources":["Exchange"],"Filter":{"Term":{"DistinguishedFolderName":%q}},"From":%d,"Query":{"QueryString":%s},"Size":%d,"Sort":[{"Field":"Time","SortDirection":"Desc"}],"EnableTopResults":false}],"LogicalId":"446c567a-02d9-b739-b9ca-616e0d45905c"}`, FolderName(folder), from, string(qJSON), size)
	endpoints := []string{
		"https://substrate.office.com/search/api/v2/query",
		"https://outlook.live.com/search/api/v2/query",
	}
	var msgs []Msg
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
		respS, err := nettls.Do(client, reqS)
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
		if n := countTotal(raw); n > 0 {
			mailSessMu.Lock()
			mailLastTotal = n
			mailSessMu.Unlock()
		}
		collectMsgs(raw, &msgs)
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

func collectMsgs(v interface{}, out *[]Msg) {
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
			m := Msg{Subject: subj}
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
			collectMsgs(src, out)
			return
		}
		for _, child := range val {
			collectMsgs(child, out)
		}
	case []interface{}:
		for _, child := range val {
			collectMsgs(child, out)
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

func RevokeThunderbirdConsent(email, password, proxyStr string) error {
	if email == "" || password == "" {
		return fmt.Errorf("need email:password")
	}
	preset := nettls.NextPreset()
	client, noRedir := nettls.NewPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.Profile)
	if client == nil || noRedir == nil {
		return fmt.Errorf("could not create client")
	}
	ua := preset.UA
	start := "https://account.live.com/consent/Manage"
	req, err := http.NewRequest("GET", start, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := nettls.Do(client, req)
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
	ppft := nettls.ExtractPPFT(page)
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
	resp2, err := nettls.Do(client, req2)
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
	resp3, err := nettls.Do(client, req3)
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
	resp4, err := nettls.Do(client, req4)
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
	resp, err := nettls.Do(client, req)
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
		respP, err := nettls.Do(client, reqP)
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
