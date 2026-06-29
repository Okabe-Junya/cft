package cfapi

import (
	"fmt"
	"net/http"
	"strings"
)

// Error wraps a Cloudflare API failure. Callers should errors.As to recover
// it and read Status / Codes when they need finer-grained handling than
// "something went wrong".
type Error struct {
	Status   int      // HTTP status
	Codes    []int    // Cloudflare error codes (may be empty)
	Messages []string // Cloudflare error messages (may be empty)
	Message  string   // Fallback message when Codes/Messages are empty
}

// Error implements the error interface.
func (e *Error) Error() string {
	if len(e.Messages) > 0 {
		parts := make([]string, 0, len(e.Messages))
		for i, m := range e.Messages {
			if i < len(e.Codes) {
				parts = append(parts, fmt.Sprintf("%d %s", e.Codes[i], m))
			} else {
				parts = append(parts, m)
			}
		}
		return fmt.Sprintf("cloudflare api: %d: %s", e.Status, strings.Join(parts, "; "))
	}
	if e.Message != "" {
		return fmt.Sprintf("cloudflare api: %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("cloudflare api: %d", e.Status)
}

// NotFound reports whether the underlying HTTP status is 404. Callers use
// this when a missing resource is the expected case (e.g. delete-twice).
func (e *Error) NotFound() bool { return e.Status == http.StatusNotFound }

// HasCode reports whether any of the Cloudflare error codes match.
func (e *Error) HasCode(code int) bool {
	for _, c := range e.Codes {
		if c == code {
			return true
		}
	}
	return false
}
