package nettls

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"net/url"
	"regexp"
	"strings"
)

func CookieValue(jar http.CookieJar, rawURL, name string) string {
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

func ExtractJSONString(src, key string) string {
	// Prefer real JSON
	var obj map[string]interface{}
	if json.Unmarshal([]byte(src), &obj) == nil {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ExtractParam(src, `"`+key+`":"`)
}

func JSONIntFrom(src, key string) (int, bool) {
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

func MustParseURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

func ExtractPPFT(htmlContent string) string {
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

func ExtractParam(urlStr, key string) string {
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

func ParseBetween(s, start, end string) string {
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
