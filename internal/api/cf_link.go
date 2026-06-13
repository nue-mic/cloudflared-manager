package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mia-clark/cloudflared-manager/internal/cfaccount"
	"github.com/mia-clark/cloudflared-manager/internal/cfapi"
)

// cfargoSuffix is appended to a tunnel id to form the proxied CNAME target a
// public hostname must point at.
const cfargoSuffix = ".cfargotunnel.com"

// instanceToken reads the connector token stored for a local instance.
func (h *CFHandler) instanceToken(id string) (string, bool) {
	_, sc, _, err := h.mgr.Get(id)
	if err != nil || sc == nil {
		return "", false
	}
	return sc.Token, sc.Token != ""
}

// TokenInfo decodes a local instance's connector token to surface the account
// tag + tunnel id it embeds (no secret), powering one-click linking.
//
// GET /api/v1/configs/{id}/cf/token-info
func (h *CFHandler) TokenInfo(w http.ResponseWriter, r *http.Request) {
	tok, has := h.instanceToken(cfParam(r, "id"))
	resp := map[string]any{"has_token": has}
	if has {
		claims, err := cfapi.DecodeTunnelToken(tok)
		if err != nil {
			resp["error"] = err.Error()
		} else {
			resp["account_tag"] = claims.AccountTag
			resp["tunnel_id"] = claims.TunnelID
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}

// bindingResp is the binding state returned to the UI.
type bindingResp struct {
	Bound           bool          `json:"bound"`
	AccountID       string        `json:"account_id,omitempty"`
	AccountName     string        `json:"account_name,omitempty"`
	CFAccountID     string        `json:"cf_account_id,omitempty"`
	TunnelID        string        `json:"tunnel_id,omitempty"`
	TunnelName      string        `json:"tunnel_name,omitempty"`
	AccountTag      string        `json:"account_tag,omitempty"`
	TokenAccountTag string        `json:"token_account_tag,omitempty"`
	TokenTunnelID   string        `json:"token_tunnel_id,omitempty"`
	Match           bool          `json:"match"`
	Tunnel          *cfapi.Tunnel `json:"tunnel,omitempty"`
}

// BindingGet returns the current instance↔tunnel binding plus a best-effort
// remote tunnel snapshot.
//
// GET /api/v1/configs/{id}/cf/binding
func (h *CFHandler) BindingGet(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	b, ok := h.store.Binding(id)
	if !ok {
		WriteJSON(w, http.StatusOK, bindingResp{Bound: false})
		return
	}
	resp := bindingResp{
		Bound:      true,
		AccountID:  b.AccountID,
		TunnelID:   b.TunnelID,
		TunnelName: b.TunnelName,
		AccountTag: b.AccountTag,
	}
	if view, ok := h.store.Get(b.AccountID); ok {
		resp.AccountName = view.Name
		resp.CFAccountID = view.AccountID
	}
	if tok, has := h.instanceToken(id); has {
		if claims, err := cfapi.DecodeTunnelToken(tok); err == nil {
			resp.TokenAccountTag = claims.AccountTag
			resp.TokenTunnelID = claims.TunnelID
			resp.Match = strings.EqualFold(claims.TunnelID, b.TunnelID)
		}
	}
	// Best-effort live tunnel snapshot; never fail the binding read on it.
	if view, ok := h.store.Get(b.AccountID); ok && view.AccountID != "" {
		if sec, err := h.store.Secret(b.AccountID); err == nil {
			if t, err := h.newClient(sec).GetTunnel(reqCtx(r), view.AccountID, b.TunnelID); err == nil {
				resp.Tunnel = t
			}
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}

// BindingSet links an instance to a remote tunnel under a stored account,
// validating ownership by fetching the tunnel from Cloudflare.
//
// PUT /api/v1/configs/{id}/cf/binding   body: {account_id, tunnel_id?}
func (h *CFHandler) BindingSet(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	if !h.mgr.Exists(id) {
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, "instance not found", nil)
		return
	}
	var body struct {
		AccountID string `json:"account_id"`
		TunnelID  string `json:"tunnel_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.AccountID == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "account_id is required", nil)
		return
	}
	view, ok := h.store.Get(body.AccountID)
	if !ok {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "未知的 Cloudflare 账号", nil)
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}

	// Resolve the tunnel id: explicit wins, else decode the instance token.
	tunnelID := strings.TrimSpace(body.TunnelID)
	var tokenTag, tokenTid string
	if tok, has := h.instanceToken(id); has {
		if claims, err := cfapi.DecodeTunnelToken(tok); err == nil {
			tokenTag, tokenTid = claims.AccountTag, claims.TunnelID
			if tunnelID == "" {
				tunnelID = claims.TunnelID
			}
		}
	}
	if tunnelID == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest,
			"无法确定 tunnel_id：请显式提供，或确保实例已配置可解码的 token", nil)
		return
	}

	sec, err := h.store.Secret(body.AccountID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	client := h.newClient(sec)

	// Ownership validation: the tunnel must exist (and not be deleted) under
	// this account.
	tunnel, err := client.GetTunnel(reqCtx(r), acc, tunnelID)
	if err != nil {
		if cfapi.IsNotFound(err) {
			WriteError(w, http.StatusBadRequest, CodeValidation,
				"该隧道不属于此账号（在该账号下未找到此 tunnel_id）", nil)
			return
		}
		writeCFError(w, err)
		return
	}
	if tunnel.DeletedAt != "" {
		WriteError(w, http.StatusBadRequest, CodeValidation, "该隧道已被删除", nil)
		return
	}

	bind := cfaccount.Binding{
		AccountID:  body.AccountID,
		TunnelID:   tunnelID,
		TunnelName: tunnel.Name,
		AccountTag: tunnel.AccountTag,
	}
	if err := h.store.SetBinding(id, bind); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}

	resp := bindingResp{
		Bound:           true,
		AccountID:       body.AccountID,
		AccountName:     view.Name,
		CFAccountID:     acc,
		TunnelID:        tunnelID,
		TunnelName:      tunnel.Name,
		AccountTag:      tunnel.AccountTag,
		TokenAccountTag: tokenTag,
		TokenTunnelID:   tokenTid,
		Match:           tokenTid == "" || strings.EqualFold(tokenTid, tunnelID),
		Tunnel:          tunnel,
	}
	WriteJSON(w, http.StatusOK, resp)
}

// BindingDelete unlinks an instance.
//
// DELETE /api/v1/configs/{id}/cf/binding
func (h *CFHandler) BindingDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteBinding(cfParam(r, "id")); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Public hostnames (ingress + DNS) ─────────────────────────────────────

type dnsStatus struct {
	ZoneID   string `json:"zone_id,omitempty"`
	ZoneName string `json:"zone_name,omitempty"`
	RecordID string `json:"record_id,omitempty"`
	Content  string `json:"content,omitempty"`
	Proxied  bool   `json:"proxied"`
	Exists   bool   `json:"exists"`
	InSync   bool   `json:"in_sync"`
}

type publicHostname struct {
	Index         int            `json:"index"`
	Hostname      string         `json:"hostname"`
	Service       string         `json:"service"`
	Path          string         `json:"path,omitempty"`
	OriginRequest map[string]any `json:"origin_request,omitempty"`
	DNS           *dnsStatus     `json:"dns,omitempty"`
}

// requireBinding resolves an instance's binding to (binding, cfAccountID,
// client). It writes a 409/500 and returns ok=false when not linked.
func (h *CFHandler) requireBinding(w http.ResponseWriter, instanceID string) (cfaccount.Binding, string, *cfapi.Client, bool) {
	b, ok := h.store.Binding(instanceID)
	if !ok {
		WriteError(w, http.StatusConflict, CodeInvalidState, "实例尚未关联 Cloudflare 账号", nil)
		return cfaccount.Binding{}, "", nil, false
	}
	view, ok := h.store.Get(b.AccountID)
	if !ok {
		WriteError(w, http.StatusConflict, CodeInvalidState, "关联的 Cloudflare 账号已不存在", nil)
		return cfaccount.Binding{}, "", nil, false
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return cfaccount.Binding{}, "", nil, false
	}
	sec, err := h.store.Secret(b.AccountID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return cfaccount.Binding{}, "", nil, false
	}
	return b, acc, h.newClient(sec), true
}

// PublicHostnamesList aggregates the tunnel's ingress rules with each
// hostname's DNS record state. DNS enrichment is best-effort: a permission or
// zone error is reported in dns_error rather than failing the read.
//
// GET /api/v1/configs/{id}/cf/public-hostnames
func (h *CFHandler) PublicHostnamesList(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	b, acc, client, ok := h.requireBinding(w, id)
	if !ok {
		return
	}
	ctx := reqCtx(r)
	cfg, err := client.GetConfiguration(ctx, acc, b.TunnelID)
	if err != nil {
		writeCFError(w, err)
		return
	}
	ingress := []cfapi.IngressRule{}
	if cfg.Config != nil {
		ingress = cfg.Config.Ingress
	}

	items := make([]publicHostname, 0, len(ingress))
	expected := b.TunnelID + cfargoSuffix
	zoneCache := newZoneCache(client)
	var dnsErr string
	for i, rule := range ingress {
		ph := publicHostname{
			Index:         i,
			Hostname:      rule.Hostname,
			Service:       rule.Service,
			Path:          rule.Path,
			OriginRequest: rule.OriginRequest,
		}
		if rule.Hostname != "" {
			st, derr := zoneCache.status(ctx, rule.Hostname, expected)
			if derr != nil {
				if dnsErr == "" {
					dnsErr = derr.Error()
				}
			} else {
				ph.DNS = st
			}
		}
		items = append(items, ph)
	}
	resp := map[string]any{"items": items, "tunnel_id": b.TunnelID}
	if dnsErr != "" {
		resp["dns_error"] = dnsErr
	}
	WriteJSON(w, http.StatusOK, resp)
}

// phWriteReq is the body for add/edit of a public hostname.
type phWriteReq struct {
	Hostname      string         `json:"hostname"`
	Service       string         `json:"service"`
	Path          string         `json:"path"`
	OriginRequest map[string]any `json:"origin_request"`
	ManageDNS     *bool          `json:"manage_dns"`
}

func (req phWriteReq) manageDNS() bool { return req.ManageDNS == nil || *req.ManageDNS }

// PublicHostnamesCreate appends a public hostname (ingress rule before the
// catch-all) and, unless manage_dns=false, upserts a proxied CNAME for it.
//
// POST /api/v1/configs/{id}/cf/public-hostnames
func (h *CFHandler) PublicHostnamesCreate(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	b, acc, client, ok := h.requireBinding(w, id)
	if !ok {
		return
	}
	var req phWriteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	req.Service = strings.TrimSpace(req.Service)
	if req.Hostname == "" || req.Service == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "hostname 和 service 必填", nil)
		return
	}
	// Serialize the GET→PUT against concurrent edits of the same tunnel.
	unlock := h.lockConfig(acc, b.TunnelID)
	defer unlock()
	ctx := reqCtx(r)
	cfg, err := client.GetConfiguration(ctx, acc, b.TunnelID)
	if err != nil {
		writeCFError(w, err)
		return
	}
	tc := ensureConfig(cfg)
	rule := cfapi.IngressRule{Hostname: req.Hostname, Service: req.Service, Path: req.Path, OriginRequest: req.OriginRequest}
	tc.Ingress = insertHostnameRule(tc.Ingress, rule)
	if _, err := client.PutConfiguration(ctx, acc, b.TunnelID, tc); err != nil {
		writeCFError(w, err)
		return
	}

	resp := map[string]any{"hostname": req.Hostname, "service": req.Service}
	if req.manageDNS() {
		st, derr := h.ensureTunnelCNAME(ctx, client, req.Hostname, b.TunnelID)
		if derr != nil {
			resp["dns_error"] = derr.Error()
		} else {
			resp["dns"] = st
		}
	}
	WriteJSON(w, http.StatusCreated, resp)
}

// PublicHostnamesUpdate edits the ingress rule at {index} (must be a hostname
// rule) and re-syncs its DNS record.
//
// PUT /api/v1/configs/{id}/cf/public-hostnames/{index}
func (h *CFHandler) PublicHostnamesUpdate(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	idx, perr := strconv.Atoi(cfParam(r, "index"))
	if perr != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "index 必须是整数", nil)
		return
	}
	b, acc, client, ok := h.requireBinding(w, id)
	if !ok {
		return
	}
	var req phWriteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	unlock := h.lockConfig(acc, b.TunnelID)
	defer unlock()
	ctx := reqCtx(r)
	cfg, err := client.GetConfiguration(ctx, acc, b.TunnelID)
	if err != nil {
		writeCFError(w, err)
		return
	}
	tc := ensureConfig(cfg)
	if idx < 0 || idx >= len(tc.Ingress) || tc.Ingress[idx].Hostname == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "index 越界或指向兜底规则", nil)
		return
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	req.Service = strings.TrimSpace(req.Service)
	if req.Hostname == "" || req.Service == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "hostname 和 service 必填", nil)
		return
	}
	tc.Ingress[idx] = cfapi.IngressRule{Hostname: req.Hostname, Service: req.Service, Path: req.Path, OriginRequest: req.OriginRequest}
	if _, err := client.PutConfiguration(ctx, acc, b.TunnelID, tc); err != nil {
		writeCFError(w, err)
		return
	}
	resp := map[string]any{"hostname": req.Hostname, "service": req.Service, "index": idx}
	if req.manageDNS() {
		st, derr := h.ensureTunnelCNAME(ctx, client, req.Hostname, b.TunnelID)
		if derr != nil {
			resp["dns_error"] = derr.Error()
		} else {
			resp["dns"] = st
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}

// PublicHostnamesDelete removes the ingress rule at {index}. ?delete_dns=true
// also removes the matching proxied CNAME.
//
// DELETE /api/v1/configs/{id}/cf/public-hostnames/{index}
func (h *CFHandler) PublicHostnamesDelete(w http.ResponseWriter, r *http.Request) {
	id := cfParam(r, "id")
	idx, perr := strconv.Atoi(cfParam(r, "index"))
	if perr != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "index 必须是整数", nil)
		return
	}
	b, acc, client, ok := h.requireBinding(w, id)
	if !ok {
		return
	}
	unlock := h.lockConfig(acc, b.TunnelID)
	defer unlock()
	ctx := reqCtx(r)
	cfg, err := client.GetConfiguration(ctx, acc, b.TunnelID)
	if err != nil {
		writeCFError(w, err)
		return
	}
	tc := ensureConfig(cfg)
	if idx < 0 || idx >= len(tc.Ingress) || tc.Ingress[idx].Hostname == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "index 越界或指向兜底规则", nil)
		return
	}
	hostname := tc.Ingress[idx].Hostname
	tc.Ingress = append(tc.Ingress[:idx], tc.Ingress[idx+1:]...)
	if _, err := client.PutConfiguration(ctx, acc, b.TunnelID, tc); err != nil {
		writeCFError(w, err)
		return
	}
	resp := map[string]any{"deleted": hostname}
	if r.URL.Query().Get("delete_dns") == "true" {
		if derr := h.deleteTunnelCNAME(ctx, client, hostname); derr != nil {
			resp["dns_error"] = derr.Error()
		} else {
			resp["dns_deleted"] = true
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}

// ── DNS helpers ──────────────────────────────────────────────────────────

// ensureConfig returns a usable *TunnelConfig from a configuration result,
// allocating one when the tunnel has no remote config yet.
func ensureConfig(cfg *cfapi.ConfigurationResult) *cfapi.TunnelConfig {
	if cfg != nil && cfg.Config != nil {
		return cfg.Config
	}
	return &cfapi.TunnelConfig{}
}

// insertHostnameRule inserts rule before the first catch-all (hostname-less)
// rule, appending a default catch-all when none exists.
func insertHostnameRule(ingress []cfapi.IngressRule, rule cfapi.IngressRule) []cfapi.IngressRule {
	ci := -1
	for i := range ingress {
		if ingress[i].Hostname == "" {
			ci = i
			break
		}
	}
	out := make([]cfapi.IngressRule, 0, len(ingress)+2)
	if ci < 0 {
		out = append(out, ingress...)
		out = append(out, rule)
		out = append(out, cfapi.IngressRule{Service: "http_status:404"})
		return out
	}
	out = append(out, ingress[:ci]...)
	out = append(out, rule)
	out = append(out, ingress[ci:]...)
	return out
}

// ensureTunnelCNAME upserts a proxied CNAME hostname → <tunnelID>.cfargotunnel.com.
func (h *CFHandler) ensureTunnelCNAME(ctx context.Context, client *cfapi.Client, hostname, tunnelID string) (*dnsStatus, error) {
	zone, err := findZone(ctx, client, hostname)
	if err != nil {
		return nil, err
	}
	expected := tunnelID + cfargoSuffix
	recs, err := client.ListDNSRecords(ctx, zone.ID, hostname)
	if err != nil {
		return nil, err
	}
	proxied := true
	desired := cfapi.DNSRecord{Type: "CNAME", Name: hostname, Content: expected, Proxied: &proxied, TTL: 1, Comment: "cfdmgr tunnel"}
	for i := range recs {
		if strings.EqualFold(recs[i].Name, hostname) && recs[i].Type == "CNAME" {
			cur := recs[i]
			inSync := cur.Content == expected && cur.Proxied != nil && *cur.Proxied
			if inSync {
				return &dnsStatus{ZoneID: zone.ID, ZoneName: zone.Name, RecordID: cur.ID, Content: cur.Content, Proxied: true, Exists: true, InSync: true}, nil
			}
			out, uerr := client.UpdateDNSRecord(ctx, zone.ID, cur.ID, desired)
			if uerr != nil {
				return nil, uerr
			}
			return &dnsStatus{ZoneID: zone.ID, ZoneName: zone.Name, RecordID: out.ID, Content: out.Content, Proxied: true, Exists: true, InSync: true}, nil
		}
	}
	out, cerr := client.CreateDNSRecord(ctx, zone.ID, desired)
	if cerr != nil {
		return nil, cerr
	}
	return &dnsStatus{ZoneID: zone.ID, ZoneName: zone.Name, RecordID: out.ID, Content: out.Content, Proxied: true, Exists: true, InSync: true}, nil
}

// deleteTunnelCNAME removes the CNAME record for hostname, if present.
func (h *CFHandler) deleteTunnelCNAME(ctx context.Context, client *cfapi.Client, hostname string) error {
	zone, err := findZone(ctx, client, hostname)
	if err != nil {
		return err
	}
	recs, err := client.ListDNSRecords(ctx, zone.ID, hostname)
	if err != nil {
		return err
	}
	for i := range recs {
		if strings.EqualFold(recs[i].Name, hostname) && recs[i].Type == "CNAME" {
			return client.DeleteDNSRecord(ctx, zone.ID, recs[i].ID)
		}
	}
	return nil
}

// zoneCandidates yields the apex-domain candidates for a hostname, most
// specific first: app.svc.example.com → [app.svc.example.com, svc.example.com,
// example.com]. The single-label TLD is excluded.
func zoneCandidates(hostname string) []string {
	parts := strings.Split(strings.TrimSuffix(hostname, "."), ".")
	out := make([]string, 0, len(parts))
	for i := 0; i+1 < len(parts); i++ {
		out = append(out, strings.Join(parts[i:], "."))
	}
	return out
}

// findZone resolves the zone owning hostname by querying Cloudflare for each
// candidate apex by EXACT name (server-side filter), so it never misses a zone
// just because the account has more zones than one list page returns.
func findZone(ctx context.Context, client *cfapi.Client, hostname string) (*cfapi.Zone, error) {
	for _, cand := range zoneCandidates(hostname) {
		zones, err := client.ListZones(ctx, cand)
		if err != nil {
			return nil, err
		}
		for i := range zones {
			if strings.EqualFold(zones[i].Name, cand) {
				z := zones[i]
				return &z, nil
			}
		}
	}
	return nil, fmt.Errorf("未找到 %s 对应的 Cloudflare zone（域名未托管或 token 无 DNS 权限）", hostname)
}

// zoneCache memoizes exact-name zone lookups and per-zone DNS record listings
// across the hostnames of one PublicHostnamesList call.
type zoneCache struct {
	client    *cfapi.Client
	zoneByCfg map[string]*cfapi.Zone       // candidate apex → zone (nil = queried, none)
	records   map[string][]cfapi.DNSRecord // zoneID → records
}

func newZoneCache(client *cfapi.Client) *zoneCache {
	return &zoneCache{client: client, zoneByCfg: map[string]*cfapi.Zone{}, records: map[string][]cfapi.DNSRecord{}}
}

func (zc *zoneCache) zoneFor(ctx context.Context, hostname string) (*cfapi.Zone, error) {
	for _, cand := range zoneCandidates(hostname) {
		if z, seen := zc.zoneByCfg[cand]; seen {
			if z != nil {
				return z, nil
			}
			continue
		}
		zones, err := zc.client.ListZones(ctx, cand)
		if err != nil {
			return nil, err
		}
		var found *cfapi.Zone
		for i := range zones {
			if strings.EqualFold(zones[i].Name, cand) {
				z := zones[i]
				found = &z
				break
			}
		}
		zc.zoneByCfg[cand] = found
		if found != nil {
			return found, nil
		}
	}
	return nil, fmt.Errorf("未找到 %s 对应的 zone", hostname)
}

func (zc *zoneCache) status(ctx context.Context, hostname, expected string) (*dnsStatus, error) {
	zone, err := zc.zoneFor(ctx, hostname)
	if err != nil {
		return nil, err
	}
	recs, ok := zc.records[zone.ID]
	if !ok {
		recs, err = zc.client.ListDNSRecords(ctx, zone.ID, "")
		if err != nil {
			return nil, err
		}
		zc.records[zone.ID] = recs
	}
	for i := range recs {
		if strings.EqualFold(recs[i].Name, hostname) && recs[i].Type == "CNAME" {
			cur := recs[i]
			proxied := cur.Proxied != nil && *cur.Proxied
			return &dnsStatus{
				ZoneID: zone.ID, ZoneName: zone.Name, RecordID: cur.ID,
				Content: cur.Content, Proxied: proxied, Exists: true,
				InSync: cur.Content == expected && proxied,
			}, nil
		}
	}
	return &dnsStatus{ZoneID: zone.ID, ZoneName: zone.Name, Exists: false, InSync: false}, nil
}
