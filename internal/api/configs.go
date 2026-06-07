package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
)

// ConfigsHandler serves the /api/v1/configs endpoints.
type ConfigsHandler struct {
	m   *manager.Manager
	log *slog.Logger
}

// NewConfigsHandler returns a handler bound to the given manager.
func NewConfigsHandler(m *manager.Manager, log *slog.Logger) *ConfigsHandler {
	return &ConfigsHandler{m: m, log: log}
}

// configEnvelope wraps an instance snapshot, the TunnelConfigV1 and the
// manager-level metadata (name/manualStart) in one response body.
//
// SECURITY: the embedded Config NEVER carries the plaintext connector
// token in API responses — it is stripped by newEnvelope. HasToken tells
// the UI whether a token is stored without revealing it; the masked form
// is available via GET /configs/{id}/token.
type configEnvelope struct {
	manager.Snapshot
	Config   *cfdconfig.TunnelConfigV1 `json:"config"`
	Cfdmgr   manager.MgrMeta           `json:"cfdmgr"`
	HasToken bool                      `json:"has_token"`
}

// newEnvelope builds a response envelope with the connector token stripped
// from the config body (see tunnel.go: the token MUST NOT leak through any
// envelope-returning endpoint). The original sc is left untouched.
func newEnvelope(snap manager.Snapshot, sc *cfdconfig.TunnelConfigV1, mm manager.MgrMeta) configEnvelope {
	hasToken := sc != nil && sc.Token != ""
	var redacted *cfdconfig.TunnelConfigV1
	if sc != nil {
		cp := *sc // shallow copy is enough: we only overwrite the scalar Token field
		cp.Token = ""
		redacted = &cp
	}
	return configEnvelope{Snapshot: snap, Config: redacted, Cfdmgr: mm, HasToken: hasToken}
}

// maskToken returns a non-reversible preview of a connector token suitable
// for display (first 4 + bullets + last 4). Short tokens are fully masked.
func maskToken(t string) string {
	n := len(t)
	if n == 0 {
		return ""
	}
	if n <= 8 {
		return strings.Repeat("•", n)
	}
	return t[:4] + "••••••••" + t[n-4:]
}

// createReq is the input body for POST /configs.
type createReq struct {
	ID     string                    `json:"id"`
	Config *cfdconfig.TunnelConfigV1 `json:"config"`
	Cfdmgr manager.MgrMeta           `json:"cfdmgr"`
}

// List returns every registered config.
func (h *ConfigsHandler) List(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"items": h.m.List()})
}

// Get returns one config snapshot plus the parsed TunnelConfigV1 body.
func (h *ConfigsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	snap, sc, mm, err := h.m.Get(id)
	if writeManagerError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, newEnvelope(snap, sc, mm))
}

// Token returns a masked preview of the stored connector token. The
// plaintext token is never served by any endpoint.
//
// GET /api/v1/configs/{id}/token
func (h *ConfigsHandler) Token(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, sc, _, err := h.m.Get(id)
	if writeManagerError(w, err) {
		return
	}
	tok := ""
	if sc != nil {
		tok = sc.Token
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"has_token": tok != "",
		"masked":    maskToken(tok),
		"length":    len(tok),
	})
}

// Create persists a new config from the supplied TunnelConfigV1 body.
func (h *ConfigsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ID == "" || req.Config == nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "id and config are required", nil)
		return
	}
	if err := h.m.Create(req.ID, req.Config, req.Cfdmgr); writeManagerError(w, err) {
		return
	}
	snap, sc, mm, _ := h.m.Get(req.ID)
	WriteJSON(w, http.StatusCreated, newEnvelope(snap, sc, mm))
}

// Update replaces the whole config body for an existing instance.
func (h *ConfigsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var body struct {
		Config *cfdconfig.TunnelConfigV1 `json:"config"`
		Cfdmgr manager.MgrMeta           `json:"cfdmgr"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Config == nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "config is required", nil)
		return
	}
	// Token preservation: API responses no longer echo the token, so the UI
	// submits an empty token to mean "keep the existing one". Only an
	// explicit non-empty token replaces the stored secret. (Clearing a token
	// is a power-user action done via the raw YAML endpoint.)
	if body.Config.Token == "" {
		if _, cur, _, gerr := h.m.Get(id); gerr == nil && cur != nil && cur.Token != "" {
			body.Config.Token = cur.Token
		}
	}
	if err := h.m.Update(id, body.Config, body.Cfdmgr); writeManagerError(w, err) {
		return
	}
	snap, sc, mm, _ := h.m.Get(id)
	WriteJSON(w, http.StatusOK, newEnvelope(snap, sc, mm))
}

// Patch applies a JSON merge over the existing TunnelConfigV1 body. The
// manager metadata (cfdmgr) is preserved unless the patch carries a
// top-level "cfdmgr" object.
func (h *ConfigsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, sc, mm, err := h.m.Get(id)
	if writeManagerError(w, err) {
		return
	}
	curBytes, err := cfdconfig.MarshalJSON(sc)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "marshal current: "+err.Error(), nil)
		return
	}
	patch, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "read body: "+err.Error(), nil)
		return
	}
	merged, err := mergeJSON(curBytes, patch)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "merge patch: "+err.Error(), nil)
		return
	}
	next, err := cfdconfig.ParseJSON(merged)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "decode merged: "+err.Error(), nil)
		return
	}
	if err := h.m.Update(id, next, mm); writeManagerError(w, err) {
		return
	}
	snap, fresh, freshMM, _ := h.m.Get(id)
	WriteJSON(w, http.StatusOK, newEnvelope(snap, fresh, freshMM))
}

// Delete stops and removes an instance.
func (h *ConfigsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := h.m.Delete(id); writeManagerError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Duplicate creates a copy under a new id supplied in the body.
func (h *ConfigsHandler) Duplicate(w http.ResponseWriter, r *http.Request) {
	src := pathID(r)
	var body struct {
		NewID string `json:"new_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.NewID == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "new_id is required", nil)
		return
	}
	_, sc, mm, err := h.m.Get(src)
	if writeManagerError(w, err) {
		return
	}
	if sc != nil {
		sc.Token = ""
	}
	if err := h.m.Create(body.NewID, sc, mm); writeManagerError(w, err) {
		return
	}
	snap, fresh, freshMM, _ := h.m.Get(body.NewID)
	WriteJSON(w, http.StatusCreated, newEnvelope(snap, fresh, freshMM))
}

// GetRaw returns the on-disk YAML bytes verbatim.
func (h *ConfigsHandler) GetRaw(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	b, err := h.m.ReadRaw(id)
	if writeManagerError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(b)
}

// PutRaw accepts a raw cloudflared YAML body and replaces the file on disk.
func (h *ConfigsHandler) PutRaw(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "read body: "+err.Error(), nil)
		return
	}
	if err := h.m.WriteRaw(id, body); writeManagerError(w, err) {
		return
	}
	snap, sc, mm, _ := h.m.Get(id)
	WriteJSON(w, http.StatusOK, newEnvelope(snap, sc, mm))
}

// Reorder persists the user's chosen display order.
func (h *ConfigsHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Order []string `json:"order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.m.Reorder(body.Order); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mergeJSON applies an RFC 7396 merge-patch onto base. It only handles
// object-typed roots, which is all our config schema needs.
func mergeJSON(base, patch []byte) ([]byte, error) {
	var b, p map[string]any
	if err := json.Unmarshal(base, &b); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, err
	}
	mergeMap(b, p)
	return json.Marshal(b)
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			if cur, ok2 := dst[k].(map[string]any); ok2 {
				mergeMap(cur, sub)
				continue
			}
		}
		dst[k] = v
	}
}
