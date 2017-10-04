package zapwriter

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fileOpen(t *testing.T) (f *FileOutput, path string, dir string, teadDown func()) {
	teadDown = func() {}

	var err error
	dir, err = ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	teadDown = func() { os.RemoveAll(dir) }

	path = filepath.Join(dir, "test.log")

	f, err = File(path)
	if err != nil {
		t.Fatal(err)
	}

	n, err := f.Write([]byte("hello world\n"))
	if err != nil || n != 12 {
		t.FailNow()
	}

	c, err := ioutil.ReadFile(path)
	if err != nil || string(c) != "hello world\n" {
		t.FailNow()
	}

	return
}

func TestFileWrite(t *testing.T) {
	f, _, _, tearDown := fileOpen(t)
	defer tearDown()

	err := f.Close()
	if err != nil {
		t.FailNow()
	}
}

func TestFileMove(t *testing.T) {
	f, path, dir, tearDown := fileOpen(t)
	defer tearDown()
	os.Rename(path, filepath.Join(dir, "test_bak.log"))
	time.Sleep(2 * time.Second)

	f.Write([]byte("new message\n"))
	c, err := ioutil.ReadFile(path)
	if err != nil || string(c) != "new message\n" {
		t.FailNow()
	}

	f.Close()
}

func TestFileDelete(t *testing.T) {
	f, path, _, tearDown := fileOpen(t)
	defer tearDown()
	os.Remove(path)
	time.Sleep(2 * time.Second)

	f.Write([]byte("new message\n"))
	c, err := ioutil.ReadFile(path)
	if err != nil || string(c) != "new message\n" {
		t.FailNow()
	}

	f.Close()
}
