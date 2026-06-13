package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cloudflared-manager/internal/cfaccount"
	"github.com/mia-clark/cloudflared-manager/internal/cfapi"
	"github.com/mia-clark/cloudflared-manager/internal/manager"
)

// CFHandler serves the Cloudflare-account integration endpoints: account CRUD
// + verification, remote tunnel / configuration / connection management, zones
// & DNS, and the instance↔tunnel binding with its public-hostname aggregation.
//
// It owns no Cloudflare state of its own — each request resolves a stored
// account to a per-call cfapi.Client. newClient is a seam so tests can point
// the client at an httptest server.
type CFHandler struct {
	store     *cfaccount.Store
	mgr       *manager.Manager
	log       *slog.Logger
	newClient func(cfaccount.Secret) *cfapi.Client
	// cfgMu serializes read-modify-write of a tunnel's remote configuration
	// (key "accountID|tunnelID"), so two concurrent public-hostname edits in
	// this process can't clobber each other through the GET→PUT window.
	cfgMu sync.Map
}

// lockConfig acquires the per-tunnel configuration mutex and returns its
// unlock func.
func (h *CFHandler) lockConfig(accountID, tunnelID string) func() {
	v, _ := h.cfgMu.LoadOrStore(accountID+"|"+tunnelID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// NewCFHandler builds the handler. store must be non-nil; mgr is used by the
// binding endpoints to read an instance's connector token.
func NewCFHandler(store *cfaccount.Store, mgr *manager.Manager, log *slog.Logger) *CFHandler {
	return &CFHandler{
		store:     store,
		mgr:       mgr,
		log:       log,
		newClient: defaultNewClient,
	}
}

func defaultNewClient(sec cfaccount.Secret) *cfapi.Client {
	return cfapi.New(cfapi.Credential{
		Type:  sec.AuthType,
		Token: sec.Token,
		Email: sec.Email,
		Key:   sec.Key,
	})
}

// cfParam returns a named chi URL parameter.
func cfParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

// registerCFRoutes mounts the full Cloudflare-integration surface on r. Shared
// by the production router (server.go) and the handler tests.
func registerCFRoutes(r chi.Router, cf *CFHandler) {
	// 账号 CRUD + 校验 + 列举 CF 账号
	r.Get("/api/v1/cf/accounts", cf.AccountsList)
	r.Post("/api/v1/cf/accounts", cf.AccountsCreate)
	r.Get("/api/v1/cf/accounts/{aid}", cf.AccountsGet)
	r.Patch("/api/v1/cf/accounts/{aid}", cf.AccountsUpdate)
	r.Delete("/api/v1/cf/accounts/{aid}", cf.AccountsDelete)
	r.Post("/api/v1/cf/accounts/{aid}/verify", cf.AccountsVerify)
	r.Get("/api/v1/cf/accounts/{aid}/cf-accounts", cf.AccountsCFList)

	// 远端隧道 / 配置 / 连接（经账号代理）
	r.Get("/api/v1/cf/accounts/{aid}/tunnels", cf.TunnelsList)
	r.Post("/api/v1/cf/accounts/{aid}/tunnels", cf.TunnelsCreate)
	r.Get("/api/v1/cf/accounts/{aid}/tunnels/{tid}", cf.TunnelsGet)
	r.Patch("/api/v1/cf/accounts/{aid}/tunnels/{tid}", cf.TunnelsUpdate)
	r.Delete("/api/v1/cf/accounts/{aid}/tunnels/{tid}", cf.TunnelsDelete)
	r.Get("/api/v1/cf/accounts/{aid}/tunnels/{tid}/token", cf.TunnelsToken)
	r.Get("/api/v1/cf/accounts/{aid}/tunnels/{tid}/configurations", cf.TunnelsGetConfig)
	r.Put("/api/v1/cf/accounts/{aid}/tunnels/{tid}/configurations", cf.TunnelsPutConfig)
	r.Get("/api/v1/cf/accounts/{aid}/tunnels/{tid}/connections", cf.TunnelsConnections)
	r.Delete("/api/v1/cf/accounts/{aid}/tunnels/{tid}/connections", cf.TunnelsCleanupConnections)

	// zones / DNS 记录
	r.Get("/api/v1/cf/accounts/{aid}/zones", cf.ZonesList)
	r.Get("/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records", cf.DNSList)
	r.Post("/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records", cf.DNSCreate)
	r.Put("/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records/{rid}", cf.DNSUpdate)
	r.Delete("/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records/{rid}", cf.DNSDelete)

	// 实例绑定 + 公共主机名聚合（复刻后台核心）
	r.Get("/api/v1/configs/{id}/cf/token-info", cf.TokenInfo)
	r.Get("/api/v1/configs/{id}/cf/binding", cf.BindingGet)
	r.Put("/api/v1/configs/{id}/cf/binding", cf.BindingSet)
	r.Delete("/api/v1/configs/{id}/cf/binding", cf.BindingDelete)
	r.Get("/api/v1/configs/{id}/cf/public-hostnames", cf.PublicHostnamesList)
	r.Post("/api/v1/configs/{id}/cf/public-hostnames", cf.PublicHostnamesCreate)
	r.Put("/api/v1/configs/{id}/cf/public-hostnames/{index}", cf.PublicHostnamesUpdate)
	r.Delete("/api/v1/configs/{id}/cf/public-hostnames/{index}", cf.PublicHostnamesDelete)
}

// clientFor resolves the stored account aid to a cfapi.Client and its redacted
// view. It writes a 404 and returns ok=false when the account is unknown.
func (h *CFHandler) clientFor(w http.ResponseWriter, aid string) (*cfapi.Client, cfaccount.View, bool) {
	view, ok := h.store.Get(aid)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "cf account not found", nil)
		return nil, cfaccount.View{}, false
	}
	sec, err := h.store.Secret(aid)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "read account secret: "+err.Error(), nil)
		return nil, cfaccount.View{}, false
	}
	return h.newClient(sec), view, true
}

// requireAccountID returns the resolved Cloudflare account id for view, or
// writes a 409 and returns ok=false when the account is unusable: credentials
// failed their last verification, or no account id has been discovered yet
// (unverified / ambiguous multi-account).
func requireAccountID(w http.ResponseWriter, view cfaccount.View) (string, bool) {
	if view.Status == cfaccount.StatusInvalid {
		WriteError(w, http.StatusConflict, CodeInvalidState,
			"该账号凭证最近一次校验未通过（invalid），请在账号页更新凭证并重新校验", nil)
		return "", false
	}
	if view.AccountID == "" {
		WriteError(w, http.StatusConflict, CodeInvalidState,
			"该账号尚未确定 Cloudflare account id，请先校验账号或指定 account_id", nil)
		return "", false
	}
	return view.AccountID, true
}

// writeCFError maps a cfapi error to an HTTP response, preserving Cloudflare's
// status (401/403/404) where possible and otherwise surfacing 502.
func writeCFError(w http.ResponseWriter, err error) {
	var ae *cfapi.APIError
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			// Upstream credential failure. Do NOT pass the 401/403 through:
			// the frontend's axios interceptor treats any 401 as "panel session
			// expired" and force-logs-out the operator. Surface it as 502 so a
			// stale Cloudflare token never evicts the panel session.
			WriteError(w, http.StatusBadGateway, CodeUpstreamFailure,
				"Cloudflare 拒绝了该账号的凭证（上游 HTTP "+strconv.Itoa(ae.StatusCode)+
					"）：请检查 API Token/Key 是否有效、是否具备所需权限。"+ae.Error(), nil)
			return
		case http.StatusNotFound:
			WriteError(w, http.StatusNotFound, CodeNotFound, ae.Error(), nil)
			return
		case http.StatusBadRequest, http.StatusConflict:
			WriteError(w, ae.StatusCode, CodeBadRequest, ae.Error(), nil)
			return
		default:
			status := ae.StatusCode
			if status < 400 {
				status = http.StatusBadGateway
			}
			WriteError(w, status, CodeUpstreamFailure, ae.Error(), nil)
			return
		}
	}
	WriteError(w, http.StatusBadGateway, CodeUpstreamFailure, "cloudflare request failed: "+err.Error(), nil)
}

// reqCtx returns the request context (single point in case we later add a
// per-call timeout independent of the client's transport timeout).
func reqCtx(r *http.Request) context.Context { return r.Context() }
