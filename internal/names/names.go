// Package names owns path <-> Drive name rules: ignore patterns, validity
// checks, and rel_path construction. rel_paths are posix-style, relative to
// the sync root, never starting or ending with '/'.
package names

import (
	"fmt"
	"path"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// TempPrefix marks synckeeper's own temp files; always ignored.
const TempPrefix = ".synckeeper.tmp."

// Ignored reports whether a base name matches any of the glob patterns
// (matched against the name only, not the full path), or is a synckeeper
// temp file.
func Ignored(name string, patterns []string) bool {
	if strings.HasPrefix(name, TempPrefix) {
		return true
	}
	for _, pat := range patterns {
		if ok, err := path.Match(pat, name); err == nil && ok {
			return true
		}
	}
	return false
}

// Validate rejects names that cannot be represented as a single path
// segment on disk. Platform-specific rules (Windows reserved names, case
// collisions) are phase 5.
func Validate(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("invalid name %q", name)
	case strings.ContainsAny(name, "/\x00"):
		return fmt.Errorf("name %q contains characters invalid in a filename", name)
	}
	return nil
}

// Join builds a child rel_path from a parent rel_path ("" = sync root) and
// a validated name.
func Join(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// FoldKey returns the key under which a name collides with its siblings on a
// filesystem that folds case and/or Unicode normalization. Two Drive names
// that produce the same key map to one path locally, so only the first may be
// materialized (the rest are skipped and reported). Normalization is folded to
// NFC; ToLower can denormalize, so a final NFC pass re-canonicalizes when both
// folds are active. With both flags false this is the identity.
func FoldKey(name string, caseFold, normFold bool) string {
	if normFold {
		name = norm.NFC.String(name)
	}
	if caseFold {
		name = strings.ToLower(name)
		if normFold {
			name = norm.NFC.String(name)
		}
	}
	return name
}
