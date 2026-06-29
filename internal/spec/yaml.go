package spec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// Parse decodes one or more spec documents from r, separated by '---'.
// Unknown fields are rejected so spec typos surface immediately.
func Parse(r io.Reader) ([]Token, error) {
	dec := yaml.NewDecoder(r, yaml.Strict(), yaml.DisallowUnknownField())
	var out []Token
	for {
		var t Token
		if err := dec.Decode(&t); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ParseFile reads and decodes spec documents from path.
func ParseFile(path string) ([]Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tokens, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return tokens, nil
}

// Load accepts file or directory paths and returns all spec tokens found.
// Directories are walked recursively for *.yaml and *.yml files; other
// extensions are ignored. The returned slice preserves traversal order
// (files first, then directories left-to-right, lexicographic within).
func Load(paths ...string) ([]Token, error) {
	var all []Token
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			ts, err := ParseFile(p)
			if err != nil {
				return nil, err
			}
			all = append(all, ts...)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			switch filepath.Ext(path) {
			case ".yaml", ".yml":
				ts, err := ParseFile(path)
				if err != nil {
					return err
				}
				all = append(all, ts...)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return all, nil
}

// FormatError renders a YAML error from goccy/go-yaml with line/column and a
// source snippet. Non-YAML errors are returned via Error().
func FormatError(err error, colored bool) string {
	if err == nil {
		return ""
	}
	return yaml.FormatError(err, colored, true)
}
