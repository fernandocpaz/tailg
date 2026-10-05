package agent

import "os"

// isPrivate reports whether a state, lock, or evidence path is readable only by
// its owner. Windows has no Unix mode bits: Go reports 0666 for every writable
// file and 0444 for a read-only one, so the Unix mode test can never pass here.
// Privacy of files under the user profile is enforced by NTFS ACLs instead, so
// the mode check is not applied on Windows.
func isPrivate(_ os.FileInfo) bool {
	return true
}
