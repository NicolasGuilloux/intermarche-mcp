package solver

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.ngrok.com/ngrok/v2"
)

// openTunnelTimeout bounds the ngrok session setup so a solve cannot hang on a
// tunnel that never comes up.
const openTunnelTimeout = 30 * time.Second

// ngrokTunnel is a TCP endpoint opened for the length of one solve. Its
// listener IS the public address, so nothing has to be port-forwarded by hand,
// and closing it takes the address away with it — the tunnel lives and dies
// with the single-use proxy it serves.
type ngrokTunnel struct {
	listener ngrok.EndpointListener
	agent    ngrok.Agent
	cancel   context.CancelFunc
	endpoint string // host:port handed to the captcha service
}

// startNgrokTunnel opens the tunnel. The agent is created here rather than
// reusing ngrok.DefaultAgent so that closing disconnects this session only.
func startNgrokTunnel() (*ngrokTunnel, error) {
	token := strings.TrimSpace(os.Getenv("NGROK_AUTHTOKEN"))
	if token == "" {
		return nil, fmt.Errorf("solver: NGROK_AUTHTOKEN is empty — no tunnel can be opened for 2captcha to come in through")
	}

	agent, err := ngrok.NewAgent(ngrok.WithAuthtoken(token))
	if err != nil {
		return nil, fmt.Errorf("solver: ngrok agent: %w", err)
	}

	// This context owns the endpoint's lifetime, not just the setup: cancelling
	// it takes the public address down, so it is only cancelled in Close. The
	// setup is bounded separately.
	ctx, cancel := context.WithCancel(context.Background())

	type opened struct {
		listener ngrok.EndpointListener
		err      error
	}
	done := make(chan opened, 1)
	go func() {
		listener, err := agent.Listen(ctx, ngrok.WithURL("tcp://"))
		done <- opened{listener, err}
	}()

	var listener ngrok.EndpointListener
	select {
	case res := <-done:
		if res.err != nil {
			cancel()
			_ = agent.Disconnect()
			return nil, fmt.Errorf("solver: open ngrok tunnel: %w", res.err)
		}
		listener = res.listener
	case <-time.After(openTunnelTimeout):
		cancel()
		_ = agent.Disconnect()
		return nil, fmt.Errorf("solver: the ngrok tunnel did not come up within %s", openTunnelTimeout)
	}

	endpoint := listener.URL().Host
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		_ = listener.Close()
		cancel()
		_ = agent.Disconnect()
		return nil, fmt.Errorf("solver: ngrok returned an unusable address %q", listener.URL())
	}

	return &ngrokTunnel{listener: listener, agent: agent, cancel: cancel, endpoint: endpoint}, nil
}

// Close takes the public address down and ends the agent session.
func (t *ngrokTunnel) Close() {
	_ = t.listener.Close()
	t.cancel()
	_ = t.agent.Disconnect()
}
