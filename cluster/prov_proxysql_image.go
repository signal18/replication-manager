// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/utils/releases"
)

const (
	// defaultProxysqlImage is prov-proxy-docker-proxysql-img's default: ProxySQL 1.4.
	defaultProxysqlImage = "signal18/proxysql:1.4"
	// proxysqlCachingSha2Image fronts databases whose accounts use caching_sha2_password only.
	// 3.0.11 is the first ProxySQL whose monitor authenticates with it (sysown/proxysql#5363);
	// 1.4 has no caching_sha2_password at all, and 2.x's monitor fails with or without TLS.
	proxysqlCachingSha2Image = "proxysql/proxysql:3.0.12"
)

// cachingSha2OnlyLine: from MySQL and Percona 8.4, mysql_native_password is disabled by default
// (removed in 9.0) and every account, root included, uses caching_sha2_password.
var cachingSha2OnlyLine = releases.Line{Major: 8, Minor: 4}

// ProxysqlImage is the ProxySQL image a proxy is provisioned with (#2360). An image set in the
// configuration (anything but the default) is used as it is. Otherwise a cluster on MySQL or
// Percona 8.4 or later gets ProxySQL 3.0.12: with 1.4 the monitor cannot log in, shuns every
// server and the proxy never routes (seen on a dev instance, MySQL 8.4.11). MariaDB and older
// MySQL keep the default.
func (cluster *Cluster) ProxysqlImage() string {
	img := strings.TrimSpace(cluster.Conf.ProvProxProxysqlImg)
	if img != "" && img != defaultProxysqlImage {
		return img
	}
	if cluster.databasesCachingSha2Only() {
		return proxysqlCachingSha2Image
	}
	return defaultProxysqlImage
}

// databasesCachingSha2Only tells whether the cluster's databases are MySQL or Percona 8.4 or
// later: from a server's version when one is known, else from the database image (its resolved
// release, else its tag; latest and lts are 8.4 or later today). An unreadable tag keeps false.
func (cluster *Cluster) databasesCachingSha2Only() bool {
	for _, s := range cluster.Servers {
		if s == nil || s.DBVersion == nil || s.DBVersion.Major == 0 {
			continue
		}
		if !s.DBVersion.IsMySQLOrPercona() {
			return false
		}
		return !(releases.Line{Major: s.DBVersion.Major, Minor: s.DBVersion.Minor}).Less(cachingSha2OnlyLine)
	}
	img := cluster.deployImage()
	switch releases.FlavorOfImage(img) {
	case "mysql", "percona":
	default:
		return false
	}
	_, tag := releases.SplitImage(img)
	switch strings.ToLower(tag) {
	case "", "latest", "lts":
		return true
	}
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i] // 8.4-oracle, 8.4.3-1
	}
	line, err := releases.ParseLine(tag)
	if err != nil {
		return false
	}
	return !line.Less(cachingSha2OnlyLine)
}
