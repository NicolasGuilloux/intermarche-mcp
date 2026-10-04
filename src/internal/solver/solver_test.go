package solver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeResolver records the Challenge it is handed instead of solving anything.
type fakeResolver struct {
	got    Challenge
	cookie string
}

func (f *fakeResolver) Name() string { return "fake" }

func (f *fakeResolver) Solve(ch Challenge) (string, error) {
	f.got = ch
	return f.cookie, nil
}

// TestRoundTripPassesCaptchaURLFromThe403 pins the reason a solve used to fail
// on every POST route: the captcha URL has to travel from the 403 body to the
// resolver. Re-deriving it by fetching the page URL cannot work — DataDome
// mints a challenge per request, and a GET on a POST-only route answers 405.
func TestRoundTripPassesCaptchaURLFromThe403(t *testing.T) {
	const captchaURL = "https://geo.captcha-delivery.com/interstitial/?initialCid=AAA&cid=BBB&hash=CCC&t=it&s=1&e=2&b=3"

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if calls == 1 {
			w.Header().Set("x-datadome", "protected")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"url":"` + captchaURL + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resolver := &fakeResolver{cookie: "FRESH"}
	tr := &Transport{
		base:      &http.Transport{},
		resolver:  resolver,
		userAgent: defaultUA,
		cachePath: filepath.Join(t.TempDir(), "datadome.json"),
		maxSolves: 1,
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/service/produits/v4/search", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if resolver.got.CaptchaURL != captchaURL {
		t.Errorf("resolver got CaptchaURL %q, want %q", resolver.got.CaptchaURL, captchaURL)
	}
	if resolver.got.PageURL != req.URL.String() {
		t.Errorf("resolver got PageURL %q, want %q", resolver.got.PageURL, req.URL.String())
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("retry status = %d, want 200", resp.StatusCode)
	}
	if body, _ := io.ReadAll(resp.Body); !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("retry body = %q", body)
	}
	if tr.cache.Datadome != "FRESH" {
		t.Errorf("cached cookie = %q, want FRESH", tr.cache.Datadome)
	}
}

// TestCaptchaURLPrefersTheOneFromThe403 checks the resolver canonicalises the
// URL it was given rather than reaching out for another one.
func TestCaptchaURLPrefersTheOneFromThe403(t *testing.T) {
	r := &twoCaptchaResolver{}
	got, err := r.captchaURL(Challenge{
		PageURL:    "https://www.intermarche.com/api/service/produits/v4/search",
		CaptchaURL: "https://geo.captcha-delivery.com/interstitial/?initialCid=AAA&cid=BBB&hash=CCC&t=it&s=1&e=2&b=3",
	})
	if err != nil {
		t.Fatalf("captchaURL: %v", err)
	}
	if !strings.HasPrefix(got, "https://geo.captcha-delivery.com/captcha/?") {
		t.Errorf("got %q, want a /captcha/ URL", got)
	}
	for _, want := range []string{"cid=BBB", "hash=CCC", "t=it", "dm=cd"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "b=3") {
		t.Errorf("got %q, should have dropped the extra b parameter", got)
	}
}

// datadomeServer answers every request with a DataDome 403 carrying captchaURL.
func datadomeServer(captchaURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-datadome", "protected")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"url":"` + captchaURL + `"}`))
	}))
}

// TestRoundTripRefusesToSolveABannedIP: a t=bv challenge means the IP itself
// is banned, so a solve would burn a credit for a cookie refused right after.
func TestRoundTripRefusesToSolveABannedIP(t *testing.T) {
	srv := datadomeServer("https://geo.captcha-delivery.com/captcha/?initialCid=AAA&cid=BBB&hash=CCC&t=bv&s=1&e=2")
	defer srv.Close()

	resolver := &fakeResolver{cookie: "FRESH"}
	tr := &Transport{
		base:      &http.Transport{},
		resolver:  resolver,
		userAgent: defaultUA,
		cachePath: filepath.Join(t.TempDir(), "datadome.json"),
		maxSolves: 1,
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/service/x", nil)
	_, err := tr.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "t=bv") {
		t.Fatalf("err = %v, want a banned-IP error", err)
	}
	if len(tr.solves) != 0 || resolver.got.CaptchaURL != "" {
		t.Fatalf("resolver was called on a banned IP")
	}
}

// TestRoundTripReportsACookieRefusedAfterTheSolve: when the fresh cookie is
// rejected too, the caller gets an explanation instead of a bare 403.
func TestRoundTripReportsACookieRefusedAfterTheSolve(t *testing.T) {
	srv := datadomeServer("https://geo.captcha-delivery.com/captcha/?initialCid=AAA&cid=BBB&hash=CCC&t=fe&s=1&e=2")
	defer srv.Close()

	tr := &Transport{
		base:      &http.Transport{},
		resolver:  &fakeResolver{cookie: "FRESH"},
		userAgent: defaultUA,
		cachePath: filepath.Join(t.TempDir(), "datadome.json"),
		maxSolves: 1,
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/service/x", nil)
	resp, err := tr.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "freshly solved cookie") {
		t.Fatalf("resp = %v, err = %v, want a refused-cookie error", resp, err)
	}
}

// TestReserveSolveSlidesWithTheWindow: the cap is per solveWindow, not per
// process — a long-running MCP server must be able to renew a cookie later.
func TestReserveSolveSlidesWithTheWindow(t *testing.T) {
	tr := &Transport{maxSolves: 1}
	if !tr.reserveSolve() {
		t.Fatal("first solve refused")
	}
	if tr.reserveSolve() {
		t.Fatal("second solve inside the window allowed")
	}
	tr.solves[0] = time.Now().Add(-solveWindow - time.Second)
	if !tr.reserveSolve() {
		t.Fatal("solve refused once the window had passed")
	}
}

// TestRoundTripDropsABlockedCookieBeforeSolving: DataDome can flag the cached
// cookie itself (t=bv) while the IP is fine. Retrying without it is free and
// must come before any paid solve, and the dropped cookie must not come back.
func TestRoundTripDropsABlockedCookieBeforeSolving(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"q":1}` {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.Contains(r.Header.Get("Cookie"), "datadome=STALE") {
			w.Header().Set("x-datadome", "protected")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"url":"https://geo.captcha-delivery.com/captcha/?initialCid=A&cid=B&hash=C&t=bv&s=1&e=2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resolver := &fakeResolver{cookie: "FRESH"}
	tr := &Transport{
		base:      &http.Transport{},
		resolver:  resolver,
		userAgent: defaultUA,
		cachePath: filepath.Join(t.TempDir(), "datadome.json"),
		cache:     cache{Datadome: "STALE"},
		maxSolves: 1,
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/service/x", strings.NewReader(`{"q":1}`))
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(tr.solves) != 0 {
		t.Fatal("a credit was spent although dropping the cookie was enough")
	}
	if tr.cache.Datadome != "" {
		t.Fatalf("cached cookie = %q, want it dropped", tr.cache.Datadome)
	}
}
