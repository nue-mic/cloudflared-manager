package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cloudflared-manager/internal/cfaccount"
	"github.com/mia-clark/cloudflared-manager/internal/cfapi"
	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
)

// ── Mock Cloudflare API ──────────────────────────────────────────────────

const (
	mockCFAccount = "cfacc"
	mockTunnelID  = "tid-123"
)

// cfMock is an in-memory stand-in for the Cloudflare API. It keeps just enough
// state (tunnel config + DNS records) for the public-hostname round-trip.
type cfMock struct {
	config   *cfapi.TunnelConfig
	dns      []cfapi.DNSRecord
	nextID   int
	force401 bool // when true, tunnel calls return upstream 401 (credential failure)
}

func newCFMock() *cfMock {
	return &cfMock{config: &cfapi.TunnelConfig{Ingress: []cfapi.IngressRule{{Service: "http_status:404"}}}}
}

func (m *cfMock) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(m.route))
	t.Cleanup(srv.Close)
	return srv
}

func cfOK(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
}

func cfFail(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": 1000, "message": "mock error"}},
		"result":  nil,
	})
}

func (m *cfMock) route(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/user/tokens/verify":
		cfOK(w, map[string]any{"id": "tok", "status": "active"})
	case p == "/user":
		cfOK(w, map[string]any{"id": "u1", "email": "ops@example.com"})
	case p == "/accounts":
		cfOK(w, []map[string]any{{"id": mockCFAccount, "name": "Prod", "type": "standard"}})
	case p == "/zones":
		cfOK(w, []map[string]any{{"id": "zone1", "name": "example.com", "status": "active"}})
	case strings.HasPrefix(p, "/zones/zone1/dns_records"):
		m.routeDNS(w, r, strings.TrimPrefix(p, "/zones/zone1/dns_records"))
	case strings.HasPrefix(p, "/accounts/"+mockCFAccount+"/cfd_tunnel"):
		m.routeTunnel(w, r, strings.TrimPrefix(p, "/accounts/"+mockCFAccount+"/cfd_tunnel"))
	default:
		cfFail(w, http.StatusNotFound)
	}
}

func (m *cfMock) routeTunnel(w http.ResponseWriter, r *http.Request, rest string) {
	if m.force401 {
		cfFail(w, http.StatusUnauthorized)
		return
	}
	switch {
	case rest == "" || rest == "/":
		if r.Method == http.MethodPost {
			cfOK(w, map[string]any{"id": "new-tid", "name": "created", "account_tag": mockCFAccount})
			return
		}
		cfOK(w, []map[string]any{{"id": mockTunnelID, "name": "web", "status": "healthy", "account_tag": mockCFAccount}})
	case rest == "/"+mockTunnelID:
		cfOK(w, map[string]any{"id": mockTunnelID, "name": "web", "status": "healthy", "account_tag": mockCFAccount})
	case rest == "/"+mockTunnelID+"/configurations":
		if r.Method == http.MethodPut {
			var body struct {
				Config *cfapi.TunnelConfig `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Config != nil {
				m.config = body.Config
			}
		}
		cfOK(w, map[string]any{"tunnel_id": mockTunnelID, "version": 7, "config": m.config})
	case rest == "/"+mockTunnelID+"/connections":
		cfOK(w, []map[string]any{{"id": "conn1", "version": "2026.1.0", "arch": "linux_amd64"}})
	case rest == "/"+mockTunnelID+"/token":
		cfOK(w, "remote-token-xyz")
	default:
		// Unknown tunnel id → 404 (drives the ownership-rejection test).
		cfFail(w, http.StatusNotFound)
	}
}

func (m *cfMock) routeDNS(w http.ResponseWriter, r *http.Request, rest string) {
	switch r.Method {
	case http.MethodGet:
		name := r.URL.Query().Get("name")
		out := []cfapi.DNSRecord{}
		for _, rec := range m.dns {
			if name == "" || strings.EqualFold(rec.Name, name) {
				out = append(out, rec)
			}
		}
		cfOK(w, out)
	case http.MethodPost:
		var rec cfapi.DNSRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		m.nextID++
		rec.ID = fmt.Sprintf("rec%d", m.nextID)
		m.dns = append(m.dns, rec)
		cfOK(w, rec)
	case http.MethodPut:
		id := strings.TrimPrefix(rest, "/")
		var rec cfapi.DNSRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = id
		for i := range m.dns {
			if m.dns[i].ID == id {
				m.dns[i] = rec
			}
		}
		cfOK(w, rec)
	case http.MethodDelete:
		id := strings.TrimPrefix(rest, "/")
		kept := m.dns[:0]
		for _, rec := range m.dns {
			if rec.ID != id {
				kept = append(kept, rec)
			}
		}
		m.dns = kept
		cfOK(w, map[string]any{"id": id})
	default:
		cfFail(w, http.StatusMethodNotAllowed)
	}
}

// ── Test harness ─────────────────────────────────────────────────────────

// cfTestEnv wires a CFHandler whose cfapi.Client points at the mock server,
// mounted on a chi router for real path-param routing.
type cfTestEnv struct {
	router http.Handler
	store  *cfaccount.Store
	mgr    *manager.Manager
	mock   *cfMock
}

func newCFTestEnv(t *testing.T) *cfTestEnv {
	t.Helper()
	dir := t.TempDir()
	store, err := cfaccount.New(filepath.Join(dir, "cf-store.json"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mgr := newTestManager(t, dir)
	mock := newCFMock()
	srv := mock.server(t)

	h := NewCFHandler(store, mgr, testLogger())
	h.newClient = func(sec cfaccount.Secret) *cfapi.Client {
		return cfapi.New(cfapi.Credential{Type: sec.AuthType, Token: sec.Token, Email: sec.Email, Key: sec.Key},
			cfapi.WithBaseURL(srv.URL))
	}
	r := chi.NewRouter()
	registerCFRoutes(r, h)
	return &cfTestEnv{router: r, store: store, mgr: mgr, mock: mock}
}

func (e *cfTestEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func mkToken(accountTag, tunnelID string) string {
	b, _ := json.Marshal(map[string]string{"a": accountTag, "t": tunnelID, "s": "c2VjcmV0"})
	return base64.StdEncoding.EncodeToString(b)
}

func mustCreateInstanceToken(t *testing.T, m *manager.Manager, id, token string) {
	t.Helper()
	sc := &cfdconfig.TunnelConfigV1{Token: token}
	sc.Edge.Protocol = "auto"
	if err := m.Create(id, sc, manager.MgrMeta{Name: id}); err != nil {
		t.Fatalf("create instance: %v", err)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestCFAccounts_CreateVerifyAutoAccountID(t *testing.T) {
	e := newCFTestEnv(t)
	rec := e.do(t, http.MethodPost, "/api/v1/cf/accounts",
		`{"name":"主账号","auth_type":"token","token":"my-cf-token-1234567890"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Account cfaccount.View `json:"account"`
		Verify  verifyResult   `json:"verify"`
	}
	decode(t, rec, &resp)
	if !resp.Verify.OK {
		t.Fatalf("verify failed: %+v", resp.Verify)
	}
	if resp.Account.AccountID != mockCFAccount || resp.Account.Status != cfaccount.StatusActive {
		t.Fatalf("account not resolved: %+v", resp.Account)
	}
	if resp.Account.HasToken == false {
		t.Fatalf("has_token should be true")
	}
	// Secret never leaks in the response.
	if strings.Contains(rec.Body.String(), "my-cf-token-1234567890") {
		t.Fatalf("token leaked in response")
	}
}

func TestCFBinding_ValidatesOwnership(t *testing.T) {
	e := newCFTestEnv(t)
	// Instance whose token embeds the right account + tunnel.
	mustCreateInstanceToken(t, e.mgr, "web", mkToken(mockCFAccount, mockTunnelID))
	// Stored, verified account.
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)

	// Bind without explicit tunnel_id → decoded from token.
	rec := e.do(t, http.MethodPut, "/api/v1/configs/web/cf/binding",
		fmt.Sprintf(`{"account_id":%q}`, view.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("bind status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp bindingResp
	decode(t, rec, &resp)
	if !resp.Bound || resp.TunnelID != mockTunnelID || !resp.Match {
		t.Fatalf("binding = %+v", resp)
	}
	if resp.TunnelName != "web" {
		t.Fatalf("tunnel name = %q", resp.TunnelName)
	}
}

func TestCFBinding_RejectsForeignTunnel(t *testing.T) {
	e := newCFTestEnv(t)
	mustCreateInstanceToken(t, e.mgr, "web", mkToken(mockCFAccount, "unknown-tid"))
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)

	rec := e.do(t, http.MethodPut, "/api/v1/configs/web/cf/binding",
		fmt.Sprintf(`{"account_id":%q}`, view.ID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for foreign tunnel, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCFPublicHostnames_AddListDelete(t *testing.T) {
	e := newCFTestEnv(t)
	mustCreateInstanceToken(t, e.mgr, "web", mkToken(mockCFAccount, mockTunnelID))
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)
	if err := e.store.SetBinding("web", cfaccount.Binding{AccountID: view.ID, TunnelID: mockTunnelID, TunnelName: "web"}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	// Add a public hostname (also upserts a proxied CNAME).
	rec := e.do(t, http.MethodPost, "/api/v1/configs/web/cf/public-hostnames",
		`{"hostname":"app.example.com","service":"http://localhost:8080"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d body=%s", rec.Code, rec.Body.String())
	}
	var addResp map[string]any
	decode(t, rec, &addResp)
	if _, ok := addResp["dns"]; !ok {
		t.Fatalf("expected dns status in add response: %v", addResp)
	}

	// The ingress now has the hostname rule before the catch-all.
	if len(e.mock.config.Ingress) != 2 || e.mock.config.Ingress[0].Hostname != "app.example.com" {
		t.Fatalf("ingress not updated: %+v", e.mock.config.Ingress)
	}
	// And a proxied CNAME to the tunnel exists.
	if len(e.mock.dns) != 1 || e.mock.dns[0].Content != mockTunnelID+cfargoSuffix {
		t.Fatalf("dns not created: %+v", e.mock.dns)
	}

	// List aggregates ingress + DNS state.
	rec = e.do(t, http.MethodGet, "/api/v1/configs/web/cf/public-hostnames", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Items []publicHostname `json:"items"`
	}
	decode(t, rec, &listResp)
	var found *publicHostname
	for i := range listResp.Items {
		if listResp.Items[i].Hostname == "app.example.com" {
			found = &listResp.Items[i]
		}
	}
	if found == nil || found.DNS == nil || !found.DNS.InSync {
		t.Fatalf("public hostname not in sync: %+v", listResp.Items)
	}

	// Delete it (index 0) with DNS cleanup.
	rec = e.do(t, http.MethodDelete, "/api/v1/configs/web/cf/public-hostnames/0?delete_dns=true", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(e.mock.config.Ingress) != 1 {
		t.Fatalf("ingress rule not removed: %+v", e.mock.config.Ingress)
	}
	if len(e.mock.dns) != 0 {
		t.Fatalf("dns not cleaned: %+v", e.mock.dns)
	}
}

func TestCFTokenInfo_DecodesInstanceToken(t *testing.T) {
	e := newCFTestEnv(t)
	mustCreateInstanceToken(t, e.mgr, "web", mkToken(mockCFAccount, mockTunnelID))
	rec := e.do(t, http.MethodGet, "/api/v1/configs/web/cf/token-info", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	decode(t, rec, &resp)
	if resp["account_tag"] != mockCFAccount || resp["tunnel_id"] != mockTunnelID {
		t.Fatalf("token info = %+v", resp)
	}
}

func TestCFTunnels_ListThroughAccount(t *testing.T) {
	e := newCFTestEnv(t)
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)
	rec := e.do(t, http.MethodGet, "/api/v1/cf/accounts/"+view.ID+"/tunnels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []cfapi.Tunnel `json:"items"`
	}
	decode(t, rec, &resp)
	if len(resp.Items) != 1 || resp.Items[0].ID != mockTunnelID {
		t.Fatalf("tunnels = %+v", resp.Items)
	}
}

func TestCFTunnels_Upstream401NotPassedThrough(t *testing.T) {
	// A stale/invalid Cloudflare token (upstream 401) must NOT surface as a
	// local 401 — that would trip the frontend interceptor into logging the
	// operator out of the panel. It should become a 502.
	e := newCFTestEnv(t)
	e.mock.force401 = true
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)
	rec := e.do(t, http.MethodGet, "/api/v1/cf/accounts/"+view.ID+"/tunnels", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for upstream 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCFTunnels_InvalidAccountBlocked(t *testing.T) {
	// An account whose last verification failed (invalid) must be refused.
	e := newCFTestEnv(t)
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusInvalid)
	rec := e.do(t, http.MethodGet, "/api/v1/cf/accounts/"+view.ID+"/tunnels", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for invalid account, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCFPublicHostnames_PreservesUnknownConfigKey(t *testing.T) {
	// Adding a public hostname must not wipe a top-level config key the tool
	// does not model (forward-compat: CF replaces the whole config).
	e := newCFTestEnv(t)
	e.mock.config = &cfapi.TunnelConfig{
		Ingress: []cfapi.IngressRule{{Service: "http_status:404"}},
		Extra:   map[string]json.RawMessage{"future-key": json.RawMessage(`{"keep":true}`)},
	}
	mustCreateInstanceToken(t, e.mgr, "web", mkToken(mockCFAccount, mockTunnelID))
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	_ = e.store.SetVerification(view.ID, mockCFAccount, "Prod", "", cfaccount.StatusActive)
	_ = e.store.SetBinding("web", cfaccount.Binding{AccountID: view.ID, TunnelID: mockTunnelID, TunnelName: "web"})

	rec := e.do(t, http.MethodPost, "/api/v1/configs/web/cf/public-hostnames",
		`{"hostname":"app.example.com","service":"http://localhost:8080","manage_dns":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := e.mock.config.Extra["future-key"]; !ok {
		t.Fatalf("unknown config key wiped on public-hostname add: %+v", e.mock.config.Extra)
	}
}

func TestCFTunnels_RequiresResolvedAccountID(t *testing.T) {
	e := newCFTestEnv(t)
	// Account created but never verified → no CF account id.
	view, _ := e.store.Create(cfaccount.CreateInput{Name: "acc", AuthType: cfaccount.AuthToken, Token: "tkn-aaaa-bbbb"})
	rec := e.do(t, http.MethodGet, "/api/v1/cf/accounts/"+view.ID+"/tunnels", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}
