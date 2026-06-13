package cfapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cfMux builds a test server that wraps result in the CF envelope and records
// the last request for header/body assertions.
type capture struct {
	method string
	path   string
	auth   string
	email  string
	key    string
	body   string
}

func newCF(t *testing.T, handler func(r *http.Request) (any, int)) (*Client, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.RequestURI()
		cap.auth = r.Header.Get("Authorization")
		cap.email = r.Header.Get("X-Auth-Email")
		cap.key = r.Header.Get("X-Auth-Key")
		if r.Body != nil {
			buf, _ := io.ReadAll(r.Body)
			cap.body = string(buf)
		}
		result, status := handler(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		env := map[string]any{"success": status < 400, "errors": []any{}, "messages": []any{}, "result": result}
		if status >= 400 {
			env["errors"] = []map[string]any{{"code": 1000, "message": "boom"}}
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	t.Cleanup(srv.Close)
	c := New(Credential{Type: AuthToken, Token: "tkn"}, WithBaseURL(srv.URL))
	return c, cap
}

func TestVerifyToken_SendsBearer(t *testing.T) {
	c, cap := newCF(t, func(r *http.Request) (any, int) {
		return map[string]any{"id": "abc", "status": "active"}, 200
	})
	v, err := c.VerifyToken(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Status != "active" {
		t.Fatalf("status = %q", v.Status)
	}
	if cap.auth != "Bearer tkn" {
		t.Fatalf("auth header = %q", cap.auth)
	}
	if cap.path != "/user/tokens/verify" {
		t.Fatalf("path = %q", cap.path)
	}
}

func TestKeyAuthHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Email") != "a@b.c" || r.Header.Get("X-Auth-Key") != "globalkey" {
			t.Errorf("missing key auth headers: email=%q key=%q", r.Header.Get("X-Auth-Email"), r.Header.Get("X-Auth-Key"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]string{{"id": "acc1", "name": "n"}}})
	}))
	defer srv.Close()
	c := New(Credential{Type: AuthKey, Email: "a@b.c", Key: "globalkey"}, WithBaseURL(srv.URL))
	accs, err := c.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accs) != 1 || accs[0].ID != "acc1" {
		t.Fatalf("accounts = %+v", accs)
	}
}

func TestListTunnels_PathAndDecode(t *testing.T) {
	c, cap := newCF(t, func(r *http.Request) (any, int) {
		return []map[string]any{
			{"id": "t1", "name": "web", "status": "healthy", "account_tag": "acc1"},
		}, 200
	})
	ts, err := c.ListTunnels(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ts) != 1 || ts[0].Name != "web" || ts[0].Status != "healthy" {
		t.Fatalf("tunnels = %+v", ts)
	}
	if cap.path != "/accounts/acc1/cfd_tunnel?is_deleted=false&per_page=100" {
		t.Fatalf("path = %q", cap.path)
	}
}

func TestGetTunnelToken_StringResult(t *testing.T) {
	c, _ := newCF(t, func(r *http.Request) (any, int) {
		return "eyJhIjoiYWNjIn0", 200
	})
	tok, err := c.GetTunnelToken(context.Background(), "acc1", "t1")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok != "eyJhIjoiYWNjIn0" {
		t.Fatalf("tok = %q", tok)
	}
}

func TestPutConfiguration_WrapsConfig(t *testing.T) {
	c, cap := newCF(t, func(r *http.Request) (any, int) {
		return map[string]any{"tunnel_id": "t1", "version": 3, "config": map[string]any{"ingress": []any{}}}, 200
	})
	cfg := &TunnelConfig{
		Ingress: []IngressRule{
			{Hostname: "app.example.com", Service: "http://localhost:8080"},
			{Service: "http_status:404"},
		},
		WarpRouting: &WarpRouting{Enabled: false},
	}
	res, err := c.PutConfiguration(context.Background(), "acc1", "t1", cfg)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if res.Version != 3 {
		t.Fatalf("version = %d", res.Version)
	}
	if cap.method != http.MethodPut {
		t.Fatalf("method = %s", cap.method)
	}
	// Body must wrap the config under a top-level "config" key.
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cap.body), &sent); err != nil {
		t.Fatalf("body not json: %v (%s)", err, cap.body)
	}
	if _, ok := sent["config"]; !ok {
		t.Fatalf("body missing config wrapper: %s", cap.body)
	}
}

func TestAPIError_Surface(t *testing.T) {
	c, _ := newCF(t, func(r *http.Request) (any, int) {
		return nil, http.StatusNotFound
	})
	_, err := c.GetTunnel(context.Background(), "acc1", "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound = false for %v", err)
	}
	ae, ok := err.(*APIError)
	if !ok || !ae.HasCode(1000) {
		t.Fatalf("expected APIError code 1000, got %v", err)
	}
}

func TestTunnelConfig_PreservesUnknownKeys(t *testing.T) {
	// A config carrying a top-level key we do not model must survive a
	// decode → mutate → encode round-trip (CF replaces the whole config).
	in := []byte(`{"ingress":[{"hostname":"a.example.com","service":"http://localhost:1"},{"service":"http_status:404"}],"warp-routing":{"enabled":true},"future-key":{"x":1}}`)
	var cfg TunnelConfig
	if err := json.Unmarshal(in, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Ingress) != 2 || cfg.WarpRouting == nil || !cfg.WarpRouting.Enabled {
		t.Fatalf("modelled fields lost: %+v", cfg)
	}
	// Mutate ingress (add a rule before the catch-all), then re-encode.
	cfg.Ingress = append([]IngressRule{{Hostname: "b.example.com", Service: "http://localhost:2"}}, cfg.Ingress...)
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := back["future-key"]; !ok {
		t.Fatalf("unknown top-level key dropped: %s", out)
	}
	if _, ok := back["warp-routing"]; !ok {
		t.Fatalf("warp-routing dropped: %s", out)
	}
}

func TestCreateDNSRecord_Body(t *testing.T) {
	c, cap := newCF(t, func(r *http.Request) (any, int) {
		return map[string]any{"id": "rec1", "type": "CNAME", "name": "app.example.com"}, 200
	})
	proxied := true
	rec, err := c.CreateDNSRecord(context.Background(), "zone1", DNSRecord{
		Type: "CNAME", Name: "app.example.com", Content: "t1.cfargotunnel.com", Proxied: &proxied, TTL: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.ID != "rec1" {
		t.Fatalf("rec = %+v", rec)
	}
	if cap.path != "/zones/zone1/dns_records" {
		t.Fatalf("path = %q", cap.path)
	}
}
