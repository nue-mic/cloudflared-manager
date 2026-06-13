// Package cfapi is a thin, dependency-free HTTP client for the Cloudflare
// REST API (https://api.cloudflare.com/client/v4). It models only the
// surface cloudflared-manager needs: account discovery, cfd_tunnel CRUD,
// tunnel configurations (ingress), connections, zones and DNS records.
//
// The package holds NO persistence and NO business logic — credentials are
// passed in per Client. Storage and the instance↔account binding live in
// internal/cfaccount; HTTP wiring lives in internal/api.
package cfapi

import "encoding/json"

// DefaultBaseURL is the Cloudflare v4 API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// ResultInfo is the pagination block returned by list endpoints.
type ResultInfo struct {
	Count      int `json:"count"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
}

// Account is one Cloudflare account visible to a credential. The id is the
// value used as {account_id} in every cfd_tunnel path.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// TokenVerify is the result of GET /user/tokens/verify.
type TokenVerify struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// User is the (subset of) GET /user used for Global-Key auth to recover the
// account email.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Tunnel mirrors the cfd_tunnel resource. Connections is left as raw JSON so
// the exact edge-connection shape can be forwarded to the UI verbatim.
type Tunnel struct {
	ID              string          `json:"id"`
	AccountTag      string          `json:"account_tag,omitempty"`
	Name            string          `json:"name"`
	CreatedAt       string          `json:"created_at,omitempty"`
	DeletedAt       string          `json:"deleted_at,omitempty"`
	ConnsActiveAt   string          `json:"conns_active_at,omitempty"`
	ConnsInactiveAt string          `json:"conns_inactive_at,omitempty"`
	Status          string          `json:"status,omitempty"`
	TunType         string          `json:"tun_type,omitempty"`
	ConfigSrc       string          `json:"config_src,omitempty"`
	RemoteConfig    bool            `json:"remote_config,omitempty"`
	Connections     json.RawMessage `json:"connections,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// CreateTunnelReq is the POST /cfd_tunnel body. config_src "cloudflare" makes
// it a remotely-managed tunnel (configuration set via the API/dashboard).
type CreateTunnelReq struct {
	Name      string `json:"name"`
	ConfigSrc string `json:"config_src,omitempty"`
	// TunnelSecret is optional; when omitted Cloudflare generates one.
	TunnelSecret string `json:"tunnel_secret,omitempty"`
}

// IngressRule is one public-hostname → service mapping. OriginRequest is a
// free-form map so every cloudflared origin parameter round-trips without the
// client needing to model each key.
type IngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Service       string         `json:"service,omitempty"`
	Path          string         `json:"path,omitempty"`
	OriginRequest map[string]any `json:"originRequest,omitempty"`
}

// WarpRouting toggles private-network (WARP) routing for the tunnel.
type WarpRouting struct {
	Enabled bool `json:"enabled"`
}

// TunnelConfig is the remotely-stored configuration body. OriginRequest is the
// tunnel-wide default; per-ingress overrides live on each IngressRule.
//
// Cloudflare only supports whole-config replacement (no PATCH), and a read →
// mutate → write cycle would silently DROP any top-level config key this struct
// does not model. Extra captures those unknown keys on decode and re-emits them
// on encode, so editing one public hostname never wipes other settings the
// operator configured in the dashboard. See custom (Un)MarshalJSON below.
type TunnelConfig struct {
	Ingress       []IngressRule  `json:"-"`
	OriginRequest map[string]any `json:"-"`
	WarpRouting   *WarpRouting   `json:"-"`
	// Extra preserves top-level config keys not modelled above (forward-compat).
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON splits the config object into the modelled fields plus an Extra
// bag for everything else, so unknown top-level keys survive a round-trip.
func (c *TunnelConfig) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.Ingress = nil
	c.OriginRequest = nil
	c.WarpRouting = nil
	c.Extra = map[string]json.RawMessage{}
	for k, v := range raw {
		switch k {
		case "ingress":
			if err := json.Unmarshal(v, &c.Ingress); err != nil {
				return err
			}
		case "originRequest":
			if err := json.Unmarshal(v, &c.OriginRequest); err != nil {
				return err
			}
		case "warp-routing":
			if err := json.Unmarshal(v, &c.WarpRouting); err != nil {
				return err
			}
		default:
			c.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON reassembles the modelled fields with the preserved Extra keys.
func (c TunnelConfig) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(c.Extra)+3)
	for k, v := range c.Extra {
		out[k] = v
	}
	if len(c.Ingress) > 0 {
		b, err := json.Marshal(c.Ingress)
		if err != nil {
			return nil, err
		}
		out["ingress"] = b
	}
	if len(c.OriginRequest) > 0 {
		b, err := json.Marshal(c.OriginRequest)
		if err != nil {
			return nil, err
		}
		out["originRequest"] = b
	}
	if c.WarpRouting != nil {
		b, err := json.Marshal(c.WarpRouting)
		if err != nil {
			return nil, err
		}
		out["warp-routing"] = b
	}
	return json.Marshal(out)
}

// ConfigurationResult is the envelope returned by the configurations endpoint.
type ConfigurationResult struct {
	AccountID string        `json:"account_id,omitempty"`
	TunnelID  string        `json:"tunnel_id,omitempty"`
	Version   int           `json:"version,omitempty"`
	Config    *TunnelConfig `json:"config"`
	Source    string        `json:"source,omitempty"`
	CreatedAt string        `json:"created_at,omitempty"`
}

// Connector is one cloudflared process attached to a tunnel (the
// connections/connectors endpoints' "Client" object).
type Connector struct {
	ID            string          `json:"id"`
	Features      []string        `json:"features,omitempty"`
	Version       string          `json:"version,omitempty"`
	Arch          string          `json:"arch,omitempty"`
	RunAt         string          `json:"run_at,omitempty"`
	ConfigVersion int             `json:"config_version,omitempty"`
	Conns         json.RawMessage `json:"conns,omitempty"`
}

// Zone is a DNS zone (domain) under an account.
type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
}

// DNSRecord models a zone DNS record. Proxied is a pointer so "not set" is
// distinguishable from explicit false on partial updates.
type DNSRecord struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content,omitempty"`
	Proxied    *bool  `json:"proxied,omitempty"`
	TTL        int    `json:"ttl,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Proxiable  bool   `json:"proxiable,omitempty"`
	CreatedOn  string `json:"created_on,omitempty"`
	ModifiedOn string `json:"modified_on,omitempty"`
}
