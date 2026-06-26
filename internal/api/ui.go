package api

import (
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strings"

	"github.com/nue-mic/cloudflared-manager/internal/manager"
)

// UIHandler serves the operator-customizable UI branding (brand name +
// browser <title>) and injects it into the SPA shell so the very first
// paint already shows the custom values (zero flash).
type UIHandler struct {
	mgr *manager.Manager
}

// NewUIHandler wires the handler to the manager that owns persistence.
func NewUIHandler(mgr *manager.Manager) *UIHandler {
	return &UIHandler{mgr: mgr}
}

// brandingResp is the GET / PUT response body. snake_case to match the
// project's Snapshot / system convention (this is NOT part of the
// camelCase TunnelConfigV1 subtree).
type brandingResp struct {
	AppName     string `json:"app_name"`
	AppSubtitle string `json:"app_subtitle"`
	HTMLTitle   string `json:"html_title"`
}

// brandingReq is the PUT body. Every field is an optional pointer: an omitted
// field keeps the current stored value, an empty string resets to default.
type brandingReq struct {
	AppName     *string `json:"app_name,omitempty"`
	AppSubtitle *string `json:"app_subtitle,omitempty"`
	HTMLTitle   *string `json:"html_title,omitempty"`
}

func toBrandingResp(b manager.Branding) brandingResp {
	return brandingResp{AppName: b.AppName, AppSubtitle: b.AppSubtitle, HTMLTitle: b.HTMLTitle}
}

// GetBranding (public, no auth) returns the effective branding so the login
// page and the <title> can render before the user is authenticated.
func (h *UIHandler) GetBranding(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, toBrandingResp(h.mgr.GetBranding()))
}

// UpdateBranding (auth) persists the branding. Omitted fields are preserved
// from the current stored value; an explicit empty string resets to default.
func (h *UIHandler) UpdateBranding(w http.ResponseWriter, r *http.Request) {
	var req brandingReq
	if !decodeJSON(w, r, &req) {
		return
	}
	next := h.mgr.GetBrandingRaw()
	if req.AppName != nil {
		next.AppName = *req.AppName
	}
	if req.AppSubtitle != nil {
		next.AppSubtitle = *req.AppSubtitle
	}
	if req.HTMLTitle != nil {
		next.HTMLTitle = *req.HTMLTitle
	}
	eff, err := h.mgr.SetBranding(next)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "persist branding: "+err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, toBrandingResp(eff))
}

var titleRe = regexp.MustCompile(`(?is)<title>.*?</title>`)

// InjectBranding rewrites the SPA index.html with the current branding:
//   - replaces the <title> with the custom html_title (HTML-escaped)
//   - injects window.__CFD_BRANDING__ before </head> so React reads the
//     brand synchronously on first render (zero flash)
//
// Branding is admin-controlled (set via the authenticated PUT) and both
// injection sites are escaped (html.EscapeString for the tag, json.Marshal —
// which escapes <, >, & to \u00xx — for the script), so there is no tag
// breakout / script injection even with hostile input.
func (h *UIHandler) InjectBranding(index []byte) []byte {
	b := h.mgr.GetBranding()
	s := string(index)

	// ReplaceAllStringFunc avoids $-group interpretation in the replacement.
	s = titleRe.ReplaceAllStringFunc(s, func(string) string {
		return "<title>" + html.EscapeString(b.HTMLTitle) + "</title>"
	})

	boot, _ := json.Marshal(toBrandingResp(b))
	script := "<script>window.__CFD_BRANDING__=" + string(boot) + ";</script>"
	if strings.Contains(s, "</head>") {
		s = strings.Replace(s, "</head>", script+"</head>", 1)
	} else {
		s = script + s
	}
	return []byte(s)
}
