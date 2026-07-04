// Package keychain stores Cloudflare API token values in the macOS Keychain.
//
// Token values never appear on disk; only their (name → cloudflare-id) mapping
// does, in internal/store. Per the design, two service strings are reserved:
//
//	ServiceBootstrap — the bootstrap token used to call the Cloudflare API
//	                   itself; one entry with account "default".
//	ServiceTokens    — the managed tokens issued by `cft apply`; one entry
//	                   per token name.
//
// On macOS, items added with this package inherit the system's default ACL:
// the calling binary (matched by code signature) can read without a prompt,
// while other processes trigger a Touch ID / password challenge. For that
// guarantee to be stable across upgrades, ship a code-signed `cft` binary;
// rebuilt unsigned binaries may be treated as a different application and
// re-prompt the user.
package keychain

// Service strings used to namespace entries in the macOS Keychain. These are
// reverse-DNS identifiers tied to the project (`cftoken`), not to the host
// user — macOS already isolates the login keychain per user, so embedding a
// user identifier here would be redundant and brittle (a change in $USER
// would orphan previously-stored secrets).
const (
	ServiceBootstrap = "dev.cftoken.bootstrap"
	ServiceTokens    = "dev.cftoken.tokens"

	// BootstrapAccount is the account name under ServiceBootstrap for the
	// default profile. With named profiles, a profile's bootstrap token is
	// stored under account=<profile>; "default" is both the literal default
	// profile name and the account a pre-profiles install already used, so
	// upgrading is transparent.
	BootstrapAccount = "default"
)

// TokenAccount returns the Keychain "account" string under ServiceTokens for a
// managed token. The default profile keeps the legacy bare-name layout so
// pre-profiles installs are unaffected; other profiles are namespaced as
// "<profile>/<name>". Token names are DNS-1123 (validated in package spec) and
// never contain "/", so the separator is unambiguous.
func TokenAccount(profile, name string) string {
	if profile == "" || profile == BootstrapAccount {
		return name
	}
	return profile + "/" + name
}

// ErrNotFound is returned by Get when the (service, account) entry does not
// exist. Callers should use errors.Is to test for it.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

// ErrNotFound signals a missing keychain entry.
var ErrNotFound = &notFoundError{msg: "keychain: entry not found"}

// Option configures New. Accepted on every platform; only the darwin
// backend acts on them.
type Option func(*config)

type config struct {
	path        string
	allowAnyApp bool
}

func newConfig(opts []Option) config {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// WithPath targets the keychain file at path instead of the login keychain.
// The keychain must already exist; a missing file surfaces the OS error at
// call time, not at New.
func WithPath(path string) Option {
	return func(c *config) { c.path = path }
}

// WithAllowAnyApp relaxes the ACL on entries written by Set so any
// application can read them without a prompt. Test-time only: it removes
// the code-signature binding that is the point of the Keychain store.
func WithAllowAnyApp() Option {
	return func(c *config) { c.allowAnyApp = true }
}

// Store is the minimal interface the rest of the codebase depends on.
// A real macOS implementation lives in keychain_darwin.go; an in-memory
// fake (Fake) is provided for unit tests, and keychain_other.go gives a
// build stub for non-darwin platforms.
type Store interface {
	// Get returns the password bytes for (service, account), or ErrNotFound.
	Get(service, account string) ([]byte, error)
	// Set creates or replaces the (service, account) entry.
	Set(service, account string, data []byte) error
	// Delete removes the (service, account) entry. No-op if absent.
	Delete(service, account string) error
	// List returns the account names that exist under service, sorted.
	List(service string) ([]string, error)
}
