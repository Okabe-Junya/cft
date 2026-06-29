// Package store persists the local profile/name → cloudflare-id map at
// $XDG_CONFIG_HOME/cftoken/index.json (or ~/Library/Application Support on
// macOS). See docs/design.md §6.2.
//
// The file never contains token values; values live in the Keychain.
//
// Schema v2 groups tokens under named profiles so a single host can manage
// several Cloudflare accounts side by side (one profile per account). A v1
// file (flat `tokens` map) is migrated on load into a single "default"
// profile; see decode.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// CurrentVersion is the on-disk schema version this package writes.
const CurrentVersion = 2

// DefaultProfile is the profile name used when none is selected, and the
// profile a migrated v1 index is folded into. Its Keychain layout matches the
// pre-profiles ("single account") install, so upgrading is transparent.
const DefaultProfile = "default"

// PartialError signals that the WithLock callback made committed mutations
// to *Index before failing and wants the resulting state persisted alongside
// returning the wrapped error. Without this opt-in, WithLock's default
// all-or-nothing semantics would discard the partial progress and leave the
// index out of sync with side-effects fn already executed (Cloudflare,
// Keychain, ...).
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// Index is the on-disk index file content.
type Index struct {
	Version int `json:"version"`
	// Current is the profile used when a command does not specify one. Empty
	// until the first profile is established.
	Current  string              `json:"current,omitempty"`
	Profiles map[string]*Profile `json:"profiles"`
}

// Profile is one account's worth of managed tokens.
type Profile struct {
	Tokens map[string]Entry `json:"tokens"`
}

// Entry is a single token record. The token value is not stored here.
type Entry struct {
	ID      string `json:"id"`
	Expires string `json:"expires,omitempty"`
}

// NewIndex returns an empty index initialised to CurrentVersion.
func NewIndex() *Index {
	return &Index{Version: CurrentVersion, Profiles: map[string]*Profile{}}
}

// DefaultPath returns the default index file path:
//
//	$XDG_CONFIG_HOME/cftoken/index.json
//
// or, when XDG_CONFIG_HOME is unset, $HOME/Library/Application Support/cftoken/index.json.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(base, "cftoken", "index.json"), nil
}

// Profile returns the named profile, creating an empty one if it does not yet
// exist. Creation is in-memory; it only reaches disk if the caller is inside
// WithLock and the call succeeds. Read-only callers (Load without WithLock)
// therefore never persist an empty profile.
func (i *Index) Profile(name string) *Profile {
	if i.Profiles == nil {
		i.Profiles = map[string]*Profile{}
	}
	p, ok := i.Profiles[name]
	if !ok {
		p = &Profile{Tokens: map[string]Entry{}}
		i.Profiles[name] = p
	}
	if p.Tokens == nil {
		p.Tokens = map[string]Entry{}
	}
	return p
}

// HasProfile reports whether name exists without creating it.
func (i *Index) HasProfile(name string) bool {
	_, ok := i.Profiles[name]
	return ok
}

// DeleteProfile removes a whole profile. No-op if absent. Clears Current when
// it pointed at the removed profile.
func (i *Index) DeleteProfile(name string) {
	delete(i.Profiles, name)
	if i.Current == name {
		i.Current = ""
	}
}

// ProfileNames returns the sorted names of all known profiles.
func (i *Index) ProfileNames() []string {
	out := make([]string, 0, len(i.Profiles))
	for n := range i.Profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Get returns the entry for name and whether it exists.
func (p *Profile) Get(name string) (Entry, bool) {
	e, ok := p.Tokens[name]
	return e, ok
}

// Set inserts or replaces the entry for name.
func (p *Profile) Set(name string, e Entry) {
	if p.Tokens == nil {
		p.Tokens = map[string]Entry{}
	}
	p.Tokens[name] = e
}

// Delete removes the entry for name. No-op if absent.
func (p *Profile) Delete(name string) {
	delete(p.Tokens, name)
}

// All returns a shallow copy of the entry map. Callers may mutate the copy
// without affecting the profile.
func (p *Profile) All() map[string]Entry {
	out := make(map[string]Entry, len(p.Tokens))
	for k, v := range p.Tokens {
		out[k] = v
	}
	return out
}

// Load reads the index file at path. A missing file yields an empty index.
// An unsupported version is rejected. A v1 file is migrated in memory.
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewIndex(), nil
		}
		return nil, err
	}
	defer f.Close()
	return decode(f)
}

// WithLock takes an exclusive flock on path, loads the index, calls fn, and
// writes the (possibly mutated) index back. If fn returns an error the file
// is not modified, unless the error unwraps to a *PartialError — in which
// case the index is written and the wrapped error is returned. This lets
// callers commit partial progress when their side-effects are not
// transactional.
//
// The directory is created with 0700 if missing. The file is created with
// 0600 if missing.
func WithLock(path string, fn func(*Index) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	}()

	idx, err := decode(f)
	if err != nil {
		return err
	}
	if err := fn(idx); err != nil {
		var pe *PartialError
		if errors.As(err, &pe) {
			if werr := rewrite(f, idx); werr != nil {
				return errors.Join(err, werr)
			}
		}
		return err
	}
	return rewrite(f, idx)
}

// rawIndex is the union of the v1 and v2 on-disk shapes, used so decode can
// detect the version and migrate without a second pass over the bytes.
type rawIndex struct {
	Version  int                 `json:"version"`
	Current  string              `json:"current,omitempty"`
	Profiles map[string]*Profile `json:"profiles,omitempty"`
	Tokens   map[string]Entry    `json:"tokens,omitempty"` // v1 only
}

func decode(f *os.File) (*Index, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return NewIndex(), nil
	}
	var raw rawIndex
	dec := json.NewDecoder(f)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}

	switch raw.Version {
	case 1:
		// Migrate the flat v1 token map into a single "default" profile. The
		// migrated index is written back the next time a command mutates it
		// under WithLock; Load alone leaves the file untouched.
		idx := NewIndex()
		idx.Current = DefaultProfile
		p := idx.Profile(DefaultProfile)
		for name, e := range raw.Tokens {
			p.Set(name, e)
		}
		return idx, nil
	case CurrentVersion:
		idx := &Index{Version: CurrentVersion, Current: raw.Current, Profiles: raw.Profiles}
		if idx.Profiles == nil {
			idx.Profiles = map[string]*Profile{}
		}
		for _, p := range idx.Profiles {
			if p.Tokens == nil {
				p.Tokens = map[string]Entry{}
			}
		}
		return idx, nil
	default:
		return nil, fmt.Errorf("unsupported index version %d (want %d)", raw.Version, CurrentVersion)
	}
}

func rewrite(f *os.File, idx *Index) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return err
	}
	return f.Sync()
}
