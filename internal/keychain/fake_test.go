package keychain

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestFake_GetMissing(t *testing.T) {
	f := NewFake()
	_, err := f.Get("svc", "acc")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFake_SetGetRoundTrip(t *testing.T) {
	f := NewFake()
	if err := f.Set("svc", "acc", []byte("secret")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := f.Get("svc", "acc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("secret")) {
		t.Errorf("Get = %q, want %q", got, "secret")
	}
}

func TestFake_SetOverwrites(t *testing.T) {
	f := NewFake()
	_ = f.Set("svc", "acc", []byte("v1"))
	_ = f.Set("svc", "acc", []byte("v2"))
	got, _ := f.Get("svc", "acc")
	if !bytes.Equal(got, []byte("v2")) {
		t.Errorf("Get = %q, want v2", got)
	}
}

func TestFake_DeleteIdempotent(t *testing.T) {
	f := NewFake()
	if err := f.Delete("svc", "missing"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
	_ = f.Set("svc", "acc", []byte("v"))
	if err := f.Delete("svc", "acc"); err != nil {
		t.Errorf("Delete present: %v", err)
	}
	if _, err := f.Get("svc", "acc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, Get err = %v, want ErrNotFound", err)
	}
}

func TestFake_ListReturnsSortedAccounts(t *testing.T) {
	f := NewFake()
	_ = f.Set("svc", "zeta", []byte("z"))
	_ = f.Set("svc", "alpha", []byte("a"))
	_ = f.Set("svc", "mike", []byte("m"))
	_ = f.Set("other", "ignored", []byte("x"))

	got, err := f.List("svc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "mike", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestFake_GetReturnsCopy(t *testing.T) {
	// Mutating the slice the caller receives must not change what's stored,
	// otherwise a downstream test that scrubs the buffer could corrupt the
	// fake's state.
	f := NewFake()
	_ = f.Set("svc", "acc", []byte("v"))
	got, _ := f.Get("svc", "acc")
	got[0] = 'X'
	again, _ := f.Get("svc", "acc")
	if !bytes.Equal(again, []byte("v")) {
		t.Errorf("Get returned shared buffer; second read = %q, want %q", again, "v")
	}
}

func TestFake_SetIsGoroutineSafe(t *testing.T) {
	f := NewFake()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = f.Set("svc", "acc", []byte{byte(i)})
		}(i)
	}
	wg.Wait()
	// Any deterministic value is fine; the test exists to catch -race.
	if _, err := f.Get("svc", "acc"); err != nil {
		t.Errorf("Get: %v", err)
	}
}

// Compile-time check that Fake implements Store.
var _ Store = (*Fake)(nil)
