// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) TarGzWrite(_path string, tw *tar.Writer, fi os.FileInfo, trimprefix string) {
	fr, err := os.Open(_path)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
	defer fr.Close()
	h := new(tar.Header)
	var link string
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		if link, err = os.Readlink(_path); err != nil {
			return
		}

	}
	h, _ = tar.FileInfoHeader(fi, link)
	if err != nil {
		return
	}
	h.Name = strings.TrimPrefix(_path, trimprefix)
	//	h.Size = fi.Size()
	//	h.Mode = int64(fi.Mode())
	//	h.ModTime = fi.ModTime()

	err = tw.WriteHeader(h)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
	if !fi.Mode().IsRegular() { //nothing more to do for non-regular
		return
	}
	_, err = io.Copy(tw, fr)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
}

func (cluster *Cluster) IterDirectory(dirPath string, tw *tar.Writer, trimprefix string) {
	dir, err := os.Open(dirPath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
	defer dir.Close()
	fis, err := dir.Readdir(0)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
	for _, fi := range fis {
		curPath := dirPath + "/" + fi.Name()
		if fi.IsDir() {
			cluster.TarGzWrite(curPath, tw, fi, trimprefix)
			cluster.IterDirectory(curPath, tw, trimprefix)
		} else {
			//	fmt.Printf("adding... %s\n", curPath)
			cluster.TarGzWrite(curPath, tw, fi, trimprefix)
		}
	}
}

func (cluster *Cluster) TarGz(outFilePath string, inPath string) {
	// file write
	fw, err := os.Create(outFilePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Compliance writing config.tar.gz failed : %s", err)
	}
	defer fw.Close()

	// gzip write
	gw := gzip.NewWriter(fw)
	defer gw.Close()

	// tar write
	tw := tar.NewWriter(gw)
	defer tw.Close()

	cluster.IterDirectory(inPath, tw, inPath+"/")

	fmt.Println("tar.gz ok")
}
