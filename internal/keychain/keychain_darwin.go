//go:build darwin

package keychain

import (
	"errors"
	"sort"

	kc "github.com/99designs/go-keychain"
)

// Darwin is the production Store backed by the macOS Keychain.
type Darwin struct {
	path        string
	ref         *kc.Keychain // nil → login keychain / default search list
	allowAnyApp bool
}

// New returns a Store backed by the login keychain, or by the keychain file
// selected with WithPath.
func New(opts ...Option) Store {
	c := newConfig(opts)
	d := Darwin{path: c.path, allowAnyApp: c.allowAnyApp}
	if c.path != "" {
		ref := kc.NewWithPath(c.path)
		d.ref = &ref
	}
	return d
}

func (d Darwin) scope(item *kc.Item) {
	if d.ref != nil {
		item.SetMatchSearchList(*d.ref)
	}
}

// Get reads (service, account) from the keychain. macOS may surface a
// Touch ID / password prompt if the caller's code signature does not match
// the ACL.
func (d Darwin) Get(service, account string) ([]byte, error) {
	q := kc.NewItem()
	q.SetSecClass(kc.SecClassGenericPassword)
	q.SetService(service)
	q.SetAccount(account)
	q.SetMatchLimit(kc.MatchLimitOne)
	q.SetReturnData(true)
	d.scope(&q)

	results, err := kc.QueryItem(q)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrNotFound
	}
	return results[0].Data, nil
}

// Set creates or replaces (service, account). The default ACL binds reads to
// the calling binary's code signature; WithAllowAnyApp relaxes it after the
// write.
func (d Darwin) Set(service, account string, data []byte) error {
	if d.allowAnyApp {
		return d.setAnyApp(service, account, data)
	}

	item := kc.NewGenericPassword(service, account, "", data, "")
	item.SetAccessible(kc.AccessibleWhenUnlocked)
	if d.ref != nil {
		item.UseKeychain(*d.ref)
	}

	err := kc.AddItem(item)
	if err != nil && errors.Is(err, kc.ErrorDuplicateItem) {
		// Already exists — update in-place.
		query := kc.NewItem()
		query.SetSecClass(kc.SecClassGenericPassword)
		query.SetService(service)
		query.SetAccount(account)
		d.scope(&query)

		update := kc.NewItem()
		update.SetData(data)
		err = kc.UpdateItem(query, update)
	}
	return err
}

// Delete removes (service, account). Returns nil when the entry is absent.
func (d Darwin) Delete(service, account string) error {
	item := kc.NewItem()
	item.SetSecClass(kc.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)
	d.scope(&item)

	err := kc.DeleteItem(item)
	if err == nil || errors.Is(err, kc.ErrorItemNotFound) {
		return nil
	}
	return err
}

// List returns sorted account names under service.
func (d Darwin) List(service string) ([]string, error) {
	q := kc.NewItem()
	q.SetSecClass(kc.SecClassGenericPassword)
	q.SetService(service)
	q.SetMatchLimit(kc.MatchLimitAll)
	q.SetReturnAttributes(true)
	d.scope(&q)

	results, err := kc.QueryItem(q)
	if err != nil {
		return nil, err
	}
	accs := make([]string, 0, len(results))
	for _, r := range results {
		accs = append(accs, r.Account)
	}
	sort.Strings(accs)
	return accs, nil
}
