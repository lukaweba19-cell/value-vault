// Package promos claims still-available Game Pass partner offers and
// formats promo/claimed-promo lines for hits.
//
// Display names come only from the hand-maintained knownTitles map (offer id
// -> title). Unknown ids are written as the raw resource (code or link) so a
// new offer never shows a guessed name; add its id here to get a nice title.
package promos

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"net/url"
	"strings"
	"time"

	"xbox-checker/internal/nettls"
)

var knownTitles = map[string]string{
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

// Title returns the display name for an offer id from the hand-maintained
// knownTitles map. Unknown ids return "" so the caller writes the raw
// resource (code or link) instead of a guessed name.
func Title(offerID string) string {
	return knownTitles[strings.ToUpper(strings.TrimSpace(offerID))]
}

// formatLine renders one promo entry: "Title [resource]" when the offer id
// is in knownTitles, otherwise the raw resource (code or link) as-is.
func formatLine(offerID, resource string) string {
	if t := Title(offerID); t != "" {
		return fmt.Sprintf("%s [%s]", t, resource)
	}
	// Unknown offer id: write the raw resource instead of a guessed name.
	// Add the id to knownTitles to get a pretty title.
	return resource
}

// CheckGamePass claims only still-available Game Pass partner offers
// and returns "Promos: Name [code/link], ...". Already-claimed offers are skipped.
func CheckGamePass(email, password, proxyStr string) string {
	preset := nettls.NextPreset()
	client, noRedir := nettls.NewPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.Profile)
	if client == nil || noRedir == nil {
		return ""
	}
	ua := preset.UA

	pre := "https://login.live.com/oauth20_authorize.srf?client_id=00000000402B5328&redirect_uri=https://login.live.com/oauth20_desktop.srf&response_type=token&scope=service::user.auth.xboxlive.com::MBI_SSL"
	req, err := http.NewRequest("GET", pre, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	resp, err := nettls.Do(client, req)
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := nettls.ExtractPPFT(string(b))
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
	resp2, err := nettls.Do(client, req2)
	if err != nil {
		return ""
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	authURL := pre + "&prompt=none"
	req3, _ := http.NewRequest("GET", authURL, nil)
	req3.Header.Set("User-Agent", ua)
	resp3, err := nettls.Do(noRedir, req3)
	if err != nil {
		return ""
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	if loc == "" {
		loc = resp3.Request.URL.String()
	}
	msa := nettls.ExtractParam(loc+"\n"+string(b3), "access_token=")
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
	respU, err := nettls.Do(client, reqU)
	if err != nil {
		return ""
	}
	bu, _ := io.ReadAll(respU.Body)
	respU.Body.Close()
	xToken := nettls.ExtractJSONString(string(bu), "Token")
	uhs := nettls.ExtractParam(string(bu), `"uhs":"`)
	if xToken == "" || uhs == "" {
		return ""
	}

	xstsBody := fmt.Sprintf(`{"Properties":{"SandboxId":"RETAIL","UserTokens":[%q]},"RelyingParty":"http://xboxlive.com","TokenType":"JWT"}`, xToken)
	reqX, _ := http.NewRequest("POST", "https://xsts.auth.xboxlive.com/xsts/authorize", strings.NewReader(xstsBody))
	reqX.Header.Set("Content-Type", "application/json")
	reqX.Header.Set("Accept", "application/json")
	respX, err := nettls.Do(client, reqX)
	if err != nil {
		return ""
	}
	bx, _ := io.ReadAll(respX.Body)
	respX.Body.Close()
	xsts := nettls.ExtractJSONString(string(bx), "Token")
	if xsts == "" {
		return ""
	}
	xbl := "XBL3.0 x=" + uhs + ";" + xsts

	reqO, _ := http.NewRequest("GET", "https://profile.gamepass.com/v2/offers", nil)
	reqO.Header.Set("authorization", xbl)
	reqO.Header.Set("Accept", "application/json")
	respO, err := nettls.Do(client, reqO)
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
	add := func(dst *[]string, offerID, resource string) {
		resource = strings.TrimSpace(resource)
		if resource == "" || seen[resource] {
			return
		}
		seen[resource] = true
		*dst = append(*dst, formatLine(offerID, resource))
	}
	for _, raw := range arr {
		off, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := off["offerStatus"].(string)
		res, _ := off["resource"].(string)
		oid, _ := off["offerId"].(string)
		switch strings.ToLower(status) {
		case "claimed":
			add(&claimed, oid, res)
		case "available":
			if oid == "" {
				continue
			}
			reqC, _ := http.NewRequest("POST", "https://profile.gamepass.com/v2/offers/"+oid, strings.NewReader("{}"))
			reqC.Header.Set("authorization", xbl)
			reqC.Header.Set("Content-Type", "application/json")
			reqC.Header.Set("Accept", "application/json")
			respC, err := nettls.Do(client, reqC)
			if err != nil {
				continue
			}
			bc, _ := io.ReadAll(respC.Body)
			respC.Body.Close()
			resource := nettls.ExtractJSONString(string(bc), "resource")
			if resource == "" {
				resource = res
			}
			add(&fresh, oid, resource)
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
