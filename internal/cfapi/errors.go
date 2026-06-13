package cfapi

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrorItem is one entry in a Cloudflare error envelope's "errors" array.
type ErrorItem struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// APIError is returned when Cloudflare responds with success:false or a
// non-2xx status. It carries the HTTP status plus the structured CF errors so
// the API layer can map them to friendly responses.
type APIError struct {
	StatusCode int
	Errors     []ErrorItem
	// Body is the raw response body, retained for diagnostics when the
	// envelope could not be parsed.
	Body string
}

func (e *APIError) Error() string {
	if len(e.Errors) > 0 {
		parts := make([]string, 0, len(e.Errors))
		for _, it := range e.Errors {
			parts = append(parts, fmt.Sprintf("%d: %s", it.Code, it.Message))
		}
		return fmt.Sprintf("cloudflare api error (http %d): %s", e.StatusCode, strings.Join(parts, "; "))
	}
	if e.Body != "" {
		body := e.Body
		if utf8.RuneCountInString(body) > 300 {
			r := []rune(body)
			body = string(r[:300])
		}
		return fmt.Sprintf("cloudflare api error (http %d): %s", e.StatusCode, body)
	}
	return fmt.Sprintf("cloudflare api error (http %d)", e.StatusCode)
}

// HasCode reports whether any CF error carries the given numeric code.
func (e *APIError) HasCode(code int) bool {
	for _, it := range e.Errors {
		if it.Code == code {
			return true
		}
	}
	return false
}

// IsNotFound reports whether err wraps an APIError with a 404 status. Several
// CF resources (deleted tunnels, missing records) surface as 404. Uses
// errors.As so a %w-wrapped APIError is still recognized.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == 404
}

// IsAuth reports whether err wraps an APIError caused by bad credentials
// (HTTP 401/403). Used to flag an account as invalid after verification.
func IsAuth(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.StatusCode == 401 || e.StatusCode == 403)
}
