// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2002: an app that is part of the database (a ColumnStore PM) receives the cluster's
// root password as the cluster's database servers do; any other app keeps its own secrets.
func TestAppSecretsEnvironment(t *testing.T) {
	plain := &App{Name: "phpmyadmin1", AppConfig: &config.AppConfig{}}
	if got := appSecretsEnvironment(plain); got != "phpmyadmin1/*" {
		t.Fatalf("an ordinary app gets its own secrets only, got %q", got)
	}
	pm := &App{Name: "pm1", AppConfig: &config.AppConfig{ProvAppDbCredentials: true}}
	if got := appSecretsEnvironment(pm); got != "pm1/* env/MYSQL_ROOT_PASSWORD" {
		t.Fatalf("a PM gets its secrets and the cluster's root password, got %q", got)
	}
}
