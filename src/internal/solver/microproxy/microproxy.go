// Package microproxy provides a single-use HTTP CONNECT proxy that lives only
// for the duration of one captcha solve.
//
// 2Captcha's workers must reach the target site from the same IP as the
// process that minted the Datadome cid, and they can only speak plaintext
// CONNECT — so the proxy has to be publicly reachable and cannot be wrapped in
// TLS. Three things keep that exposure small: the listener is torn down as
// soon as the solve returns, its credentials are freshly generated per solve
// and never reused, and CONNECT is refused for anything outside the allowed
// domain list.
//
// Exposing the listener to the internet is the caller's job (port forward,
// tunnel, firewall rule): Listen is the local bind address and Advertise is
// the public host:port the solver is told to use.
package microproxy

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// defaultTTL bounds the listener's life even if the caller never closes it
	// (a panicking or wedged solve must not leave a public port open).
	defaultTTL = 5 * time.Minute
	// connectTimeout is how long a client gets to send its CONNECT line.
	connectTimeout = 30 * time.Second
	dialTimeout    = 15 * time.Second
)

// defaultAllowed covers what a Datadome solve legitimately needs: the site
// that issued the challenge, the captcha delivery network, and the echo
// services 2Captcha's workers use to confirm which IP the proxy gives them
// (ident.me/tnedi.me), plus the geolocation and timezone lookups they run on
// it (ip-api.com, worldtimeapi.org) — read-only endpoints, harmless to allow.
// Their browser also reaches for google.com and friends; refusing that noise
// does not disturb the solve.
var defaultAllowed = []string{
	"intermarche.com",
	"captcha-delivery.com",
	"ident.me",
	"tnedi.me",
	"ip-api.com",
	"worldtimeapi.org",
}

// defaultPorts keeps the tunnel to normal web traffic.
var defaultPorts = []string{"80", "443"}

// Config describes one ephemeral proxy.
type Config struct {
	// Listen is the local bind address, e.g. "0.0.0.0:18888". Ignored when
	// Listener is set.
	Listen string
	// Listener, when set, is served as-is instead of binding Listen — an ngrok
	// endpoint, say, which is already reachable from the internet. Close takes
	// it down with the rest.
	Listener net.Listener
	// Advertise is the public "host:port" handed to the captcha service. It is
	// never dialled locally — the caller is responsible for routing it to
	// Listen.
	Advertise string
	// Allowed lists the domains CONNECT is permitted for. A domain matches
	// itself and its subdomains. Empty means defaultAllowed.
	Allowed []string
	// Ports lists the destination ports CONNECT is permitted for. Empty means
	// defaultPorts.
	Ports []string
	// TTL bounds the listener's life. Zero means defaultTTL.
	TTL time.Duration

	// allowLoopback disables the private-address guard. Tests only.
	allowLoopback bool
}

// Proxy is a running ephemeral proxy. Close it as soon as the solve returns.
type Proxy struct {
	cfg      Config
	ln       net.Listener
	login    string
	password string
	deadline time.Time
	timer    *time.Timer

	wg sync.WaitGroup

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	tunnels  int
	rejected int
}

// Start binds the listener and mints one-shot credentials for it.
func Start(cfg Config) (*Proxy, error) {
	if cfg.Listener == nil && strings.TrimSpace(cfg.Listen) == "" {
		return nil, fmt.Errorf("microproxy: no listen address")
	}
	if strings.TrimSpace(cfg.Advertise) == "" {
		return nil, fmt.Errorf("microproxy: no advertise address")
	}
	if _, _, err := net.SplitHostPort(cfg.Advertise); err != nil {
		return nil, fmt.Errorf("microproxy: advertise must be host:port: %w", err)
	}
	if len(cfg.Allowed) == 0 {
		cfg.Allowed = defaultAllowed
	}
	if len(cfg.Ports) == 0 {
		cfg.Ports = defaultPorts
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultTTL
	}

	// Hex keeps the credentials free of ':' and '@', which would break the
	// "login:password@host:port" string captcha services expect.
	login, err := secret(6)
	if err != nil {
		return nil, err
	}
	password, err := secret(24)
	if err != nil {
		return nil, err
	}

	ln := cfg.Listener
	if ln == nil {
		bound, err := net.Listen("tcp", cfg.Listen)
		if err != nil {
			return nil, fmt.Errorf("microproxy: listen on %s: %w", cfg.Listen, err)
		}
		ln = bound
	}

	p := &Proxy{
		cfg:      cfg,
		ln:       ln,
		login:    login,
		password: password,
		deadline: time.Now().Add(cfg.TTL),
		conns:    map[net.Conn]struct{}{},
	}
	p.timer = time.AfterFunc(cfg.TTL, func() { _ = p.Close() })
	p.wg.Add(1)
	go p.serve()
	return p, nil
}

// Credentials returns the one-shot login and password for this proxy.
func (p *Proxy) Credentials() (login, password string) { return p.login, p.password }

// Endpoint returns the public host:port to hand to the captcha service.
func (p *Proxy) Endpoint() string { return p.cfg.Advertise }

// Addr returns the local address actually bound (useful when Listen used :0).
func (p *Proxy) Addr() net.Addr { return p.ln.Addr() }

// Stats reports how many tunnels were established and how many requests were
// refused (bad credentials, disallowed target, malformed request).
func (p *Proxy) Stats() (tunnels, rejected int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tunnels, p.rejected
}

// Close tears down the listener and every live tunnel. It is idempotent.
func (p *Proxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	live := make([]net.Conn, 0, len(p.conns))
	for c := range p.conns {
		live = append(live, c)
	}
	p.mu.Unlock()

	p.timer.Stop()
	err := p.ln.Close()
	for _, c := range live {
		_ = c.Close()
	}
	p.wg.Wait()
	return err
}

func (p *Proxy) serve() {
	defer p.wg.Done()
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			continue
		}

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = conn.Close()
			return
		}
		p.conns[conn] = struct{}{}
		p.mu.Unlock()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.handle(conn)
		}()
	}
}

func (p *Proxy) handle(conn net.Conn) {
	defer func() {
		p.mu.Lock()
		delete(p.conns, conn)
		p.mu.Unlock()
		_ = conn.Close()
	}()

	from := conn.RemoteAddr().String()
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		p.reject(conn, from, "", "malformed request", "")
		return
	}
	if req.Method != http.MethodConnect {
		p.reject(conn, from, req.Host, "only CONNECT is proxied", "405 Method Not Allowed")
		return
	}
	if !p.authorized(req.Header.Get("Proxy-Authorization")) {
		// The realm is deliberately generic: it is sent to whoever knocks.
		p.reject(conn, from, req.Host, "bad credentials",
			"407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"proxy\"")
		return
	}

	host, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		host, port = req.Host, "443"
	}
	if !allowed(port, p.cfg.Ports) {
		p.reject(conn, from, req.Host, "port not allowed", "403 Forbidden")
		return
	}
	if !p.allowedHost(host) {
		p.reject(conn, from, req.Host, "host not in allow list", "403 Forbidden")
		return
	}

	addr, err := p.resolve(host, port)
	if err != nil {
		p.reject(conn, from, req.Host, err.Error(), "403 Forbidden")
		return
	}

	upstream, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		p.reject(conn, from, req.Host, "upstream dial failed", "502 Bad Gateway")
		return
	}
	defer upstream.Close()

	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}

	p.mu.Lock()
	p.tunnels++
	p.mu.Unlock()

	// Every tunnel dies with the lease, whatever the peers do.
	_ = conn.SetDeadline(p.deadline)
	_ = upstream.SetDeadline(p.deadline)

	done := make(chan struct{})
	go func() {
		// br, not conn: the client may already have pipelined bytes after the
		// CONNECT line, and those sit in the reader's buffer.
		_, _ = io.Copy(upstream, br)
		_ = upstream.SetReadDeadline(time.Now())
		close(done)
	}()
	_, _ = io.Copy(conn, upstream)
	<-done
}

// authorized compares the Basic credentials in constant time.
func (p *Proxy) authorized(header string) bool {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return false
	}
	login, password, ok := strings.Cut(string(raw), ":")
	if !ok {
		return false
	}
	okLogin := subtle.ConstantTimeCompare([]byte(login), []byte(p.login)) == 1
	okPassword := subtle.ConstantTimeCompare([]byte(password), []byte(p.password)) == 1
	return okLogin && okPassword
}

// allowedHost matches a domain from the allow list, or any of its subdomains.
func (p *Proxy) allowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range p.cfg.Allowed {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// resolve turns host:port into an ip:port, refusing addresses that point back
// into the local network: an allowed domain whose DNS answer is 127.0.0.1 or
// 10.x would otherwise turn the tunnel into a way in.
func (p *Proxy) resolve(host, port string) (string, error) {
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if !p.cfg.allowLoopback && !routable(ip) {
			continue
		}
		return net.JoinHostPort(ip.String(), port), nil
	}
	return "", fmt.Errorf("host resolves to a non-routable address")
}

func routable(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func (p *Proxy) reject(conn net.Conn, from, target, reason, status string) {
	p.mu.Lock()
	p.rejected++
	p.mu.Unlock()
	if status != "" {
		_, _ = io.WriteString(conn, "HTTP/1.1 "+status+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}
	if target == "" {
		target = "-"
	}
	fmt.Fprintf(os.Stderr, "microproxy: refused %s from %s (%s)\n", target, from, reason)
}

func allowed(value string, list []string) bool {
	for _, v := range list {
		if strings.TrimSpace(v) == value {
			return true
		}
	}
	return false
}

func secret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("microproxy: generate credentials: %w", err)
	}
	return hex.EncodeToString(b), nil
}
