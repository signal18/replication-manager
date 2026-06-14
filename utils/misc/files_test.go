package misc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/disk"
)

func TestResolveFilesystemUsesLongestMatchingMountpoint(t *testing.T) {
	oldDiskPartitions := diskPartitionsFunc
	oldCachedPartitions := cachedDiskPartitions()
	defer func() {
		diskPartitionsFunc = oldDiskPartitions
		storeDiskPartitions(oldCachedPartitions)
	}()
	storeDiskPartitions(nil)

	root := t.TempDir()
	mountpoint := filepath.Join(root, "mnt", "backup")
	bindMountpoint := filepath.Join(mountpoint, "bind")
	if err := os.MkdirAll(filepath.Join(mountpoint, "cluster"), 0o755); err != nil {
		t.Fatalf("mkdir mountpoint: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bindMountpoint, "cluster"), 0o755); err != nil {
		t.Fatalf("mkdir bind mountpoint: %v", err)
	}

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{
			{Device: "/dev/root", Mountpoint: string(os.PathSeparator)},
			{Device: "/dev/sda1", Mountpoint: mountpoint},
			{Device: "/dev/sda1", Mountpoint: bindMountpoint},
		}, nil
	}

	baseFS, err := ResolveFilesystem(filepath.Join(mountpoint, "cluster", "base.sql"))
	if err != nil {
		t.Fatalf("ResolveFilesystem base mount: %v", err)
	}
	bindFS, err := ResolveFilesystem(filepath.Join(bindMountpoint, "cluster", "bind.sql"))
	if err != nil {
		t.Fatalf("ResolveFilesystem bind mount: %v", err)
	}

	if baseFS.Mountpoint != mountpoint {
		t.Fatalf("expected base mountpoint %q, got %q", mountpoint, baseFS.Mountpoint)
	}
	if bindFS.Mountpoint != bindMountpoint {
		t.Fatalf("expected bind mountpoint %q, got %q", bindMountpoint, bindFS.Mountpoint)
	}
	if baseFS.Key == bindFS.Key {
		t.Fatalf("expected distinct filesystem keys for distinct mountpoints, got %q", baseFS.Key)
	}
	if bindFS.Key != bindMountpoint {
		t.Fatalf("expected bind mount key %q, got %q", bindMountpoint, bindFS.Key)
	}
}

func TestResolveFilesystemFallsBackWhenPartitionsLookupFails(t *testing.T) {
	oldDiskPartitions := diskPartitionsFunc
	oldCachedPartitions := cachedDiskPartitions()
	defer func() {
		diskPartitionsFunc = oldDiskPartitions
		storeDiskPartitions(oldCachedPartitions)
	}()
	storeDiskPartitions(nil)

	root := t.TempDir()
	nested := filepath.Join(root, "cluster", "node1")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return nil, errors.New("partitions unavailable")
	}

	fsPath, err := ResolveFilesystem(filepath.Join(nested, "backup.sql"))
	if err != nil {
		t.Fatalf("ResolveFilesystem fallback returned error: %v", err)
	}
	if fsPath.Mountpoint == "" {
		t.Fatalf("expected fallback mountpoint to be populated")
	}
	if fsPath.Key == "" {
		t.Fatalf("expected fallback key to be populated")
	}
	if !pathWithinMount(nested, fsPath.Mountpoint) {
		t.Fatalf("expected fallback mountpoint %q to contain nested path %q", fsPath.Mountpoint, nested)
	}
	if fsPath.Key != fsPath.Mountpoint {
		t.Fatalf("expected fallback key %q to match mountpoint", fsPath.Key)
	}
}

func TestResolveFilesystemKeyStableAcrossPartitionLookupAvailability(t *testing.T) {
	oldDiskPartitions := diskPartitionsFunc
	oldCachedPartitions := cachedDiskPartitions()
	defer func() {
		diskPartitionsFunc = oldDiskPartitions
		storeDiskPartitions(oldCachedPartitions)
	}()
	storeDiskPartitions(nil)

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{{Device: "/dev/root", Mountpoint: string(os.PathSeparator)}}, nil
	}
	withPartitions, err := ResolveFilesystem(string(os.PathSeparator))
	if err != nil {
		t.Fatalf("ResolveFilesystem with partitions returned error: %v", err)
	}

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return nil, errors.New("partitions unavailable")
	}
	withoutPartitions, err := ResolveFilesystem(string(os.PathSeparator))
	if err != nil {
		t.Fatalf("ResolveFilesystem without partitions returned error: %v", err)
	}

	if withPartitions.Key != withoutPartitions.Key {
		t.Fatalf("expected stable key across partition availability changes, got %q vs %q", withPartitions.Key, withoutPartitions.Key)
	}
	if withPartitions.Mountpoint != withoutPartitions.Mountpoint {
		t.Fatalf("expected stable mountpoint across partition availability changes, got %q vs %q", withPartitions.Mountpoint, withoutPartitions.Mountpoint)
	}
}

func TestResolveFilesystemBindMountKeyStableAcrossPartitionLookupAvailability(t *testing.T) {
	oldDiskPartitions := diskPartitionsFunc
	oldCachedPartitions := cachedDiskPartitions()
	defer func() {
		diskPartitionsFunc = oldDiskPartitions
		storeDiskPartitions(oldCachedPartitions)
	}()
	storeDiskPartitions(nil)

	root := t.TempDir()
	mountpoint := filepath.Join(root, "mnt", "backup")
	bindMountpoint := filepath.Join(mountpoint, "bind")
	bindFile := filepath.Join(bindMountpoint, "cluster", "bind.sql")
	if err := os.MkdirAll(filepath.Dir(bindFile), 0o755); err != nil {
		t.Fatalf("mkdir bind path: %v", err)
	}

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{
			{Device: "/dev/root", Mountpoint: string(os.PathSeparator)},
			{Device: "/dev/sda1", Mountpoint: mountpoint},
			{Device: "/dev/sda1", Mountpoint: bindMountpoint},
		}, nil
	}
	withPartitions, err := ResolveFilesystem(bindFile)
	if err != nil {
		t.Fatalf("ResolveFilesystem with partitions returned error: %v", err)
	}

	diskPartitionsFunc = func(all bool) ([]disk.PartitionStat, error) {
		return nil, errors.New("partitions unavailable")
	}
	withoutPartitions, err := ResolveFilesystem(bindFile)
	if err != nil {
		t.Fatalf("ResolveFilesystem without partitions returned error: %v", err)
	}

	if withPartitions.Key != bindMountpoint {
		t.Fatalf("expected bind mount key %q with partitions, got %q", bindMountpoint, withPartitions.Key)
	}
	if withoutPartitions.Key != bindMountpoint {
		t.Fatalf("expected cached bind mount key %q without partitions, got %q", bindMountpoint, withoutPartitions.Key)
	}
	if withPartitions.Mountpoint != withoutPartitions.Mountpoint {
		t.Fatalf("expected stable bind mountpoint across partition availability changes, got %q vs %q", withPartitions.Mountpoint, withoutPartitions.Mountpoint)
	}
}

func TestResolveFilesystemWithPartitionsUsesCachedPartitionsWhenNil(t *testing.T) {
	oldDiskPartitions := diskPartitionsFunc
	oldCachedPartitions := cachedDiskPartitions()
	defer func() {
		diskPartitionsFunc = oldDiskPartitions
		storeDiskPartitions(oldCachedPartitions)
	}()
	storeDiskPartitions(nil)

	root := t.TempDir()
	mountpoint := filepath.Join(root, "mnt", "backup")
	bindMountpoint := filepath.Join(mountpoint, "bind")
	bindFile := filepath.Join(bindMountpoint, "cluster", "bind.sql")
	if err := os.MkdirAll(filepath.Dir(bindFile), 0o755); err != nil {
		t.Fatalf("mkdir bind path: %v", err)
	}

	storeDiskPartitions([]disk.PartitionStat{
		{Device: "/dev/root", Mountpoint: string(os.PathSeparator)},
		{Device: "/dev/sda1", Mountpoint: mountpoint},
		{Device: "/dev/sda1", Mountpoint: bindMountpoint},
	})

	fsPath, err := ResolveFilesystemWithPartitions(bindFile, nil)
	if err != nil {
		t.Fatalf("ResolveFilesystemWithPartitions returned error: %v", err)
	}
	if fsPath.Key != bindMountpoint {
		t.Fatalf("expected cached bind mount key %q, got %q", bindMountpoint, fsPath.Key)
	}
	if fsPath.Mountpoint != bindMountpoint {
		t.Fatalf("expected cached bind mountpoint %q, got %q", bindMountpoint, fsPath.Mountpoint)
	}
}
