package solver

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	api2captcha "github.com/2captcha/2captcha-go"
)

// twoCaptchaResolver solves Datadome through the 2Captcha service
// (https://2captcha.com).
//
// Unlike Salamoonder, 2Captcha has no proxied "fetch the challenge" helper, so
// we fetch the challenge ourselves through the SAME proxy we hand to 2Captcha:
// DataDome binds the minted cid to that egress IP and 2Captcha's workers must
// replay the solve from it, so a proxy is required for this provider.
type twoCaptchaResolver struct {
	apiKey    string
	userAgent string
	proxy     string

	http *http.Client
}

func newTwoCaptchaResolver(cfg resolverConfig) (Resolver, error) {
	apiKey := strings.TrimSpace(os.Getenv("TWOCAPTCHA_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("solver: TWOCAPTCHA_API_KEY is not set")
	}
	if cfg.proxy == "" {
		return nil, fmt.Errorf("solver: 2captcha requires a proxy so the challenge cid and the solve share one IP — set CAPTCHA_PROXY")
	}

	tr := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	pu, err := url.Parse(cfg.proxy)
	if err != nil {
		return nil, fmt.Errorf("solver: invalid proxy URL %q: %w", cfg.proxy, err)
	}
	tr.Proxy = http.ProxyURL(pu)

	return &twoCaptchaResolver{
		apiKey:    apiKey,
		userAgent: cfg.userAgent,
		proxy:     cfg.proxy,
		http:      &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}, nil
}

func (r *twoCaptchaResolver) Name() string { return "2captcha" }

func (r *twoCaptchaResolver) Solve(challengeURL string) (string, error) {
	captchaURL, err := r.fetchCaptchaURL(challengeURL)
	if err != nil {
		return "", err
	}

	proxyType, proxyAuth, perr := splitProxy(r.proxy)
	if perr != nil {
		return "", perr
	}

	client := api2captcha.NewClient(r.apiKey)
	dd := api2captcha.DataDome{
		Url:        challengeURL,
		CaptchaUrl: captchaURL,
		UserAgent:  r.userAgent,
		Proxytype:  proxyType,
		Proxy:      proxyAuth,
	}

	code, _, err := client.Solve(dd.ToRequest())
	if err != nil {
		return "", fmt.Errorf("solver: 2captcha could not solve intermarche's Datadome challenge: %w — check the proxy is residential/FR and matches the challenge IP, or switch provider (CAPTCHA_PROVIDER=salamoonder)", err)
	}
	if code == "" {
		return "", fmt.Errorf("solver: 2captcha returned an empty cookie")
	}
	return stripDatadome(code), nil
}

// fetchCaptchaURL fetches the challenge through the proxy and extracts the
// geo.captcha-delivery.com captcha URL 2Captcha needs.
func (r *twoCaptchaResolver) fetchCaptchaURL(challengeURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, challengeURL, nil)
	if err != nil {
		return "", fmt.Errorf("solver: build challenge request: %w", err)
	}
	req.Header.Set("User-Agent", r.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")

	resp, err := r.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("solver: fetch challenge: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	// API JSON 403: {"url":"https://geo.captcha-delivery.com/captcha/?..."}.
	var body struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &body) == nil && strings.Contains(body.URL, "captcha-delivery.com") {
		return canonicalSliderURL(body.URL)
	}
	return "", fmt.Errorf("solver: no Datadome challenge present (status %d) — nothing to solve", resp.StatusCode)
}

// splitProxy turns a "scheme://user:pass@host:port" URL into the (type, auth)
// pair 2Captcha expects: type is upper-case (HTTP/HTTPS/SOCKS4/SOCKS5) and
// auth is "user:pass@host:port" (no scheme).
func splitProxy(raw string) (proxyType, auth string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("solver: invalid proxy URL %q: %w", raw, err)
	}
	proxyType = strings.ToUpper(u.Scheme)
	if proxyType == "" {
		proxyType = "HTTP"
	}
	auth = u.Host
	if u.User != nil {
		auth = u.User.String() + "@" + u.Host
	}
	return proxyType, auth, nil
}
