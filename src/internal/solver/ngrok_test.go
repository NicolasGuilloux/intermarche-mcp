package solver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ngrokAgent(t *testing.T, payload string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tunnels" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestNgrokEndpointMatchesForwardedPort(t *testing.T) {
	api := ngrokAgent(t, `{"tunnels":[
		{"public_url":"https://abc.ngrok.io","proto":"https","config":{"addr":"http://localhost:3000"}},
		{"public_url":"tcp://4.tcp.eu.ngrok.io:19243","proto":"tcp","config":{"addr":"localhost:18888"}}
	]}`)

	got, err := ngrokEndpoint(api, "0.0.0.0:18888")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := "4.tcp.eu.ngrok.io:19243"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestNgrokEndpointRejectsTunnelForAnotherPort(t *testing.T) {
	api := ngrokAgent(t, `{"tunnels":[
		{"public_url":"tcp://4.tcp.eu.ngrok.io:19243","proto":"tcp","config":{"addr":"localhost:9999"}}
	]}`)

	_, err := ngrokEndpoint(api, "0.0.0.0:18888")
	if err == nil {
		t.Fatal("a tunnel forwarding elsewhere must not be used")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("error should name what was found, got: %v", err)
	}
}

func TestNgrokEndpointWithoutTCPTunnel(t *testing.T) {
	api := ngrokAgent(t, `{"tunnels":[
		{"public_url":"https://abc.ngrok.io","proto":"https","config":{"addr":"http://localhost:18888"}}
	]}`)

	if _, err := ngrokEndpoint(api, "0.0.0.0:18888"); err == nil {
		t.Fatal("an HTTP tunnel is not usable as a proxy endpoint")
	}
}

func TestNgrokEndpointWithoutAgent(t *testing.T) {
	// Port 1 is reserved and never listening.
	_, err := ngrokEndpoint("http://127.0.0.1:1", "0.0.0.0:18888")
	if err == nil {
		t.Fatal("a missing agent must be reported")
	}
	if !strings.Contains(err.Error(), "ngrok tcp 18888") {
		t.Fatalf("error should tell how to start the tunnel, got: %v", err)
	}
}

func TestNgrokAPISelection(t *testing.T) {
	cases := map[string]struct {
		api string
		ok  bool
	}{
		// A bare "ngrok" opens the tunnel here, so there is no agent to query.
		"ngrok":                       {"", false},
		"ngrok:":                      {defaultNgrokAPI, true},
		"ngrok:http://127.0.0.1:4041": {"http://127.0.0.1:4041", true},
		"203.0.113.7:18888":           {"", false},
		"":                            {"", false},
	}
	for advertise, want := range cases {
		api, ok := ngrokAPI(advertise)
		if ok != want.ok || api != want.api {
			t.Errorf("ngrokAPI(%q) = (%q, %v), want (%q, %v)", advertise, api, ok, want.api, want.ok)
		}
	}
}

func TestForwardedPort(t *testing.T) {
	for addr, want := range map[string]string{
		"18888":                  "18888",
		"localhost:18888":        "18888",
		"http://localhost:18888": "18888",
		"0.0.0.0:18888":          "18888",
	} {
		if got := forwardedPort(addr); got != want {
			t.Errorf("forwardedPort(%q) = %q, want %q", addr, got, want)
		}
	}
}
