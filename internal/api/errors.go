package api

import (
	"net/http"

	"github.com/nue-mic/cloudflared-manager/internal/api/apiresp"
)

// Re-exports of the apiresp helpers so handlers in this package can use
// the short form (api.WriteJSON / api.WriteError) without each handler
// importing apiresp directly.

type ErrorCode = apiresp.ErrorCode

const (
	CodeBadRequest      = apiresp.CodeBadRequest
	CodeUnauthorized    = apiresp.CodeUnauthorized
	CodeForbidden       = apiresp.CodeForbidden
	CodeNotFound        = apiresp.CodeNotFound
	CodeConflict        = apiresp.CodeConflict
	CodeValidation      = apiresp.CodeValidation
	CodeInternal        = apiresp.CodeInternal
	CodeConfigNotFound  = apiresp.CodeConfigNotFound
	CodeConfigExists    = apiresp.CodeConfigExists
	CodeInvalidState    = apiresp.CodeInvalidState
	CodeProxyNotFound   = apiresp.CodeProxyNotFound
	CodeProxyExists     = apiresp.CodeProxyExists
	CodeUpstreamFailure = apiresp.CodeUpstreamFailure
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	apiresp.JSON(w, status, v)
}

func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string, details map[string]any) {
	apiresp.Error(w, status, code, message, details)
}
