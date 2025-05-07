// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

func (configurator *Configurator) SetConfig(conf config.Config) {
	configurator.ClusterConfig = conf
	configurator.DBTags = strings.Split(conf.ProvTags, ",")
	configurator.ProxyTags = strings.Split(conf.ProvProxTags, ",")
}

func (configurator *Configurator) SetLogger(logger *logrus.Logger) {
	configurator.Logger = logger
}

func (configurator *Configurator) SetDBTags(newtags []string) {
	configurator.DBTags = newtags
}

func (configurator *Configurator) SetProxyTags(newtags []string) {
	configurator.ProxyTags = newtags
}

func (configurator *Configurator) SetDBCores(value string) {
	configurator.ClusterConfig.ProvCores = value
}

func (configurator *Configurator) SetDBMemory(value string) {
	configurator.ClusterConfig.ProvMem = value
}

func (configurator *Configurator) SetDBDisk(value string) {
	configurator.ClusterConfig.ProvDisk = value
}

func (configurator *Configurator) SetDBDiskIOPS(value string) {
	configurator.ClusterConfig.ProvIops = value
}

func (configurator *Configurator) SetDBMaxConnections(value string) {
	valueNum, err := strconv.Atoi(value)
	if err != nil {
		configurator.ClusterConfig.ProvMaxConnections = 1000
		return
	}
	configurator.ClusterConfig.ProvMaxConnections = valueNum
}

func (configurator *Configurator) SetDBExpireLogDays(value string) {
	valueNum, err := strconv.Atoi(value)
	if err != nil {
		configurator.ClusterConfig.ProvExpireLogDays = 5
	}
	configurator.ClusterConfig.ProvExpireLogDays = valueNum
}

func (configurator *Configurator) SetProxyCores(value string) {
	configurator.ClusterConfig.ProvProxCores = value
}

func (configurator *Configurator) SetProxyMemorySize(value string) {
	configurator.ClusterConfig.ProvProxMem = value
}

func (configurator *Configurator) SetProxyDiskSize(value string) {
	configurator.ClusterConfig.ProvProxDisk = value
}

func (configurator *Configurator) CopyPreservedVariables(dirpath string, skipslavestart bool) error {
	srcpath := filepath.Join(dirpath, "99_preserved.cnf")
	destpath := filepath.Join(dirpath, "init/etc/mysql/custom.d/99_preserved.cnf")

	// Check if the source file exists
	if _, err := os.Stat(srcpath); os.IsNotExist(err) {
		return nil
	}

	// Copy the file
	in, err := os.Open(srcpath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(destpath)
	if err != nil {
		return err
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	if skipslavestart {
		// Add skip-slave-start option to the file
		if _, err := out.WriteString("\nskip-slave-start=1\n"); err != nil {
			return err
		}
	}

	err = out.Sync()
	if err != nil {
		return err
	}

	si, err := os.Stat(srcpath)
	if err != nil {
		return err
	}
	err = os.Chmod(destpath, si.Mode())
	if err != nil {
		return err
	}

	return nil
}
