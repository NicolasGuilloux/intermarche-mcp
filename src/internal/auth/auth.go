package auth

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nover/intermarche-mcp/internal/config"
)

const redirectURI = "https://www.intermarche.com/api/connexion"

// Login performs the Authorization Code + PKCE flow.
// It opens the browser for Keycloak login, then asks the user to paste
// the redirect URL so the CLI can extract the authorization code.
func Login(ctx context.Context) error {
	ts, err := loadTokens()
	if err != nil {
		return fmt.Errorf("checking existing tokens: %w", err)
	}
	if ts != nil && ts.Valid() {
		fmt.Println("Already logged in.")
		return nil
	}
	if ts != nil && ts.RefreshToken != "" {
		refreshed, err := refreshTokens(ctx, ts.RefreshToken)
		if err == nil {
			if err := saveTokens(refreshed); err != nil {
				return fmt.Errorf("saving refreshed tokens: %w", err)
			}
			fmt.Println("Session refreshed. Already logged in.")
			return nil
		}
	}

	verifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generating PKCE verifier: %w", err)
	}
	challenge := codeChallenge(verifier)

	state, err := generateCodeVerifier() // reuse for random state
	if err != nil {
		return fmt.Errorf("generating state: %w", err)
	}

	authURL := config.AuthURL + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {config.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {config.Scopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"response_mode":         {"fragment"},
	}.Encode()

	fmt.Println()
	fmt.Println("Opening browser for login...")
	fmt.Println("If it doesn't open, visit this URL:")
	fmt.Println()
	fmt.Printf("  %s\n", authURL)
	fmt.Println()

	openBrowser(authURL)

	fmt.Println("After logging in, copy the full URL from your browser's address bar")
	fmt.Println("(it will start with https://www.intermarche.com/api/connexion#...)")
	fmt.Println()
	fmt.Print("Paste URL here: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input received")
	}
	rawURL := strings.TrimSpace(scanner.Text())

	code, err := extractCodeFromURL(rawURL, state)
	if err != nil {
		return err
	}

	tokens, err := exchangeCode(ctx, code, verifier)
	if err != nil {
		return err
	}

	if err := saveTokens(tokens); err != nil {
		return fmt.Errorf("saving tokens: %w", err)
	}

	fmt.Println("Login successful!")
	return nil
}

// extractCodeFromURL extracts the authorization code from a redirect URL.
// With response_mode=fragment, the code is in the URL fragment (#code=...).
func extractCodeFromURL(rawURL, expectedState string) (string, error) {
	// The URL might contain a fragment (#code=...) or query params (?code=...)
	// Try fragment first (response_mode=fragment), then query params as fallback.

	// url.Parse doesn't parse fragment params, so we handle it manually.
	parts := strings.SplitN(rawURL, "#", 2)

	var params url.Values
	if len(parts) == 2 && parts[1] != "" {
		var err error
		params, err = url.ParseQuery(parts[1])
		if err != nil {
			return "", fmt.Errorf("parsing URL fragment: %w", err)
		}
	} else {
		// Fallback: try query parameters
		u, err := url.Parse(rawURL)
		if err != nil {
			return "", fmt.Errorf("parsing URL: %w", err)
		}
		params = u.Query()
	}

	if errMsg := params.Get("error"); errMsg != "" {
		desc := params.Get("error_description")
		return "", fmt.Errorf("login failed: %s (%s)", errMsg, desc)
	}

	code := params.Get("code")
	if code == "" {
		return "", fmt.Errorf("no authorization code found in URL — make sure you copied the full URL including the # part")
	}

	if s := params.Get("state"); s != "" && s != expectedState {
		return "", fmt.Errorf("state mismatch — possible CSRF, please try again")
	}

	return code, nil
}

func exchangeCode(ctx context.Context, code, verifier string) (*TokenSet, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {config.ClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging authorization code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("token exchange failed: %s (%s)", errResp.Error, errResp.Description)
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	return &TokenSet{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		IDToken:      raw.IDToken,
		TokenType:    raw.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
	}, nil
}

// Logout revokes the refresh token (best-effort) and deletes stored tokens.
func Logout() error {
	ts, err := loadTokens()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}

	if ts != nil && ts.RefreshToken != "" {
		data := url.Values{
			"client_id":       {config.ClientID},
			"token":           {ts.RefreshToken},
			"token_type_hint": {"refresh_token"},
		}
		req, err := http.NewRequest("POST", config.RevokeURL, strings.NewReader(data.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	if err := clearTokens(); err != nil {
		return fmt.Errorf("clearing tokens: %w", err)
	}

	fmt.Println("Logged out.")
	return nil
}

// GetToken returns a valid access token, refreshing if needed.
func GetToken(ctx context.Context) (string, error) {
	ts, err := loadTokens()
	if err != nil {
		return "", fmt.Errorf("loading tokens: %w", err)
	}
	if ts == nil {
		return "", fmt.Errorf("not logged in, run: intermarche-mcp login")
	}

	if ts.Valid() {
		return ts.AccessToken, nil
	}

	if ts.RefreshToken == "" {
		_ = clearTokens()
		return "", fmt.Errorf("session expired, run: intermarche-mcp login")
	}

	refreshed, err := refreshTokens(ctx, ts.RefreshToken)
	if err != nil {
		_ = clearTokens()
		return "", fmt.Errorf("session expired (refresh failed: %v), run: intermarche-mcp login", err)
	}

	if err := saveTokens(refreshed); err != nil {
		return "", fmt.Errorf("saving refreshed tokens: %w", err)
	}

	return refreshed.AccessToken, nil
}

// LoginPassword authenticates using the Resource Owner Password Credentials
// (ROPC) grant — posting credentials directly to the Keycloak token endpoint.
// This avoids the browser-based OAuth redirect dance entirely.
func LoginPassword(ctx context.Context, email, password string) error {
	ts, err := loadTokens()
	if err != nil {
		return fmt.Errorf("checking existing tokens: %w", err)
	}
	if ts != nil && ts.Valid() {
		fmt.Println("Already logged in.")
		return nil
	}
	if ts != nil && ts.RefreshToken != "" {
		refreshed, err := refreshTokens(ctx, ts.RefreshToken)
		if err == nil {
			if err := saveTokens(refreshed); err != nil {
				return fmt.Errorf("saving refreshed tokens: %w", err)
			}
			fmt.Println("Session refreshed. Already logged in.")
			return nil
		}
	}

	data := url.Values{
		"grant_type": {"password"},
		"client_id":  {config.ClientID},
		"username":   {email},
		"password":   {password},
		"scope":      {config.Scopes},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("login failed: %s (%s)", errResp.Error, errResp.Description)
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}

	tokens := &TokenSet{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		IDToken:      raw.IDToken,
		TokenType:    raw.TokenType,
		ExpiresAt:    time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
	}

	if err := saveTokens(tokens); err != nil {
		return fmt.Errorf("saving tokens: %w", err)
	}

	fmt.Println("Login successful!")
	return nil
}

func openBrowser(url string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	default:
		return
	}
	_ = exec.Command(cmd, url).Start()
}
