package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/nue-mic/cloudflared-manager/internal/api/apiresp"
)

// Recover turns panics inside handlers into 500 responses instead of
// crashing the process.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic in handler",
						slog.Any("panic", rec),
						slog.String("path", r.URL.Path),
						slog.String("stack", string(debug.Stack())),
					)
					apiresp.Error(w, http.StatusInternalServerError, apiresp.CodeInternal, "internal server error", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
