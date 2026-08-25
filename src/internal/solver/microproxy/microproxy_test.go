package microproxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// echoServer stands in for the target site behind the tunnel.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func startTest(t *testing.T, cfg Config) *Proxy {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	cfg.Advertise = "203.0.113.7:8888"
	cfg.allowLoopback = true
	p, err := Start(cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// connect sends a CONNECT and returns the status line plus the live connection.
func connect(t *testing.T, p *Proxy, target, login, password string) (string, net.Conn) {
	t.Helper()
	c, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	auth := ""
	if login != "" || password != "" {
		auth = "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(login+":"+password)) + "\r\n"
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, auth)

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = c.Close()
		t.Fatalf("read response: %v", err)
	}
	_ = c.SetReadDeadline(time.Time{})
	return resp.Status, c
}

func TestTunnelsAllowedTarget(t *testing.T) {
	echo := echoServer(t)
	_, port, _ := net.SplitHostPort(echo.Addr().String())
	p := startTest(t, Config{Allowed: []string{"127.0.0.1"}, Ports: []string{port}})

	login, password := p.Credentials()
	status, conn := connect(t, p, "127.0.0.1:"+port, login, password)
	defer conn.Close()
	if !strings.HasPrefix(status, "200") {
		t.Fatalf("status = %q, want 200", status)
	}

	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want %q", buf, "ping")
	}
	if tunnels, _ := p.Stats(); tunnels != 1 {
		t.Fatalf("tunnels = %d, want 1", tunnels)
	}
}

func TestRejectsBadCredentials(t *testing.T) {
	echo := echoServer(t)
	_, port, _ := net.SplitHostPort(echo.Addr().String())
	p := startTest(t, Config{Allowed: []string{"127.0.0.1"}, Ports: []string{port}})

	login, _ := p.Credentials()
	for name, creds := range map[string][2]string{
		"wrong password": {login, "0000"},
		"wrong login":    {"0000", "0000"},
		"no credentials": {"", ""},
	} {
		status, conn := connect(t, p, "127.0.0.1:"+port, creds[0], creds[1])
		conn.Close()
		if !strings.HasPrefix(status, "407") {
			t.Errorf("%s: status = %q, want 407", name, status)
		}
	}
}

func TestRejectsHostOutsideAllowList(t *testing.T) {
	p := startTest(t, Config{Allowed: []string{"intermarche.com"}})
	login, password := p.Credentials()

	for _, target := range []string{
		"example.com:443",              // unrelated domain
		"intermarche.com.evil.net:443", // suffix trick
		"notintermarche.com:443",       // missing dot boundary
	} {
		status, conn := connect(t, p, target, login, password)
		conn.Close()
		if !strings.HasPrefix(status, "403") {
			t.Errorf("%s: status = %q, want 403", target, status)
		}
	}
}

func TestAllowsSubdomainOfAllowedHost(t *testing.T) {
	p := startTest(t, Config{Allowed: []string{"intermarche.com"}})
	if !p.allowedHost("www.intermarche.com") {
		t.Error("www.intermarche.com should be allowed")
	}
	if !p.allowedHost("intermarche.com") {
		t.Error("intermarche.com should be allowed")
	}
	if p.allowedHost("intermarche.com.evil.net") {
		t.Error("intermarche.com.evil.net must not be allowed")
	}
}

func TestRejectsPortOutsideAllowList(t *testing.T) {
	p := startTest(t, Config{Allowed: []string{"intermarche.com"}, Ports: []string{"443"}})
	login, password := p.Credentials()
	status, conn := connect(t, p, "intermarche.com:22", login, password)
	conn.Close()
	if !strings.HasPrefix(status, "403") {
		t.Fatalf("status = %q, want 403", status)
	}
}

func TestCloseStopsListenerAndIsIdempotent(t *testing.T) {
	p := startTest(t, Config{Allowed: []string{"intermarche.com"}})
	addr := p.Addr().String()
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("listener still accepts connections after Close")
	}
}

func TestTTLClosesProxy(t *testing.T) {
	p := startTest(t, Config{Allowed: []string{"intermarche.com"}, TTL: 100 * time.Millisecond})
	addr := p.Addr().String()
	time.Sleep(300 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("listener outlived its TTL")
	}
}

func TestCredentialsAreFreshPerProxy(t *testing.T) {
	a := startTest(t, Config{Allowed: []string{"intermarche.com"}})
	b := startTest(t, Config{Allowed: []string{"intermarche.com"}})
	aLogin, aPassword := a.Credentials()
	bLogin, bPassword := b.Credentials()
	if aLogin == bLogin || aPassword == bPassword {
		t.Fatal("credentials must not repeat across proxies")
	}
	if len(aPassword) < 32 {
		t.Fatalf("password too short: %d chars", len(aPassword))
	}
}

func TestStartRejectsBadAdvertise(t *testing.T) {
	if _, err := Start(Config{Listen: "127.0.0.1:0", Advertise: "203.0.113.7"}); err == nil {
		t.Fatal("advertise without a port should be refused")
	}
}
