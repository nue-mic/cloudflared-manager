// Package cfaccount persists Cloudflare account credentials (encrypted at
// rest) and the local-instance↔remote-tunnel bindings. It is independent of
// internal/cfapi: it never talks to Cloudflare itself, it only stores the
// material the API layer uses to build a cfapi.Client.
package cfaccount

// Auth kinds, mirroring cfapi.AuthToken / cfapi.AuthKey (kept as plain strings
// to avoid importing cfapi here).
const (
	AuthToken = "token"
	AuthKey   = "key"
)

// Account verification states.
const (
	StatusUnverified = "unverified"
	StatusActive     = "active"
	StatusInvalid    = "invalid"
)

// Account is the persisted record. SecretToken / SecretKey hold the encrypted
// credential material and are NEVER serialized to API responses.
type Account struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	AuthType       string `json:"auth_type"`
	AccountID      string `json:"account_id,omitempty"`   // Cloudflare account id
	AccountName    string `json:"account_name,omitempty"` // Cloudflare account display name
	Email          string `json:"email,omitempty"`        // for AuthKey
	SecretToken    string `json:"secret_token,omitempty"` // encrypted (AuthToken)
	SecretKey      string `json:"secret_key,omitempty"`   // encrypted (AuthKey)
	Status         string `json:"status"`
	LastVerifiedAt string `json:"last_verified_at,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// View is the redacted, API-safe projection of an Account. It exposes whether
// secrets are present without revealing them.
type View struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	AuthType       string `json:"auth_type"`
	AccountID      string `json:"account_id"`
	AccountName    string `json:"account_name"`
	Email          string `json:"email"`
	HasToken       bool   `json:"has_token"`
	HasKey         bool   `json:"has_key"`
	Status         string `json:"status"`
	LastVerifiedAt string `json:"last_verified_at"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// view projects an Account to its redacted form.
func (a Account) view() View {
	return View{
		ID:             a.ID,
		Name:           a.Name,
		AuthType:       a.AuthType,
		AccountID:      a.AccountID,
		AccountName:    a.AccountName,
		Email:          a.Email,
		HasToken:       a.SecretToken != "",
		HasKey:         a.SecretKey != "",
		Status:         a.Status,
		LastVerifiedAt: a.LastVerifiedAt,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

// Secret is the decrypted credential material handed to the API layer to build
// a cfapi.Client. It must never be persisted or returned to clients.
type Secret struct {
	AuthType string
	Token    string
	Email    string
	Key      string
}

// Binding links a local instance id to a remote Cloudflare tunnel under a
// stored account.
type Binding struct {
	AccountID  string `json:"account_id"` // local cfaccount id
	TunnelID   string `json:"tunnel_id"`
	TunnelName string `json:"tunnel_name,omitempty"`
	AccountTag string `json:"account_tag,omitempty"` // Cloudflare account tag from token
	LinkedAt   string `json:"linked_at,omitempty"`
}

// fileModel is the on-disk JSON document.
type fileModel struct {
	Version  int                `json:"version"`
	Accounts []Account          `json:"accounts"`
	Bindings map[string]Binding `json:"bindings"`
}
