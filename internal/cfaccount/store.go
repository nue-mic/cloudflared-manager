package cfaccount

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when an account id is unknown.
var ErrNotFound = errors.New("cf account not found")

// Store is the thread-safe, encrypted registry of Cloudflare accounts and
// instance bindings, persisted atomically to a single JSON file.
type Store struct {
	path string
	box  *secretBox

	mu   sync.Mutex
	data *fileModel
}

// New opens (or initializes) the store at storePath using the data-encryption
// key at keyPath (generated on first use).
func New(storePath, keyPath string) (*Store, error) {
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("load secret key: %w", err)
	}
	box, err := newSecretBox(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	s := &Store{
		path: storePath,
		box:  box,
		data: &fileModel{Version: 1, Accounts: []Account{}, Bindings: map[string]Binding{}},
	}
	b, rerr := os.ReadFile(storePath)
	switch {
	case rerr == nil:
		if uerr := json.Unmarshal(b, s.data); uerr != nil {
			return nil, fmt.Errorf("parse cf store: %w", uerr)
		}
		if s.data.Bindings == nil {
			s.data.Bindings = map[string]Binding{}
		}
		if s.data.Accounts == nil {
			s.data.Accounts = []Account{}
		}
		if s.data.Version == 0 {
			s.data.Version = 1
		}
		// Encrypt any plaintext secrets discovered on load (migration), then
		// persist so the file is never left with bare credentials. A migration
		// failure is FATAL: continuing would leave a credential in plaintext
		// while pretending all is well (decrypt() passes plaintext through).
		changed, merr := s.migrateEncryptLocked()
		if merr != nil {
			return nil, fmt.Errorf("migrate plaintext secrets: %w", merr)
		}
		if changed {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
		}
	case errors.Is(rerr, os.ErrNotExist):
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, rerr
	}
	return s, nil
}

// CreateInput is the payload for Create.
type CreateInput struct {
	Name      string
	AuthType  string
	Token     string
	Email     string
	Key       string
	AccountID string // optional explicit Cloudflare account id
}

// UpdateInput carries partial changes. Empty string fields are left unchanged,
// matching the "submit empty secret = keep existing" convention.
type UpdateInput struct {
	Name      string
	AuthType  string
	Token     string
	Email     string
	Key       string
	AccountID string
}

// List returns redacted views of every account.
func (s *Store) List() []View {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]View, 0, len(s.data.Accounts))
	for _, a := range s.data.Accounts {
		out = append(out, a.view())
	}
	return out
}

// Get returns one account's redacted view.
func (s *Store) Get(id string) (View, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexLocked(id); i >= 0 {
		return s.data.Accounts[i].view(), true
	}
	return View{}, false
}

// Secret returns the decrypted credential material for id.
func (s *Store) Secret(id string) (Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return Secret{}, ErrNotFound
	}
	a := s.data.Accounts[i]
	tok, err := s.box.decrypt(a.SecretToken)
	if err != nil {
		return Secret{}, err
	}
	key, err := s.box.decrypt(a.SecretKey)
	if err != nil {
		return Secret{}, err
	}
	return Secret{AuthType: a.AuthType, Token: tok, Email: a.Email, Key: key}, nil
}

// Create stores a new account, encrypting its secrets. The redacted view is
// returned.
func (s *Store) Create(in CreateInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(in.Name) == "" {
		return View{}, errors.New("name is required")
	}
	if in.AuthType != AuthToken && in.AuthType != AuthKey {
		return View{}, fmt.Errorf("auth_type must be %q or %q", AuthToken, AuthKey)
	}
	a := Account{
		ID:        newID(),
		Name:      strings.TrimSpace(in.Name),
		AuthType:  in.AuthType,
		AccountID: strings.TrimSpace(in.AccountID),
		Email:     strings.TrimSpace(in.Email),
		Status:    StatusUnverified,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	if err := s.applySecretsLocked(&a, in.Token, in.Key); err != nil {
		return View{}, err
	}
	if err := validateCredentialPresence(a); err != nil {
		return View{}, err
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return View{}, err
	}
	return a.view(), nil
}

// Update applies partial changes to an account. Empty secret fields keep the
// existing encrypted values.
func (s *Store) Update(id string, in UpdateInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return View{}, ErrNotFound
	}
	a := s.data.Accounts[i]
	if strings.TrimSpace(in.Name) != "" {
		a.Name = strings.TrimSpace(in.Name)
	}
	if in.AuthType == AuthToken || in.AuthType == AuthKey {
		a.AuthType = in.AuthType
	}
	if in.AccountID != "" {
		a.AccountID = strings.TrimSpace(in.AccountID)
	}
	if in.Email != "" {
		a.Email = strings.TrimSpace(in.Email)
	}
	if err := s.applySecretsLocked(&a, in.Token, in.Key); err != nil {
		return View{}, err
	}
	if err := validateCredentialPresence(a); err != nil {
		return View{}, err
	}
	// Any credential change invalidates the previous verification.
	if in.Token != "" || in.Key != "" || in.AuthType != "" {
		a.Status = StatusUnverified
	}
	a.UpdatedAt = now()
	s.data.Accounts[i] = a
	if err := s.saveLocked(); err != nil {
		return View{}, err
	}
	return a.view(), nil
}

// SetVerification records the outcome of a credential check.
func (s *Store) SetVerification(id, cfAccountID, cfAccountName, email, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return ErrNotFound
	}
	a := &s.data.Accounts[i]
	if cfAccountID != "" {
		a.AccountID = cfAccountID
	}
	if cfAccountName != "" {
		a.AccountName = cfAccountName
	}
	if email != "" {
		a.Email = email
	}
	a.Status = status
	a.LastVerifiedAt = now()
	a.UpdatedAt = now()
	return s.saveLocked()
}

// Delete removes an account.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return ErrNotFound
	}
	s.data.Accounts = append(s.data.Accounts[:i], s.data.Accounts[i+1:]...)
	return s.saveLocked()
}

// ── Bindings ─────────────────────────────────────────────────────────────

// Binding returns the binding for an instance id.
func (s *Store) Binding(instanceID string) (Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data.Bindings[instanceID]
	return b, ok
}

// SetBinding stores/updates a binding.
func (s *Store) SetBinding(instanceID string, b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.LinkedAt == "" {
		b.LinkedAt = now()
	}
	s.data.Bindings[instanceID] = b
	return s.saveLocked()
}

// DeleteBinding removes a binding (no error if absent).
func (s *Store) DeleteBinding(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Bindings[instanceID]; !ok {
		return nil
	}
	delete(s.data.Bindings, instanceID)
	return s.saveLocked()
}

// ── internal helpers ─────────────────────────────────────────────────────

func (s *Store) indexLocked(id string) int {
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			return i
		}
	}
	return -1
}

// applySecretsLocked encrypts and assigns non-empty token/key onto a.
func (s *Store) applySecretsLocked(a *Account, token, key string) error {
	if token != "" {
		enc, err := s.box.encrypt(token)
		if err != nil {
			return err
		}
		a.SecretToken = enc
	}
	if key != "" {
		enc, err := s.box.encrypt(key)
		if err != nil {
			return err
		}
		a.SecretKey = enc
	}
	return nil
}

// migrateEncryptLocked encrypts any plaintext secret discovered on load.
// Returns whether anything changed plus the first encryption error, if any.
// An error MUST be treated as fatal by the caller — a swallowed error would
// leave a plaintext credential on disk indefinitely.
func (s *Store) migrateEncryptLocked() (bool, error) {
	changed := false
	for i := range s.data.Accounts {
		a := &s.data.Accounts[i]
		if a.SecretToken != "" && !isEncrypted(a.SecretToken) {
			enc, err := s.box.encrypt(a.SecretToken)
			if err != nil {
				return changed, fmt.Errorf("encrypt token for %s: %w", a.ID, err)
			}
			a.SecretToken = enc
			changed = true
		}
		if a.SecretKey != "" && !isEncrypted(a.SecretKey) {
			enc, err := s.box.encrypt(a.SecretKey)
			if err != nil {
				return changed, fmt.Errorf("encrypt key for %s: %w", a.ID, err)
			}
			a.SecretKey = enc
			changed = true
		}
	}
	return changed, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// fsync the temp file before rename so a crash can never leave a
	// truncated credentials file (atomic-write durability).
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Best-effort directory fsync so the rename itself is durable. Not all
	// platforms support directory sync (Windows returns an error); ignore it.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// validateCredentialPresence ensures the account carries the secret its
// auth_type requires.
func validateCredentialPresence(a Account) error {
	switch a.AuthType {
	case AuthToken:
		if a.SecretToken == "" {
			return errors.New("token is required for auth_type=token")
		}
	case AuthKey:
		if a.SecretKey == "" || a.Email == "" {
			return errors.New("email and api_key are required for auth_type=key")
		}
	}
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "acc_" + hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
