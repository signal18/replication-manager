// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package common

import (
	"context"
	"time"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

// DeadlineFS answers every FUSE request within Timeout (#2091). A program writing to a FUSE
// mount waits in the kernel for the mount's answer, a wait nothing can cancel; when the mount
// is served by the same process (replication-manager's backups folder), a request stuck on S3
// would hold it for good. Each request runs on its own copy, in a goroutine: answered in time,
// its result is copied back; not, the kernel gets EIO at the deadline and the waiting program
// goes on, the late request finishing in the background on its copy, so it never touches a
// buffer the FUSE server has reused.
type DeadlineFS struct {
	Fs      fuseutil.FileSystem
	Timeout time.Duration
	// OnLate is told the operation that missed its deadline (may be nil)
	OnLate func(op string)
}

// lateGrace is how long a late request may still run in the background: its own S3 requests
// carry their timeout, this only bounds its goroutine.
const lateGrace = 10 * time.Minute

func within[T any](d DeadlineFS, name string, op *T, prepare func(*T) T, finish func(dst, src *T), run func(context.Context, *T) error) error {
	cp := prepare(op)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.Timeout+lateGrace)
		defer cancel()
		var err error
		func() {
			defer LogPanic(&err)
			err = run(ctx, &cp)
		}()
		done <- err
	}()
	timer := time.NewTimer(d.Timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil {
			finish(op, &cp)
		}
		return err
	case <-timer.C:
		if d.OnLate != nil {
			d.OnLate(name)
		}
		return fuse.EIO
	}
}

// the plain case: the request has no buffer, its copy is the whole struct
func copyOf[T any](o *T) T        { return *o }
func copyBack[T any](dst, src *T) { *dst = *src }

// a request that fills a buffer: the copy fills its own, copied back when in time
func ownBuffer(dst []byte) []byte { return make([]byte, len(dst)) }

// copyRead copies what the copy read into the request's buffer, never past either buffer.
func copyRead(dst, src []byte, n int) int {
	if n > len(src) {
		n = len(src)
	}
	return copy(dst, src[:n])
}

func (d DeadlineFS) StatFS(ctx context.Context, op *fuseops.StatFSOp) error {
	return within(d, "StatFS", op, copyOf[fuseops.StatFSOp], copyBack[fuseops.StatFSOp], d.Fs.StatFS)
}
func (d DeadlineFS) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) error {
	return within(d, "LookUpInode", op, copyOf[fuseops.LookUpInodeOp], copyBack[fuseops.LookUpInodeOp], d.Fs.LookUpInode)
}
func (d DeadlineFS) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) error {
	return within(d, "GetInodeAttributes", op, copyOf[fuseops.GetInodeAttributesOp], copyBack[fuseops.GetInodeAttributesOp], d.Fs.GetInodeAttributes)
}
func (d DeadlineFS) SetInodeAttributes(ctx context.Context, op *fuseops.SetInodeAttributesOp) error {
	return within(d, "SetInodeAttributes", op, copyOf[fuseops.SetInodeAttributesOp], copyBack[fuseops.SetInodeAttributesOp], d.Fs.SetInodeAttributes)
}
func (d DeadlineFS) ForgetInode(ctx context.Context, op *fuseops.ForgetInodeOp) error {
	return within(d, "ForgetInode", op, copyOf[fuseops.ForgetInodeOp], copyBack[fuseops.ForgetInodeOp], d.Fs.ForgetInode)
}
func (d DeadlineFS) MkDir(ctx context.Context, op *fuseops.MkDirOp) error {
	return within(d, "MkDir", op, copyOf[fuseops.MkDirOp], copyBack[fuseops.MkDirOp], d.Fs.MkDir)
}
func (d DeadlineFS) MkNode(ctx context.Context, op *fuseops.MkNodeOp) error {
	return within(d, "MkNode", op, copyOf[fuseops.MkNodeOp], copyBack[fuseops.MkNodeOp], d.Fs.MkNode)
}
func (d DeadlineFS) CreateFile(ctx context.Context, op *fuseops.CreateFileOp) error {
	return within(d, "CreateFile", op, copyOf[fuseops.CreateFileOp], copyBack[fuseops.CreateFileOp], d.Fs.CreateFile)
}
func (d DeadlineFS) CreateLink(ctx context.Context, op *fuseops.CreateLinkOp) error {
	return within(d, "CreateLink", op, copyOf[fuseops.CreateLinkOp], copyBack[fuseops.CreateLinkOp], d.Fs.CreateLink)
}
func (d DeadlineFS) CreateSymlink(ctx context.Context, op *fuseops.CreateSymlinkOp) error {
	return within(d, "CreateSymlink", op, copyOf[fuseops.CreateSymlinkOp], copyBack[fuseops.CreateSymlinkOp], d.Fs.CreateSymlink)
}
func (d DeadlineFS) Rename(ctx context.Context, op *fuseops.RenameOp) error {
	return within(d, "Rename", op, copyOf[fuseops.RenameOp], copyBack[fuseops.RenameOp], d.Fs.Rename)
}
func (d DeadlineFS) RmDir(ctx context.Context, op *fuseops.RmDirOp) error {
	return within(d, "RmDir", op, copyOf[fuseops.RmDirOp], copyBack[fuseops.RmDirOp], d.Fs.RmDir)
}
func (d DeadlineFS) Unlink(ctx context.Context, op *fuseops.UnlinkOp) error {
	return within(d, "Unlink", op, copyOf[fuseops.UnlinkOp], copyBack[fuseops.UnlinkOp], d.Fs.Unlink)
}
func (d DeadlineFS) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) error {
	return within(d, "OpenDir", op, copyOf[fuseops.OpenDirOp], copyBack[fuseops.OpenDirOp], d.Fs.OpenDir)
}
func (d DeadlineFS) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) error {
	return within(d, "ReadDir", op,
		func(o *fuseops.ReadDirOp) fuseops.ReadDirOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.ReadDirOp) { dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead) },
		d.Fs.ReadDir)
}
func (d DeadlineFS) ReleaseDirHandle(ctx context.Context, op *fuseops.ReleaseDirHandleOp) error {
	return within(d, "ReleaseDirHandle", op, copyOf[fuseops.ReleaseDirHandleOp], copyBack[fuseops.ReleaseDirHandleOp], d.Fs.ReleaseDirHandle)
}
func (d DeadlineFS) OpenFile(ctx context.Context, op *fuseops.OpenFileOp) error {
	return within(d, "OpenFile", op, copyOf[fuseops.OpenFileOp], copyBack[fuseops.OpenFileOp], d.Fs.OpenFile)
}
func (d DeadlineFS) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) error {
	return within(d, "ReadFile", op,
		func(o *fuseops.ReadFileOp) fuseops.ReadFileOp { c := *o; c.Dst = ownBuffer(o.Dst); return c },
		func(dst, src *fuseops.ReadFileOp) { dst.BytesRead = copyRead(dst.Dst, src.Dst, src.BytesRead) },
		d.Fs.ReadFile)
}
func (d DeadlineFS) WriteFile(ctx context.Context, op *fuseops.WriteFileOp) error {
	return within(d, "WriteFile", op,
		// the data is the FUSE server's buffer: the copy writes its own
		func(o *fuseops.WriteFileOp) fuseops.WriteFileOp {
			c := *o
			c.Data = append([]byte(nil), o.Data...)
			return c
		},
		func(dst, src *fuseops.WriteFileOp) {},
		d.Fs.WriteFile)
}
func (d DeadlineFS) SyncFile(ctx context.Context, op *fuseops.SyncFileOp) error {
	return within(d, "SyncFile", op, copyOf[fuseops.SyncFileOp], copyBack[fuseops.SyncFileOp], d.Fs.SyncFile)
}
func (d DeadlineFS) FlushFile(ctx context.Context, op *fuseops.FlushFileOp) error {
	return within(d, "FlushFile", op, copyOf[fuseops.FlushFileOp], copyBack[fuseops.FlushFileOp], d.Fs.FlushFile)
}
func (d DeadlineFS) ReleaseFileHandle(ctx context.Context, op *fuseops.ReleaseFileHandleOp) error {
	return within(d, "ReleaseFileHandle", op, copyOf[fuseops.ReleaseFileHandleOp], copyBack[fuseops.ReleaseFileHandleOp], d.Fs.ReleaseFileHandle)
}
func (d DeadlineFS) ReadSymlink(ctx context.Context, op *fuseops.ReadSymlinkOp) error {
	return within(d, "ReadSymlink", op, copyOf[fuseops.ReadSymlinkOp], copyBack[fuseops.ReadSymlinkOp], d.Fs.ReadSymlink)
}
func (d DeadlineFS) RemoveXattr(ctx context.Context, op *fuseops.RemoveXattrOp) error {
	return within(d, "RemoveXattr", op, copyOf[fuseops.RemoveXattrOp], copyBack[fuseops.RemoveXattrOp], d.Fs.RemoveXattr)
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
		d.Fs.GetXattr)
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
		d.Fs.ListXattr)
}
func (d DeadlineFS) SetXattr(ctx context.Context, op *fuseops.SetXattrOp) error {
	return within(d, "SetXattr", op,
		func(o *fuseops.SetXattrOp) fuseops.SetXattrOp {
			c := *o
			c.Value = append([]byte(nil), o.Value...)
			return c
		},
		func(dst, src *fuseops.SetXattrOp) {},
		d.Fs.SetXattr)
}
func (d DeadlineFS) Fallocate(ctx context.Context, op *fuseops.FallocateOp) error {
	return within(d, "Fallocate", op, copyOf[fuseops.FallocateOp], copyBack[fuseops.FallocateOp], d.Fs.Fallocate)
}
func (d DeadlineFS) Destroy() { d.Fs.Destroy() }

var _ fuseutil.FileSystem = DeadlineFS{}
