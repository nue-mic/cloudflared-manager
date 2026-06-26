package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/nue-mic/cloudflared-manager/pkg/cfdconfig"
	"github.com/nue-mic/cloudflared-manager/pkg/cfdflags"
)

// ValidateHandler serves POST /api/v1/validate. It accepts either JSON
// (Content-Type application/json) carrying a TunnelConfigV1 body, or raw
// cloudflared YAML bytes (any other Content-Type).
type ValidateHandler struct{}

// NewValidateHandler builds a ValidateHandler.
func NewValidateHandler() *ValidateHandler { return &ValidateHandler{} }

type validateResp struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Validate parses and validates a cloudflared tunnel config without persisting it.
func (h *ValidateHandler) Validate(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "read body: "+err.Error(), nil)
		return
	}

	var sc *cfdconfig.TunnelConfigV1
	if strings.Contains(ct, "application/json") {
		parsed, jerr := cfdconfig.ParseJSON(body)
		if jerr != nil {
			WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{jerr.Error()}})
			return
		}
		sc = parsed
	} else {
		parsed, perr := cfdconfig.ParseYAML(body)
		if perr != nil {
			WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{perr.Error()}})
			return
		}
		sc = parsed
	}

	if verr := sc.Validate(); verr != nil {
		WriteJSON(w, http.StatusOK, validateResp{Valid: false, Errors: []string{verr.Error()}})
		return
	}

	// Cross-field rule: postQuantum requires protocol == "quic".
	var warnings []string
	if sc.Edge.PostQuantum && sc.Edge.Protocol != "quic" {
		WriteJSON(w, http.StatusOK, validateResp{
			Valid:  false,
			Errors: []string{"edge.postQuantum requires edge.protocol == \"quic\""},
		})
		return
	}

	// Advanced env override whitelist warnings.
	for k := range sc.AdvancedEnvOverrides {
		if !cfdflags.AllowEnvOverride(k) {
			warnings = append(warnings, "advancedEnvOverrides key "+k+" is not in the permitted allowlist and will be ignored at spawn time")
		}
	}

	resp := validateResp{Valid: true}
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	WriteJSON(w, http.StatusOK, resp)
}
