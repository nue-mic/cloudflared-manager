package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cloudflared-manager/internal/manager"
)

// pathID returns the chi URL param "id" or empty string.
func pathID(r *http.Request) string {
	return chi.URLParam(r, "id")
}

// pathVersion returns the chi URL param "version" or empty string.
func pathVersion(r *http.Request) string {
	return chi.URLParam(r, "version")
}

// decodeJSON parses the request body into dst. A 400 is written and false
// returned on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid JSON body: "+err.Error(), nil)
		return false
	}
	return true
}

// decodeJSONOptional is like decodeJSON but treats an empty body (io.EOF) as a
// successful no-op, for endpoints whose request body is optional. This is
// robust to clients that send no body, an empty body, OR a chunked body with no
// Content-Length. Only a malformed NON-empty body yields a 400.
func decodeJSONOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true // no body → leave dst at its defaults
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid JSON body: "+err.Error(), nil)
		return false
	}
	return true
}

// writeManagerError maps manager sentinel errors to HTTP responses.
// Returns true if it handled the error.
func writeManagerError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, manager.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeConfigNotFound, err.Error(), nil)
	case errors.Is(err, manager.ErrExists):
		WriteError(w, http.StatusConflict, CodeConfigExists, err.Error(), nil)
	default:
		// State-machine violations ("already running" / "not running") are
		// 409 Conflict — the resource exists but its current state forbids
		// the requested transition.
		msg := err.Error()
		if msg == "already running" || msg == "not running" {
			WriteError(w, http.StatusConflict, CodeInvalidState, msg, nil)
		} else {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, msg, nil)
		}
	}
	return true
}
