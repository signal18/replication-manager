// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
)

// freshNodeInstallWindowSec is how close to its start a node's system catalogue must
// have been installed for the node to count as installed with this start.
const freshNodeInstallWindowSec int64 = 15 * 60

// FreshNode is what was observed on a node to decide whether it may be restored
// automatically (#1979). Fresh is proven, never assumed: a read that fails leaves it
// false.
type FreshNode struct {
	Fresh         bool   `json:"fresh"`
	InstallAgeSec int64  `json:"installAgeSec"`
	StartAgeSec   int64  `json:"startAgeSec"`
	UserTables    bool   `json:"userTables"`
	Reason        string `json:"reason"`
}

// CheckFreshNode reads the node live (never from the ServerMonitor, rebuilt at every
// reload). Fresh = its system catalogue was installed with this start AND it holds no
// user table. An old node never restarted also reads "installed with this start": the
// user tables are what tell it apart. Anything unreadable is not fresh.
func (server *ServerMonitor) CheckFreshNode() FreshNode {
	if server == nil || server.Conn == nil || server.DBVersion == nil {
		return FreshNode{Reason: "not connected"}
	}
	installAge, startAge, _, err := dbhelper.GetSystemCatalogAge(server.Conn, server.DBVersion)
	if err != nil {
		return FreshNode{Reason: "system catalogue age not readable: " + err.Error()}
	}
	userTables, _, err := dbhelper.HasUserTables(server.Conn, server.DBVersion)
	if err != nil {
		return FreshNode{InstallAgeSec: installAge, StartAgeSec: startAge, Reason: "user tables not readable: " + err.Error()}
	}
	fn := FreshNode{InstallAgeSec: installAge, StartAgeSec: startAge, UserTables: userTables}
	switch {
	case userTables:
		fn.Reason = "it holds user tables"
	case installAge-startAge > freshNodeInstallWindowSec:
		fn.Reason = fmt.Sprintf("its system catalogue was installed %ds before this start", installAge-startAge)
	default:
		fn.Fresh = true
		fn.Reason = fmt.Sprintf("system catalogue installed with this start (%ds before it), no user table", installAge-startAge)
	}
	return fn
}

// checkFreshNodeStates opens CINF0013 on each StandAlone server proven fresh, the node an
// automatic reseed may fill. Only StandAlone servers are read: a running master or
// replica is never a reseed target.
func (cluster *Cluster) checkFreshNodeStates() {
	for _, s := range cluster.Servers {
		if s == nil || s.State != stateUnconn {
			continue
		}
		if fn := s.CheckFreshNode(); fn.Fresh {
			cluster.SetState("CINF0013", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0013"], s.URL, fn.Reason), ErrFrom: "TOPO", ServerUrl: s.URL})
		}
	}
}

// myloaderCritical keeps the last CRITICAL lines myloader writes on stderr before it
// aborts (SIGTRAP on a GLib g_critical): the error it returns names the cause instead of
// "signal: trace/breakpoint trap" alone (#1979).
type myloaderCritical struct {
	mu    sync.Mutex
	lines []string
}

const myloaderCriticalKeep = 3

func (c *myloaderCritical) observe(line string) {
	if !strings.Contains(line, "CRITICAL") {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, strings.TrimSpace(line))
	if len(c.lines) > myloaderCriticalKeep {
		c.lines = c.lines[len(c.lines)-myloaderCriticalKeep:]
	}
}

func (c *myloaderCritical) wrap(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lines) == 0 {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.Join(c.lines, " / "))
}

// replayMasterSystemUsers dumps the master's accounts and grants live (the --system=user
// dump of the users sidecar) and replays them on this freshly restored node through the
// system-catalogue path (classify, publish, restoreSystemCatalog). A fresh node's own
// system tables stay those of its release; only the accounts come from the master.
func (server *ServerMonitor) replayMasterSystemUsers(master *ServerMonitor, sqlLogBin int) error {
	cluster := server.ClusterGroup
	suffix, err := randomHexSuffix(6)
	if err != nil {
		return err
	}
	writer, err := server.newDirectReseedSystemArtifactWriter("myloader-users-"+suffix, time.Now().UTC())
	if err != nil {
		return err
	}
	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), master.mysqldumpUserArgs()...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Accounts of %s for %s: %s", master.URL, server.URL, strings.ReplaceAll(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))
	stdout, err := dumpCmd.StdoutPipe()
	if err != nil {
		writer.discard()
		return err
	}
	stderr, _ := dumpCmd.StderrPipe()
	if err := dumpCmd.Start(); err != nil {
		writer.discard()
		return err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.copyLogs(stderr, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	result, classifyErr := splitdump.ClassifyStream(stdout, splitdump.ClassifyOptions{ApplicationWriter: io.Discard, SystemWriter: writer})
	wg.Wait()
	waitErr := dumpCmd.Wait()
	if classifyErr != nil || waitErr != nil {
		writer.discard()
		return errors.Join(classifyErr, waitErr)
	}
	if !result.HasSystemContent {
		writer.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Accounts of %s: the dump carried no account for %s", master.URL, server.URL)
		return nil
	}
	return server.publishAndReplayReseedMysqldumpSystemArtifact(writer, result.Metadata, "live:"+master.URL, sqlLogBin)
}
