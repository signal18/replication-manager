//go:build !unix

package backupmgr

import (
	"fmt"
	"runtime"
)

func unmountPath(path string) error {
	return fmt.Errorf("unmount not supported on %s", runtime.GOOS)
}
