// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// #2360: MySQL/Percona 8.4+ get a ProxySQL whose monitor speaks caching_sha2_password; the others
// keep the default; an image set in the configuration always wins.
func TestProxysqlImage(t *testing.T) {
	mysql84, _ := version.NewVersion("MySQL", 8, 4, 11)
	mysql80, _ := version.NewVersion("MySQL", 8, 0, 41)
	mariadb, _ := version.NewVersion("MariaDB", 13, 0, 2)
	cases := []struct {
		name, dbImg, proxyImg string
		running               *version.Version
		want                  string
	}{
		{"mysql 8.4 image", "mysql:8.4", "", nil, proxysqlCachingSha2Image},
		{"mysql 8.4 patch", "mysql:8.4.11", defaultProxysqlImage, nil, proxysqlCachingSha2Image},
		{"mysql 9", "mysql:9.1", defaultProxysqlImage, nil, proxysqlCachingSha2Image},
		{"percona 8.4 suffixed", "percona/percona-server:8.4.3-3", defaultProxysqlImage, nil, proxysqlCachingSha2Image},
		{"mysql latest", "mysql:latest", defaultProxysqlImage, nil, proxysqlCachingSha2Image},
		{"mysql 8.0", "mysql:8.0.41", defaultProxysqlImage, nil, defaultProxysqlImage},
		{"mariadb", "mariadb:13.0", defaultProxysqlImage, nil, defaultProxysqlImage},
		{"unreadable tag", "mysql:oracle", defaultProxysqlImage, nil, defaultProxysqlImage},
		{"explicit image wins", "mysql:8.4", "proxysql/proxysql:2.4.8", nil, "proxysql/proxysql:2.4.8"},
		{"running 8.4 beats the image", "mysql:8.0", defaultProxysqlImage, mysql84, proxysqlCachingSha2Image},
		{"running 8.0 beats the image", "mysql:8.4", defaultProxysqlImage, mysql80, defaultProxysqlImage},
		{"running mariadb", "mysql:8.4", defaultProxysqlImage, mariadb, defaultProxysqlImage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{Conf: &config.Config{ProvDbImg: c.dbImg, ProvProxProxysqlImg: c.proxyImg}}
			if c.running != nil {
				cl.Servers = []*ServerMonitor{{DBVersion: c.running}}
			}
			if got := cl.ProxysqlImage(); got != c.want {
				t.Fatalf("ProxysqlImage() = %q, want %q", got, c.want)
			}
		})
	}
}
