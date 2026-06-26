package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/nue-mic/cloudflared-manager/internal/api/apiresp"
)

// Bearer enforces a static Bearer token on every request. The token may be
// supplied via the `Authorization: Bearer <token>` header or, as a fallback
// for WebSocket clients that cannot set headers, via the `token` query
// parameter.
func Bearer(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := extractToken(r)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				apiresp.Error(w, http.StatusUnauthorized, apiresp.CodeUnauthorized, "missing or invalid token", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	return ""
}
