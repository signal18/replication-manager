package app

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

func (app *AppWebDevOps) LocalhostProvisionService(errorChan chan error) error {

	out := &bytes.Buffer{}
	path := app.Datadir + "/var"
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		errorChan <- err
		return err
	}
	app.GetAppConfig()
	os.Symlink(app.Datadir+"/init/data", path)

	err = app.LocalhostStartService()
	if err != nil {
		errorChan <- err
		return err

	}
	errorChan <- nil
	return nil
}

func (app *AppWebDevOps) LocalhostStartService() error {
	app.GetAppConfig()
	//init haproxy do start or reload
	app.Init()

	return nil
}

func (app *AppWebDevOps) LocalhostStopService() error {

	pid, err := os.ReadFile(app.Datadir + "/var/nginx.pid")
	if err != nil {
		return errors.New("No such file " + app.Datadir + "/var/nginx.pid")
	}
	killCmd := exec.Command("kill", "-9", strings.Trim(string(pid), "\n"))
	killCmd.Run()
	return nil
}

func (app *AppWebDevOps) LocalhostUnprovisionService(errorChan chan error) error {
	app.LocalhostStopService()
	os.RemoveAll(app.Datadir + "/var")
	errorChan <- nil
	return nil
}
