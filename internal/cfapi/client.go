// Package cfapi is a thin facade over github.com/cloudflare/cloudflare-go/v7
// that exposes the subset cft needs (verify, list permission groups, resolve
// zone, create / update / roll / delete token) behind cft's own types.
//
// Why this exists rather than reaching for the SDK directly from cli/:
//
//   - cft has been built around stable domain types (Token, TokenSpec,
//     TokenPolicy, PermissionGroup, *Error.NotFound()) since v1. Keeping
//     those types here lets the cobra layer ignore SDK churn — the
//     Stainless-generated SDK ships a major version every few months and
//     occasionally renames or restructures types in ways that would
//     otherwise ripple into every subcommand.
//   - The SDK uses `param.Field[T]` wrappers and several "Union" interface
//     types whose ergonomics are awkward at the call site. Translating
//     once here keeps cli/translate.go straightforward.
//   - Errors from the SDK are *cloudflare.Error (alias for
//     internal/apierror.Error). cli/delete.go relies on
//     errors.As(err, &cfapi.Error) and apiErr.NotFound(); we preserve that
//     contract by wrapping the SDK error here.
package cfapi

import (
	"errors"
	"net/http"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

// Client talks to the Cloudflare REST API using a bearer token. Construct via
// New; the zero value is not usable.
type Client struct {
	cf *cloudflare.Client
}

// Option configures a Client at construction time. It survives from the
// hand-rolled era as the public knob shape; internally each Option appends
// to the SDK's option.RequestOption slice.
type Option func(*clientConfig)

type clientConfig struct {
	sdkOpts []option.RequestOption
}

// WithBaseURL overrides the API root. Used by tests with httptest.Server.
func WithBaseURL(u string) Option {
	return func(c *clientConfig) {
		c.sdkOpts = append(c.sdkOpts, option.WithBaseURL(u))
	}
}

// WithHTTPClient overrides the underlying *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *clientConfig) {
		c.sdkOpts = append(c.sdkOpts, option.WithHTTPClient(h))
	}
}

// WithMaxRetries caps the number of additional attempts the SDK will make on
// retryable responses. Pass 0 to disable retries entirely (useful in tests).
func WithMaxRetries(n int) Option {
	return func(c *clientConfig) {
		c.sdkOpts = append(c.sdkOpts, option.WithMaxRetries(n))
	}
}

// New returns a Client authenticated with the given bearer token.
//
// cloudflare.NewClient prepends DefaultClientOptions(), which silently picks
// up CLOUDFLARE_API_KEY / CLOUDFLARE_EMAIL / CLOUDFLARE_API_USER_SERVICE_KEY /
// CLOUDFLARE_BASE_URL from the environment. cft must authenticate with
// exactly the token it was handed and talk only to the real API: stray
// X-Auth-* headers alongside the Bearer token make Cloudflare reject the
// request, and an inherited base URL would ship token material to whatever
// host the variable names. The options below are applied after the SDK's
// env-derived defaults, so they win; explicit Options (tests' WithBaseURL)
// are appended later still and keep working.
func New(token string, opts ...Option) *Client {
	cfg := &clientConfig{
		sdkOpts: []option.RequestOption{
			option.WithBaseURL("https://api.cloudflare.com/client/v4/"),
			option.WithAPIToken(token),
			option.WithHeaderDel("X-Auth-Key"),
			option.WithHeaderDel("X-Auth-Email"),
			option.WithHeaderDel("X-Auth-User-Service-Key"),
		},
	}
	for _, o := range opts {
		o(cfg)
	}
	return &Client{cf: cloudflare.NewClient(cfg.sdkOpts...)}
}

// mapError converts an SDK error into cft's *Error so the existing
// errors.As(err, &cfapi.Error) and NotFound() checks in cli/* keep working.
// Non-API errors (context cancel, transport failures) pass through untouched.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *cloudflare.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	out := &Error{Status: apiErr.StatusCode}
	for _, d := range apiErr.Errors {
		out.Codes = append(out.Codes, int(d.Code))
		out.Messages = append(out.Messages, d.Message)
	}
	if len(out.Messages) == 0 {
		// Fall back so the user still sees something actionable even when
		// Cloudflare returned a non-JSON gateway error.
		out.Message = apiErr.Error()
	}
	return out
}
