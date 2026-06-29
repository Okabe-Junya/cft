// Command cft manages least-privilege Cloudflare API tokens with the macOS
// Keychain as the secret store. See docs/design.md.
package main

import (
	"os"
	"runtime/debug"

	"github.com/Okabe-Junya/cft/internal/cli"
)

// version derives from the VCS stamping go build performs since Go 1.24:
// a (pseudo-)version on clean checkouts, the short revision (+dirty) on
// modified ones, "dev" outside a VCS checkout.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

func main() {
	os.Exit(cli.Execute(version()))
}
