package keychain

import "testing"

func TestTokenAccount(t *testing.T) {
	cases := []struct {
		profile, name, want string
	}{
		{"", "tok", "tok"},                            // empty profile → legacy bare name
		{"default", "tok", "tok"},                     // default profile → legacy bare name
		{"gmail", "tok", "gmail/tok"},                 // named profile → namespaced
		{"alumni", "dns-editor", "alumni/dns-editor"}, // separator is unambiguous (names are DNS-1123)
		{BootstrapAccount, "x", "x"},                  // BootstrapAccount is the default profile
	}
	for _, c := range cases {
		if got := TokenAccount(c.profile, c.name); got != c.want {
			t.Errorf("TokenAccount(%q, %q) = %q, want %q", c.profile, c.name, got, c.want)
		}
	}
}
