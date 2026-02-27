package api

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const BaseURL = "https://www.intermarche.com/api/service"

// TokenRefreshFunc is called when a request gets a 401 to obtain a fresh token.
type TokenRefreshFunc func() (string, error)

// Client is a reusable HTTP client that injects the required Intermarché
// headers (session ID, device fingerprint, tracing, x-red-*, x-oauth)
// into every request.
type Client struct {
	HTTP        *http.Client
	Token       string
	SessionID   string
	DeviceFP    string
	RefreshFunc TokenRefreshFunc
}

// New creates a new API client with the given OAuth access token.
// If transport is non-nil it is used as the HTTP round-tripper (the solver).
func New(token string, transport http.RoundTripper) *Client {
	sessionID := uuid.NewString()
	httpClient := &http.Client{}
	if transport != nil {
		httpClient.Transport = transport
	}
	return &Client{
		HTTP:      httpClient,
		Token:     token,
		SessionID: sessionID,
		DeviceFP:  "ghost_" + sessionID,
	}
}

// Do executes an HTTP request with all required Intermarché headers injected.
// If the response is 401 and a RefreshFunc is set, it refreshes the token and retries once.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	c.setHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized && c.RefreshFunc != nil {
		resp.Body.Close()
		newToken, refreshErr := c.RefreshFunc()
		if refreshErr != nil {
			return nil, fmt.Errorf("token refresh after 401: %w", refreshErr)
		}
		c.Token = newToken

		if req.GetBody != nil {
			body, _ := req.GetBody()
			req.Body = body
		} else {
			req.Body = io.NopCloser(strings.NewReader(""))
		}
		c.setHeaders(req)
		return c.HTTP.Do(req)
	}

	return resp, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("x-red-device", "red_fo_desktop")
	req.Header.Set("x-red-version", "3")
	req.Header.Set("x-call-trace", "web")
	req.Header.Set("x-is-server", "false")
	req.Header.Set("X-ITM-SESSION-ID", c.SessionID)
	req.Header.Set("X-ITM-DEVICE-FP", c.DeviceFP)
	req.Header.Set("X-B3-TraceId", generateTraceID())
	req.Header.Set("X-B3-SpanId", generateTraceID()[:16])
	req.Header.Set("X-B3-Sampled", "1")

	req.Header.Set("x-service-name", extractServiceName(req.URL.Path))

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
}

// extractServiceName returns the first path segment of the backend URL.
// For a path like /api/service/produits/v4/pdvs/..., it returns "produits".
func extractServiceName(path string) string {
	// Strip the /api/service/ prefix if present
	trimmed := strings.TrimPrefix(path, "/api/service/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "api"
}

// generateTraceID returns a random 32-char hex string for Zipkin tracing.
func generateTraceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
