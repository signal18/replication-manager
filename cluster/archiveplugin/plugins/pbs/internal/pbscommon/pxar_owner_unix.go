//go:build !windows

package pbscommon

import (
	"os"
	"syscall"
)

// fileOwner is the uid/gid a pxar entry records for fi. On Unix it is the real
// owner, so a restore (as root, or as that same user) gives the files back
// to whom they belonged; a restore by another unprivileged user could not
// chown them anyway.
func fileOwner(fi os.FileInfo) (uid, gid uint32) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid, st.Gid
	}
	return processOwner()
}

// processOwner is the uid/gid of this process, for entries with no file on
// disk (virtual files).
func processOwner() (uid, gid uint32) {
	return uint32(os.Getuid()), uint32(os.Getgid()) // #nosec G115 -- uid/gid are non-negative on Unix
}
