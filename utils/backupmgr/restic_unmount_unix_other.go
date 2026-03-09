//go:build unix && !linux && !darwin

package backupmgr

import (
	"errors"

	"golang.org/x/sys/unix"
)

func unmountPath(path string) error {
	err := unix.Unmount(path, 0)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return nil
}
