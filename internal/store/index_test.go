package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func tempIndexPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "index.json")
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	idx, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx == nil {
		t.Fatal("nil index")
	}
	if len(idx.Profiles) != 0 {
		t.Errorf("expected empty profiles, got %v", idx.Profiles)
	}
	if idx.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", idx.Version, CurrentVersion)
	}
}

func TestWithLock_CreatesFileAndPersists(t *testing.T) {
	path := tempIndexPath(t)
	err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("foo", Entry{ID: "abc123", Expires: "2026-12-31"})
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}

	// File should exist with 0600 perm.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := idx.Profile(DefaultProfile).Get("foo")
	if !ok {
		t.Fatal("foo not found")
	}
	if got.ID != "abc123" || got.Expires != "2026-12-31" {
		t.Errorf("Get(foo) = %+v", got)
	}
}

func TestWithLock_FnErrorRollsBack(t *testing.T) {
	path := tempIndexPath(t)

	// Seed with one entry.
	if err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("seed", Entry{ID: "s1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Attempt a mutation that fails inside fn.
	sentinel := errString("boom")
	err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("foo", Entry{ID: "f1"})
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}

	// Re-read: should not contain "foo".
	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Profile(DefaultProfile).Get("foo"); ok {
		t.Error("foo was persisted despite fn error")
	}
	if _, ok := idx.Profile(DefaultProfile).Get("seed"); !ok {
		t.Error("seed entry lost")
	}
}

func TestWithLock_PartialErrorPersistsMutations(t *testing.T) {
	path := tempIndexPath(t)

	if err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("seed", Entry{ID: "s1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	inner := fmt.Errorf("boom")
	err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("kept", Entry{ID: "k1"})
		return &PartialError{Err: inner}
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, inner) {
		t.Errorf("err does not wrap inner: %v", err)
	}
	var pe *PartialError
	if !errors.As(err, &pe) {
		t.Errorf("err is not *PartialError: %v", err)
	}

	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Profile(DefaultProfile).Get("kept"); !ok {
		t.Error("kept entry not persisted on PartialError")
	}
	if _, ok := idx.Profile(DefaultProfile).Get("seed"); !ok {
		t.Error("seed entry lost")
	}
}

func TestIndex_GetSetDeleteAll(t *testing.T) {
	idx := NewIndex()
	idx.Profile(DefaultProfile).Set("a", Entry{ID: "1"})
	idx.Profile(DefaultProfile).Set("b", Entry{ID: "2"})

	if e, ok := idx.Profile(DefaultProfile).Get("a"); !ok || e.ID != "1" {
		t.Errorf("Get(a) = %v, %v", e, ok)
	}

	all := idx.Profile(DefaultProfile).All()
	if len(all) != 2 {
		t.Errorf("All() len = %d, want 2", len(all))
	}
	// Mutating the copy must not affect the index.
	all["a"] = Entry{ID: "changed"}
	if e, _ := idx.Profile(DefaultProfile).Get("a"); e.ID != "1" {
		t.Error("All() did not return a copy")
	}

	idx.Profile(DefaultProfile).Delete("a")
	if _, ok := idx.Profile(DefaultProfile).Get("a"); ok {
		t.Error("Delete did not remove a")
	}
	idx.Profile(DefaultProfile).Delete("missing") // must not panic
}

func TestConcurrentWithLock_NoLostUpdates(t *testing.T) {
	path := tempIndexPath(t)
	const N = 50

	var wg sync.WaitGroup
	wg.Add(N)
	for k := 0; k < N; k++ {
		go func(k int) {
			defer wg.Done()
			err := WithLock(path, func(i *Index) error {
				i.Profile(DefaultProfile).Set(name(k), Entry{ID: name(k)})
				return nil
			})
			if err != nil {
				t.Errorf("WithLock: %v", err)
			}
		}(k)
	}
	wg.Wait()

	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(idx.Profile(DefaultProfile).All()); got != N {
		t.Errorf("len(tokens) = %d, want %d", got, N)
	}
}

func TestLoad_MigratesV1ToDefaultProfile(t *testing.T) {
	path := tempIndexPath(t)
	v1 := []byte(`{"version":1,"tokens":{"dns-editor":{"id":"abc","expires":"2027-01-01"}}}`)
	if err := os.WriteFile(path, v1, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", idx.Version, CurrentVersion)
	}
	if idx.Current != DefaultProfile {
		t.Errorf("Current = %q, want %q", idx.Current, DefaultProfile)
	}
	e, ok := idx.Profile(DefaultProfile).Get("dns-editor")
	if !ok || e.ID != "abc" || e.Expires != "2027-01-01" {
		t.Errorf("migrated entry = %+v, ok=%v", e, ok)
	}
	// Migration is lazy: Load must not rewrite the file on disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":1`) {
		t.Errorf("Load rewrote the v1 file; want it untouched, got: %s", raw)
	}
}

func TestProfiles_AreIsolated(t *testing.T) {
	path := tempIndexPath(t)
	if err := WithLock(path, func(i *Index) error {
		// Same token name in two profiles must not collide.
		i.Profile("a").Set("tok", Entry{ID: "ida"})
		i.Profile("b").Set("tok", Entry{ID: "idb"})
		i.Current = "a"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if ea, _ := idx.Profile("a").Get("tok"); ea.ID != "ida" {
		t.Errorf("profile a tok = %+v", ea)
	}
	if eb, _ := idx.Profile("b").Get("tok"); eb.ID != "idb" {
		t.Errorf("profile b tok = %+v", eb)
	}
	if idx.Current != "a" {
		t.Errorf("Current = %q, want a", idx.Current)
	}
	if names := idx.ProfileNames(); len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("ProfileNames = %v, want [a b]", names)
	}
}

func TestDeleteProfile_ClearsCurrent(t *testing.T) {
	idx := NewIndex()
	idx.Profile("gone").Set("t", Entry{ID: "1"})
	idx.Current = "gone"
	idx.DeleteProfile("gone")
	if idx.HasProfile("gone") {
		t.Error("profile not deleted")
	}
	if idx.Current != "" {
		t.Errorf("Current = %q, want cleared", idx.Current)
	}
}

func TestLoad_UnsupportedVersion(t *testing.T) {
	path := tempIndexPath(t)
	raw := []byte(`{"version": 99, "tokens": {}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported index version") {
		t.Errorf("err = %v, want unsupported-version error", err)
	}
}

func TestLoad_GarbageJSON(t *testing.T) {
	path := tempIndexPath(t)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("want decode error, got nil")
	}
}

func TestWithLock_PreservesFormatting(t *testing.T) {
	path := tempIndexPath(t)
	if err := WithLock(path, func(i *Index) error {
		i.Profile(DefaultProfile).Set("x", Entry{ID: "y"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("written JSON is invalid: %s", raw)
	}
	if !strings.Contains(string(raw), "  ") {
		t.Errorf("expected indented JSON, got: %s", raw)
	}
}

func TestDefaultPath_UsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/xdg/cftoken/index.json" {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestDefaultPath_FallsBackToLibrary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "/cftoken/index.json") {
		t.Errorf("DefaultPath = %q, want suffix .../cftoken/index.json", got)
	}
	if !strings.Contains(got, "Library/Application Support") {
		t.Errorf("DefaultPath = %q, want to include Library/Application Support", got)
	}
}

// errString is a comparable error used as a sentinel in tests.
type errString string

func (e errString) Error() string { return string(e) }

func name(k int) string {
	return "name-" + itoa(k)
}

// itoa avoids importing strconv just for tests; covers small non-negative ints.
func itoa(k int) string {
	if k == 0 {
		return "0"
	}
	digits := []byte{}
	for k > 0 {
		digits = append([]byte{byte('0' + k%10)}, digits...)
		k /= 10
	}
	return string(digits)
}
