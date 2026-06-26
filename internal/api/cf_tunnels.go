package api

import (
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/cfapi"
)

// TunnelsList lists the remote tunnels under a stored account.
func (h *CFHandler) TunnelsList(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	tunnels, err := client.ListTunnels(reqCtx(r), acc)
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": tunnels})
}

// TunnelsCreate creates a remote tunnel.
func (h *CFHandler) TunnelsCreate(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		ConfigSrc string `json:"config_src"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "name is required", nil)
		return
	}
	t, err := client.CreateTunnel(reqCtx(r), acc, cfapi.CreateTunnelReq{Name: body.Name, ConfigSrc: body.ConfigSrc})
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, t)
}

// TunnelsGet fetches one remote tunnel.
func (h *CFHandler) TunnelsGet(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	t, err := client.GetTunnel(reqCtx(r), acc, cfParam(r, "tid"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, t)
}

// TunnelsUpdate renames a remote tunnel.
func (h *CFHandler) TunnelsUpdate(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "name is required", nil)
		return
	}
	t, err := client.UpdateTunnel(reqCtx(r), acc, cfParam(r, "tid"), body.Name)
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, t)
}

// TunnelsDelete removes a remote tunnel.
func (h *CFHandler) TunnelsDelete(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	if err := client.DeleteTunnel(reqCtx(r), acc, cfParam(r, "tid")); err != nil {
		writeCFError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TunnelsToken returns the connector token for a remote tunnel. Treated as a
// secret: only available to authenticated operators on explicit request.
func (h *CFHandler) TunnelsToken(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	tok, err := client.GetTunnelToken(reqCtx(r), acc, cfParam(r, "tid"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"token": tok})
}

// TunnelsGetConfig fetches the remote ingress/origin configuration.
func (h *CFHandler) TunnelsGetConfig(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	cfg, err := client.GetConfiguration(reqCtx(r), acc, cfParam(r, "tid"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, cfg)
}

// TunnelsPutConfig replaces the remote configuration wholesale.
func (h *CFHandler) TunnelsPutConfig(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	var body struct {
		Config *cfapi.TunnelConfig `json:"config"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Config == nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "config is required", nil)
		return
	}
	cfg, err := client.PutConfiguration(reqCtx(r), acc, cfParam(r, "tid"), body.Config)
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, cfg)
}

// TunnelsConnections lists the active connectors of a remote tunnel.
func (h *CFHandler) TunnelsConnections(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	conns, err := client.ListConnections(reqCtx(r), acc, cfParam(r, "tid"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": conns})
}

// TunnelsCleanupConnections removes idle connections (all, or one client_id).
func (h *CFHandler) TunnelsCleanupConnections(w http.ResponseWriter, r *http.Request) {
	client, view, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	acc, ok := requireAccountID(w, view)
	if !ok {
		return
	}
	if err := client.CleanupConnections(reqCtx(r), acc, cfParam(r, "tid"), r.URL.Query().Get("client_id")); err != nil {
		writeCFError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
