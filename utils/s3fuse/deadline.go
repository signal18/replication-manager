// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

// DeadlineFS answers every FUSE request within Timeout (#2091). A program writing to a FUSE
// mount waits in the kernel for the mount's answer, a wait nothing can cancel; served by the
// same process (replication-manager's backups folder), a request stuck on S3 would hold it
// for good. Each request runs on its own copy, in a goroutine: answered in time, its result is
// passed on; not, the kernel gets EIO at the deadline and the waiting program goes on, the
// late request finishing in the background on its copy: it never touches a buffer the FUSE
// server reused, and the buffers it hands back are released (its Callback) once it ends.
type DeadlineFS struct {
	Fs      fuseutil.FileSystem
	Timeout time.Duration
	// OnLate is told the operation that missed its deadline (may be nil)
	OnLate func(op string)
	// OnPanic is told a request that panicked, answered EIO (may be nil)
	OnPanic func(op string, recovered interface{}, stack []byte)
}

// lateGrace bounds the goroutine of a late request; its own S3 requests carry their timeout
const lateGrace = 10 * time.Minute

// within runs one request on its copy. prepare makes the copy (its own buffers), finish passes
// a result in time on to the request, release frees what a late request handed back.
func within[T any](d DeadlineFS, name string, op *T, prepare func(*T) T, finish func(dst, src *T), release func(*T), run func(context.Context, *T) error) error {
	cp := prepare(op)
	// unbuffered: a result is handed over only to a caller still waiting, else it is released
	done := make(chan error)
	late := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.Timeout+lateGrace)
		defer cancel()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					if d.OnPanic != nil {
						d.OnPanic(name, r, debug.Stack())
					}
					err = fuse.EIO
				}
			}()
			err = run(ctx, &cp)
		}()
		select {
		case done <- err:
		case <-late:
			if release != nil {
				release(&cp)
			}
		}
	}()
	timer := time.NewTimer(d.Timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil {
			finish(op, &cp)
		} else if release != nil {
			release(&cp)
		}
		return err
	case <-timer.C:
		close(late)
		if d.OnLate != nil {
			d.OnLate(name)
		}
		return fuse.EIO
	}
}

// the plain case: the request has no buffer, its copy is the whole struct
func copyOf[T any](o *T) T        { return *o }
func copyBack[T any](dst, src *T) { *dst = *src }

// ownBuffer is the copy's own buffer for a request that fills one
func ownBuffer(dst []byte) []byte {
	if dst == nil {
		return nil
	}
	return make([]byte, len(dst))
}

// copyRead copies what the copy read into the request's buffer, never past either buffer
func copyRead(dst, src []byte, n int) int {
	if n > len(src) {
		n = len(src)
	}
	return copy(dst, src[:n])
}

func (d DeadlineFS) StatFS(ctx context.Context, op *fuseops.StatFSOp) error {
	return within(d, "StatFS", op, copyOf[fuseops.StatFSOp], copyBack[fuseops.StatFSOp], nil, d.Fs.StatFS)
}
func (d DeadlineFS) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) error {
	return within(d, "LookUpInode", op, copyOf[fuseops.LookUpInodeOp], copyBack[fuseops.LookUpInodeOp], nil, d.Fs.LookUpInode)
}
func (d DeadlineFS) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) error {
	return within(d, "GetInodeAttributes", op, copyOf[fuseops.GetInodeAttributesOp], copyBack[fuseops.GetInodeAttributesOp], nil, d.Fs.GetInodeAttributes)
}
func (d DeadlineFS) SetInodeAttributes(ctx context.Context, op *fuseops.SetInodeAttributesOp) error {
	return within(d, "SetInodeAttributes", op, copyOf[fuseops.SetInodeAttributesOp], copyBack[fuseops.SetInodeAttributesOp], nil, d.Fs.SetInodeAttributes)
}
func (d DeadlineFS) ForgetInode(ctx context.Context, op *fuseops.ForgetInodeOp) error {
	return within(d, "ForgetInode", op, copyOf[fuseops.ForgetInodeOp], copyBack[fuseops.ForgetInodeOp], nil, d.Fs.ForgetInode)
}
func (d DeadlineFS) BatchForget(ctx context.Context, op *fuseops.BatchForgetOp) error {
	return within(d, "BatchForget", op,
		func(o *fuseops.BatchForgetOp) fuseops.BatchForgetOp {
			c := *o
			c.Entries = append([]fuseops.BatchForgetEntry(nil), o.Entries...)
			return c
		},
		func(dst, src *fuseops.BatchForgetOp) {}, nil, d.Fs.BatchForget)
}
func (d DeadlineFS) MkDir(ctx context.Context, op *fuseops.MkDirOp) error {
	return within(d, "MkDir", op, copyOf[fuseops.MkDirOp], copyBack[fuseops.MkDirOp], nil, d.Fs.MkDir)
}
func (d DeadlineFS) MkNode(ctx context.Context, op *fuseops.MkNodeOp) error {
	return within(d, "MkNode", op, copyOf[fuseops.MkNodeOp], copyBack[fuseops.MkNodeOp], nil, d.Fs.MkNode)
}
func (d DeadlineFS) CreateFile(ctx context.Context, op *fuseops.CreateFileOp) error {
	return within(d, "CreateFile", op, copyOf[fuseops.CreateFileOp], copyBack[fuseops.CreateFileOp], nil, d.Fs.CreateFile)
}
func (d DeadlineFS) CreateLink(ctx context.Context, op *fuseops.CreateLinkOp) error {
	return within(d, "CreateLink", op, copyOf[fuseops.CreateLinkOp], copyBack[fuseops.CreateLinkOp], nil, d.Fs.CreateLink)
}
func (d DeadlineFS) CreateSymlink(ctx context.Context, op *fuseops.CreateSymlinkOp) error {
	return within(d, "CreateSymlink", op, copyOf[fuseops.CreateSymlinkOp], copyBack[fuseops.CreateSymlinkOp], nil, d.Fs.CreateSymlink)
}
func (d DeadlineFS) Rename(ctx context.Context, op *fuseops.RenameOp) error {
	return within(d, "Rename", op, copyOf[fuseops.RenameOp], copyBack[fuseops.RenameOp], nil, d.Fs.Rename)
}
func (d DeadlineFS) RmDir(ctx context.Context, op *fuseops.RmDirOp) error {
	return within(d, "RmDir", op, copyOf[fuseops.RmDirOp], copyBack[fuseops.RmDirOp], nil, d.Fs.RmDir)
}
func (d DeadlineFS) Unlink(ctx context.Context, op *fuseops.UnlinkOp) error {
	return within(d, "Unlink", op, copyOf[fuseops.UnlinkOp], copyBack[fuseops.UnlinkOp], nil, d.Fs.Unlink)
}
func (d DeadlineFS) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) error {
	return within(d, "OpenDir", op, copyOf[fuseops.OpenDirOp], copyBack[fuseops.OpenDirOp], nil, d.Fs.OpenDir)
}
func (d DeadlineFS) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) error {
	return within(d, "ReadDir", op,
		func(o *fuseops.ReadDirOp) fuseops.ReadDirOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.ReadDirOp) { dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead) },
		nil, d.Fs.ReadDir)
}
func (d DeadlineFS) ReleaseDirHandle(ctx context.Context, op *fuseops.ReleaseDirHandleOp) error {
	return within(d, "ReleaseDirHandle", op, copyOf[fuseops.ReleaseDirHandleOp], copyBack[fuseops.ReleaseDirHandleOp], nil, d.Fs.ReleaseDirHandle)
}
func (d DeadlineFS) OpenFile(ctx context.Context, op *fuseops.OpenFileOp) error {
	return within(d, "OpenFile", op, copyOf[fuseops.OpenFileOp], copyBack[fuseops.OpenFileOp], nil, d.Fs.OpenFile)
}

// ReadFile: a vectored read (no Dst) hands back the file system's own slices in Data, freed
// by its Callback after the reply: passed on when in time, released by the late request.
func (d DeadlineFS) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) error {
	return within(d, "ReadFile", op,
		func(o *fuseops.ReadFileOp) fuseops.ReadFileOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.ReadFileOp) {
			if dst.Dst != nil {
				dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead)
			} else {
				dst.Data, dst.BytesRead = src.Data, src.BytesRead
			}
			dst.Callback = src.Callback
		},
		func(c *fuseops.ReadFileOp) {
			if c.Callback != nil {
				c.Callback()
			}
		},
		d.Fs.ReadFile)
}

// WriteFile: the data is the FUSE server's buffer, reused at once; the copy writes its own
// (which the file system may keep: SuppressReuse on the copy only).
func (d DeadlineFS) WriteFile(ctx context.Context, op *fuseops.WriteFileOp) error {
	return within(d, "WriteFile", op,
		func(o *fuseops.WriteFileOp) fuseops.WriteFileOp {
			c := *o
			c.Data = append([]byte(nil), o.Data...)
			return c
		},
		func(dst, src *fuseops.WriteFileOp) { dst.Callback = src.Callback },
		func(c *fuseops.WriteFileOp) {
			if c.Callback != nil {
				c.Callback()
			}
		},
		d.Fs.WriteFile)
}
func (d DeadlineFS) SyncFile(ctx context.Context, op *fuseops.SyncFileOp) error {
	return within(d, "SyncFile", op, copyOf[fuseops.SyncFileOp], copyBack[fuseops.SyncFileOp], nil, d.Fs.SyncFile)
}
func (d DeadlineFS) FlushFile(ctx context.Context, op *fuseops.FlushFileOp) error {
	return within(d, "FlushFile", op, copyOf[fuseops.FlushFileOp], copyBack[fuseops.FlushFileOp], nil, d.Fs.FlushFile)
}
func (d DeadlineFS) ReleaseFileHandle(ctx context.Context, op *fuseops.ReleaseFileHandleOp) error {
	return within(d, "ReleaseFileHandle", op, copyOf[fuseops.ReleaseFileHandleOp], copyBack[fuseops.ReleaseFileHandleOp], nil, d.Fs.ReleaseFileHandle)
}
func (d DeadlineFS) ReadSymlink(ctx context.Context, op *fuseops.ReadSymlinkOp) error {
	return within(d, "ReadSymlink", op, copyOf[fuseops.ReadSymlinkOp], copyBack[fuseops.ReadSymlinkOp], nil, d.Fs.ReadSymlink)
}
func (d DeadlineFS) RemoveXattr(ctx context.Context, op *fuseops.RemoveXattrOp) error {
	return within(d, "RemoveXattr", op, copyOf[fuseops.RemoveXattrOp], copyBack[fuseops.RemoveXattrOp], nil, d.Fs.RemoveXattr)
}
func (d DeadlineFS) GetXattr(ctx context.Context, op *fuseops.GetXattrOp) error {
	return within(d, "GetXattr", op,
		func(o *fuseops.GetXattrOp) fuseops.GetXattrOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.GetXattrOp) {
			// a zero-length Dst asks for the size only: BytesRead is the size, nothing copied
			if len(dst.Dst) == 0 {
				dst.BytesRead = src.BytesRead
				return
			}
			dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead)
		},
		nil, d.Fs.GetXattr)
}
func (d DeadlineFS) ListXattr(ctx context.Context, op *fuseops.ListXattrOp) error {
	return within(d, "ListXattr", op,
		func(o *fuseops.ListXattrOp) fuseops.ListXattrOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.ListXattrOp) {
			if len(dst.Dst) == 0 {
				dst.BytesRead = src.BytesRead
				return
			}
			dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead)
		},
		nil, d.Fs.ListXattr)
}
func (d DeadlineFS) SetXattr(ctx context.Context, op *fuseops.SetXattrOp) error {
	return within(d, "SetXattr", op,
		func(o *fuseops.SetXattrOp) fuseops.SetXattrOp {
			c := *o
			c.Value = append([]byte(nil), o.Value...)
			return c
		},
		func(dst, src *fuseops.SetXattrOp) {}, nil, d.Fs.SetXattr)
}
func (d DeadlineFS) Fallocate(ctx context.Context, op *fuseops.FallocateOp) error {
	return within(d, "Fallocate", op, copyOf[fuseops.FallocateOp], copyBack[fuseops.FallocateOp], nil, d.Fs.Fallocate)
}
func (d DeadlineFS) SyncFS(ctx context.Context, op *fuseops.SyncFSOp) error {
	return within(d, "SyncFS", op, copyOf[fuseops.SyncFSOp], copyBack[fuseops.SyncFSOp], nil, d.Fs.SyncFS)
}
func (d DeadlineFS) Poll(ctx context.Context, op *fuseops.PollOp) error {
	return within(d, "Poll", op, copyOf[fuseops.PollOp], copyBack[fuseops.PollOp], nil, d.Fs.Poll)
}

// SetConnection passes the FUSE connection on: the file system notifies the kernel through it
func (d DeadlineFS) SetConnection(c *fuse.Connection) { d.Fs.SetConnection(c) }
func (d DeadlineFS) Destroy()                         { d.Fs.Destroy() }

var _ fuseutil.FileSystem = DeadlineFS{}
