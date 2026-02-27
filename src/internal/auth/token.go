package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nover/intermarche-mcp/internal/config"
)

type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (ts *TokenSet) Valid() bool {
	return ts != nil && ts.AccessToken != "" && time.Now().Before(ts.ExpiresAt.Add(-30*time.Second))
}

// ForceRefresh refreshes the access token using the stored refresh token,
// regardless of whether the current access token appears locally valid.
// Returns the new access token.
func ForceRefresh(ctx context.Context) (string, error) {
	ts, err := loadTokens()
	if err != nil {
		return "", fmt.Errorf("loading tokens: %w", err)
	}
	if ts == nil || ts.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token available, run: intermarche-mcp login")
	}
	refreshed, err := refreshTokens(ctx, ts.RefreshToken)
	if err != nil {
		_ = clearTokens()
		return "", fmt.Errorf("refresh failed: %w", err)
	}
	if err := saveTokens(refreshed); err != nil {
		return "", fmt.Errorf("saving refreshed tokens: %w", err)
	}
	return refreshed.AccessToken, nil
}

func refreshTokens(ctx context.Context, refreshToken string) (*TokenSet, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {config.ClientID},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("token refresh failed: %s (%s)", errResp.Error, errResp.Description)
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
