//go:build !darwin

package keychain

import "errors"

// errUnsupported is returned by every method on the non-darwin stub so the
// binary still compiles on other platforms (CI runs Linux) while making it
// obvious at runtime that cft is macOS-only.
var errUnsupported = errors.New("keychain: cft requires macOS (the Keychain backend is unavailable on this platform)")

type stub struct{}

// New returns a Store stub that fails every call on non-darwin platforms.
func New(opts ...Option) Store {
	_ = newConfig(opts)
	return stub{}
}

func (stub) Get(_, _ string) ([]byte, error) { return nil, errUnsupported }
func (stub) Set(_, _ string, _ []byte) error { return errUnsupported }
func (stub) Delete(_, _ string) error        { return errUnsupported }
func (stub) List(_ string) ([]string, error) { return nil, errUnsupported }
