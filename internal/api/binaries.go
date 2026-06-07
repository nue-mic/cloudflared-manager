package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/mia-clark/cloudflared-manager/internal/cfdbin"
)

// versionParamRE guards the {version} path param (and install body) against
// path-traversal: a version tag must be a single safe path segment. The
// "latest" sentinel matches and is resolved by the downloader.
var versionParamRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validVersionParam(v string) bool { return versionParamRE.MatchString(v) }

// BinariesHandler exposes the /api/v1/binaries/* endpoints.
type BinariesHandler struct {
	store          *cfdbin.Store
	dl             *cfdbin.Downloader
	logger         *slog.Logger
	defaultVersion string // CFDM_CLOUDFLARED_DEFAULT_VERSION; used when Install omits version
}

// NewBinariesHandler constructs a handler. store and dl may be nil; in that
// case every mutating endpoint returns 503. defaultVersion ("" or "latest")
// is the version Install falls back to when the request omits one.
func NewBinariesHandler(store *cfdbin.Store, dl *cfdbin.Downloader, defaultVersion string, logger *slog.Logger) *BinariesHandler {
	if defaultVersion == "" {
		defaultVersion = "latest"
	}
	return &BinariesHandler{store: store, dl: dl, logger: logger, defaultVersion: defaultVersion}
}

// List returns all locally installed cloudflared versions.
//
// GET /api/v1/binaries
func (h *BinariesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := h.store.List()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "list binaries: "+err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Available returns releases available for download from GitHub.
//
// GET /api/v1/binaries/available
func (h *BinariesHandler) Available(w http.ResponseWriter, r *http.Request) {
	if h.dl == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "downloader not configured", nil)
		return
	}
	items, err := h.dl.Available(r.Context(), 10)
	if err != nil {
		WriteError(w, http.StatusBadGateway, CodeUpstreamFailure, "fetch releases: "+err.Error(), nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Install downloads and stores a cloudflared binary.
//
// POST /api/v1/binaries/install
// Body: {"version":"2026.5.2"}  — version is required.
func (h *BinariesHandler) Install(w http.ResponseWriter, r *http.Request) {
	if h.store == nil || h.dl == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "binary store not configured", nil)
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Version == "" {
		body.Version = h.defaultVersion // CFDM_CLOUDFLARED_DEFAULT_VERSION (default "latest")
	}
	if !validVersionParam(body.Version) {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid version tag", nil)
		return
	}

	// Run the potentially long download in the request context so the client
	// can cancel it. Timeouts should be set by a reverse proxy / the caller.
	meta, err := h.store.Install(r.Context(), h.dl, body.Version)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			WriteError(w, http.StatusGatewayTimeout, CodeInternal, "download cancelled or timed out", nil)
			return
		}
		WriteError(w, http.StatusBadGateway, CodeUpstreamFailure, "install: "+err.Error(), nil)
		return
	}

	h.logger.Info("binary installed", slog.String("version", meta.Version), slog.String("sha256", meta.SHA256))
	WriteJSON(w, http.StatusCreated, meta)
}

// Activate sets the active cloudflared version.
//
// POST /api/v1/binaries/{version}/activate
func (h *BinariesHandler) Activate(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "binary store not configured", nil)
		return
	}
	version := pathVersion(r)
	if version == "" || !validVersionParam(version) {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid or missing version path parameter", nil)
		return
	}
	if err := h.store.Activate(version); err != nil {
		if errors.Is(err, cfdbin.ErrNotInstalled) {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error(), nil)
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeInternal, "activate: "+err.Error(), nil)
		return
	}
	h.logger.Info("binary activated", slog.String("version", version))
	WriteJSON(w, http.StatusOK, map[string]any{"version": version, "active": true})
}

// Delete removes an installed version from the store.
//
// DELETE /api/v1/binaries/{version}
func (h *BinariesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "binary store not configured", nil)
		return
	}
	version := pathVersion(r)
	if version == "" || !validVersionParam(version) {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid or missing version path parameter", nil)
		return
	}
	if err := h.store.Delete(version); err != nil {
		if errors.Is(err, cfdbin.ErrNotInstalled) {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error(), nil)
			return
		}
		WriteError(w, http.StatusConflict, CodeConflict, err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
