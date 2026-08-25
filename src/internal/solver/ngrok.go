package solver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// ngrokScheme is the CAPTCHA_PROXY_ADVERTISE value asking this process to open
// the tunnel itself, for the length of one solve (see ngroktunnel.go).
// "ngrok:<url>" instead reads the address from an agent someone else runs.
const ngrokScheme = "ngrok"

const defaultNgrokAPI = "http://127.0.0.1:4040"

// ngrokAPI returns the API of the externally-run agent to query, and whether
// the advertise value asked for one. A bare "ngrok" does not: that means the
// tunnel is opened here instead.
func ngrokAPI(advertise string) (string, bool) {
	api, ok := strings.CutPrefix(strings.TrimSpace(advertise), ngrokScheme+":")
	if !ok {
		return "", false
	}
	if api = strings.TrimSpace(api); api == "" {
		return defaultNgrokAPI, true
	}
	return api, true
}

// ngrokEndpoint asks a running ngrok agent for the public host:port of the TCP
// tunnel forwarding to listen. Resolved per solve, so restarting ngrok — which
// hands out a new random address every time — needs no config change.
func ngrokEndpoint(api, listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("solver: cannot read the port from CAPTCHA_PROXY_LISTEN %q: %w", listen, err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(api, "/") + "/api/tunnels")
	if err != nil {
		return "", fmt.Errorf("solver: no ngrok agent answering on %s — start one with `ngrok tcp %s`: %w", api, port, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("solver: ngrok agent on %s returned status %d", api, resp.StatusCode)
	}

	var body struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
			Config    struct {
				Addr string `json:"addr"`
			} `json:"config"`
		} `json:"tunnels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("solver: cannot read the ngrok tunnel list: %w", err)
	}

	var seen []string
	for _, t := range body.Tunnels {
		if t.Proto != "tcp" {
			continue
		}
		seen = append(seen, t.Config.Addr)
		if forwardedPort(t.Config.Addr) != port {
			continue
		}
		endpoint := strings.TrimPrefix(t.PublicURL, "tcp://")
		if _, _, err := net.SplitHostPort(endpoint); err != nil {
			return "", fmt.Errorf("solver: ngrok reported an unusable address %q", t.PublicURL)
		}
		return endpoint, nil
	}

	if len(seen) == 0 {
		return "", fmt.Errorf("solver: the ngrok agent on %s has no TCP tunnel — start one with `ngrok tcp %s`", api, port)
	}
	return "", fmt.Errorf("solver: no ngrok TCP tunnel forwards to port %s (found: %s)", port, strings.Join(seen, ", "))
}

// forwardedPort extracts the port from an ngrok `config.addr`, which comes in
// several shapes: "18888", "localhost:18888" or "http://localhost:18888".
func forwardedPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}
