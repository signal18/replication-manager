package helpers

import (
	"errors"
	"os"
	"strconv"
)

type WorkDir struct {
	dir string
}

func (w *WorkDir) Dir() string {
	return w.dir
}

func (w *WorkDir) Create(dir string, max_size int) error {

	// We need to check if the path length is not too long, otherwise we cannot store
	//the generated socket files with an unique MD5 hash.

	if len(dir) > max_size {
		return errors.New("Your workdir path " + dir + " is too long. Please use a path with a maximum length of: " + strconv.Itoa(max_size) + " characters")
	}

	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return errors.New("Could not create working directory at " + dir + ", exiting...")
			} else {
				w.dir = dir
				return nil
			}
		} else {
			return err
		}
	}
	w.dir = dir
	return nil
}
