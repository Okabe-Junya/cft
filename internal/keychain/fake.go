package keychain

import (
	"sort"
	"sync"
)

// Fake is an in-memory Store for unit tests. It is goroutine-safe.
type Fake struct {
	mu    sync.Mutex
	items map[fakeKey][]byte
}

type fakeKey struct{ service, account string }

// NewFake returns an empty in-memory Store.
func NewFake() *Fake { return &Fake{items: map[fakeKey][]byte{}} }

// Get returns the data for (service, account) or ErrNotFound.
func (f *Fake) Get(service, account string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[fakeKey{service, account}]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

// Set inserts or overwrites the entry.
func (f *Fake) Set(service, account string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	f.items[fakeKey{service, account}] = cp
	return nil
}

// Delete removes the entry. No-op if absent.
func (f *Fake) Delete(service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, fakeKey{service, account})
	return nil
}

// List returns sorted accounts under service.
func (f *Fake) List(service string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var accs []string
	for k := range f.items {
		if k.service == service {
			accs = append(accs, k.account)
		}
	}
	sort.Strings(accs)
	return accs, nil
}
