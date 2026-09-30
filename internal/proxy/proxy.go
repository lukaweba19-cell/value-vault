// Package proxy loads proxy lists, fetches proxies from server URLs and keeps
// live pools that health-check proxies in the background.
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"xbox-checker/internal/core"
	"xbox-checker/internal/nettls"
)

var reProxyHost = regexp.MustCompile(`(?i)(?:(?:https?|socks[45]h?)://)?(?:[^\s:@]+(?::[^\s@]*)?@)?((?:\d{1,3}\.){3}\d{1,3}|[a-z0-9._\-]+):(\d{2,5})`)

func Load(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	// Treat as proxy list file if it ends with .txt or the path exists as a file
	if strings.HasSuffix(strings.ToLower(input), ".txt") || core.FileExists(input) {
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
			if n := nettls.FormatURL(line); n != "" {
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

type Pool struct {
	mu   sync.Mutex
	all  []string
	live []string
	dead []string
	i    int
}

var (
	pxPools   = map[string]*Pool{}
	pxPoolsMu sync.Mutex
)

func EnsurePool(key string, list []string) *Pool {
	pxPoolsMu.Lock()
	p := pxPools[key]
	if p == nil {
		p = &Pool{all: append([]string{}, list...)}
		pxPools[key] = p
		go testPool(key)
	} else if len(list) > 0 && len(list) != len(p.all) {
		p.mu.Lock()
		p.all = append([]string{}, list...)
		p.mu.Unlock()
	}
	pxPoolsMu.Unlock()
	return p
}

func TakeLive(key string, list []string) string {
	if len(list) == 0 {
		return ""
	}
	p := EnsurePool(key, list)
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

func testPool(key string) {
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
				ok := probe(px)
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

func probe(px string) bool {
	c := nettls.NewClient(px, 6*time.Second, true)
	if c == nil {
		return false
	}
	req, err := http.NewRequest("GET", "https://login.live.com/", nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := nettls.Do(c, req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode > 0 && resp.StatusCode < 500
}

func ParseLines(body string) []string {
	body = strings.TrimSpace(body)
	seen := map[string]struct{}{}
	var proxies []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if n := nettls.FormatURL(s); n != "" {
			s = n
		} else if m := reProxyHost.FindString(s); m != "" {
			if n := nettls.FormatURL(m); n != "" {
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
						proxies = append(proxies, ParseLines(string(b))...)
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

func FetchFromURLs(urls []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	cli := nettls.NewClient("", 25*time.Second, true)
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
		for _, p := range ParseLines(body) {
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
