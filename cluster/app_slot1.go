// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/pflag"
)

type AppSlot1 struct {
	App
}

func NewAppSlot1(placement int, cluster *Cluster, App1Host string) *AppSlot1 {
	app := new(AppSlot1)

	app.Type = config.ConstAppSlot1
	app.Host, app.Port = misc.SplitHostPort(App1Host)

	if app.Name == "" {
		app.Name = app.Host
	}
	// Source name will equal to cluster name

	return app
}

func (app *AppSlot1) AddFlags(flags *pflag.FlagSet, conf *config.Config) {

}

func (app *AppSlot1) Refresh() error {
	return nil
}
