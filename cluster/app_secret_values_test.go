// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// Every app setting tagged secret:"true" goes to the stores under its setting's name, in clear
// for the store; an unset one and a plain setting do not.
func TestAppSecretValuesFromTags(t *testing.T) {
	conf := &config.Config{SecretKey: []byte("0123456789abcdef")}
	cl := &Cluster{Name: "c1", Conf: conf}
	app := &App{AppConfig: &config.AppConfig{
		AppDbPass:         conf.GetEncryptedString("db-pass"),
		AppRandomPassword: conf.GetEncryptedString("random-pass"),
		AppAPIToken:       conf.GetEncryptedString("api-token"),
		AppAPITokenID:     "id1",
		AppDbUser:         "user1",
	}}
	got := cl.appSecretValues(app)
	want := map[string]string{"app-db-pass": "db-pass", "app-random-password": "random-pass", "app-api-token": "api-token"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s not copied as its value", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d values, want %d", len(got), len(want))
	}
	app.AppConfig.AppAPIToken = ""
	if _, ok := cl.appSecretValues(app)["app-api-token"]; ok {
		t.Error("an unset secret is copied")
	}
}
