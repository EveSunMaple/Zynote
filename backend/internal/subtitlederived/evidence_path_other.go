//go:build !windows

package subtitlederived

import "path/filepath"

func canonicalEvidencePath(path string) (string, error) { return filepath.EvalSymlinks(path) }
