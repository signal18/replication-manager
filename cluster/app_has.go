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
	"net"
	"os"

	"github.com/signal18/replication-manager/config"
)

func (app *App) hasCookie(key string) bool {
	if _, err := os.Stat(app.Datadir + "/@" + key); os.IsNotExist(err) {
		return false
	}
	return true
}

func (app *App) HasProvisionCookie() bool {
	return app.hasCookie("cookie_prov")
}

func (app *App) HasUnprovisionCookie() bool {
	return app.hasCookie("cookie_unprov")
}

func (app *App) HasWaitStartCookie() bool {
	return app.hasCookie("cookie_waitstart")
}

func (app *App) HasWaitStopCookie() bool {
	return app.hasCookie("cookie_waitstop")
}

func (app *App) HasRestartCookie() bool {
	return app.hasCookie("cookie_restart")
}

func (app *App) HasConfigCookie() bool {
	return app.hasCookie("cookie_config")
}

func (app *App) HasReprovCookie() bool {
	return app.hasCookie("cookie_reprov")
}

func (app *App) IsRunning() bool {
	return !app.IsDown()
}

func (app *App) IsIgnored() bool {
	// implement logic for zone and disable route janitor to app
	return false
}

func (app *App) IsDown() bool {
	if app.State == stateFailed || app.State == stateSuspect || app.State == stateErrorAuth {
		return true
	}
	return false
}

func (app *App) HasDNS() bool {
	if net.ParseIP(app.Host) == nil || app.GetOrchestrator() == config.ConstOrchestratorOpenSVC || app.GetOrchestrator() == config.ConstOrchestratorKubernetes {
		return true
	}
	return false
}
