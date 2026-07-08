// Package names owns path <-> Drive name rules: ignore patterns, validity
// checks, and rel_path construction. rel_paths are posix-style, relative to
// the sync root, never starting or ending with '/'.
package names

import (
	"fmt"
	"path"
	"strings"
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
