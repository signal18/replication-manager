//go:build linux

package backupmgr

import (
	"errors"

	"golang.org/x/sys/unix"
)

func unmountPath(path string) error {
	err := unix.Unmount(path, unix.MNT_DETACH)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return nil
}
