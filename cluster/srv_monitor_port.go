// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// monitorExtraPortRetry: after the extra port failed, repman's connections go to the
// service port for this long before the extra port is tried again. One failed attempt
// per window, not one per tick: a filtered extra port would otherwise add a connect
// timeout to every tick, and delay the failure detection of a node that is down (#2150).
const monitorExtraPortRetry = time.Minute

// connectServicePort opens the fallback connection on the service port; a variable so the
// tests replace the server that answers there.
var connectServicePort = func(dsn string, seconds int) (*sqlx.DB, error) {
	return connectBounded("mysql", dsn, seconds)
}

// monitorExtraPortFallback holds the DSN of the service port, written by SetDSN, and the
// record that the extra port failed: until when the service port is used, and why, for
// WARN0267. Under its own mutex: SetDSN runs from the API while Ping connects (#2150).
type monitorExtraPortFallback struct {
	mu         sync.Mutex
	serviceDSN string
	until      time.Time
	reason     string
}

func (f *monitorExtraPortFallback) setServiceDSN(dsn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serviceDSN = dsn
}

func (f *monitorExtraPortFallback) getServiceDSN() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.serviceDSN
}

func (f *monitorExtraPortFallback) active(now time.Time) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return now.Before(f.until), f.reason
}

func (f *monitorExtraPortFallback) start(now time.Time, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.until = now.Add(monitorExtraPortRetry)
	f.reason = reason
}

// monitorExtraPort is the port repman's own SQL connections use: db-servers-monitor-extra-port
// (MariaDB extra_port) when it is set, else the service port of db-servers-hosts. Only repman's
// connection moves: proxies, replication (CHANGE MASTER) and the backup tools keep server.Port,
// the port applications use. Not for PostgreSQL, nor through an SSH tunnel (#2150).
func (server *ServerMonitor) monitorExtraPort() string {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.Conf.DbServersMonitorExtraPort <= 0 || cluster.Conf.TunnelHost != "" || server.IsPostgreSQLHost() {
		return server.Port
	}
	return strconv.Itoa(cluster.Conf.DbServersMonitorExtraPort)
}

// extraPortUnusable tells the errors after which the service port is tried: the extra
// port does not answer (refused, timeout: not enabled on that node) or is full (1040,
// extra_max_connections reached). Any other server error, an access denied first, would
// be the same on the service port.
func extraPortUnusable(err error) bool {
	if driverErr, ok := err.(*mysql.MySQLError); ok {
		return driverErr.Number == 1040
	}
	return true
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// getNewDBConnPort is GetNewDBConn returning the port the connection was opened on, so
// Ping moves the global pool when the monitoring port changes. With an extra port set it
// connects there, and on the service port when the extra port fails or failed less than
// monitorExtraPortRetry ago (#2150).
func (server *ServerMonitor) getNewDBConnPort() (*sqlx.DB, string, error) {
	// Split-brain simulator: a cut database link (whole cluster, or just the
	// master when the master is colocated on the isolated side) drives the
	// health checks to failed exactly like a real network loss.
	if server.ClusterGroup != nil {
		if server.ClusterGroup.IsDatabaseFailureSimulated() {
			return nil, "", errors.New("split-brain simulation: database link down")
		}
		if server.ClusterGroup.SimulatedOnFreezeDBAccess(server.URL) {
			return nil, "", errors.New("split-brain simulation: master link down")
		}
	}
	serviceDSN := server.extraPortFallback.getServiceDSN()
	if serviceDSN == "" {
		conn, err := server.connectMonitorDSN()
		return conn, server.monitorExtraPort(), err
	}
	extraPort := server.monitorExtraPort()
	now := time.Now()
	if inFallback, _ := server.extraPortFallback.active(now); !inFallback {
		conn, err := server.connectMonitorDSN()
		if err == nil || !extraPortUnusable(err) {
			return conn, extraPort, err
		}
		server.extraPortFallback.start(now, err.Error())
		// A node already suspect or failed whose extra port timed out is most likely
		// down: a second connect timeout on the service port would only delay this tick.
		// The next tick, inside the window, goes to the service port directly.
		if isTimeout(err) && server.IsDown() {
			return nil, extraPort, err
		}
	}
	conn, err := connectServicePort(serviceDSN, server.ClusterGroup.Conf.Timeout)
	if err != nil {
		return nil, server.Port, err
	}
	if cluster := server.ClusterGroup; cluster != nil {
		_, reason := server.extraPortFallback.active(now)
		cluster.SetState("WARN0267", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0267"], server.URL, extraPort, reason, server.Port), ErrFrom: "MON", ServerUrl: server.URL})
	}
	return conn, server.Port, nil
}

// SetMonitorExtraPort sets db-servers-monitor-extra-port and rebuilds the servers' DSN:
// the next Ping connects on the new port and moves the global pool to it (#2150).
func (cluster *Cluster) SetMonitorExtraPort(port int) {
	cluster.Conf.DbServersMonitorExtraPort = port
	for _, server := range cluster.Servers {
		if server != nil {
			server.SetDSN()
		}
	}
}

// moveGlobalPool makes conn the global pool when it was opened on another port than the
// current pool: the monitoring port changed (db-servers-monitor-extra-port set or cleared,
// its fallback to the service port started or ended). The old pool is closed after the
// grace for the queries still running on it. Reports whether conn became the pool (#2150).
func (server *ServerMonitor) moveGlobalPool(conn *sqlx.DB, port string) bool {
	if conn == nil || server.Conn == nil || port == "" || port == server.poolPort {
		return false
	}
	cluster := server.ClusterGroup
	if old := server.Conn; old != conn {
		go func() {
			time.Sleep(replacedPoolGrace)
			old.Close()
		}()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s monitoring connection moved from port %s to port %s", server.URL, server.poolPort, port)
	server.Conn = conn
	server.poolPort = port
	return true
}
