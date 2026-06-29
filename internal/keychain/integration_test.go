//go:build darwin && integration

// Integration tests that exercise the real macOS Keychain via the Darwin
// backend. Skipped by default; opt in with:
//
//	go test -tags=integration ./internal/keychain/...
//
// Each test creates a throwaway keychain file under t.TempDir (via
// keychain.WithPath), so the developer's login keychain is never touched and
// no confirmation prompts appear.
package keychain_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	gokeychain "github.com/99designs/go-keychain"
	"github.com/Okabe-Junya/cft/internal/keychain"
)

const testService = "dev.cftoken.integration"

func newTempKeychain(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cft-integration.keychain")
	kc, err := gokeychain.NewKeychain(path, "cft-integration-password")
	if err != nil {
		t.Fatalf("create temp keychain: %v", err)
	}
	t.Cleanup(func() { _ = kc.Delete() })
	return path
}

func newStore(t *testing.T) keychain.Store {
	t.Helper()
	return keychain.New(keychain.WithPath(newTempKeychain(t)))
}

func TestIntegration_SetGetDelete_RoundTrips(t *testing.T) {
	ks := newStore(t)

	want := []byte("integration-secret-value")
	if err := ks.Set(testService, "alice", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := ks.Get(testService, "alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Get = %q, want %q", got, want)
	}
	if err := ks.Delete(testService, "alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := ks.Get(testService, "alice"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_Set_OverwritesExistingValue(t *testing.T) {
	ks := newStore(t)

	if err := ks.Set(testService, "x", []byte("v1")); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := ks.Set(testService, "x", []byte("v2")); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := ks.Get(testService, "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("Get = %q, want v2", got)
	}
}

func TestIntegration_Get_MissingReturnsErrNotFound(t *testing.T) {
	ks := newStore(t)

	_, err := ks.Get(testService, "nobody-home")
	if !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_Delete_MissingIsNoop(t *testing.T) {
	ks := newStore(t)

	if err := ks.Delete(testService, "ghost"); err != nil {
		t.Errorf("Delete on absent entry returned %v, want nil", err)
	}
}

func TestIntegration_List_ReturnsSortedAccounts(t *testing.T) {
	ks := newStore(t)

	for _, n := range []string{"charlie", "alice", "bob"} {
		if err := ks.Set(testService, n, []byte("v")); err != nil {
			t.Fatalf("Set %s: %v", n, err)
		}
	}
	got, err := ks.List(testService)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("List not sorted: %v", got)
	}
	want := map[string]bool{"alice": true, "bob": true, "charlie": true}
	if len(got) != len(want) {
		t.Fatalf("List len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("List has unexpected account %q", n)
		}
	}
}

// readWithSecurityTool reads the entry with /usr/bin/security — a different
// signing identity than this test binary. Without the any-app ACL the read
// hangs on a GUI confirmation prompt; the timeout turns that into a failure.
func readWithSecurityTool(t *testing.T, path, service, account string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security",
		"find-generic-password", "-s", service, "-a", account, "-w", path).Output()
	return strings.TrimSpace(string(out)), err
}

func TestIntegration_AllowAnyApp_OtherProcessCanRead(t *testing.T) {
	path := newTempKeychain(t)
	ks := keychain.New(keychain.WithPath(path), keychain.WithAllowAnyApp())

	const want = "readable-by-any-app"
	if err := ks.Set(testService, "any-app", []byte(want)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := readWithSecurityTool(t, path, testService, "any-app")
	if err != nil {
		t.Fatalf("security find-generic-password: %v (timeout = ACL was not relaxed)", err)
	}
	if got != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

func TestIntegration_AllowAnyApp_RelaxesOnOverwrite(t *testing.T) {
	path := newTempKeychain(t)
	strict := keychain.New(keychain.WithPath(path))
	relaxed := keychain.New(keychain.WithPath(path), keychain.WithAllowAnyApp())

	if err := strict.Set(testService, "upgraded", []byte("v1")); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := relaxed.Set(testService, "upgraded", []byte("v2")); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := readWithSecurityTool(t, path, testService, "upgraded")
	if err != nil {
		t.Fatalf("security find-generic-password: %v (timeout = ACL was not relaxed)", err)
	}
	if got != "v2" {
		t.Errorf("read %q, want v2", got)
	}
}
