// Package nettls wraps bogdanfinn/tls-client with browser fingerprint
// presets, proxy URL normalization and shared response/HTML extractors.
package nettls

import (
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Microsoft login endpoints shared by the Xbox, Epic, promos and mail flows.
const (
	MSAuthPreflightURL = "https://login.live.com/oauth20_authorize.srf?redirect_uri=https://sisu.xboxlive.com/connect/oauth/XboxLive&response_type=code&client_id=000000004420578E&scope=XboxLive.Signin%20+XboxLive.offline_access&lw=1&fl=dob,easi2&xsup=1&nopa=2"
	MSLoginPostURL     = "https://login.live.com/ppsecure/post.srf?nopa=2&client_id=000000004420578E"
	MSOAuthAuthorize   = "https://login.live.com/oauth20_authorize.srf?client_id=000000000004773A&response_type=token&scope=PIFD.Read+PIFD.Create+PIFD.Update+PIFD.Delete&redirect_uri=https%3A%2F%2Faccount.microsoft.com%2Fauth%2Fcomplete-silent-delegate-auth&prompt=none"
)

func isPort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func proxyUserinfo(user, pass string) string {
	if pass == "" {
		return url.User(user).String()
	}
	return url.UserPassword(user, pass).String()
}

// FormatURL turns every common list format into a URL tls-client can use
// as an HTTP CONNECT proxy (HAProxy http/connect mode) or SOCKS.
//
// Accepted:
//
//	http://user:pass@host:port
//	socks5://user:pass@host:port
//	user:pass@host:port
//	host:port
//	host:port:user:pass
//	user:pass:host:port
func FormatURL(proxyStr string) string {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return ""
	}
	proxyStr = strings.TrimPrefix(proxyStr, "\ufeff")

	scheme := "http"
	low := strings.ToLower(proxyStr)
	switch {
	case strings.HasPrefix(low, "https://"):
		scheme = "http" // CONNECT still speaks HTTP to the proxy
		proxyStr = proxyStr[len("https://"):]
	case strings.HasPrefix(low, "http://"):
		scheme = "http"
		proxyStr = proxyStr[len("http://"):]
	case strings.HasPrefix(low, "socks5h://"):
		scheme = "socks5"
		proxyStr = proxyStr[len("socks5h://"):]
	case strings.HasPrefix(low, "socks5://"):
		scheme = "socks5"
		proxyStr = proxyStr[len("socks5://"):]
	case strings.HasPrefix(low, "socks4://"):
		scheme = "socks4"
		proxyStr = proxyStr[len("socks4://"):]
	}

	host, port, user, pass := "", "", "", ""

	if at := strings.LastIndex(proxyStr, "@"); at >= 0 {
		cred := proxyStr[:at]
		hp := proxyStr[at+1:]
		if i := strings.Index(cred, ":"); i >= 0 {
			user, pass = cred[:i], cred[i+1:]
		} else {
			user = cred
		}
		host, port = splitHostPort(hp)
	} else {
		parts := strings.Split(proxyStr, ":")
		switch len(parts) {
		case 2:
			host, port = parts[0], parts[1]
		case 3:
			// host:port:user
			if isPort(parts[1]) {
				host, port, user = parts[0], parts[1], parts[2]
			} else if isPort(parts[2]) {
				user, host, port = parts[0], parts[1], parts[2]
			} else {
				host, port = parts[0], parts[1]
			}
		case 4:
			if isPort(parts[1]) {
				// host:port:user:pass
				host, port, user, pass = parts[0], parts[1], parts[2], parts[3]
			} else if isPort(parts[3]) {
				// user:pass:host:port
				user, pass, host, port = parts[0], parts[1], parts[2], parts[3]
			} else {
				host, port = parts[0], parts[1]
			}
		default:
			if len(parts) > 4 && isPort(parts[1]) {
				host, port = parts[0], parts[1]
				user = parts[2]
				pass = strings.Join(parts[3:], ":")
			} else if len(parts) > 4 && isPort(parts[len(parts)-1]) {
				port = parts[len(parts)-1]
				host = parts[len(parts)-2]
				user = parts[0]
				pass = strings.Join(parts[1:len(parts)-2], ":")
			} else {
				return scheme + "://" + proxyStr
			}
		}
	}

	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	addr := host
	if port != "" {
		addr = net.JoinHostPort(host, port)
	}
	if user != "" {
		return scheme + "://" + proxyUserinfo(user, pass) + "@" + addr
	}
	return scheme + "://" + addr
}

func splitHostPort(hp string) (string, string) {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return h, p
	}
	// host only, or host:port that failed (e.g. missing port)
	if i := strings.LastIndex(hp, ":"); i > 0 && isPort(hp[i+1:]) {
		return hp[:i], hp[i+1:]
	}
	return hp, ""
}

// Browser JA3/H2 fingerprints shipped by bogdanfinn/tls-client v1.7.x.
// Each worker starts on a different Profile; each new client on that worker
// advances to the next fingerprint so traffic is not pinned to one Chrome build.
type Preset struct {
	Profile profiles.ClientProfile
	UA      string
}

var presets = []Preset{
	{profiles.Chrome_117, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36"},
	{profiles.Chrome_120, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
	{profiles.Chrome_124, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"},
	{profiles.Firefox_117, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:117.0) Gecko/20100101 Firefox/117.0"},
	{profiles.Firefox_120, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0"},
	{profiles.Firefox_123, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:123.0) Gecko/20100101 Firefox/123.0"},
	{profiles.Safari_16_0, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Safari/605.1.15"},
	{profiles.Safari_IOS_16_0, "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1"},
	{profiles.Safari_IOS_17_0, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{profiles.Opera_91, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 OPR/91.0.4516.20"},
}

var tlsProfileIdx uint64

type FP struct {
	id int
	n  uint64
}

func NewWorkerFP(id int) *FP {
	return &FP{id: id}
}

func (w *FP) NextPreset() Preset {
	if w == nil {
		return NextPreset()
	}
	if len(presets) == 0 {
		return Preset{Profile: profiles.DefaultClientProfile, UA: profileUserAgent(profiles.DefaultClientProfile)}
	}
	i := atomic.AddUint64(&w.n, 1) - 1
	return presets[int((uint64(w.id)+i)%uint64(len(presets)))]
}

func (w *FP) NextProfile() profiles.ClientProfile {
	return w.NextPreset().Profile
}

func NextPreset() Preset {
	if len(presets) == 0 {
		return Preset{Profile: profiles.DefaultClientProfile, UA: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"}
	}
	i := atomic.AddUint64(&tlsProfileIdx, 1) - 1
	return presets[int(i%uint64(len(presets)))]
}

func NextProfile() profiles.ClientProfile {
	return NextPreset().Profile
}

func profileUserAgent(p profiles.ClientProfile) string {
	// ClientProfile contains maps and is not comparable with ==.
	// The library exposes the hello id via GetClientHelloStr().
	id := p.GetClientHelloStr()
	for _, pre := range presets {
		if pre.Profile.GetClientHelloStr() == id {
			return pre.UA
		}
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
}

func NewClient(proxyStr string, timeout time.Duration, followRedirect bool) tls_client.HttpClient {
	return NewClientProfile(proxyStr, timeout, followRedirect, NextProfile())
}

func NewClientProfile(proxyStr string, timeout time.Duration, followRedirect bool, Profile profiles.ClientProfile) tls_client.HttpClient {
	jar := tls_client.NewCookieJar()
	secs := int(timeout / time.Second)
	if secs < 1 {
		secs = 5
	}
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(secs),
		tls_client.WithClientProfile(Profile),
		tls_client.WithCookieJar(jar),
		tls_client.WithInsecureSkipVerify(),
		tls_client.WithRandomTLSExtensionOrder(),
	}
	if !followRedirect {
		opts = append(opts, tls_client.WithNotFollowRedirects())
	}
	if p := FormatURL(proxyStr); p != "" {
		opts = append(opts, tls_client.WithProxyUrl(p))
	}
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		opts[1] = tls_client.WithClientProfile(profiles.DefaultClientProfile)
		c, err = tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
		if err != nil {
			return nil
		}
	}
	return c
}

func NewPair(proxyStr string, followTimeout, noRedirTimeout time.Duration) (tls_client.HttpClient, tls_client.HttpClient) {
	return NewPairProfile(proxyStr, followTimeout, noRedirTimeout, NextProfile())
}

func NewPairProfile(proxyStr string, followTimeout, noRedirTimeout time.Duration, Profile profiles.ClientProfile) (tls_client.HttpClient, tls_client.HttpClient) {
	follow := NewClientProfile(proxyStr, followTimeout, true, Profile)
	noRedir := NewClientProfile(proxyStr, noRedirTimeout, false, Profile)
	if follow == nil || noRedir == nil {
		return follow, noRedir
	}
	noRedir.SetCookieJar(follow.GetCookieJar())
	return follow, noRedir
}

func newProxyClient(proxyStr string, timeout time.Duration, followRedirect bool) tls_client.HttpClient {
	return NewClient(proxyStr, timeout, followRedirect)
}

func Do(c tls_client.HttpClient, req *http.Request) (*http.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("tls client is nil")
	}
	return c.Do(req)
}
