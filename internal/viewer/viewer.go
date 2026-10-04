// Package viewer implements the Valid Viewer: valid.txt parsing, tagging,
// stats and the JSON/SSE endpoints for the viewer window.
package viewer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	stdhttp "net/http"

	"xbox-checker/internal/core"
)

func ValidTxtPath() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "valid.txt")
		if core.FileExists(p) {
			return p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, "valid.txt")
		if core.FileExists(p) {
			return p
		}
	}
	return "valid.txt"
}

type Account struct {
	ID       int               `json:"id"`
	Line     int               `json:"line"`
	Email    string            `json:"email"`
	Password string            `json:"password"`
	Raw      string            `json:"raw"`
	Fields   map[string]string `json:"fields"`
	Tags     []string          `json:"tags"`
}

var (
	mu   sync.Mutex
	accs []Account
	stat map[string]any
	ver  int64
	// Identity of the valid.txt the cache was built from: page requests
	// re-parse only when one of these changes, not on every call.
	loadedPath string
	loadedSize int64
	loadedMod  int64
)

// maybeReload refreshes the cached accounts only when valid.txt actually
// changed (path, size or mtime), so a page request never re-parses the whole
// file just because the cache was consulted again.
func maybeReload() {
	path := ValidTxtPath()
	var size, mod int64
	if info, err := os.Stat(path); err == nil {
		size, mod = info.Size(), info.ModTime().UnixNano()
	}
	mu.Lock()
	need := accs == nil || path != loadedPath || size != loadedSize || mod != loadedMod
	mu.Unlock()
	if need {
		Load()
	}
}

func Load() {
	path := ValidTxtPath()
	f, err := os.Open(path)
	list := []Account{}
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
	mu.Lock()
	ver++
	st["version"] = ver
	accs = list
	stat = st
	loadedPath = path
	if info != nil {
		loadedSize = info.Size()
		loadedMod = info.ModTime().UnixNano()
	} else {
		loadedSize, loadedMod = 0, 0
	}
	mu.Unlock()
}

func labeledField(p string) bool {
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

func parseValidLine(line int, raw string) Account {
	parts := strings.Split(raw, " | ")
	if len(parts) == 1 {
		parts = strings.Split(raw, "|")
	}
	acc := Account{ID: line, Line: line, Raw: raw, Fields: map[string]string{}}
	if len(parts) > 0 {
		acc.Email = strings.TrimSpace(parts[0])
	}
	start := 1
	if len(parts) > 1 && !labeledField(parts[1]) {
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
	acc.Tags = deriveTags(acc)
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

func deriveTags(a Account) []string {
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
	if payPalMatches(a) {
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

func payPalMatches(a Account) bool {
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

func accountMatches(a Account, terms []string) bool {
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

func HandleAccounts(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	maybeReload()
	mu.Lock()
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
	// Fast path — no search/tag/country/sort: page straight out of the
	// cached slice instead of rebuilding and reallocating a match list for
	// every page request.
	if q == "" && tag == "" && country == "" && sortKey == "" {
		total := len(accs)
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
		if start > total {
			start = total
		}
		end := start + limit
		if end > total {
			end = total
		}
		pageAccs := append([]Account(nil), accs[start:end]...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": pageAccs, "count": total, "page": page, "pages": pages, "limit": limit})
		return
	}
	out := make([]Account, 0, len(accs))
	terms := strings.Fields(q)
	for _, a := range accs {
		if len(terms) > 0 && !accountMatches(a, terms) {
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
	mu.Unlock()
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

func HandleStats(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	maybeReload()
	mu.Lock()
	st := stat
	mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

func HandleReload(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	Load()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func HandleEvents(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, ok := w.(stdhttp.Flusher)
	if !ok {
		return
	}
	path := ValidTxtPath()
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

func HandleValidList(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	path := ValidTxtPath()
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
