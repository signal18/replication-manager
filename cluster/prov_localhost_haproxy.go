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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) LocalhostUnprovisionHaProxyService(prx *HaproxyProxy) error {
	cluster.LocalhostStopHaProxyService(prx)
	os.RemoveAll(prx.Datadir + "/var")
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionHaProxyService(prx *HaproxyProxy) error {

	out := &bytes.Buffer{}
	path := prx.Datadir + "/var"
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Remove datadir done: %s", out.Bytes())
	prx.GetProxyConfig()
	os.Symlink(prx.Datadir+"/init/data", path)

	err = cluster.LocalhostStartHaProxyService(prx)
	if err != nil {
		cluster.errorChan <- err
		return err

	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopHaProxyService(prx *HaproxyProxy) error {

	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,"TEST", "Killing database %s %d", server.Id, server.Process.Pid)

	pid, err := os.ReadFile(prx.Datadir + "/var/haproxy.pid")
	if err != nil {
		return errors.New("No such file " + prx.Datadir + "/var/haproxy.pid")
	}
	killCmd := exec.Command("kill", "-9", strings.Trim(string(pid), "\n"))
	killCmd.Run()
	return nil
}

func (cluster *Cluster) LocalhostStartHaProxyService(prx *HaproxyProxy) error {
	prx.GetProxyConfig()
	//init haproxy do start or reload
	prx.Init()
	/*mariadbdCmd := exec.Command(cluster.Conf.HaproxyBinaryPath+"/haproxy", "--config="+prx.Datadir+"/init/etc/haproxy.cnf", "--datadir="+prx.Datadir+"/var")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err := mariadbdCmd.Run()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s ", err)
			fmt.Printf("Command finished with error: %v", err)
		}
	}()
	time.Sleep(time.Millisecond * 2000)
	prx.Process = mariadbdCmd.Process*/
	//	mariadbdCmd.Process.Release()

	return nil
}

// localhostCheckScriptTemplate is the Localhost-orchestrator equivalent of
// the checkmaster/checkslave scripts baked into
// share/opensvc/moduleset_mariadb.svc.mrm.proxy.json for OpenSVC's
// haproxy_check.cfg. HAProxy's external-check engine invokes it as
// "<script> <proxy_addr> <proxy_port> <server_addr> <server_port>" per
// server, so $3/$4 below are the checked server's host/port -- matching
// the OpenSVC scripts' own use of $3/$4. wget is tried first (present on
// nearly every distro); when it's missing the /dev/tcp fallback needs bash
// specifically (not just any POSIX sh), so it's only attempted after
// confirming bash exists, mirroring the OpenSVC scripts' own two guarded
// branches.
//
// This is a deliberate, self-contained duplication of that moduleset
// content, not an oversight: reusing GetProxyConfig()/GenerateProxyConfig()
// here would mean depending on Configurator.LoadProxyModules() having
// already populated ProxyModule.Rulesets (only guaranteed after a full
// Configurator.Init(), not in every context Init() below can run in), and
// its path convention lands the files at a confusing nested
// "<Datadir>/init/init/checkmaster" (built around OpenSVC/K8s's own
// tar-then-extract-elsewhere workflow, which Localhost never does) --
// neither of which this self-contained version needs to care about.
const localhostCheckScriptTemplate = `#!/bin/sh
echo $3 $4
if command -v wget >/dev/null 2>&1
then
 ret=$(wget -O - -q "http://%[1]s/clusters/%[2]s/servers/$3/$4/%[3]s")
elif command -v bash >/dev/null 2>&1
then
 ret=$(bash -c 'HOST="$1"; PORT="${HOST##*:}"; HOST="${HOST%%:*}"; exec 3<>/dev/tcp/$HOST/$PORT || exit 1; printf "GET %%s HTTP/1.0\r\nHost: %%s\r\nConnection: close\r\n\r\n" "$2" "$HOST" >&3; cat <&3' _ "%[1]s" "/clusters/%[2]s/servers/$3/$4/%[3]s")
 sep=$(printf '\r\n\r\n')
 ret="${ret#*$sep}"
else
 exit 1
fi
n=$(echo "$ret" | grep -c "200")
echo "$n"
if [ "$n" -eq 1 ]
then
 echo "true"
 exit 0
fi
exit 1
`

// localhostCheckScriptContent renders localhostCheckScriptTemplate for one
// status endpoint ("master-status" or "slave-status") against this
// cluster's own repman API -- the same address OpenSVC's checkmaster/
// checkslave reach via %%ENV:SVC_CONF_ENV_MRM_API_ADDR%%
// (cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort, see
// cluster/prx_get.go's GetEnv()). The target route is the unauthenticated
// "/clusters/{name}/servers/{host}/{port}/{status}" compatibility path
// (server/http.go, "API 2.0 compatibility for external checks"), not the
// /api/-prefixed one.
func (cluster *Cluster) localhostCheckScriptContent(statusPath string) string {
	apiAddr := cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	// url.PathEscape, not cluster.Name verbatim: cluster.Name lands inside a
	// double-quoted shell string in localhostCheckScriptTemplate (the wget
	// URL and, in the /dev/tcp fallback, an HTTP request line built via
	// printf). Today it's operator-controlled config, not attacker input,
	// but PathEscape percent-encodes anything outside URL-path-safe
	// characters -- including '"', '`', '$', '\' -- so a cluster name with
	// any of those can't break out of that quoting and inject shell syntax
	// into a script HAProxy's external-check then executes, if cluster
	// naming ever becomes less trusted (e.g. self-service creation).
	return fmt.Sprintf(localhostCheckScriptTemplate, apiAddr, url.PathEscape(cluster.Name), statusPath)
}

// writeLocalhostHaproxyCheckScripts (re)writes checkmaster/checkslave next
// to this proxy's local haproxy.cfg (proxy.Datadir/init/) so
// haproxy-mode=externalcheck's external-check command has somewhere to exec
// on the Localhost orchestrator, matching the mechanism OpenSVC externalcheck
// already has via its moduleset-provisioned container paths. Rewritten on
// every call (not just once) so an API-address or cluster-name change is
// picked up on the next Init() without requiring a manual redeploy.
func (proxy *HaproxyProxy) writeLocalhostHaproxyCheckScripts() (checkmasterPath, checkslavePath string, err error) {
	cluster := proxy.ClusterGroup
	initDir := filepath.Join(proxy.Datadir, "init")
	if err := os.MkdirAll(initDir, 0750); err != nil {
		return "", "", err
	}

	checkmasterPath = filepath.Join(initDir, "checkmaster")
	checkslavePath = filepath.Join(initDir, "checkslave")

	if err := os.WriteFile(checkmasterPath, []byte(cluster.localhostCheckScriptContent("master-status")), 0755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(checkslavePath, []byte(cluster.localhostCheckScriptContent("slave-status")), 0755); err != nil {
		return "", "", err
	}

	return checkmasterPath, checkslavePath, nil
}
