// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
)

// waitLocalhostMysqldStopped polls the pid file mysqld was started with
// (path/<server.Id>.pid) until the process it names is gone, or returns an
// error once the timeout elapses. It works off the pid file rather than
// server.Process because the running mysqld may predate this repman process
// (e.g. after a repman restart), in which case server.Process is nil even
// though the server is very much alive.
func (cluster *Cluster) waitLocalhostMysqldStopped(server *ServerMonitor, path string) error {
	pidPath := path + "/" + server.Id + ".pid"
	data, err := os.ReadFile(pidPath)
	if err != nil {
		// No pid file: nothing known to be running, nothing to wait for.
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	for i := 0; i < 60; i++ {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process no longer exists.
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("mysqld (pid %d) did not exit within timeout", pid)
}

func (cluster *Cluster) LocalhostUnprovisionDatabaseService(server *ServerMonitor) error {
	cluster.LocalhostStopDatabaseService(server)
	cmd := exec.Command("rm", "-rf", server.Datadir)
	out := &bytes.Buffer{}
	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Remove datadir done: %s", out.Bytes())
	cluster.master = nil
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionGetVersionFromMysqld(server *ServerMonitor) string {
	out := &bytes.Buffer{}
	versionCmd := exec.Command(cluster.Conf.ProvDBBinaryBasedir+"/mysqld", "--version")
	versionCmd.Stdout = out

	err := versionCmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "mysqld version err: %s", out.Bytes())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return ""
	}
	return strings.ToLower(out.String())
}

func (cluster *Cluster) LocalhostProvisionDatabaseService(server *ServerMonitor) error {

	out := &bytes.Buffer{}
	path := server.Datadir + "/var"
	// Provision is a from-scratch action, so path is always wiped and
	// recreated empty first -- never merged into. This used to be a
	// commented-out dead code block (a bare "cp -rp init/data/. path" onto
	// whatever was already there was the only thing that actually ran),
	// which made repeated provision attempts non-idempotent: any partial
	// InnoDB state left behind by an earlier failed attempt (e.g. an
	// ibdata1 with no matching redo log) survived into the next attempt's
	// "var" untouched, and InnoDB does not tolerate a half-there system
	// tablespace -- it aborts instead of completing initialization around
	// it. Confirmed live: two servers whose "var" carried exactly this kind
	// of leftover state from earlier failed attempts kept failing
	// provision with "File ./.system/innodb/redo/ib_logfile0 was not
	// found" even after the actual bug (missing Dir on the mysql_install_db
	// exec.Command, see below) was fixed elsewhere in this function --
	// only wiping "var" first made those same servers provision cleanly.
	//
	// Stop first, the same way LocalhostUnprovisionDatabaseService already
	// does before its own "rm -rf", so re-provisioning a server that's
	// still actually running stops it cleanly instead of deleting its
	// datadir out from under a live mariadbd process. A server that isn't
	// running has no Conn, so Shutdown() no-ops with an error that's fine
	// to ignore here -- any other error means we believed the server to be
	// reachable and the shutdown command itself failed, so abort rather
	// than risk deleting a live datadir.
	if err := cluster.LocalhostStopDatabaseService(server); err != nil && server.Conn != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Failed to stop database before reprovision on %s: %s", server.URL, err)
		cluster.errorChan <- err
		return err
	}
	// SHUTDOWN (issued above) only asks mysqld to stop -- it does not wait
	// for the process to actually exit, so a fixed sleep here was a race:
	// on a slow shutdown (large InnoDB buffer pool flush, "WAIT FOR ALL
	// SLAVES" on a busy master, etc.) RemoveAll below could still run while
	// mysqld was mid-shutdown and still holding/writing the datadir. Poll
	// the pid file this mysqld was started with instead, and refuse to
	// touch the datadir if it's still alive after the timeout.
	if err := cluster.waitLocalhostMysqldStopped(server, path); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Refusing to remove datadir %q on %s: %s", path, server.URL, err)
		cluster.errorChan <- err
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Failed to remove existing datadir %q: %s", path, err)
		cluster.errorChan <- err
		return err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Failed to create datadir %q: %s", path, err)
		cluster.errorChan <- err
		return err
	}
	if err := server.GetDatabaseConfig(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Database config generation failed for %s: %s", server.URL, err)
		cluster.errorChan <- err
		return err
	}
	///	os.Symlink(server.Datadir+"/init/data", path)

	/*cmd = exec.Command("cp", "-rp", cluster.Conf.ShareDir+"/tests/data"+cluster.Conf.ProvDatadirVersion, path)

	// Attach buffer to command
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "Copy fresh datadir done: %s", out.Bytes())
	*/
	cmd := exec.Command("cp", "-rp", server.Datadir+"/init/data/.", path)
	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "cp -rp %s %s failed %s ", server.Datadir+"/init/data/.system", path, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "init fresh datadir err: %s", out.Bytes())
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "copy datadir done: %s", out.Bytes())

	var sysCmd *exec.Cmd
	err = errors.New("No database version found")
	version := cluster.LocalhostProvisionGetVersionFromMysqld(server)
	if version == "" {
		cluster.errorChan <- err
		return err
	}
	if strings.Contains(version, "mariadb") {
		// --auth-root-authentication-method=normal: modern MariaDB packages
		// (this one included) default mysql_install_db to "socket" auth,
		// which only seeds root@localhost reachable via the Unix socket as
		// the matching OS user. This code's own startup-check loop and
		// bootstrap GRANTs connect over TCP (server.DSN, 127.0.0.1) --
		// without "normal" here, that TCP connection is refused with
		// "Host '127.0.0.1' is not allowed to connect to this MariaDB
		// server" for every server this orchestrator ever provisions.
		// "normal" restores the historical behavior (password-based
		// root@localhost/127.0.0.1/::1, empty password here) this code has
		// always assumed.
		sysCmd = exec.Command(cluster.Conf.ProvDBClientBasedir+"/mysql_install_db", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--datadir="+server.Datadir+"/var", "--basedir="+cluster.Conf.ProvDBBinaryBasedir+"/../", "--auth-root-authentication-method=normal", "--force")
	} else {
		sysCmd = exec.Command(cluster.Conf.ProvDBBinaryBasedir+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--datadir="+server.Datadir+"/var", "--basedir="+cluster.Conf.ProvDBBinaryBasedir+"/../", "--initialize", "--initialize-insecure")
	}
	// The generated my.cnf (default_path.cnf) points innodb_data_home_dir,
	// innodb_log_group_home_dir, tmpdir, log_error, etc. at paths relative to
	// the server's own datadir ("./.system/..."). Without Dir set here, those
	// resolve against this repman process's own working directory instead --
	// InnoDB then looks for (and never finds) its redo log at the wrong
	// location entirely ("File ./.system/innodb/redo/ib_logfile0 was not
	// found"), aborting initialization. Confirmed live: same command,
	// otherwise unchanged, succeeds once Dir is set to the actual datadir.
	sysCmd.Dir = server.Datadir + "/var"
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", sysCmd.String())
	sysCmd.Stdout = out
	err = sysCmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "init fresh datadir err: %s", out.Bytes())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "init fresh datadir done: %s", out.Bytes())
	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, "TEST", "Found no os process continue with start ")
		}

	}

	/*	err := os.RemoveAll(path + "/" + server.Id + ".pid")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
			return err
		}*/

	err = cluster.LocalhostStartDatabaseServiceFistTime(server)
	if err != nil {
		cluster.errorChan <- err
		return err

	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopDatabaseService(server *ServerMonitor) error {
	server.StopSlave()
	return server.Shutdown()
}

func (cluster *Cluster) LocalhostStartDatabaseServiceFistTime(server *ServerMonitor) error {

	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, "TEST", "Found no os process continue with start ")
		}

	}
	path := server.Datadir + "/var"
	/*	err := os.RemoveAll(path + "/" + server.Id + ".pid")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
			return err
		}*/
	usr, err := user.Current()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}
	user := usr.Username
	version := cluster.LocalhostProvisionGetVersionFromMysqld(server)
	if version == "" {
		return errors.New("mysqld --version not found ")
	}
	time.Sleep(time.Millisecond * 2000)
	if strings.Contains(version, "mariadb") {
		user = "root"
	}
	mariadbdCmd := exec.Command(cluster.Conf.ProvDBBinaryBasedir+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.GetDatabaseSocket(), "--user="+user, "--bind-address=0.0.0.0", "--pid_file="+path+"/"+server.Id+".pid")
	// See LocalhostProvisionDatabaseService's sysCmd.Dir comment: my.cnf's
	// "./.system/..." relative paths need this process's CWD to be the
	// server's own datadir, not repman's.
	mariadbdCmd.Dir = path

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err = mariadbdCmd.Run()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s ", err)
		}
		fmt.Printf("Command finished with error: %v", err)
	}()
	exitloop := 0
	time.Sleep(time.Millisecond * 4000)
	for exitloop < 30 {
		haveerror := false
		time.Sleep(time.Millisecond * 2000)
		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "Waiting database startup ")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Waiting database first start   .. %s", out)

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't get replication-manager process user: %s", err)
		}
		dsn := user + ":@unix(" + server.GetDatabaseSocket() + ")/?timeout=15s"
		conn, err2 := sqlx.Open("mysql", dsn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "DNS start prov localhost first time : %s\n", dsn)
		if err2 == nil {
			defer conn.Close()
			_, err := conn.Exec("set sql_log_bin=0")
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", "set sql_log_bin=0", err)
			}

			_, err = conn.Exec("delete from mysql.user where password='' and user!='mariadb.sys'")
			if err != nil {
				//	haveerror = true
				// don't trigger error for mysql 5.7 and mariadb 10.4 that does not have password column

				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, " %s %s ", "delete from mysql.user where password=''", err)
			}
			grants := "grant all on *.* to '" + server.User + "'@'localhost' identified by '" + server.Pass + "' WITH GRANT OPTION"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", grants)
			grants = "grant all on *.* to '" + server.User + "'@'%' identified by '" + server.Pass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.User + "'@'127.0.0.1' identified by '" + server.Pass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.ClusterGroup.GetRplUser() + "'@'localhost' identified by '" + server.ClusterGroup.GetRplPass() + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", grants)
			grants = "grant all on *.* to '" + server.ClusterGroup.GetRplUser() + "'@'%' identified by '" + server.ClusterGroup.GetRplPass() + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.ClusterGroup.GetRplUser() + "'@'127.0.0.1' identified by '" + server.ClusterGroup.GetRplPass() + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", grants, err)
			}
			_, err = conn.Exec("flush privileges")
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", "flush privileges", err)
			}
			query := "RESET MASTER"
			if server.DBVersion.IsMySQLOrPerconaGreater84() {
				query = "RESET BINARY LOGS AND GTIDS"
			}

			_, err = conn.Exec(query)
			if err != nil {
				haveerror = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s %s ", "reset master", err)
			}

			if !haveerror {
				exitloop = 100
			}

		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Database connection to init user  %s ", err2)
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database started.")

	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database timeout.")
		return errors.New("Failed to start")
	}

	//	mariadbdCmd.Process.Release()

	return nil
}

func (cluster *Cluster) LocalhostStartDatabaseService(server *ServerMonitor) error {
	server.GetDatabaseConfig()
	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, "TEST", "Found no os process continue with start ")
		}

	}
	path := server.Datadir + "/var"
	/*	err := os.RemoveAll(path + "/" + server.Id + ".pid")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
			return err
		}*/
	usr, err := user.Current()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}
	//	mariadbdCmd := exec.Command(cluster.Conf.ProvDBBinaryBasedir+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf --port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.Datadir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--general_log=1", "--general_log_file="+path+"/"+server.Id+".log", "--pid_file="+path+"/"+server.Id+".pid", "--log-error="+path+"/"+server.Id+".err")
	time.Sleep(time.Millisecond * 2000)
	mariadbdCmd := exec.Command(cluster.Conf.ProvDBBinaryBasedir+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.GetDatabaseSocket(), "--user="+usr.Username, "--bind-address=0.0.0.0", "--pid_file="+path+"/"+server.Id+".pid")
	// See LocalhostProvisionDatabaseService's sysCmd.Dir comment: my.cnf's
	// "./.system/..." relative paths need this process's CWD to be the
	// server's own datadir, not repman's.
	mariadbdCmd.Dir = path
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err = mariadbdCmd.Run()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s ", err)
		}
		fmt.Printf("Command finished with error: %v", err)
	}()

	exitloop := 0
	time.Sleep(time.Millisecond * 4000)
	for exitloop < 30 {

		time.Sleep(time.Millisecond * 2000)
		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "Waiting database startup ")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Waiting database startup %d: %s", exitloop, out.String())
		conn, err2 := sqlx.Open("mysql", server.DSN)
		if err2 == nil {
			defer conn.Close()
			exitloop = 100

		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Database connection to init user  %s ", err2)
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database started.")

	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database timeout.")
		return errors.New("Failed to start")
	}
	server.Process = mariadbdCmd.Process
	//	mariadbdCmd.Process.Release()

	return nil
}
