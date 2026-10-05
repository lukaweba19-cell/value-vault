// Package editor implements the combo editor: parsing, analysis, extraction,
// filtering, generation and signature banners behind /api/editor/*.
package editor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	stdhttp "net/http"

	"xbox-checker/internal/core"
)

var reEditorEmail = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,24}`)

func editorStore() string {
	dir := filepath.Join(core.SessionsDir, "signatures")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "index.json")
}

type Banner struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func loadBanners() []Banner {
	b, err := os.ReadFile(editorStore())
	if err != nil {
		return nil
	}
	var list []Banner
	_ = json.Unmarshal(b, &list)
	return list
}

func saveBanners(list []Banner) {
	b, _ := json.MarshalIndent(list, "", "  ")
	_ = os.WriteFile(editorStore(), b, 0644)
}

type kvCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func SortCounts(m map[string]int) []kvCount {
	return SortCountsN(m, 40)
}

func SortCountsN(m map[string]int, n int) []kvCount {
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

func PeekEmailHost(ln []byte) (edom, host string) {
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

func ParseComboLine(raw string) (email, pass, url, kind string) {
	s := strings.TrimSpace(raw)
	if s == "" || s[0] == '#' || (len(s) > 1 && s[0] == '/' && (s[1] == '/' || s[1] == '*')) {
		return "", "", "", "skip"
	}
	s = strings.TrimPrefix(s, "\ufeff")
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

func ComboUserPass(ln string) (email, pass string) {
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

func HandleAnalyzeStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
	res := analyzeSourceLive(path, "", send)
	res["done"] = true
	send(res)
}

func analyzeSource(path, inline string) map[string]any {
	return analyzeSourceLive(path, inline, nil)
}

func analyzeSourceLive(path, inline string, live func(map[string]any)) map[string]any {
	var (
		r    io.Reader
		size int64
		src  = path
		f    *os.File
	)
	if p := core.ResolveLocalFile(strings.TrimSpace(path)); p != "" {
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
		edom, hst := PeekEmailHost(ln)
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
				"domains": SortCountsN(dom, 10), "urls": SortCountsN(host, 10), "done": false,
			})
		}
	}
	if comboLines > 0 && len(kindN) == 0 {
		kindN["ulp"] = comboLines
	}
	big := size > 2<<20
	return map[string]any{
		"ok":      true,
		"path":    src,
		"lines":   comboLines,
		"raw":     rawN,
		"size":    size,
		"large":   big,
		"kinds":   kindN,
		"domains": SortCountsN(dom, 800),
		"urls":    SortCountsN(host, 800),
	}
}

func analyzeText(text, path string) map[string]any {
	return analyzeSource(path, text)
}

func HandleAnalyze(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	p := core.ResolveLocalFile(strings.TrimSpace(req.Path))
	if strings.TrimSpace(req.Text) == "" && (p == "" || !core.FileExists(p)) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "file not found: " + req.Path})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(analyzeSource(req.Path, req.Text))
}

func HandleExtract(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path      string `json:"path"`
		Text      string `json:"text"`
		Dest      string `json:"dest"`
		Banner    string `json:"banner"`
		BannerEnd string `json:"banner_end"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	src := core.ResolveLocalFile(req.Path)
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
	if src != "" && core.FileExists(src) {
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
		e, p := ComboUserPass(sc.Text())
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
	res := analyzeSource(dest, "")
	res["extracted"] = n
	res["path"] = dest
	res["dest"] = dest
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

type filterReq struct {
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

func RunFilter(req filterReq, live func(map[string]any)) map[string]any {
	src := core.ResolveLocalFile(req.Path)
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
	if src != "" && core.FileExists(src) {
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
				live(map[string]any{"ok": true, "extracted": n, "scanned": scanned, "size": size, "domains": SortCountsN(emailHits, 10), "done": false})
			}
			continue
		}
		e, p := ComboUserPass(ln)
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
			live(map[string]any{"ok": true, "extracted": n, "scanned": scanned, "size": size, "domains": SortCountsN(emailHits, 10), "done": false})
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
		"dest": dest, "path": dest, "domains": SortCountsN(emailHits, 200), "done": true,
	}
}

func HandleFilterStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
	res := RunFilter(filterReq{
		Path:       r.URL.Query().Get("path"),
		EmailTypes: r.URL.Query().Get("email_types"),
		URL:        r.URL.Query().Get("url"),
		CountOnly:  true,
	}, send)
	res["done"] = true
	send(res)
}

func HandleFilter(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req filterReq
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
	res := RunFilter(req, nil)
	if ok, _ := res["ok"].(bool); !ok {
		if err, _ := res["error"].(string); err != "" {
			stdhttp.Error(w, err, 400)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func HandleGenerate(w stdhttp.ResponseWriter, r *stdhttp.Request) {
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
	res := analyzeText(joined, "")
	res["text"] = joined
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func HandleSave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		Path      string `json:"path"`
		Text      string `json:"text"`
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

func HandleBanners(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == stdhttp.MethodPost {
		var b Banner
		_ = json.NewDecoder(r.Body).Decode(&b)
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "Banner"
		}
		list := loadBanners()
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
		saveBanners(list)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": list})
		return
	}
	if r.Method == stdhttp.MethodDelete {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		list := loadBanners()
		out := list[:0]
		for _, b := range list {
			if name != "" && strings.EqualFold(b.Name, name) {
				continue
			}
			out = append(out, b)
		}
		saveBanners(out)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": out})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "banners": loadBanners()})
}
