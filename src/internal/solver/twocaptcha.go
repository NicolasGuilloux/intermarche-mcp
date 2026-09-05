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
	"github.com/nover/intermarche-mcp/internal/solver/microproxy"
)

// twoCaptchaResolver solves Datadome through the 2Captcha service
// (https://2captcha.com).
//
// 2Captcha has no proxied "fetch the challenge" helper, so the challenge is
// fetched from the SAME egress the workers will use: DataDome
// binds the minted cid to that IP and 2Captcha's workers must replay the solve
// from it, so this provider always needs a proxy. Two shapes are supported:
//
//   - CAPTCHA_PROXY: a standing proxy, used for the challenge fetch and handed
//     to the workers.
//   - CAPTCHA_PROXY_LISTEN/_ADVERTISE: a single-use proxy served by this
//     process for the length of the solve (see internal/solver/microproxy).
//     The challenge is then fetched directly, since both legs egress from this
//     host.
type twoCaptchaResolver struct {
	apiKey    string
	userAgent string
	proxy     string
	// ephemeral, when set, replaces the standing proxy: a single-use one is
	// served for the length of a solve and torn down straight after.
	ephemeral *microproxy.Config

	http *http.Client
}

func newTwoCaptchaResolver(cfg resolverConfig) (Resolver, error) {
	apiKey := strings.TrimSpace(os.Getenv("TWOCAPTCHA_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("solver: TWOCAPTCHA_API_KEY is not set")
	}
	if cfg.proxy == "" && cfg.ephemeral == nil {
		return nil, fmt.Errorf("solver: 2captcha requires a proxy so the challenge cid and the solve share one IP — set CAPTCHA_PROXY, or CAPTCHA_PROXY_LISTEN + CAPTCHA_PROXY_ADVERTISE to serve a single-use one")
	}

	tr := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	// With an ephemeral proxy the workers egress from this host, so the
	// challenge is fetched directly — same IP, which is what Datadome binds
	// the cid to.
	if cfg.proxy != "" {
		pu, err := url.Parse(cfg.proxy)
		if err != nil {
			return nil, fmt.Errorf("solver: invalid proxy URL %q: %w", cfg.proxy, err)
		}
		tr.Proxy = http.ProxyURL(pu)
	}

	return &twoCaptchaResolver{
		apiKey:    apiKey,
		userAgent: cfg.userAgent,
		proxy:     cfg.proxy,
		ephemeral: cfg.ephemeral,
		http:      &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}, nil
}

func (r *twoCaptchaResolver) Name() string { return "2captcha" }

func (r *twoCaptchaResolver) Solve(ch Challenge) (string, error) {
	captchaURL, err := r.captchaURL(ch)
	if err != nil {
		return "", err
	}

	access, aerr := r.workerAccess()
	if aerr != nil {
		return "", aerr
	}
	defer access.close()

	client := api2captcha.NewClient(r.apiKey)
	dd := api2captcha.DataDome{
		Url:        ch.PageURL,
		CaptchaUrl: captchaURL,
		UserAgent:  r.userAgent,
		Proxytype:  access.proxyType,
		Proxy:      access.auth,
	}

	code, _, err := client.Solve(dd.ToRequest())
	if err != nil {
		return "", access.explain(err)
	}
	if code == "" {
		return "", fmt.Errorf("solver: 2captcha returned an empty cookie")
	}
	return stripDatadome(code), nil
}

// workerAccess is how 2Captcha's workers reach intermarche.com for one solve:
// a standing proxy, or a single-use one — itself optionally behind an ngrok
// tunnel opened for that solve alone.
type workerAccess struct {
	proxyType string
	auth      string
	proxy     *microproxy.Proxy // nil when a standing CAPTCHA_PROXY is used
	tunnel    *ngrokTunnel      // nil unless the tunnel is managed here
}

// close destroys everything that was stood up for the solve, in the order that
// stops new connections first.
func (w *workerAccess) close() {
	if w.proxy != nil {
		tunnels, rejected := w.proxy.Stats()
		_ = w.proxy.Close()
		fmt.Fprintf(os.Stderr, "solver: single-use proxy closed (%d tunnel(s), %d request(s) refused)\n", tunnels, rejected)
	}
	if w.tunnel != nil {
		w.tunnel.Close()
		fmt.Fprintln(os.Stderr, "solver: ngrok tunnel closed")
	}
}

// explain turns a failed solve into the most useful message available: a
// single-use proxy that saw no tunnel at all means the workers never got in,
// which is a reachability problem rather than a solving one.
func (w *workerAccess) explain(err error) error {
	if w.proxy != nil {
		if tunnels, _ := w.proxy.Stats(); tunnels == 0 {
			if w.tunnel != nil {
				return fmt.Errorf("solver: the ngrok tunnel %s was open but no 2captcha worker came through it: %w", w.proxy.Endpoint(), err)
			}
			return fmt.Errorf("solver: 2captcha never connected to the single-use proxy advertised as %s — check that address is reachable from the internet and forwards to %s: %w", w.proxy.Endpoint(), w.proxy.Addr(), err)
		}
	}
	return fmt.Errorf("solver: 2captcha could not solve intermarche's Datadome challenge: %w — check the proxy is residential/FR and matches the challenge IP", err)
}

// workerAccess prepares that access. With CAPTCHA_PROXY_LISTEN it stands up a
// single-use proxy: fresh credentials, a domain allow list, and a listener that
// dies with the solve. With CAPTCHA_PROXY_ADVERTISE=ngrok it also opens the
// tunnel that lets the workers in, and closes it on the way out.
func (r *twoCaptchaResolver) workerAccess() (*workerAccess, error) {
	if r.ephemeral == nil {
		proxyType, auth, err := splitProxy(r.proxy)
		if err != nil {
			return nil, err
		}
		return &workerAccess{proxyType: proxyType, auth: auth}, nil
	}

	cfg := *r.ephemeral
	access := &workerAccess{proxyType: "HTTP"}

	switch api, external := ngrokAPI(cfg.Advertise); {
	case cfg.Advertise == ngrokScheme:
		// The tunnel is the listener: nothing to bind, nothing to forward.
		tunnel, err := startNgrokTunnel()
		if err != nil {
			return nil, err
		}
		access.tunnel = tunnel
		cfg.Listener = tunnel.listener
		cfg.Advertise = tunnel.endpoint
		fmt.Fprintf(os.Stderr, "solver: ngrok tunnel opened on %s\n", tunnel.endpoint)
	case external:
		// An agent someone else runs: its address changes on every restart, so
		// it is resolved fresh for each solve.
		endpoint, err := ngrokEndpoint(api, cfg.Listen)
		if err != nil {
			return nil, err
		}
		cfg.Advertise = endpoint
	}

	p, err := microproxy.Start(cfg)
	if err != nil {
		access.close()
		return nil, fmt.Errorf("solver: %w", err)
	}
	login, password := p.Credentials()
	access.proxy = p
	access.auth = login + ":" + password + "@" + p.Endpoint()
	fmt.Fprintf(os.Stderr, "solver: single-use proxy listening on %s, advertised to 2captcha as %s\n", p.Addr(), p.Endpoint())
	return access, nil
}

// captchaURL returns the geo.captcha-delivery.com URL 2Captcha needs.
//
// The 403 that triggered the solve normally carries it, and that one is
// authoritative: DataDome mints a challenge per request, so refetching PageURL
// would only work if the same method and headers were replayed — a GET on a
// POST-only API route answers 405 and no challenge at all. Fetching is the
// fallback for a 403 that announced itself through the x-datadome header alone.
func (r *twoCaptchaResolver) captchaURL(ch Challenge) (string, error) {
	if ch.CaptchaURL != "" {
		return canonicalSliderURL(ch.CaptchaURL)
	}
	return r.fetchCaptchaURL(ch.PageURL)
}

// fetchCaptchaURL fetches the challenge from the egress the workers will use
// (the standing proxy, or this host when the proxy is ephemeral) and extracts
// the geo.captcha-delivery.com captcha URL 2Captcha needs.
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
	return "", fmt.Errorf("solver: GET %s returned %d with no Datadome challenge, and the 403 that triggered the solve carried no captcha URL — nothing to solve", challengeURL, resp.StatusCode)
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
