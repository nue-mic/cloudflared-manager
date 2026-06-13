package cfaccount

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "cf-store.json"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, dir
}

func TestSecretBox_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	key, err := loadOrCreateKey(filepath.Join(dir, "k"))
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	box, err := newSecretBox(key)
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	const plain = "a-very-secret-cloudflare-token-eyJhIjoi"
	enc, err := box.encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, encPrefix) {
		t.Fatalf("missing prefix: %q", enc)
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	dec, err := box.decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round-trip mismatch: %q", dec)
	}
	// Plaintext passthrough.
	if got, _ := box.decrypt("legacy-plain"); got != "legacy-plain" {
		t.Fatalf("plaintext passthrough failed: %q", got)
	}
}

func TestCreateGetSecret_TokenAuth(t *testing.T) {
	s, dir := newStore(t)
	v, err := s.Create(CreateInput{Name: "主账号", AuthType: AuthToken, Token: "secrettoken12345"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !v.HasToken || v.Status != StatusUnverified || v.Name != "主账号" {
		t.Fatalf("view = %+v", v)
	}
	sec, err := s.Secret(v.ID)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if sec.Token != "secrettoken12345" || sec.AuthType != AuthToken {
		t.Fatalf("secret = %+v", sec)
	}
	// On-disk file must NOT contain the plaintext token.
	raw, _ := os.ReadFile(filepath.Join(dir, "cf-store.json"))
	if strings.Contains(string(raw), "secrettoken12345") {
		t.Fatalf("plaintext token leaked to disk: %s", raw)
	}
}

func TestCreate_KeyAuthRequiresEmail(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(CreateInput{Name: "k", AuthType: AuthKey, Key: "globalkey"}); err == nil {
		t.Fatal("expected error without email")
	}
	v, err := s.Create(CreateInput{Name: "k", AuthType: AuthKey, Key: "globalkey", Email: "a@b.c"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !v.HasKey || v.Email != "a@b.c" {
		t.Fatalf("view = %+v", v)
	}
}

func TestUpdate_KeepsSecretWhenEmpty(t *testing.T) {
	s, _ := newStore(t)
	v, _ := s.Create(CreateInput{Name: "n", AuthType: AuthToken, Token: "orig-token-123456"})
	// Update name only; token left empty must be preserved.
	if _, err := s.Update(v.ID, UpdateInput{Name: "renamed"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	sec, _ := s.Secret(v.ID)
	if sec.Token != "orig-token-123456" {
		t.Fatalf("token not preserved: %q", sec.Token)
	}
	got, _ := s.Get(v.ID)
	if got.Name != "renamed" {
		t.Fatalf("name = %q", got.Name)
	}
	// Replacing the token resets verification status.
	_ = s.SetVerification(v.ID, "cfacc", "My CF", "", StatusActive)
	if _, err := s.Update(v.ID, UpdateInput{Token: "new-token-7890"}); err != nil {
		t.Fatalf("Update token: %v", err)
	}
	got, _ = s.Get(v.ID)
	if got.Status != StatusUnverified {
		t.Fatalf("status after token change = %q", got.Status)
	}
	sec, _ = s.Secret(v.ID)
	if sec.Token != "new-token-7890" {
		t.Fatalf("token not replaced: %q", sec.Token)
	}
}

func TestSetVerificationAndPersistence(t *testing.T) {
	s, dir := newStore(t)
	v, _ := s.Create(CreateInput{Name: "n", AuthType: AuthToken, Token: "tok-aaaa-bbbb"})
	if err := s.SetVerification(v.ID, "cfacc123", "Prod", "", StatusActive); err != nil {
		t.Fatalf("SetVerification: %v", err)
	}
	// Reopen from disk: account id + verification survive, secret decrypts.
	s2, err := New(filepath.Join(dir, "cf-store.json"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := s2.Get(v.ID)
	if !ok || got.AccountID != "cfacc123" || got.Status != StatusActive || got.AccountName != "Prod" {
		t.Fatalf("reopened view = %+v ok=%v", got, ok)
	}
	sec, _ := s2.Secret(v.ID)
	if sec.Token != "tok-aaaa-bbbb" {
		t.Fatalf("secret after reopen = %q", sec.Token)
	}
}

func TestBindings(t *testing.T) {
	s, _ := newStore(t)
	if _, ok := s.Binding("inst1"); ok {
		t.Fatal("unexpected binding")
	}
	if err := s.SetBinding("inst1", Binding{AccountID: "acc_x", TunnelID: "tid1", TunnelName: "web"}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	b, ok := s.Binding("inst1")
	if !ok || b.TunnelID != "tid1" || b.LinkedAt == "" {
		t.Fatalf("binding = %+v ok=%v", b, ok)
	}
	if err := s.DeleteBinding("inst1"); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if _, ok := s.Binding("inst1"); ok {
		t.Fatal("binding not deleted")
	}
}

func TestDelete(t *testing.T) {
	s, _ := newStore(t)
	v, _ := s.Create(CreateInput{Name: "n", AuthType: AuthToken, Token: "tok-12345678"})
	if err := s.Delete(v.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get(v.ID); ok {
		t.Fatal("account not deleted")
	}
	if err := s.Delete("nope"); err != ErrNotFound {
		t.Fatalf("Delete missing = %v", err)
	}
}

func TestMigratePlaintextSecrets(t *testing.T) {
	dir := t.TempDir()
	// Hand-write a store file with a PLAINTEXT secret (simulating import).
	fm := fileModel{
		Version: 1,
		Accounts: []Account{{
			ID: "acc_1", Name: "legacy", AuthType: AuthToken,
			SecretToken: "plain-legacy-token", Status: StatusUnverified,
		}},
		Bindings: map[string]Binding{},
	}
	b, _ := json.MarshalIndent(fm, "", "  ")
	storePath := filepath.Join(dir, "cf-store.json")
	if err := os.WriteFile(storePath, b, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := New(storePath, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Secret still decrypts to the same plaintext...
	sec, _ := s.Secret("acc_1")
	if sec.Token != "plain-legacy-token" {
		t.Fatalf("secret = %q", sec.Token)
	}
	// ...but the on-disk file is now encrypted.
	raw, _ := os.ReadFile(storePath)
	if strings.Contains(string(raw), "plain-legacy-token") {
		t.Fatalf("plaintext secret not migrated: %s", raw)
	}
	if !strings.Contains(string(raw), encPrefix) {
		t.Fatalf("expected encrypted secret on disk: %s", raw)
	}
}
