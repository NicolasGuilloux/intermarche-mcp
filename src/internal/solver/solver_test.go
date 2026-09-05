package solver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
