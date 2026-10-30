package media

import (
	"runtime"
	"testing"
)

func skipWithoutPosixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell script and symlinks")
	}
}
