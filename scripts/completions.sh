#!/usr/bin/env sh
# Generate shell completion scripts for packaging (Homebrew cask + archives).
# Run via `go run` so it works in the goreleaser `before` hook, before the
# release binaries are built. Cobra's completion command walks the command
# tree only and never touches the Keychain, so it runs on any platform.
set -e

rm -rf completions
mkdir -p completions

for sh in bash zsh fish; do
	go run ./cmd/cft completion "$sh" >"completions/cft.$sh"
done
