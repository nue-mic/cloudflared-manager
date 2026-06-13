package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthToken / AuthKey are the two supported credential kinds.
const (
	AuthToken = "token" // Authorization: Bearer <token>
	AuthKey   = "key"   // X-Auth-Email + X-Auth-Key (legacy Global API Key)
)

// Credential carries the Cloudflare authentication material for one account.
// Only the fields relevant to Type are read.
type Credential struct {
	Type  string // AuthToken | AuthKey
	Token string // for AuthToken
	Email string // for AuthKey
	Key   string // for AuthKey
}

func (c Credential) apply(r *http.Request) {
	switch c.Type {
	case AuthKey:
		r.Header.Set("X-Auth-Email", c.Email)
		r.Header.Set("X-Auth-Key", c.Key)
	default: // AuthToken
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// Client is a Cloudflare API client bound to a single credential.
type Client struct {
	base string
	hc   *http.Client
	cred Credential
}

// Option customizes a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (used by tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.base = strings.TrimRight(u, "/") }
}

// WithHTTPClient supplies a custom http.Client (timeouts, transport).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}

// New builds a Client for cred. Defaults: official base URL, 20s timeout.
func New(cred Credential, opts ...Option) *Client {
	c := &Client{
		base: DefaultBaseURL,
		hc:   &http.Client{Timeout: 20 * time.Second},
		cred: cred,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// envelope is the standard Cloudflare response wrapper.
type envelope struct {
	Success    bool            `json:"success"`
	Errors     []ErrorItem     `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *ResultInfo     `json:"result_info"`
}

// do performs an authenticated request and unpacks the CF envelope. body is
// JSON-encoded when non-nil; out (when non-nil) receives the decoded result.
// It returns the pagination block when present.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (*ResultInfo, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.cred.apply(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var env envelope
	// Cloudflare always returns a JSON envelope; a decode failure means an
	// upstream/proxy error page — surface the status + raw body.
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if !env.Success || resp.StatusCode >= 400 {
		return nil, &APIError{StatusCode: resp.StatusCode, Errors: env.Errors, Body: string(raw)}
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("decode result: %w", err)
		}
	}
	return env.ResultInfo, nil
}

// ── Identity / accounts ──────────────────────────────────────────────────

// VerifyToken validates the bound API token (AuthToken credentials only).
func (c *Client) VerifyToken(ctx context.Context) (*TokenVerify, error) {
	var v TokenVerify
	if _, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// GetUser returns the current user (used to recover email for AuthKey creds).
func (c *Client) GetUser(ctx context.Context) (*User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodGet, "/user", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ListAccounts returns every account the credential can reach. The first
// entry's id is the natural default account_id.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	var out []Account
	if _, err := c.do(ctx, http.MethodGet, "/accounts?per_page=50", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Tunnels ──────────────────────────────────────────────────────────────

// ListTunnels lists non-deleted tunnels under an account.
func (c *Client) ListTunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false&per_page=100", url.PathEscape(accountID))
	var out []Tunnel
	if _, err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTunnel fetches one tunnel by id.
func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (*Tunnel, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", url.PathEscape(accountID), url.PathEscape(tunnelID))
	var t Tunnel
	if _, err := c.do(ctx, http.MethodGet, p, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTunnel creates a remotely-managed tunnel (config_src defaults to
// "cloudflare" when the caller leaves it empty).
func (c *Client) CreateTunnel(ctx context.Context, accountID string, req CreateTunnelReq) (*Tunnel, error) {
	if req.ConfigSrc == "" {
		req.ConfigSrc = "cloudflare"
	}
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel", url.PathEscape(accountID))
	var t Tunnel
	if _, err := c.do(ctx, http.MethodPost, p, req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTunnel renames a tunnel.
func (c *Client) UpdateTunnel(ctx context.Context, accountID, tunnelID, name string) (*Tunnel, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", url.PathEscape(accountID), url.PathEscape(tunnelID))
	var t Tunnel
	if _, err := c.do(ctx, http.MethodPatch, p, map[string]any{"name": name}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTunnel removes a tunnel. Cloudflare rejects deletion while connections
// remain; callers should CleanupConnections first when forcing.
func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", url.PathEscape(accountID), url.PathEscape(tunnelID))
	_, err := c.do(ctx, http.MethodDelete, p, nil, nil)
	return err
}

// GetTunnelToken returns the connector token for a tunnel (result is a string).
func (c *Client) GetTunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", url.PathEscape(accountID), url.PathEscape(tunnelID))
	var tok string
	if _, err := c.do(ctx, http.MethodGet, p, nil, &tok); err != nil {
		return "", err
	}
	return tok, nil
}

// ── Configurations (ingress) ─────────────────────────────────────────────

// GetConfiguration fetches the remotely-managed tunnel configuration.
func (c *Client) GetConfiguration(ctx context.Context, accountID, tunnelID string) (*ConfigurationResult, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", url.PathEscape(accountID), url.PathEscape(tunnelID))
	var cr ConfigurationResult
	if _, err := c.do(ctx, http.MethodGet, p, nil, &cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

// PutConfiguration replaces the entire tunnel configuration. Cloudflare only
// supports whole-config replacement, so cfg must be the complete desired state.
func (c *Client) PutConfiguration(ctx context.Context, accountID, tunnelID string, cfg *TunnelConfig) (*ConfigurationResult, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", url.PathEscape(accountID), url.PathEscape(tunnelID))
	body := map[string]any{"config": cfg}
	var cr ConfigurationResult
	if _, err := c.do(ctx, http.MethodPut, p, body, &cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

// ── Connections / connectors ─────────────────────────────────────────────

// ListConnections lists the active connectors (clients) of a tunnel.
func (c *Client) ListConnections(ctx context.Context, accountID, tunnelID string) ([]Connector, error) {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/connections", url.PathEscape(accountID), url.PathEscape(tunnelID))
	var out []Connector
	if _, err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CleanupConnections removes idle connections. An empty clientID removes all
// connectors.
func (c *Client) CleanupConnections(ctx context.Context, accountID, tunnelID, clientID string) error {
	p := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/connections", url.PathEscape(accountID), url.PathEscape(tunnelID))
	if clientID != "" {
		p += "?client_id=" + url.QueryEscape(clientID)
	}
	_, err := c.do(ctx, http.MethodDelete, p, nil, nil)
	return err
}

// ── Zones / DNS ──────────────────────────────────────────────────────────

// ListZones lists zones. A non-empty name filters by exact domain.
func (c *Client) ListZones(ctx context.Context, name string) ([]Zone, error) {
	p := "/zones?per_page=50"
	if name != "" {
		p += "&name=" + url.QueryEscape(name)
	}
	var out []Zone
	if _, err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListDNSRecords lists records in a zone. A non-empty name filters by record
// name (exact host).
func (c *Client) ListDNSRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	p := fmt.Sprintf("/zones/%s/dns_records?per_page=100", url.PathEscape(zoneID))
	if name != "" {
		p += "&name=" + url.QueryEscape(name)
	}
	var out []DNSRecord
	if _, err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateDNSRecord creates a record (typically a proxied CNAME to the tunnel).
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, rec DNSRecord) (*DNSRecord, error) {
	p := fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(zoneID))
	var out DNSRecord
	if _, err := c.do(ctx, http.MethodPost, p, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDNSRecord replaces a record.
func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, rec DNSRecord) (*DNSRecord, error) {
	p := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(recordID))
	var out DNSRecord
	if _, err := c.do(ctx, http.MethodPut, p, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDNSRecord removes a record.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	p := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(recordID))
	_, err := c.do(ctx, http.MethodDelete, p, nil, nil)
	return err
}
