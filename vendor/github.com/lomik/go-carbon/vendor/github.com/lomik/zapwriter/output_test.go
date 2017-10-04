package zapwriter

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "test.log")

	out, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	out.Write([]byte("hello world\n"))
	out.Sync()

	c, err := ioutil.ReadFile(path)
	if err != nil || string(c) != "hello world\n" {
		t.FailNow()
	}
}
