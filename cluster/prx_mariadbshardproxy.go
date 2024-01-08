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
	"errors"
	"fmt"
	"hash/crc64"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type MariadbShardProxy struct {
	Proxy
}

func NewMariadbShardProxy(placement int, cluster *Cluster, proxyHost string) *MariadbShardProxy {
	conf := cluster.Conf
	prx := new(MariadbShardProxy)
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSShardProxyPartitions, conf.MdbsHostsIPV6, conf.MdbsJanitorWeights)
	prx.Type = config.ConstProxySpider
	prx.Host, prx.Port = misc.SplitHostPort(proxyHost)
	prx.User, prx.Pass = misc.SplitPair(cluster.Conf.GetDecryptedValue("shardproxy-credential"))
	prx.ReadPort, _ = strconv.Atoi(prx.GetPort())
	prx.ReadWritePort, _ = strconv.Atoi(prx.GetPort())
	prx.Name = prx.Host
	if conf.ProvNetCNI {
		if conf.ClusterHead == "" {
			prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
		} else {
			prx.Host = prx.Host + "." + conf.ClusterHead + ".svc." + conf.ProvOrchestratorCluster
		}
		prx.Port = "3306"
	}

	prx.WritePort, _ = strconv.Atoi(prx.GetPort())
	if cluster.Conf.ProvNetCNI {
		host := strings.Split(prx.Host, ".")[0]
		if cluster.Conf.ClusterHead != "" {
			prx.ShardProxy, _ = cluster.newServerMonitor(host+":"+prx.Port, prx.User, prx.Pass, true, cluster.GetDomainHeadCluster())
		} else {
			prx.ShardProxy, _ = cluster.newServerMonitor(host+":"+prx.Port, prx.User, prx.Pass, true, cluster.GetDomain())
		}
	} else {
		prx.ShardProxy, _ = cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, true, "")
	}
	prx.ShardProxy.SlapOSDatadir = prx.SlapOSDatadir

	return prx
}

func (proxy *MariadbShardProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.MdbsProxyOn, "shardproxy", false, "MariaDB Spider proxy")
	flags.BoolVar(&conf.MdbsProxyDebug, "shardproxy-debug", false, "MariaDB Spider proxy")
	flags.IntVar(&conf.MdbsProxyLogLevel, "shardproxy-log-level", 0, "MariaDB Spider proxy")
	flags.StringVar(&conf.MdbsProxyHosts, "shardproxy-servers", "127.0.0.1:3307", "MariaDB spider proxy hosts IP:Port,IP:Port")
	flags.StringVar(&conf.MdbsJanitorWeights, "shardproxy-janitor-weights", "100", "Weight of each MariaDB spider host inside janitor proxy")
	flags.StringVar(&conf.MdbsProxyCredential, "shardproxy-credential", "root:mariadb", "MariaDB spider proxy credential")
	flags.BoolVar(&conf.MdbsProxyCopyGrants, "shardproxy-copy-grants", true, "Copy grants from shards master")
	flags.BoolVar(&conf.MdbsProxyLoadSystem, "shardproxy-load-system", true, "Load Spider system tables")
	flags.StringVar(&conf.MdbsUniversalTables, "shardproxy-universal-tables", "replication_manager_schema.bench", "MariaDB spider proxy table list that are federarated to all master")
	flags.StringVar(&conf.MdbsIgnoreTables, "shardproxy-ignore-tables", "", "MariaDB spider proxy master table list that are ignored")
	flags.StringVar(&conf.MdbsHostsIPV6, "shardproxy-servers-ipv6", "", "ipv6 bind address ")
}

func (proxy *MariadbShardProxy) Init() {
	cluster := proxy.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Init MdbShardProxy %s %s", proxy.Host, proxy.Port)
	cluster.ShardProxyBootstrap(proxy)
	if cluster.Conf.MdbsProxyLoadSystem {
		cluster.ShardProxyCreateSystemTable(proxy)
	}
	cluster.CheckMdbShardServersSchema(proxy)
	cluster.AddShardingHostGroup(proxy)
}

func (proxy *MariadbShardProxy) GetProxyConfig() string {
	cluster := proxy.ClusterGroup
	if proxy.ShardProxy == nil {
		proxy.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Can't get shard proxy config start monitoring")
		proxy.ClusterGroup.ShardProxyBootstrap(proxy)
		return proxy.ShardProxy.GetDatabaseConfig()
	} else {
		return proxy.ShardProxy.GetDatabaseConfig()
	}
}

func (proxy *MariadbShardProxy) Failover() {
	cluster := proxy.ClusterGroup
	if cluster.master == nil {
		return
	}
	schemas, _, err := cluster.master.GetSchemas()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Could not fetch master schemas %s", err)
	}
	foundReplicationManagerSchema := false
	for _, s := range schemas {
		if s == "replication_manager_schema" {
			foundReplicationManagerSchema = true
		}
		checksum64 := crc64.Checksum([]byte(s+"_"+cluster.GetName()), cluster.crcTable)

		query := "CREATE OR REPLACE SERVER RW" + strconv.FormatUint(checksum64, 10) + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(cluster.master.Host) + "', DATABASE '" + s + "', USER '" + cluster.master.User + "', PASSWORD '" + cluster.master.Pass + "', PORT " + cluster.master.Port + ")"
		_, err = proxy.ShardProxy.Conn.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, "ERROR: query %s %s", query, err)
		}
		for _, slave := range cluster.slaves {
			query := "CREATE OR REPLACE SERVER RO" + strconv.FormatUint(checksum64, 10) + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(slave.Host) + "', DATABASE '" + s + "', USER '" + slave.User + "', PASSWORD '" + slave.Pass + "', PORT " + slave.Port + ")"
			_, err = proxy.ShardProxy.Conn.Exec(query)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, "ERROR: query %s %s", query, err)
			}
		}

		query = "CREATE DATABASE IF NOT EXISTS " + s
		_, err = proxy.ShardProxy.Conn.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
		}

	}
	if !foundReplicationManagerSchema {
		cluster.master.Conn.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	}
	query := "FLUSH TABLES"
	_, err = proxy.ShardProxy.Conn.Exec(query)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, "ERROR: query %s %s", query, err)
	}
}

func (proxy *MariadbShardProxy) BackendsStateChange() {
	return
}

func (proxy *MariadbShardProxy) SetMaintenance(s *ServerMonitor) {
	return
}

func (cluster *Cluster) CheckMdbShardServersSchema(proxy *MariadbShardProxy) {
	if cluster.master == nil {
		return
	}
	if proxy.ShardProxy.Conn == nil {
		return
	}
	schemas, _, err := cluster.master.GetSchemas()
	if err != nil {
		cluster.StateMachine.AddState("WARN0089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0089"], cluster.master.URL), ErrFrom: "PROXY", ServerUrl: cluster.master.URL})
		return
	}
	foundReplicationManagerSchema := false
	for _, s := range schemas {
		if s == "replication_manager_schema" {
			foundReplicationManagerSchema = true
		}
		checksum64 := crc64.Checksum([]byte(s+"_"+cluster.GetName()), cluster.crcTable)

		query := "CREATE SERVER IF NOT EXISTS RW" + strconv.FormatUint(checksum64, 10) + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(cluster.master.Host) + "', DATABASE '" + s + "', USER '" + cluster.master.User + "', PASSWORD '" + cluster.master.Pass + "', PORT " + cluster.master.Port + ")"
		_, err = proxy.ShardProxy.Conn.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "query %s %s", query, err)
		}
		for _, slave := range cluster.slaves {

			query := "CREATE SERVER IF NOT EXISTS RO" + strconv.FormatUint(checksum64, 10) + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(slave.Host) + "', DATABASE '" + s + "', USER '" + slave.User + "', PASSWORD '" + slave.Pass + "', PORT " + slave.Port + ")"
			_, err = proxy.ShardProxy.Conn.Exec(query)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "query %s %s", query, err)
			}
		}
		query2 := "CREATE DATABASE IF NOT EXISTS " + s
		_, err = proxy.ShardProxy.Conn.Exec(query2)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Failed query %s %s", query2, err)
		}

	}
	if !foundReplicationManagerSchema {
		cluster.master.Conn.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	}

}

func (proxy *MariadbShardProxy) CertificatesReload() error {
	proxy.ShardProxy.CertificatesReload()
	return nil
}

func (proxy *MariadbShardProxy) Refresh() error {
	if proxy.ShardProxy == nil {
		//proxy.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxy,LvlErr, "Sharding proxy refresh no database monitor yet initialize")
		proxy.ClusterGroup.StateMachine.AddState("ERR00086", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(proxy.ClusterGroup.GetErrorList()["ERR00086"]), ErrFrom: "PROXY", ServerUrl: proxy.GetURL()})
		return errors.New("Sharding proxy refresh no database monitor yet initialize")
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go proxy.ShardProxy.Ping(wg)
	wg.Wait()

	err := proxy.ShardProxy.Refresh()
	if err != nil {
		//proxy.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxy,LvlErr, "Sharding proxy refresh error (%s)", err)
		return err
	}
	proxy.Version = proxy.ShardProxy.Variables["VERSION"]

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	servers, _, _ := dbhelper.GetServers(proxy.ShardProxy.Conn)
	for _, s := range servers {
		myport := strconv.FormatUint(uint64(s.Port), 10)
		var bke = Backend{
			Host:         s.Host,
			Port:         myport,
			PrxName:      s.Host + ":" + myport,
			PrxStatus:    "ONLINE",
			PrxHostgroup: "WRITE",
		}

		//PrxConnections: s.Variables,
		//PrxByteIn:      strconv.Itoa(proxysqlByteOut),
		//PrxByteOut:     strconv.Itoa(proxysqlByteIn),
		//PrxLatency:     strconv.Itoa(proxysqlLatency),

		proxy.BackendsWrite = append(proxy.BackendsWrite, bke)

		var bkeread = Backend{
			Host:         s.Host,
			Port:         myport,
			PrxName:      s.Host + ":" + myport,
			PrxStatus:    "ONLINE",
			PrxHostgroup: "READ",
		}
		proxy.BackendsRead = append(proxy.BackendsRead, bkeread)
	}
	proxy.ClusterGroup.CheckMdbShardServersSchema(proxy)
	return nil
}

func (proxy *MariadbShardProxy) RotateProxyPasswords(password string) {
	if proxy.ShardProxy.IsRunning() {
		proxy.ShardProxy.SetCredential(proxy.ShardProxy.URL, proxy.ShardProxy.User, password)
	}

	return
}

func (cluster *Cluster) refreshMdbsproxy(oldmaster *ServerMonitor, proxy *MariadbShardProxy) error {
	if proxy.ShardProxy == nil {
		return errors.New("Sharding proxy no database monitor yet initialize")
	}
	err := proxy.Refresh()
	if err != nil {

		return err
	}
	proxy.Version = proxy.ShardProxy.Variables["VERSION"]

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	servers, _, _ := dbhelper.GetServers(proxy.ShardProxy.Conn)
	for _, s := range servers {
		myport := strconv.FormatUint(uint64(s.Port), 10)
		var bke = Backend{
			Host:         s.Host,
			Port:         myport,
			PrxName:      s.Host + ":" + myport,
			PrxStatus:    "ONLINE",
			PrxHostgroup: "WRITE",
		}

		//PrxConnections: s.Variables,
		//PrxByteIn:      strconv.Itoa(proxysqlByteOut),
		//PrxByteOut:     strconv.Itoa(proxysqlByteIn),
		//PrxLatency:     strconv.Itoa(proxysqlLatency),

		proxy.BackendsWrite = append(proxy.BackendsWrite, bke)

		var bkeread = Backend{
			Host:         s.Host,
			Port:         myport,
			PrxName:      s.Host + ":" + myport,
			PrxStatus:    "ONLINE",
			PrxHostgroup: "READ",
		}
		proxy.BackendsRead = append(proxy.BackendsRead, bkeread)
	}
	cluster.CheckMdbShardServersSchema(proxy)
	return nil
}

func (cluster *Cluster) ShardProxyGetShardClusters() map[string]*Cluster {
	shardCluster := make(map[string]*Cluster)
	for _, cl := range cluster.clusterList {
		if cl.Conf.MdbsProxyOn && (cl.Conf.ClusterHead == cluster.Name || cl.Name == cluster.Name) {
			shardCluster[cl.Name] = cl
		}
	}
	return shardCluster
}

func (cluster *Cluster) ShardProxyGetHeadCluster() *Cluster {

	for _, cl := range cluster.clusterList {
		if cl.Conf.MdbsProxyOn && cl.Conf.ClusterHead == "" {
			return cl
		}
	}
	return nil
}

func (cluster *Cluster) ShardProxyCreateVTable(proxy *MariadbShardProxy, schema string, table string, duplicates []*ServerMonitor, withreshard bool) error {
	if proxy.ShardProxy.Conn == nil {
		return errors.New("Shard Proxy not yet defined")
	}
	checksum64 := crc64.Checksum([]byte(schema+"_"+cluster.GetName()), cluster.crcTable)
	var err error
	var ddl string
	if len(duplicates) == 1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Creating federation table in MdbShardProxy %s", schema+"."+table)
		ddl, err = cluster.GetTableDLLNoFK(schema, table, cluster.master)
		cluster.CheckMdbShardServersSchema(proxy)
		query := "CREATE OR REPLACE TABLE " + schema + "." + ddl + " ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "\", srv \"RW" + strconv.FormatUint(checksum64, 10) + "\"'"
		err = cluster.RunQueryWithLog(proxy.ShardProxy, query)
		if err != nil {
			return err
		}
		if duplicates[0].ClusterGroup.Conf.ClusterHead != "" {
			duplicates[0].ClusterGroup.AddShardingQueryRules(schema, table)
		}
	} else if strings.Contains(cluster.Conf.MdbsUniversalTables, schema+"."+table) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Creating universal table in MdbShardProxy %s", schema+"."+table)
		ddl, err = cluster.GetTableDLLNoFK(schema, table, cluster.master)
		srv_def := " srv \""
		link_status_def := " link_status \""
		for _, cl := range cluster.ShardProxyGetShardClusters() {
			cl.CheckMdbShardServersSchema(proxy)
			checksum64 := crc64.Checksum([]byte(schema+"_"+cl.GetName()), cluster.crcTable)
			srv_def = srv_def + "RW" + strconv.FormatUint(checksum64, 10) + " "
			link_status_def = link_status_def + "0 "
		}
		srv_def = srv_def + "\" "
		link_status_def = link_status_def + "\" "
		query := "CREATE OR REPLACE TABLE " + schema + "." + ddl + " ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "\",  mbk \"2\", mkd \"2\", msi \"" + proxy.ShardProxy.Variables["SERVER_ID"] + "\", " + srv_def + ", " + link_status_def + "'"

		err = cluster.RunQueryWithLog(proxy.ShardProxy, query)
		if err != nil {
			return err
		}
		duplicates[0].ClusterGroup.AddShardingQueryRules(schema, table)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Creating split table in MdbShardProxy %s", schema+"."+table)
		query := "SELECT  column_name,(select COLUMN_TYPE from information_schema.columns C where C.TABLE_NAME=TABLE_NAME AND C.COLUMN_NAME=COLUMN_NAME AND C.TABLE_SCHEMA=TABLE_SCHEMA LIMIT 1) as TYPE   from information_schema.KEY_COLUMN_USAGE WHERE CONSTRAINT_NAME='PRIMARY' AND CONSTRAINT_SCHEMA='" + schema + "' AND (TABLE_NAME='" + table + "' OR  TABLE_NAME='" + table + "_reshard') AND ORDINAL_POSITION=1"
		var pk, ftype string
		err := duplicates[0].Conn.QueryRowx(query).Scan(&pk, &ftype)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed Founding hash key %s %s", query, err)
			return err
		}
		hashFunc := "HASH"
		if strings.Contains(strings.ToLower(ftype), "char") {
			hashFunc = "KEY"
		}
		ddl, err = cluster.GetTableDLLNoFK(schema, table, cluster.master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlWarn, "Failed query %s %s", query, err)
			ddl, err = cluster.GetTableDLLNoFK(schema, table+"_reshard`", cluster.master)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
				return err
			}
			ddl = strings.Replace(ddl, table+"_reshard", table, 1)
		}

		query = "CREATE OR REPLACE TABLE `" + schema + "`." + ddl + " ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "\"' PARTITION BY " + hashFunc + " (" + pk + ") (\n"
		i := 1
		clusterList := cluster.ShardProxyGetShardClusters()
		for _, cl := range clusterList {
			cl.CheckMdbShardServersSchema(proxy)
			checksum64 := crc64.Checksum([]byte(schema+"_"+cl.GetName()), cluster.crcTable)
			query = query + " PARTITION pt" + strconv.Itoa(i) + " COMMENT ='srv \"RW" + strconv.FormatUint(checksum64, 10) + "\", tbl \"" + table + "\", database \"" + schema + "\"'"
			if i != len(clusterList) {
				query = query + ",\n"
			}
			i++
			if cl.Conf.ClusterHead == "" {
				cl.AddShardingQueryRules(schema, table)
			}
		}

		query = query + "\n)"
		err = cluster.RunQueryWithLog(proxy.ShardProxy, "CREATE DATABASE IF NOT EXISTS "+schema)
		if err != nil {
			return err
		}
		err = cluster.RunQueryWithLog(proxy.ShardProxy, query)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) ShardSetUniversalTable(proxy *MariadbShardProxy, schema string, table string) error {
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("Universal table no valid master on current cluster")
	}
	var duplicates []*ServerMonitor
	for _, cl := range cluster.ShardProxyGetShardClusters() {
		destmaster := cl.GetMaster()
		if destmaster == nil {
			return errors.New("Universal table no valid master on dest cluster")
		}
		ddl, err := cluster.GetTableDLLNoFK(schema, table, master)
		if err != nil {
			return err
		}
		pos := strings.Index(ddl, "(")
		query := "CREATE OR REPLACE TABLE " + schema + "." + table + "_copy " + ddl[pos:len(ddl)]

		err = cluster.RunQueryWithLog(destmaster, "CREATE DATABASE IF NOT EXISTS "+schema)
		if err != nil {
			return err
		}
		err = cluster.RunQueryWithLog(destmaster, query)
		if err != nil {
			return err
		}

		duplicates = append(duplicates, destmaster)
	}
	cluster.Conf.MdbsUniversalTables = cluster.Conf.MdbsUniversalTables + "," + schema + "." + table + "_copy"
	cluster.Conf.MdbsUniversalTables = cluster.Conf.MdbsUniversalTables + "," + schema + "." + table

	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*MariadbShardProxy); ok {
			err := cluster.ShardProxyCreateVTable(pr, schema, table+"_copy", duplicates, false)
			if err != nil {
				return err
			}

			query := "CREATE OR REPLACE SERVER local_" + schema + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(pr.Host) + "', DATABASE '" + schema + "', USER '" + pr.User + "', PASSWORD '" + pr.Pass + "', PORT " + pr.Port + ")"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "CREATE OR REPLACE VIEW "+schema+"."+table+"_old AS SELECT * FROM "+schema+"."+table)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "RENAME TABLE "+schema+"."+table+"_old TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO "+schema+"."+table+"_old")
			if err != nil {
				return err
			}
			query = "CREATE OR REPLACE TABLE " + schema + "." + table + "_rpl ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "_old " + table + "_copy\", srv \"local_" + schema + " local_" + schema + "\", link_status \"0 1\"'"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "DROP VIEW "+schema+"."+table+"_old ")
			if err != nil {
				return err
			}
			query = "RENAME TABLE " + schema + "." + table + " TO " + schema + "." + table + "_old, " + schema + "." + table + "_rpl TO  " + schema + "." + table
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			query = "SELECT spider_copy_tables('" + schema + "." + table + "','0','1')"
			pr.ShardProxy.Conn.SetConnMaxLifetime(3595 * time.Second)
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			cluster.ShardProxyCreateVTable(pr, schema, table, duplicates, false)
			for _, cl := range cluster.ShardProxyGetShardClusters() {
				destmaster := cl.GetMaster()
				if destmaster == nil {
					return errors.New("Universal table, no valid master on dest cluster")
				}
				err = cluster.RunQueryWithLog(destmaster, "DROP TABLE IF EXISTS "+schema+"."+table)
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(destmaster, "CREATE OR REPLACE VIEW "+schema+"."+table+"  AS SELECT * FROM "+schema+"."+table+"_copy")
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(destmaster, "RENAME TABLE "+schema+"."+table+" TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO  "+schema+"."+table)
				if err != nil {
					return err
				}

				err = cluster.RunQueryWithLog(destmaster, "RENAME TABLE  "+schema+"."+table+" TO "+schema+"."+table+"_old , "+schema+"."+table+"_copy TO "+schema+"."+table)
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(destmaster, "DROP VIEW  "+schema+"."+table+"_old")
				if err != nil {
					return err
				}
			}
			query = "DROP TABLE " + schema + "." + table + "_copy,  " + schema + "." + table + "_old"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			//work can be done to a single proxy
			return nil
		}
	}
	return nil
}

func (cluster *Cluster) ShardProxyMoveTable(proxy *MariadbShardProxy, schema string, table string, destCluster *Cluster) error {
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("Move table no valid master on current cluster")
	}
	destmaster := destCluster.GetMaster()
	if destmaster == nil {
		return errors.New("Move table no valid master on dest cluster")
	}
	ddl, err := cluster.GetTableDLLNoFK(schema, table, master)
	if err != nil {
		return err
	}
	pos := strings.Index(ddl, "(")
	query := "CREATE OR REPLACE TABLE " + schema + "." + table + "_copy " + ddl[pos:len(ddl)]

	err = cluster.RunQueryWithLog(destmaster, "CREATE DATABASE IF NOT EXISTS "+schema)
	if err != nil {
		return err
	}
	err = cluster.RunQueryWithLog(destmaster, query)
	if err != nil {
		return err
	}
	var duplicates []*ServerMonitor
	duplicates = append(duplicates, destmaster)

	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*MariadbShardProxy); ok {
			err := destCluster.ShardProxyCreateVTable(pr, schema, table+"_copy", duplicates, false)
			if err != nil {
				return err
			}

			query := "CREATE OR REPLACE SERVER local_" + schema + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '" + misc.Unbracket(pr.Host) + "', DATABASE '" + schema + "', USER '" + pr.User + "', PASSWORD '" + pr.Pass + "', PORT " + pr.Port + ")"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "CREATE OR REPLACE VIEW "+schema+"."+table+"_old AS SELECT * FROM "+schema+"."+table)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "RENAME TABLE "+schema+"."+table+"_old TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO "+schema+"."+table+"_old")
			if err != nil {
				return err
			}
			query = "CREATE OR REPLACE TABLE " + schema + "." + table + "_rpl ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "_old " + table + "_copy\", srv \"local_" + schema + " local_" + schema + "\", link_status \"0 1\"'"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "DROP VIEW "+schema+"."+table+"_old ")
			if err != nil {
				return err
			}
			query = "RENAME TABLE " + schema + "." + table + " TO " + schema + "." + table + "_old, " + schema + "." + table + "_rpl TO  " + schema + "." + table
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			query = "SELECT spider_copy_tables('" + schema + "." + table + "','0','1')"
			pr.ShardProxy.Conn.SetConnMaxLifetime(3595 * time.Second)
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(destmaster, "DROP TABLE IF EXISTS "+schema+"."+table)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(destmaster, "CREATE OR REPLACE VIEW "+schema+"."+table+"  AS SELECT * FROM "+schema+"."+table+"_copy")
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(destmaster, "RENAME TABLE "+schema+"."+table+" TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO  "+schema+"."+table)
			if err != nil {
				return err
			}

			err = cluster.ShardProxyCreateVTable(pr, schema, table, duplicates, false)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(destmaster, "RENAME TABLE  "+schema+"."+table+" TO "+schema+"."+table+"_old , "+schema+"."+table+"_copy TO "+schema+"."+table)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(destmaster, "DROP VIEW  "+schema+"."+table+"_old")
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(master, "DROP TABLE IF EXISTS "+schema+"."+table)
			if err != nil {
				return err
			}
			query = "DROP TABLE " + schema + "." + table + "_copy,  " + schema + "." + table + "_old"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			//work can be done to a single proxy
			return nil
		}
	}
	return nil
}

func (cluster *Cluster) ShardProxyReshardTable(proxy *MariadbShardProxy, schema string, table string, clusters map[string]*Cluster) error {

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("Reshard no valid master on current cluster")
	}
	ddl, err := cluster.GetTableDLLNoFK(schema, table, master)
	if err != nil {
		return errors.New("Reshard error getting table definition")
	}
	pos := strings.Index(ddl, "(")
	query := "CREATE OR REPLACE TABLE " + schema + "." + table + "_reshard " + ddl[pos:len(ddl)]
	var duplicates []*ServerMonitor
	for _, cl := range clusters {
		master := cl.GetMaster()
		if master != nil {
			err := cluster.RunQueryWithLog(master, "CREATE DATABASE IF NOT EXISTS "+schema)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(master, "CREATE DATABASE IF NOT EXISTS replication_manager_schema")
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(master, query)
			if err != nil {
				return err
			}
			duplicates = append(duplicates, master)
		}
	}

	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*MariadbShardProxy); ok {
			err := cluster.ShardProxyCreateVTable(pr, schema, table+"_reshard", duplicates, false)
			if err != nil {
				return err
			}
			query := "CREATE OR REPLACE SERVER local_" + schema + " FOREIGN DATA WRAPPER mysql OPTIONS (HOST '127.0.0.1', DATABASE '" + schema + "', USER '" + pr.User + "', PASSWORD '" + pr.Pass + "', PORT " + pr.Port + ")"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "CREATE OR REPLACE VIEW "+schema+"."+table+"_old AS SELECT * FROM "+schema+"."+table)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "RENAME TABLE "+schema+"."+table+"_old TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO "+schema+"."+table+"_old")
			if err != nil {
				return err
			}
			query = "CREATE OR REPLACE TABLE " + schema + "." + table + "_rpl ENGINE=spider comment='wrapper \"mysql\", table \"" + table + "_old " + table + "_reshard\", srv \"local_" + schema + " local_" + schema + "\", link_status \"0 1\"'"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			err = cluster.RunQueryWithLog(pr.ShardProxy, "DROP VIEW "+schema+"."+table+"_old ")
			if err != nil {
				return err
			}
			query = "RENAME TABLE " + schema + "." + table + " TO " + schema + "." + table + "_old, " + schema + "." + table + "_rpl TO  " + schema + "." + table
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			//	query = "SELECT *  from " + schema + "." + table + " limit 1"
			//	cluster.RunQueryWithLog(pr.ShardProxy, query)

			ct := 0
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Online data copy...")
			myconn, err := pr.ShardProxy.GetNewDBConn()
			if err != nil {
				return err
			}
			defer myconn.Close()
			query = "SELECT spider_copy_tables('" + schema + "." + table + "','0','1') as res from dual "
			//	var ctx context.Context
			var res int32
			for {
				//		pr.ShardProxy.Conn.SetConnMaxLifetime(3595 * time.Second)
				err := myconn.QueryRow(query).Scan(&res)
				//	err = cluster.RunQueryWithLog(pr.ShardProxy, query)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "copy error...", err)
					if ct == 2 {
						return err
					}
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Copy Result... %d", res)
					break
				}
				ct++
			}

			duplicates = nil
			for _, cl := range clusters {
				master := cl.GetMaster()

				err := cluster.RunQueryWithLog(master, "DROP TABLE IF EXISTS "+schema+"."+table)
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(master, "CREATE OR REPLACE VIEW "+schema+"."+table+"  AS SELECT * FROM "+schema+"."+table+"_reshard")
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(master, "RENAME TABLE "+schema+"."+table+" TO "+schema+"."+table+"_back, "+schema+"."+table+"_back TO  "+schema+"."+table)
				if err != nil {
					return err
				}

				if cl.GetName() != cluster.GetName() {
					duplicates = append(duplicates, cl.GetMaster())
				}
			}
			cluster.ShardProxyCreateVTable(pr, schema, table, duplicates, false)
			for _, cl := range clusters {

				master := cl.GetMaster()
				err := cluster.RunQueryWithLog(master, "RENAME TABLE  "+schema+"."+table+" TO "+schema+"."+table+"_old , "+schema+"."+table+"_reshard TO "+schema+"."+table)
				if err != nil {
					return err
				}
				err = cluster.RunQueryWithLog(master, "DROP VIEW  "+schema+"."+table+"_old")
				if err != nil {
					return err
				}
			}
			query = "DROP TABLE " + schema + "." + table + "_reshard,  " + schema + "." + table + "_old"
			err = cluster.RunQueryWithLog(pr.ShardProxy, query)
			if err != nil {
				return err
			}
			return nil
		}

	}
	return nil
}

func (cluster *Cluster) RunQueryWithLog(server *ServerMonitor, query string) error {

	_, err := server.Conn.Exec(query)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Sharding Proxy %s %s %s", server.URL, query, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Sharding Proxy %s %s", server.URL, query)
	return nil
}

func (cluster *Cluster) ShardProxyBootstrap(proxy *MariadbShardProxy) error {

	var err error
	if proxy.ShardProxy != nil {
		return nil
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go proxy.ShardProxy.Ping(wg)
	wg.Wait()

	return err
}

func (cluster *Cluster) ShardProxyCreateSystemTable(proxy *MariadbShardProxy) error {

	params := "?timeout=60s"

	dsn := proxy.User + ":" + proxy.Pass + "@"
	dsn += "tcp(" + proxy.Host + ":" + proxy.Port + ")/" + params
	c, err := sqlx.Open("mysql", dsn)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Could initialize MariaDB Sharding Proxy %s", err)
		return err
	}

	if c != nil {
		var ct int
		query := "select count(*) from mysql.proc where name='spider_fix_system_tables'"
		c.QueryRowx(query).Scan(&ct)
		if ct > 0 {
			return nil
		}

		sql := "REPLACE INTO mysql.proc(db,name,type,specific_name,language,sql_data_access,is_deterministic,security_type,param_list,returns,body,definer,created,modified,sql_mode,comment,character_set_client,collation_connection,db_collation,body_utf8) VALUES ('mysql','spider_plugin_installer','PROCEDURE','spider_plugin_installer','SQL','CONTAINS_SQL','NO','DEFINER','','',0x626567696E0A2020736574204077696E5F706C7567696E203A3D20494628404076657273696F6E5F636F6D70696C655F6F73206C696B65202757696E25272C20312C2030293B0A20207365742040686176655F7370696465725F706C7567696E203A3D20303B0A202073656C6563742040686176655F7370696465725F706C7567696E203A3D20312066726F6D20494E464F524D4154494F4E5F534348454D412E706C7567696E7320776865726520504C5547494E5F4E414D45203D2027535049444552273B0A202069662040686176655F7370696465725F706C7567696E203D2030207468656E200A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020696E7374616C6C20706C7567696E2073706964657220736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020696E7374616C6C20706C7567696E2073706964657220736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203A3D20303B0A202073656C6563742040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203A3D20312066726F6D20494E464F524D4154494F4E5F534348454D412E706C7567696E7320776865726520504C5547494E5F4E414D45203D20275350494445525F414C4C4F435F4D454D273B0A202069662040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203D2030207468656E200A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020696E7374616C6C20706C7567696E207370696465725F616C6C6F635F6D656D20736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020696E7374616C6C20706C7567696E207370696465725F616C6C6F635F6D656D20736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F6469726563745F73716C5F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F6469726563745F73716C5F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F6469726563745F73716C273B0A202069662040686176655F7370696465725F6469726563745F73716C5F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F62675F6469726563745F73716C5F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F62675F6469726563745F73716C5F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F62675F6469726563745F73716C273B0A202069662040686176655F7370696465725F62675F6469726563745F73716C5F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020637265617465206167677265676174652066756E6374696F6E207370696465725F62675F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020637265617465206167677265676174652066756E6374696F6E207370696465725F62675F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F70696E675F7461626C655F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F70696E675F7461626C655F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F70696E675F7461626C65273B0A202069662040686176655F7370696465725F70696E675F7461626C655F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F70696E675F7461626C652072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F70696E675F7461626C652072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F636F70795F7461626C65735F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F636F70795F7461626C65735F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F636F70795F7461626C6573273B0A202069662040686176655F7370696465725F636F70795F7461626C65735F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F636F70795F7461626C65732072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F636F70795F7461626C65732072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A0A20207365742040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F666C7573685F7461626C655F6D6F6E5F6361636865273B0A202069662040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F666C7573685F7461626C655F6D6F6E5F63616368652072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F666C7573685F7461626C655F6D6F6E5F63616368652072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A0A656E64,'root@127.0.0.1','2017-04-30 17:21:26','2017-04-30 17:21:26','STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION','','utf8','utf8_general_ci','latin1_swedish_ci',0x626567696E0A2020736574204077696E5F706C7567696E203A3D20494628404076657273696F6E5F636F6D70696C655F6F73206C696B65202757696E25272C20312C2030293B0A20207365742040686176655F7370696465725F706C7567696E203A3D20303B0A202073656C6563742040686176655F7370696465725F706C7567696E203A3D20312066726F6D20494E464F524D4154494F4E5F534348454D412E706C7567696E7320776865726520504C5547494E5F4E414D45203D2027535049444552273B0A202069662040686176655F7370696465725F706C7567696E203D2030207468656E200A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020696E7374616C6C20706C7567696E2073706964657220736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020696E7374616C6C20706C7567696E2073706964657220736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203A3D20303B0A202073656C6563742040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203A3D20312066726F6D20494E464F524D4154494F4E5F534348454D412E706C7567696E7320776865726520504C5547494E5F4E414D45203D20275350494445525F414C4C4F435F4D454D273B0A202069662040686176655F7370696465725F695F735F616C6C6F635F6D656D5F706C7567696E203D2030207468656E200A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020696E7374616C6C20706C7567696E207370696465725F616C6C6F635F6D656D20736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020696E7374616C6C20706C7567696E207370696465725F616C6C6F635F6D656D20736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F6469726563745F73716C5F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F6469726563745F73716C5F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F6469726563745F73716C273B0A202069662040686176655F7370696465725F6469726563745F73716C5F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F62675F6469726563745F73716C5F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F62675F6469726563745F73716C5F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F62675F6469726563745F73716C273B0A202069662040686176655F7370696465725F62675F6469726563745F73716C5F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A202020202020637265617465206167677265676174652066756E6374696F6E207370696465725F62675F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A202020202020637265617465206167677265676174652066756E6374696F6E207370696465725F62675F6469726563745F73716C2072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F70696E675F7461626C655F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F70696E675F7461626C655F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F70696E675F7461626C65273B0A202069662040686176655F7370696465725F70696E675F7461626C655F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F70696E675F7461626C652072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F70696E675F7461626C652072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A20207365742040686176655F7370696465725F636F70795F7461626C65735F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F636F70795F7461626C65735F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F636F70795F7461626C6573273B0A202069662040686176655F7370696465725F636F70795F7461626C65735F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F636F70795F7461626C65732072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F636F70795F7461626C65732072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A0A20207365742040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203A3D20303B0A202073656C6563742040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203A3D20312066726F6D206D7973716C2E66756E63207768657265206E616D65203D20277370696465725F666C7573685F7461626C655F6D6F6E5F6361636865273B0A202069662040686176655F7370696465725F666C7573685F7461626C655F6D6F6E5F63616368655F756466203D2030207468656E0A202020206966204077696E5F706C7567696E203D2030207468656E200A2020202020206372656174652066756E6374696F6E207370696465725F666C7573685F7461626C655F6D6F6E5F63616368652072657475726E7320696E7420736F6E616D65202768615F7370696465722E736F273B0A20202020656C73650A2020202020206372656174652066756E6374696F6E207370696465725F666C7573685F7461626C655F6D6F6E5F63616368652072657475726E7320696E7420736F6E616D65202768615F7370696465722E646C6C273B0A20202020656E642069663B0A2020656E642069663B0A0A656E64),('mysql','spider_fix_one_table','PROCEDURE','spider_fix_one_table','SQL','CONTAINS_SQL','NO','DEFINER',0x7461625F6E616D65206368617228323535292C20746573745F636F6C5F6E616D65206368617228323535292C205F73716C2074657874,'',0x626567696E0A20207365742040636F6C5F657869737473203A3D20303B0A202073656C656374203120696E746F2040636F6C5F6578697374732066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D207461625F6E616D650A202020202020414E4420434F4C554D4E5F4E414D45203D20746573745F636F6C5F6E616D653B0A202069662040636F6C5F657869737473203D2030207468656E0A2020202073656C656374204073746D74203A3D205F73716C3B0A20202020707265706172652073705F73746D74312066726F6D204073746D743B0A20202020657865637574652073705F73746D74313B0A2020656E642069663B0A656E64,'root@127.0.0.1','2017-04-30 17:21:26','2017-04-30 17:21:26','STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION','','utf8','utf8_general_ci','latin1_swedish_ci',0x626567696E0A20207365742040636F6C5F657869737473203A3D20303B0A202073656C656374203120696E746F2040636F6C5F6578697374732066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D207461625F6E616D650A202020202020414E4420434F4C554D4E5F4E414D45203D20746573745F636F6C5F6E616D653B0A202069662040636F6C5F657869737473203D2030207468656E0A2020202073656C656374204073746D74203A3D205F73716C3B0A20202020707265706172652073705F73746D74312066726F6D204073746D743B0A20202020657865637574652073705F73746D74313B0A2020656E642069663B0A656E64),('mysql','spider_fix_system_tables','PROCEDURE','spider_fix_system_tables','SQL','CONTAINS_SQL','NO','DEFINER','','',0x626567696E0A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C2027736572766572272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A20202020616464207365727665722063686172283634292064656661756C74206E756C6C2C0A2020202061646420736368656D652063686172283634292064656661756C74206E756C6C2C0A2020202061646420686F73742063686172283634292064656661756C74206E756C6C2C0A2020202061646420706F727420636861722835292064656661756C74206E756C6C2C0A2020202061646420736F636B65742063686172283634292064656661756C74206E756C6C2C0A2020202061646420757365726E616D652063686172283634292064656661756C74206E756C6C2C0A202020206164642070617373776F72642063686172283634292064656661756C74206E756C6C2C0A20202020616464207467745F64625F6E616D652063686172283634292064656661756C74206E756C6C2C0A20202020616464207467745F7461626C655F6E616D652063686172283634292064656661756C74206E756C6C27293B0A20200A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F7861270A202020202020414E4420434F4C554D4E5F4E414D45203D202764617461273B0A202069662040636F6C5F7479706520213D202762696E617279283132382927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F7861206D6F6469667920646174612062696E6172792831323829206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F78615F6D656D626572270A202020202020414E4420434F4C554D4E5F4E414D45203D202764617461273B0A202069662040636F6C5F7479706520213D202762696E617279283132382927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D626572206D6F6469667920646174612062696E6172792831323829206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C20276C696E6B5F6964272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A2020202061646420636F6C756D6E206C696E6B5F696420696E74206E6F74206E756C6C2064656661756C742030206166746572207461626C655F6E616D652C0A2020202064726F70207072696D617279206B65792C0A20202020616464207072696D617279206B6579202864625F6E616D652C207461626C655F6E616D652C206C696E6B5F69642927293B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C20276C696E6B5F737461747573272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A2020202061646420636F6C756D6E206C696E6B5F7374617475732074696E79696E74206E6F74206E756C6C2064656661756C74203127293B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F78615F6D656D626572272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D6265720A2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C0A2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C0A2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C0A2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C0A2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C0A2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C0A2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C0A2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C0A2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C0A2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C0A2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C0A2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C0A2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C0A2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C0A2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F6C696E6B5F6D6F6E5F73657276657273272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C0A2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C0A2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C0A2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C0A2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C0A2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C0A2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C0A2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D20276C696E6B5F6964273B0A202069662040636F6C5F7479706520213D20276368617228352927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A202020206D6F64696679206C696E6B5F69642063686172283529206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736964273B0A202069662040636F6C5F7479706520213D2027696E742831302920756E7369676E656427207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A202020206D6F646966792073696420696E7420756E7369676E6564206E6F74206E756C6C2064656661756C7420303B0A2020656E642069663B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F78615F6D656D626572270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D6265720A20202020202064726F70207072696D617279206B65792C0A20202020202061646420696E64657820696478312028646174612C20666F726D61745F69642C2067747269645F6C656E6774682C20686F7374292C0A2020202020206D6F6469667920736F636B65742074657874206E6F74206E756C6C2C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F7461626C6573270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A2020202020206D6F6469667920736F636B657420746578742C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A2020202020206D6F6469667920736F636B657420746578742C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A656E64,'root@127.0.0.1','2017-04-30 17:21:26','2017-04-30 17:21:26','STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION','','utf8','utf8_general_ci','latin1_swedish_ci',0x626567696E0A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C2027736572766572272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65735C6E20202020616464207365727665722063686172283634292064656661756C74206E756C6C2C5C6E2020202061646420736368656D652063686172283634292064656661756C74206E756C6C2C5C6E2020202061646420686F73742063686172283634292064656661756C74206E756C6C2C5C6E2020202061646420706F727420636861722835292064656661756C74206E756C6C2C5C6E2020202061646420736F636B65742063686172283634292064656661756C74206E756C6C2C5C6E2020202061646420757365726E616D652063686172283634292064656661756C74206E756C6C2C5C6E202020206164642070617373776F72642063686172283634292064656661756C74206E756C6C2C5C6E20202020616464207467745F64625F6E616D652063686172283634292064656661756C74206E756C6C2C5C6E20202020616464207467745F7461626C655F6E616D652063686172283634292064656661756C74206E756C6C27293B0A20200A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F7861270A202020202020414E4420434F4C554D4E5F4E414D45203D202764617461273B0A202069662040636F6C5F7479706520213D202762696E617279283132382927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F7861206D6F6469667920646174612062696E6172792831323829206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F78615F6D656D626572270A202020202020414E4420434F4C554D4E5F4E414D45203D202764617461273B0A202069662040636F6C5F7479706520213D202762696E617279283132382927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D626572206D6F6469667920646174612062696E6172792831323829206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C20276C696E6B5F6964272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65735C6E2020202061646420636F6C756D6E206C696E6B5F696420696E74206E6F74206E756C6C2064656661756C742030206166746572207461626C655F6E616D652C5C6E2020202064726F70207072696D617279206B65792C5C6E20202020616464207072696D617279206B6579202864625F6E616D652C207461626C655F6E616D652C206C696E6B5F69642927293B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C20276C696E6B5F737461747573272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65735C6E2020202061646420636F6C756D6E206C696E6B5F7374617475732074696E79696E74206E6F74206E756C6C2064656661756C74203127293B0A20200A20200A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F78615F6D656D626572272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D6265725C6E2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C5C6E2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C5C6E2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C5C6E2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C5C6E2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C5C6E2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C5C6E2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C5C6E2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F7461626C6573272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F7461626C65735C6E2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C5C6E2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C5C6E2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C5C6E2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C5C6E2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C5C6E2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C5C6E2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C5C6E2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A202063616C6C206D7973716C2E7370696465725F6669785F6F6E655F7461626C6528277370696465725F6C696E6B5F6D6F6E5F73657276657273272C202773736C5F6361272C0A20202027616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572735C6E2020202061646420636F6C756D6E2073736C5F63612063686172283634292064656661756C74206E756C6C2061667465722070617373776F72642C5C6E2020202061646420636F6C756D6E2073736C5F6361706174682063686172283634292064656661756C74206E756C6C2061667465722073736C5F63612C5C6E2020202061646420636F6C756D6E2073736C5F636572742063686172283634292064656661756C74206E756C6C2061667465722073736C5F6361706174682C5C6E2020202061646420636F6C756D6E2073736C5F6369706865722063686172283634292064656661756C74206E756C6C2061667465722073736C5F636572742C5C6E2020202061646420636F6C756D6E2073736C5F6B65792063686172283634292064656661756C74206E756C6C2061667465722073736C5F6369706865722C5C6E2020202061646420636F6C756D6E2073736C5F7665726966795F7365727665725F636572742074696E79696E74206E6F74206E756C6C2064656661756C7420302061667465722073736C5F6B65792C5C6E2020202061646420636F6C756D6E2064656661756C745F66696C652063686172283634292064656661756C74206E756C6C2061667465722073736C5F7665726966795F7365727665725F636572742C5C6E2020202061646420636F6C756D6E2064656661756C745F67726F75702063686172283634292064656661756C74206E756C6C2061667465722064656661756C745F66696C6527293B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D20276C696E6B5F6964273B0A202069662040636F6C5F7479706520213D20276368617228352927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A202020206D6F64696679206C696E6B5F69642063686172283529206E6F74206E756C6C2064656661756C742027273B0A2020656E642069663B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736964273B0A202069662040636F6C5F7479706520213D2027696E742831302920756E7369676E656427207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A202020206D6F646966792073696420696E7420756E7369676E6564206E6F74206E756C6C2064656661756C7420303B0A2020656E642069663B0A0A20200A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F78615F6D656D626572270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F78615F6D656D6265720A20202020202064726F70207072696D617279206B65792C0A20202020202061646420696E64657820696478312028646174612C20666F726D61745F69642C2067747269645F6C656E6774682C20686F7374292C0A2020202020206D6F6469667920736F636B65742074657874206E6F74206E756C6C2C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F7461626C6573270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F7461626C65730A2020202020206D6F6469667920736F636B657420746578742C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A202073656C65637420434F4C554D4E5F5459504520494E544F2040636F6C5F747970652066726F6D20494E464F524D4154494F4E5F534348454D412E434F4C554D4E530A202020207768657265205441424C455F534348454D41203D20276D7973716C270A202020202020414E44205441424C455F4E414D45203D20277370696465725F6C696E6B5F6D6F6E5F73657276657273270A202020202020414E4420434F4C554D4E5F4E414D45203D2027736F636B6574273B0A202069662040636F6C5F74797065203D2027636861722836342927207468656E0A20202020616C746572207461626C65206D7973716C2E7370696465725F6C696E6B5F6D6F6E5F736572766572730A2020202020206D6F6469667920736F636B657420746578742C0A2020202020206D6F646966792073736C5F636120746578742C0A2020202020206D6F646966792073736C5F63617061746820746578742C0A2020202020206D6F646966792073736C5F6365727420746578742C0A2020202020206D6F646966792073736C5F6B657920746578742C0A2020202020206D6F646966792064656661756C745F66696C6520746578743B0A2020656E642069663B0A656E64)"
		_, err = c.Exec(sql)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s", err)
			return err
		}

		query = "FLUSH TABLES"
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		query = "CALL mysql.spider_plugin_installer()"
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		var sv map[string]string
		sv, _, err = dbhelper.GetVariables(c, proxy.ShardProxy.DBVersion)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Spider release is %s", sv["SPIDER_VERSION"])

		query = `create table if not exists mysql.spider_xa(
    format_id int not null default 0,
    gtrid_length int not null default 0,
    bqual_length int not null default 0,
    data char(128) charset binary not null default '',
    status char(8) not null default '',
    primary key (data, format_id, gtrid_length),
    key idx1 (status)
  ) engine=MyISAM default charset=utf8 collate=utf8_bin`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
		}
		query = ` create table if not exists mysql.spider_xa_member(
    format_id int not null default 0,
    gtrid_length int not null default 0,
    bqual_length int not null default 0,
    data char(128) charset binary not null default '',
    scheme char(64) not null default '',
    host char(64) not null default '',
    port char(5) not null default '',
    socket text not null,
    username char(64) not null default '',
    password char(64) not null default '',
    ssl_ca text,
    ssl_capath text,
    ssl_cert text,
    ssl_cipher char(64) default null,
    ssl_key text,
    ssl_verify_server_cert tinyint not null default 0,
    default_file text,
    default_group char(64) default null,
    key idx1 (data, format_id, gtrid_length, host)
    ) engine=MyISAM default charset=utf8 collate=utf8_bin`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}
		query = `create table if not exists mysql.spider_xa_failed_log(
      format_id int not null default 0,
      gtrid_length int not null default 0,
      bqual_length int not null default 0,
      data char(128) charset binary not null default '',
      scheme char(64) not null default '',
      host char(64) not null default '',
      port char(5) not null default '',
      socket text not null,
      username char(64) not null default '',
      password char(64) not null default '',
      ssl_ca text,
      ssl_capath text,
      ssl_cert text,
      ssl_cipher char(64) default null,
      ssl_key text,
      ssl_verify_server_cert tinyint not null default 0,
      default_file text,
      default_group char(64) default null,
      thread_id int default null,
      status char(8) not null default '',
      failed_time timestamp not null default current_timestamp,
      key idx1 (data, format_id, gtrid_length, host)
    ) engine=MyISAM default charset=utf8 collate=utf8_bin`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		query = `create table if not exists mysql.spider_tables(
      db_name char(64) not null default '',
      table_name char(64) not null default '',
      link_id int not null default 0,
      priority bigint not null default 0,
      server char(64) default null,
      scheme char(64) default null,
      host char(64) default null,
      port char(5) default null,
      socket text,
      username char(64) default null,
      password char(64) default null,
      ssl_ca text,
      ssl_capath text,
      ssl_cert text,
      ssl_cipher char(64) default null,
      ssl_key text,
      ssl_verify_server_cert tinyint not null default 0,
      default_file text,
      default_group char(64) default null,
      tgt_db_name char(64) default null,
      tgt_table_name char(64) default null,
      link_status tinyint not null default 1,
		  primary key (db_name, table_name, link_id),
      key idx1 (priority)
    ) engine=MyISAM default charset=utf8 collate=utf8_bin`

		query = `	create table if not exists mysql.spider_tables(
	db_name char(64) not null default '',
	table_name char(199) not null default '',
	link_id int not null default 0,
	priority bigint not null default 0,
	server char(64) default null,
	scheme char(64) default null,
	host char(64) default null,
	port char(5) default null,
	socket text,
	username char(64) default null,
	password char(64) default null,
	ssl_ca text,
	ssl_capath text,
	ssl_cert text,
	ssl_cipher char(64) default null,
	ssl_key text,
	ssl_verify_server_cert tinyint not null default 0,
	monitoring_binlog_pos_at_failing tinyint not null default 0,
	default_file text,
	default_group char(64) default null,
	tgt_db_name char(64) default null,
	tgt_table_name char(64) default null,
	link_status tinyint not null default 1,
	block_status tinyint not null default 0,
	static_link_id char(64) default null,
	primary key (db_name, table_name, link_id),
	key idx1 (priority),
	unique key uidx1 (db_name, table_name, static_link_id)
) engine=MyISAM default charset=utf8 collate=utf8_bin;
`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}
		query = `create table if not exists mysql.spider_link_mon_servers(
      db_name char(64) not null default '',
      table_name char(64) not null default '',
      link_id char(5) not null default '',
      sid int unsigned not null default 0,
      server char(64) default null,
      scheme char(64) default null,
      host char(64) default null,
      port char(5) default null,
      socket text,
      username char(64) default null,
      password char(64) default null,
      ssl_ca text,
      ssl_capath text,
      ssl_cert text,
      ssl_cipher char(64) default null,
      ssl_key text,
      ssl_verify_server_cert tinyint not null default 0,
      default_file text,
      default_group char(64) default null,
      primary key (db_name, table_name, link_id, sid)
    ) engine=MyISAM default charset=utf8 collate=utf8_bin`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}
		query = `create table if not exists mysql.spider_link_failed_log(
      db_name char(64) not null default '',
      table_name char(64) not null default '',
      link_id int not null default 0,
      failed_time timestamp not null default current_timestamp
    ) engine=MyISAM default charset=utf8 collate=utf8_bin`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		query = `		create table if not exists mysql.spider_table_sts(
  db_name char(64) not null default '',
  table_name char(199) not null default '',
  data_file_length bigint unsigned not null default 0,
  max_data_file_length bigint unsigned not null default 0,
  index_file_length bigint unsigned not null default 0,
  records bigint unsigned not null default 0,
  mean_rec_length bigint unsigned not null default 0,
  check_time datetime not null default '0000-00-00 00:00:00',
  create_time datetime not null default '0000-00-00 00:00:00',
  update_time datetime not null default '0000-00-00 00:00:00',
  primary key (db_name, table_name)
) engine=MyISAM default charset=utf8 collate=utf8_bin;
`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		query = `create table if not exists mysql.spider_table_crd(
  db_name char(64) not null default '',
  table_name char(199) not null default '',
  key_seq int unsigned not null default 0,
  cardinality bigint not null default 0,
  primary key (db_name, table_name, key_seq)
) engine=MyISAM default charset=utf8 collate=utf8_bin;
`
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

		/*	query = "truncate mysql.proc"
			_, err = c.Exec(query)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxy,LvlErr, "Failed query %s %s", query, err)
				return err
			}*/

		query = "call mysql.spider_fix_system_tables()"
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}
		query = "CALL mysql.spider_plugin_installer()"
		_, err = c.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Failed query %s %s", query, err)
			return err
		}

	}
	c.Close()
	return nil
}

func (cluster *Cluster) MdbsproxyCopyTable(oldmaster *ServerMonitor, newmaster *ServerMonitor, proxy *MariadbShardProxy) {

}
