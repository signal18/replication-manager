package misc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// #2114: a file already owned keeps its ctime (the backup fingerprints hold it)
func TestChownRLeavesOwnedFilesAlone(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "sub", "dump.sql")
	os.MkdirAll(filepath.Dir(f), 0o755)
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctime := func() syscall.Timespec {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		return statCtime(fi.Sys().(*syscall.Stat_t))
	}
	before := ctime()
	time.Sleep(20 * time.Millisecond) // past the kernel's timestamp step
	if err := ChownR(d, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if after := ctime(); after != before {
		t.Fatalf("an owned file was chowned: ctime %v -> %v", before, after)
	}
}
