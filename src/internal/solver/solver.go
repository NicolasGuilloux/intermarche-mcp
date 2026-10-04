// Package solver provides a browser-free http.RoundTripper that obtains a
// cleared Datadome cookie through a captcha-solving service.
//
// Cost control is the priority: a paid solve is only ever triggered when an
// API call actually returns a 403 Datadome captcha AND no cached cookie works.
// The solved cookie is persisted to disk and reused across runs, so steady
// state costs zero solves.
package solver

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nover/intermarche-mcp/internal/config"
	"github.com/nover/intermarche-mcp/internal/solver/microproxy"
)

const (
	target = "https://www.intermarche.com/accueil"
	// A desktop Chrome UA; must stay consistent between the solve and the
	// subsequent API calls (Datadome cross-checks the cookie against the UA).
	defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
)

// cache is the on-disk state reused across runs to avoid paid solves.
type cache struct {
	Datadome string `json:"datadome"`
	DeviceID string `json:"device_id"` // stable itm_device_id, generated once
	Solved   string `json:"solved_at"` // RFC3339 timestamp of the last solve
}

// Transport implements http.RoundTripper using a captcha-solver cookie.
type Transport struct {
	base      *http.Transport
	resolver  Resolver
	userAgent string

	proxy string // shared HTTP/S proxy for challenge fetch + API calls

	mu        sync.Mutex
	cache     cache
	cachePath string
	solves    []time.Time // solves started within the last solveWindow
	maxSolves int         // cap per solveWindow to protect the account balance
}

// proxyURL reads the residential proxy used for BOTH the challenge fetch and
// the API calls, so the Datadome cid and its usage share one IP (required for
// IP-locked Datadome configs like intermarche.com). Empty = direct.
func proxyURL() string {
	// CAPTCHA_PROXY is the canonical name; IMT_PROXY is a deprecated fallback.
	for _, k := range []string{"CAPTCHA_PROXY", "IMT_PROXY"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// ephemeralProxy reads the settings of the single-use proxy served to the
// captcha provider for the duration of a solve. CAPTCHA_PROXY_LISTEN is the
// local bind address and CAPTCHA_PROXY_ADVERTISE the public host:port that
// reaches it, which the caller is responsible for routing — unless it is
// "ngrok", in which case the tunnel is opened per solve and needs no bind
// address at all. Nil = disabled.
func ephemeralProxy() (*microproxy.Config, error) {
	listen := strings.TrimSpace(os.Getenv("CAPTCHA_PROXY_LISTEN"))
	advertise := strings.TrimSpace(os.Getenv("CAPTCHA_PROXY_ADVERTISE"))
	if listen == "" && advertise == "" {
		return nil, nil
	}
	if advertise == "" {
		return nil, fmt.Errorf("solver: CAPTCHA_PROXY_LISTEN is set but CAPTCHA_PROXY_ADVERTISE is empty — the solver needs the public host:port that reaches %s, or %q to open a tunnel itself", listen, ngrokScheme)
	}
	if listen == "" && advertise != ngrokScheme {
		return nil, fmt.Errorf("solver: CAPTCHA_PROXY_ADVERTISE=%q needs CAPTCHA_PROXY_LISTEN — only %q brings its own listener", advertise, ngrokScheme)
	}
	cfg := &microproxy.Config{Listen: listen, Advertise: advertise}
	// CAPTCHA_PROXY_ALLOW overrides the domains the tunnel may reach.
	if allow := strings.TrimSpace(os.Getenv("CAPTCHA_PROXY_ALLOW")); allow != "" {
		for _, d := range strings.Split(allow, ",") {
			if d = strings.TrimSpace(d); d != "" {
				cfg.Allowed = append(cfg.Allowed, d)
			}
		}
	}
	return cfg, nil
}

// solveWindow is the sliding window CAPTCHA_MAX_SOLVES applies to. A window
// rather than a per-process total, so a long-running MCP server can still
// renew a cookie that expires hours later.
const solveWindow = 5 * time.Minute

// maxSolvesPerWindow caps paid solves per solveWindow. Override with
// CAPTCHA_MAX_SOLVES. Keeps a broken-cookie retry loop from draining funds.
func maxSolvesPerWindow() int {
	if v := strings.TrimSpace(os.Getenv("CAPTCHA_MAX_SOLVES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 1
}

// New builds the transport, loading any previously solved cookie from disk.
// It does NOT solve anything yet (no cost): the first solve happens lazily on
// the first 403 captcha.
func New() (*Transport, error) {
	ua := os.Getenv("IMT_USER_AGENT")
	if ua == "" {
		ua = defaultUA
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}

	base := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	proxy := proxyURL()
	ephemeral, err := ephemeralProxy()
	if err != nil {
		return nil, err
	}
	if ephemeral != nil && proxy != "" {
		return nil, fmt.Errorf("solver: CAPTCHA_PROXY and CAPTCHA_PROXY_LISTEN are mutually exclusive — the API calls would egress through the proxy while the solver egresses from this host, and Datadome rejects that IP mismatch")
	}
	if proxy != "" {
		pu, perr := url.Parse(proxy)
		if perr != nil {
			return nil, fmt.Errorf("solver: invalid proxy URL %q: %w", proxy, perr)
		}
		base.Proxy = http.ProxyURL(pu)
	}

	resolver, err := newResolver(resolverConfig{userAgent: ua, proxy: proxy, ephemeral: ephemeral})
	if err != nil {
		return nil, err
	}

	t := &Transport{
		base:      base,
		resolver:  resolver,
		userAgent: ua,
		proxy:     proxy,
		cachePath: filepath.Join(dir, "datadome.json"),
		maxSolves: maxSolvesPerWindow(),
	}
	t.loadCache()
	if t.cache.DeviceID == "" {
		t.cache.DeviceID = uuid.NewString()
		t.saveCache()
	}
	return t, nil
}

func (t *Transport) deviceFP() string { return "ghost_" + t.cache.DeviceID }

// RoundTrip sends the request with the cached cookie (HTTP/1.1 + Chrome
// headers). On a Datadome 403 it first retries without the cookie (free),
// and only then solves once, caches, and retries.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.do(req)
	if err != nil {
		return nil, err
	}
	captchaURL, isDD := datadomeCaptcha(resp)
	if !isDD {
		return resp, nil
	}
	resp.Body.Close()

	// The cached cookie may be the culprit rather than the IP: DataDome flags
	// the cid itself (a solver-minted cookie can come back t=bv while the same
	// IP without it gets a 200). Dropping it costs nothing, so try that first.
	if sent := sentDatadome(req); sent != "" {
		t.dropCookie(sent)
		fmt.Fprintf(os.Stderr, "solver: Datadome 403 on %s with the cached cookie — dropped it, retrying without\n", req.URL.Path)
		rewind(req)
		if resp, err = t.do(req); err != nil {
			return nil, err
		}
		if captchaURL, isDD = datadomeCaptcha(resp); !isDD {
			return resp, nil
		}
		resp.Body.Close()
	}

	// t=bv without any cookie means DataDome has banned this client outright:
	// a solve still returns a cookie, but it is refused on the very next call,
	// so the credit is wasted.
	if ipBanned(captchaURL) {
		return nil, fmt.Errorf("solver: Datadome blocks this client even without a cookie (challenge t=bv) — solving cannot help, no credit spent; retry from another IP (CAPTCHA_PROXY with a residential FR proxy, or another network)")
	}

	if !t.reserveSolve() {
		return nil, fmt.Errorf("solver: Datadome 403 but solve cap reached (%d per %s) — refusing to spend more credits for now; set CAPTCHA_MAX_SOLVES to raise it (%s)", t.maxSolves, solveWindow, describeBlock(resp, captchaURL))
	}

	fmt.Fprintf(os.Stderr, "solver: Datadome 403 on %s — solving with %s (this costs one credit)\n", req.URL.Path, t.resolver.Name())
	if err := t.solve(Challenge{PageURL: req.URL.String(), CaptchaURL: captchaURL}); err != nil {
		return nil, err
	}

	rewind(req)
	resp, err = t.do(req)
	if err != nil {
		return nil, err
	}
	// A cookie that is refused straight after the solve is not stale: the
	// clearance does not match this request's IP or fingerprint. Handing the
	// bare 403 back would only surface as an opaque status code.
	if captchaURL, isDD := datadomeCaptcha(resp); isDD {
		resp.Body.Close()
		return nil, fmt.Errorf("solver: Datadome still answers 403 with the freshly solved cookie (%s) — the solve and this request must egress from the same IP", describeBlock(resp, captchaURL))
	}
	return resp, nil
}

// rewind restores the request body so the request can be sent again.
func rewind(req *http.Request) {
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			req.Body = body
		}
	}
}

// sentDatadome returns the datadome cookie value the request went out with.
func sentDatadome(req *http.Request) string {
	for _, part := range strings.Split(req.Header.Get("Cookie"), ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "datadome="); ok {
			return v
		}
	}
	return ""
}

// dropCookie forgets the cached cookie, unless another request has already
// replaced it with a newer one.
func (t *Transport) dropCookie(value string) {
	t.mu.Lock()
	if t.cache.Datadome != value {
		t.mu.Unlock()
		return
	}
	t.cache.Datadome = ""
	t.mu.Unlock()
	t.saveCache()
}

// ipBanned reports whether the captcha URL is DataDome's "blocked" variant
// (t=bv), as opposed to a regular challenge (t=fe).
func ipBanned(captchaURL string) bool {
	u, err := url.Parse(captchaURL)
	return err == nil && u.Query().Get("t") == "bv"
}

func (t *Transport) do(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	dd := t.cache.Datadome
	devID := t.cache.DeviceID
	t.mu.Unlock()

	setIfAbsent(req, "user-agent", t.userAgent)
	setIfAbsent(req, "accept", "application/json, text/plain, */*")
	setIfAbsent(req, "accept-language", "fr-FR,fr;q=0.9")
	setIfAbsent(req, "accept-encoding", "identity")
	setIfAbsent(req, "sec-ch-ua", `"Chromium";v="141", "Not?A_Brand";v="24", "Google Chrome";v="141"`)
	setIfAbsent(req, "sec-ch-ua-mobile", "?0")
	setIfAbsent(req, "sec-ch-ua-platform", `"Windows"`)
	setIfAbsent(req, "sec-fetch-dest", "empty")
	setIfAbsent(req, "sec-fetch-mode", "cors")
	setIfAbsent(req, "sec-fetch-site", "same-origin")
	setIfAbsent(req, "origin", "https://www.intermarche.com")
	setIfAbsent(req, "referer", "https://www.intermarche.com/")

	// Cookie: the solved datadome plus a stable itm_device_id so cart sync
	// events aren't dropped (server keys sessions on this fingerprint).
	var cookie strings.Builder
	if dd != "" {
		cookie.WriteString("datadome=")
		cookie.WriteString(dd)
		cookie.WriteString("; ")
	}
	cookie.WriteString(`itm_device_id={"id":"`)
	cookie.WriteString(devID)
	cookie.WriteString(`","active":false}`)
	req.Header.Set("Cookie", cookie.String())

	req.Header.Set("X-ITM-DEVICE-FP", t.deviceFP())
	req.Header.Set("X-ITM-SESSION-ID", t.deviceFP())
	req.Header.Set("x-itm-user-agent", t.userAgent)

	return t.base.RoundTrip(req)
}

// reserveSolve takes a slot in the current window, or reports that none is
// left. The slot is taken before the solve starts so concurrent 403s cannot
// all slip under the cap, and it is kept even if the solve fails: a failed
// solve may still have been billed.
func (t *Transport) reserveSolve() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	recent := t.solves[:0]
	for _, at := range t.solves {
		if now.Sub(at) < solveWindow {
			recent = append(recent, at)
		}
	}
	t.solves = recent
	if len(t.solves) >= t.maxSolves {
		return false
	}
	t.solves = append(t.solves, now)
	return true
}

// solve delegates the DataDome solve to the configured provider and caches the
// returned cookie.
//
// ch describes the 403 that was just received. Its CaptchaURL is the one
// DataDome minted for THAT request, from this process' egress — the same one
// the workers are given — so it is handed straight to the resolver rather than
// being fetched again.
func (t *Transport) solve(ch Challenge) error {
	if ch.PageURL == "" {
		ch.PageURL = target
	}

	solved, err := t.resolver.Solve(ch)
	if err != nil {
		return err
	}
	if solved == "" {
		return fmt.Errorf("solver: %s returned an empty cookie", t.resolver.Name())
	}

	t.mu.Lock()
	t.cache.Datadome = solved
	t.cache.Solved = time.Now().Format(time.RFC3339)
	n := len(t.solves)
	t.mu.Unlock()
	t.saveCache()

	fmt.Fprintf(os.Stderr, "solver: ⚠️  solved (%d credit(s) used in the last %s); cookie cached at %s\n", n, solveWindow, t.cachePath)
	return nil
}

func (t *Transport) loadCache() {
	b, err := os.ReadFile(t.cachePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &t.cache)
}

func (t *Transport) saveCache() {
	t.mu.Lock()
	b, _ := json.MarshalIndent(t.cache, "", "  ")
	path := t.cachePath
	t.mu.Unlock()
	_ = os.WriteFile(path, b, 0600)
}

// IsAlive is always true; the transport is connectionless.
func (t *Transport) IsAlive() bool { return true }

// Close releases idle connections.
func (t *Transport) Close() error {
	t.base.CloseIdleConnections()
	return nil
}

// datadomeCaptcha reports whether resp is a Datadome captcha block and, if so,
// returns the captcha-delivery URL embedded in the JSON body (when present).
// It only treats a 403 as a captcha when the body carries a captcha-delivery
// URL or the x-datadome header is set, so genuine authorization 403s don't
// burn a paid solve. The body is restored so a non-captcha response can still
// be consumed by the caller.
func datadomeCaptcha(resp *http.Response) (string, bool) {
	if resp.StatusCode != http.StatusForbidden {
		return "", false
	}

	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return "", resp.Header.Get("x-datadome") != ""
	}

	var body struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(b, &body) == nil && strings.Contains(body.URL, "captcha-delivery.com") {
		return body.URL, true
	}
	return "", resp.Header.Get("x-datadome") != ""
}

// describeBlock summarises a Datadome 403 for error messages: the challenge
// type (t=fe is a fresh challenge, t=bv a banned IP) and the x-datadome-cid.
func describeBlock(resp *http.Response, captchaURL string) string {
	kind := "-"
	if u, err := url.Parse(captchaURL); err == nil && captchaURL != "" {
		kind = u.Path + " t=" + u.Query().Get("t")
	}
	return fmt.Sprintf("challenge %s, x-datadome=%q", kind, resp.Header.Get("x-datadome"))
}

func setIfAbsent(req *http.Request, key, val string) {
	if req.Header.Get(key) == "" {
		req.Header.Set(key, val)
	}
}
