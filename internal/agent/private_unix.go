//go:build !windows

package agent

import "os"

// isPrivate reports whether a state, lock, or evidence path is readable only by
// its owner: no group or world permission bits.
func isPrivate(info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}
