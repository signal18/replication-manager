//go:build windows

package pbscommon

import "os"

// fileOwner is the uid/gid a pxar entry records for fi. Windows has no
// Unix owner: a fixed 1000, as the archive always used.
func fileOwner(os.FileInfo) (uid, gid uint32) { return 1000, 1000 }

// processOwner is the uid/gid recorded for virtual files.
func processOwner() (uid, gid uint32) { return 1000, 1000 }
