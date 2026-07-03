# cft

macOS-only CLI for managing least-privilege Cloudflare API tokens declaratively. Token values live in the macOS Keychain — never in YAML or state files.

## Why

Cloudflare does not support OIDC for API authentication. The common alternative — managing tokens with Terraform — writes the generated token value into `tfstate` in plaintext ([cloudflare/terraform-provider-cloudflare#6964](https://github.com/cloudflare/terraform-provider-cloudflare/issues/6964)). `cft` keeps the declarative ergonomics while keeping the secret in the macOS Keychain.

## Install

Homebrew:

```sh
brew install Okabe-Junya/tap/cft
```

With the Go toolchain:

```sh
go install github.com/Okabe-Junya/cft/cmd/cft@latest
```

From source (produces `./bin/cft`):

```sh
make build
```

## Quick taste

```sh
cft login                                  # store bootstrap token in Keychain
cft apply ./tokens/dns-editor.yaml         # idempotent create / policy update
cft list                                   # name / id / expires
cft rotate dns-editor-example-com          # re-issue token value
cft exec  dns-editor-example-com -- terraform plan
cft delete dns-editor-example-com
```

```yaml
# ./tokens/dns-editor.yaml
name: dns-editor-example-com
policies:
  - permissions:
      - DNS Write
    zone: example.com
expires: 2026-09-01
```

## Shell completion

The Homebrew cask installs bash, zsh, and fish completions automatically — start
a new shell after `brew install` and completion works. (Homebrew symlinks them
into its completion directories; ensure your shell sources Homebrew's completion
setup, e.g. `autoload -Uz compinit && compinit` for zsh.)

For non-Homebrew installs, generate the script yourself:

```sh
cft completion zsh  > "$(brew --prefix)/share/zsh/site-functions/_cft"   # zsh
cft completion bash > /usr/local/etc/bash_completion.d/cft                # bash
cft completion fish > ~/.config/fish/completions/cft.fish                 # fish
```

`cft rotate`, `exec`, and `delete` complete token names (scoped to the selected
profile) and `cft profile use` completes profile names, read from the local index.

The Amazon Q / Kiro CLI popup uses its own bundled completion specs, not these
scripts; `cft`'s spec is maintained upstream in [withfig/autocomplete](https://github.com/withfig/autocomplete).

## Multiple accounts (profiles)

A **profile** bundles one Cloudflare account's bootstrap token and its managed
tokens. Use profiles when you operate more than one account (e.g. migrating
domains between accounts).

```sh
cft profile add account-a          # store a bootstrap token under "account-a" (becomes current if none set)
cft profile add account-b          # store another account's bootstrap token
cft profile use account-a          # set the current profile
cft profile list                   # current marker, bootstrap presence, token count
cft apply ./tokens/...             # acts on the current profile
cft --profile account-b list       # one-off override for a single command
```

Profile selection precedence: `--profile` flag → `$CFT_PROFILE` → the `current`
pointer (`cft profile use`) → `default`. Existing single-account installs need
no migration: they *are* the `default` profile, with the same Keychain layout
as before.

## Scope

- macOS only (the Keychain backend is the point; non-darwin builds compile but every call returns "cft requires macOS").
- Multiple Cloudflare accounts via named profiles (`cft profile`); one account per profile.
- Personal / local use.

## Editor support

[schema/cft-token.schema.json](./schema/cft-token.schema.json) is a JSON Schema for token spec files. Any editor running [yaml-language-server](https://github.com/redhat-developer/yaml-language-server) (VS Code via the YAML extension, Neovim via yamlls, …) can use it for completion, hover docs, and inline validation — including the "exactly one of `zone` / `account` / `user`" rule.

`cft schema` prints the schema embedded in the binary, so any checkout-independent location works:

```sh
cft schema > ~/.config/cft/schema.json
```

Associate it per file with a modeline — relative to the YAML file, absolute (`~` is not expanded), or via the raw URL:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Okabe-Junya/cft/main/schema/cft-token.schema.json
name: dns-editor-example-com
...
```

or per directory in VS Code (`.vscode/settings.json`, paths relative to the workspace root):

```json
{
  "yaml.schemas": {
    "./schema/cft-token.schema.json": "tokens/**/*.{yaml,yml}"
  }
}
```

The schema mirrors what `cft apply` enforces (`internal/spec`); `TestSchema_MatchesGoValidation` keeps the two from drifting. Cross-file rules (unique names) and existence checks against the Cloudflare API are still apply-time only.

## Build

```sh
make build           # produces ./bin/cft
make check           # golangci-lint (vet/staticcheck/errcheck/...) + race tests
make integration     # macOS only; exercises the real Keychain
```

`cft --version` reports the VCS-derived version Go stamps into the binary (a pseudo-version on clean checkouts, the commit revision plus `+dirty` otherwise). For a one-off invocation without installing, `go run ./cmd/cft <subcommand>` also works.

`make integration` creates throwaway keychain files under a temp directory; the login keychain is never touched and no prompts appear. CI does not run these.

## Keychain selection

By default `cft` uses the login keychain. `--keychain <path>` (available on every subcommand) switches to a keychain file, e.g. one created with `security create-keychain`:

```sh
cft --keychain ~/work.keychain login
cft --keychain ~/work.keychain exec dns-editor-example-com -- terraform plan
```

`cft login --allow-any-app` relaxes the ACL on the stored token so any application can read it without a confirmation prompt. Test-time only — it removes the code-signature binding described below.

### Code signing

The Keychain ACL macOS attaches to entries is bound to the *creating* binary's code signature. If you rebuild `cft` and the signature changes, macOS will treat the new binary as a different application and re-prompt for Touch ID on every read. Sign the binary you actually use (e.g. `codesign --sign - ./bin/cft` for a local ad-hoc signature) to keep the prompts to first-run only.

## License

MIT
