// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"os"

	"github.com/signal18/replication-manager/config"
)

func (app *App) DelLock() {
	app.Lock.Unlock()
}

func (app *App) delCookie(key string) error {
	err := os.Remove(app.Datadir + "/@" + key)
	cluster := app.ClusterGroup
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg, "Remove cookie (%s) %s", key, err)
	}

	return err
}

func (app *App) DelProvisionCookie() error {
	return app.delCookie("cookie_prov")
}

func (app *App) DelUnprovisionCookie() error {
	return app.delCookie("cookie_unprov")
}

func (app *App) DelReprovisionCookie() error {
	return app.delCookie("cookie_reprov")
}

func (app *App) DelRestartCookie() error {
	return app.delCookie("cookie_restart")
}

func (app *App) DelWaitStartCookie() error {
	return app.delCookie("cookie_waitstart")
}

func (app *App) DelWaitStopCookie() error {
	return app.delCookie("cookie_waitstop")
}

func (app *App) DelConfigCookie() error {
	return app.delCookie("cookie_config")
}

func (app *App) DelConfigRefreshCookie() error {
	return app.delCookie("cookie_configrefresh")
}

func (app *App) DelNoConfigFetchCookie() error {
	return app.delCookie("cookie_noconfigfetch")
}
