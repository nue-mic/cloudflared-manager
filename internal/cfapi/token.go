package cfapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TokenClaims are the fields cloudflared-manager extracts from a connector
// token. The secret is intentionally NOT surfaced.
type TokenClaims struct {
	// AccountTag is the Cloudflare account id the tunnel belongs to.
	AccountTag string
	// TunnelID is the cfd_tunnel UUID.
	TunnelID string
}

// rawClaims is the JSON shape inside a connector token: {"a":..,"t":..,"s":..}.
type rawClaims struct {
	A string `json:"a"`
	T string `json:"t"`
	S string `json:"s"`
}

// DecodeTunnelToken decodes a cloudflared connector token (base64-of-JSON)
// into its account tag and tunnel id WITHOUT contacting Cloudflare. It is the
// basis of local ownership validation: decode the instance's token, then
// confirm the decoded account/tunnel match the linked CF account.
//
// The token is tolerated in std/url base64, with or without padding, and may
// carry surrounding whitespace.
func DecodeTunnelToken(token string) (TokenClaims, error) {
	tok := strings.TrimSpace(token)
	if tok == "" {
		return TokenClaims{}, errors.New("empty token")
	}
	raw, err := decodeBase64Any(tok)
	if err != nil {
		return TokenClaims{}, fmt.Errorf("token is not valid base64: %w", err)
	}
	var c rawClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return TokenClaims{}, fmt.Errorf("token payload is not valid JSON: %w", err)
	}
	if c.A == "" || c.T == "" {
		return TokenClaims{}, errors.New("token missing account tag or tunnel id")
	}
	return TokenClaims{AccountTag: c.A, TunnelID: c.T}, nil
}

// decodeBase64Any tries the four base64 alphabets cloudflared has emitted
// across versions and returns the first successful decode.
func decodeBase64Any(s string) ([]byte, error) {
	encs := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encs {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}
