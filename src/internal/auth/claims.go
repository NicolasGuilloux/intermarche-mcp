package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// UserID extracts the "sub" claim from a JWT access token without verifying the signature.
func UserID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parsing JWT claims: %w", err)
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("JWT has no 'sub' claim")
	}

	// Keycloak federated users have sub = "f:<idp-id>:f:<user-uuid>:<email>".
	// The cart API expects the plain user UUID.
	if strings.HasPrefix(claims.Sub, "f:") {
		parts := strings.Split(claims.Sub, ":")
		if len(parts) >= 4 {
			return parts[3], nil
		}
	}

	return claims.Sub, nil
}
