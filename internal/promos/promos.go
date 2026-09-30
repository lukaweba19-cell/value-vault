// Package promos claims still-available Game Pass partner offers and
// formats promo/claimed-promo lines for hits.
package promos

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"net/url"
	"regexp"
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

// Titles pulled from offer API / claim response when id is not in the map yet.
var titleHints = []struct {
	sub   string
	title string
}{
	{"delta force", "Delta Force - Season 11 Benefits Pack"},
	{"deltaforce", "Delta Force - Season 11 Benefits Pack"},
}

func ExtractOfferTitle(m map[string]interface{}) string {
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
			if t := ExtractOfferTitle(sub); t != "" {
				return t
			}
		}
	}
	return ""
}

func isGenericTitle(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	switch low {
	case "", "store token", "token", "perk", "offer", "product", "null", "undefined", "xbox store code", "store":
		return true
	}
	return false
}

func Title(offerID, resource, apiTitle string) string {
	apiTitle = strings.TrimSpace(apiTitle)
	offerID = strings.ToUpper(strings.TrimSpace(offerID))
	// Prefer explicit ID map first (covers store-token style claims)
	if t, ok := knownTitles[offerID]; ok {
		return t
	}
	if apiTitle != "" && !isGenericTitle(apiTitle) {
		low := strings.ToLower(apiTitle)
		for _, h := range titleHints {
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
	add := func(dst *[]string, offerID, resource, apiTitle string) {
		resource = strings.TrimSpace(resource)
		if resource == "" || seen[resource] {
			return
		}
		seen[resource] = true
		*dst = append(*dst, fmt.Sprintf("%s [%s]", Title(offerID, resource, apiTitle), resource))
	}
	for _, raw := range arr {
		off, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := off["offerStatus"].(string)
		res, _ := off["resource"].(string)
		oid, _ := off["offerId"].(string)
		apiTitle := ExtractOfferTitle(off)
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
			respC, err := nettls.Do(client, reqC)
			if err != nil {
				continue
			}
			bc, _ := io.ReadAll(respC.Body)
			respC.Body.Close()
			var claim map[string]interface{}
			_ = json.Unmarshal(bc, &claim)
			resource := nettls.ExtractJSONString(string(bc), "resource")
			if resource == "" {
				resource = res
			}
			claimTitle := ExtractOfferTitle(claim)
			if claimTitle == "" {
				claimTitle = apiTitle
			}
			// Persist title by offer id when we learn it
			if claimTitle != "" && oid != "" {
				knownTitles[strings.ToUpper(oid)] = Title(oid, resource, claimTitle)
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
