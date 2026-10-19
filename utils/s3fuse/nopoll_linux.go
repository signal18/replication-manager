// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// disablePoll makes the kernel stop asking this mount whether its files can be polled, before
// anything else of the process opens one (#2091). Go adds every file it opens to its poller
// with an epoll_ctl the scheduler does not see as a system call: on a FUSE file the kernel then
// sends a poll request to the file system, served by this same process, and waits. A garbage
// collection starting meanwhile cannot stop that goroutine, so nothing runs, the request's
// answer included, and the process freezes (dev3, 2026-10-17 and 2026-10-19). GeeseFS answers
// poll with ENOSYS and the kernel then never asks this mount again: one file is created and
// added to an epoll here, every call a system call the scheduler sees, so the answer is served
// while this thread waits.
func disablePoll(point string) error {
	name := filepath.Join(point, fmt.Sprintf(".replication-manager-poll.%d", os.Getpid()))
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("s3fuse: poll probe %s: %w", name, err)
	}
	defer unix.Unlink(name)
	defer unix.Close(fd)
	ep, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return fmt.Errorf("s3fuse: poll probe: %w", err)
	}
	defer unix.Close(ep)
	ev := unix.EpollEvent{Events: unix.EPOLLIN, Fd: int32(fd)}
	// unix.EpollCtl is a raw system call: the very trap this avoids
	_, _, errno := unix.Syscall6(unix.SYS_EPOLL_CTL, uintptr(ep), unix.EPOLL_CTL_ADD, uintptr(fd), uintptr(unsafe.Pointer(&ev)), 0, 0)
	if errno != 0 && errno != unix.EPERM {
		return fmt.Errorf("s3fuse: poll probe: %w", errno)
	}
	return nil
}
