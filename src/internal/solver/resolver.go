package solver

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/nover/intermarche-mcp/internal/solver/microproxy"
)

// Resolver solves a Datadome challenge and returns the cleared `datadome`
// cookie value. Each implementation wraps a third-party captcha-solving
// service. The Transport owns the cookie cache and HTTP plumbing; the Resolver
// only turns a challenge into a cookie.
type Resolver interface {
	// Name identifies the provider for logs and errors.
	Name() string
	// Solve solves the Datadome challenge reachable from challengeURL (the API
	// URL that just returned a 403) and returns the bare `datadome` cookie
	// value (no "datadome=" prefix, no attributes).
	Solve(challengeURL string) (string, error)
}

// resolverConfig carries the settings every provider needs.
type resolverConfig struct {
	userAgent string
	proxy     string
	// ephemeral, when set, makes the provider serve its own single-use proxy
	// for the duration of a solve instead of using a standing one.
	ephemeral *microproxy.Config
}

// newResolver builds the captcha provider. CAPTCHA_PROVIDER is still read for
// compatibility, but 2captcha is the only provider left and the default. The
// provider validates its own credentials.
func newResolver(cfg resolverConfig) (Resolver, error) {
	switch provider := strings.ToLower(strings.TrimSpace(os.Getenv("CAPTCHA_PROVIDER"))); provider {
	case "", "2captcha", "twocaptcha":
		return newTwoCaptchaResolver(cfg)
	default:
		return nil, fmt.Errorf("solver: unsupported CAPTCHA_PROVIDER %q — 2captcha is the only provider", provider)
	}
}

// stripDatadome extracts the bare cookie value from a raw Set-Cookie-style
// string such as "datadome=ABC; Path=/; ...". A string that is already a bare
// value is returned unchanged.
func stripDatadome(cookieStr string) string {
	v := cookieStr
	if i := strings.Index(cookieStr, "datadome="); i >= 0 {
		v = cookieStr[i+len("datadome="):]
	}
	return strings.SplitN(v, ";", 2)[0]
}

// canonicalSliderURL rewrites a geo.captcha-delivery.com captcha URL into the
// exact query shape captcha solvers expect: it keeps only initialCid, hash,
// cid, t, referer, s, e and forces dm=cd (dropping extras such as `b`).
// DataDome's API 403 hands back a near-miss of this URL that solvers refuse
// with a "DD_..." support error otherwise.
func canonicalSliderURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse captcha url: %w", err)
	}
	in := u.Query()
	out := url.Values{}
	for _, k := range []string{"initialCid", "hash", "cid", "t", "referer", "s", "e"} {
		if v := in.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	out.Set("dm", "cd")
	return "https://geo.captcha-delivery.com/captcha/?" + out.Encode(), nil
}
