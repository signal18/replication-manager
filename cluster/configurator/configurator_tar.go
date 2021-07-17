// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func (configurator *Configurator) TarGzWrite(_path string, tw *tar.Writer, fi os.FileInfo, trimprefix string) error {
	fr, err := os.Open(_path)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	defer fr.Close()
	h := new(tar.Header)
	var link string
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		if link, err = os.Readlink(_path); err != nil {
			return nil
		}

	}
	h, _ = tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	h.Name = strings.TrimPrefix(_path, trimprefix)
	//	h.Size = fi.Size()
	//	h.Mode = int64(fi.Mode())
	//	h.ModTime = fi.ModTime()

	err = tw.WriteHeader(h)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	if !fi.Mode().IsRegular() { //nothing more to do for non-regular
		return nil
	}
	_, err = io.Copy(tw, fr)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	return nil
}

func (configurator *Configurator) TarGz(outFilePath string, inPath string) error {
	// file write
	fw, err := os.Create(outFilePath)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	defer fw.Close()

	// gzip write
	gw := gzip.NewWriter(fw)
	defer gw.Close()

	// tar write
	tw := tar.NewWriter(gw)
	defer tw.Close()

	configurator.IterDirectory(inPath, tw, inPath+"/")

	return nil
}

func (configurator *Configurator) IterDirectory(dirPath string, tw *tar.Writer, trimprefix string) error {
	dir, err := os.Open(dirPath)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	defer dir.Close()
	fis, err := dir.Readdir(0)
	if err != nil {
		return errors.New(fmt.Sprintf("Compliance writing config.tar.gz failed : %s", err))
	}
	for _, fi := range fis {
		curPath := dirPath + "/" + fi.Name()
		if fi.IsDir() {
			err := configurator.TarGzWrite(curPath, tw, fi, trimprefix)
			if err != nil {
				return err
			}
			configurator.IterDirectory(curPath, tw, trimprefix)
		} else {
			//	fmt.Printf("adding... %s\n", curPath)
			configurator.TarGzWrite(curPath, tw, fi, trimprefix)
		}
	}
	return nil
}
