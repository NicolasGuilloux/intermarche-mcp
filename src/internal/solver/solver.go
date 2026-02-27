// Package solver provides a browser-free http.RoundTripper that obtains a
// cleared Datadome cookie through the Salamoonder captcha-solving service.
//
// Cost control is the priority: a paid solve is only ever triggered when an
// API call actually returns a 403 Datadome captcha AND no cached cookie works.
// The solved cookie is persisted to disk and reused across runs, so steady
// state costs zero solves.
package solver

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	salamoonder "github.com/salamoonder-llc/salamoonder-go"

	"github.com/google/uuid"
	"github.com/nover/intermarche-mcp/internal/config"
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

// Transport implements http.RoundTripper using a Salamoonder-solved cookie.
type Transport struct {
	base      *http.Transport
	apiKey    string
	userAgent string

	proxy string // shared HTTP/S proxy for challenge fetch + API calls

	mu         sync.Mutex
	cache      cache
	cachePath  string
	solveCount int
	maxSolves  int // hard cap per process to protect the account balance
}

// proxyURL reads the residential proxy used for BOTH the challenge fetch and
// the API calls, so the Datadome cid and its usage share one IP (required for
// IP-locked Datadome configs like intermarche.com). Empty = direct.
func proxyURL() string {
	for _, k := range []string{"SALAMOONDER_PROXY", "IMT_PROXY"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// maxSolvesPerRun caps paid solves in a single process run. Override with
// SALAMOONDER_MAX_SOLVES. Keeps a broken-cookie retry loop from draining funds.
func maxSolvesPerRun() int {
	if v := strings.TrimSpace(os.Getenv("SALAMOONDER_MAX_SOLVES")); v != "" {
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
	apiKey := strings.TrimSpace(os.Getenv("SALAMOONDER_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("solver: SALAMOONDER_API_KEY is not set")
	}

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
	if proxy != "" {
		pu, perr := url.Parse(proxy)
		if perr != nil {
			return nil, fmt.Errorf("solver: invalid proxy URL %q: %w", proxy, perr)
		}
		base.Proxy = http.ProxyURL(pu)
	}

	t := &Transport{
		base:      base,
		apiKey:    apiKey,
		userAgent: ua,
		proxy:     proxy,
		cachePath: filepath.Join(dir, "datadome.json"),
		maxSolves: maxSolvesPerRun(),
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
// headers). On a Datadome 403 captcha it solves once, caches, and retries.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.do(req)
	if err != nil {
		return nil, err
	}
	if !isDatadomeCaptcha(resp) {
		return resp, nil
	}

	// Stale/missing cookie — spend one solve, then retry once.
	resp.Body.Close()

	t.mu.Lock()
	over := t.solveCount >= t.maxSolves
	t.mu.Unlock()
	if over {
		return nil, fmt.Errorf("solver: Datadome 403 but solve cap reached (%d) — refusing to spend more credits this run; set SALAMOONDER_MAX_SOLVES to raise it", t.maxSolves)
	}

	fmt.Fprintf(os.Stderr, "solver: Datadome 403 on %s — solving (this costs one Salamoonder credit)\n", req.URL.Path)
	if err := t.solve(); err != nil {
		return nil, err
	}

	if req.GetBody != nil {
		body, bErr := req.GetBody()
		if bErr == nil {
			req.Body = body
		}
	}
	return t.do(req)
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

// solve runs the full Salamoonder DataDome slider flow and caches the cookie.
func (t *Transport) solve() error {
	client, err := salamoonder.New(t.apiKey)
	if err != nil {
		return fmt.Errorf("solver: %w", err)
	}

	hdr := map[string]string{"User-Agent": t.userAgent}
	// Fetch the challenge through the SAME proxy used for API calls so the
	// minted Datadome cid is bound to that egress IP.
	resp, err := client.Get(target, &salamoonder.RequestOptions{Headers: hdr, Proxy: t.proxy})
	if err != nil {
		return fmt.Errorf("solver: fetch challenge: %w", err)
	}
	if resp.StatusCode == 200 && !strings.Contains(resp.Text, "var dd=") {
		return fmt.Errorf("solver: no challenge present (status 200) — nothing to solve")
	}

	ddCookie := resp.Cookies.Get("datadome")
	sliderURL, err := client.Datadome.ParseSliderURL(resp.Text, ddCookie, target)
	if err != nil {
		return fmt.Errorf("solver: parse slider URL: %w", err)
	}

	taskID, err := client.Task.CreateTask("DataDomeSliderSolver", map[string]interface{}{
		"captcha_url":  sliderURL,
		"user_agent":   t.userAgent,
		"country_code": "fr",
	})
	if err != nil {
		return fmt.Errorf("solver: create task: %w", err)
	}

	sol, err := client.Task.GetTaskResult(taskID, 2)
	if err != nil {
		return fmt.Errorf("solver: solve: %w", err)
	}
	solMap, _ := sol.(map[string]interface{})
	cookieStr, _ := solMap["cookie"].(string)
	if cookieStr == "" {
		return fmt.Errorf("solver: solution had no cookie: %v", sol)
	}
	solved := cookieStr
	if i := strings.Index(cookieStr, "datadome="); i >= 0 {
		solved = strings.SplitN(cookieStr[i+len("datadome="):], ";", 2)[0]
	}

	t.mu.Lock()
	t.cache.Datadome = solved
	t.cache.Solved = time.Now().Format(time.RFC3339)
	t.solveCount++
	n := t.solveCount
	t.mu.Unlock()
	t.saveCache()

	fmt.Fprintf(os.Stderr, "solver: ⚠️  solved (%d credit(s) used this run); cookie cached at %s\n", n, t.cachePath)
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

func isDatadomeCaptcha(resp *http.Response) bool {
	return resp.StatusCode == http.StatusForbidden
}

func setIfAbsent(req *http.Request, key, val string) {
	if req.Header.Get(key) == "" {
		req.Header.Set(key, val)
	}
}
