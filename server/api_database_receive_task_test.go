// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	log "github.com/sirupsen/logrus"
)

// jobsScriptBody builds a job body with the jobs script's own functions, as a database server's
// dbjobs posts it (the jobs key, no database password).
func jobsScriptBody(t *testing.T, payload, jobsKey string) []byte {
	t.Helper()
	for _, bin := range []string{"bash", "openssl", "sha256sum"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	fns := `src=../share/scripts/dbjobs_new.sh
eval "$(sed -n '/^pad_pkcs7() {/,/^}/p; /^derive_key() {/,/^}/p; /^jobs_key() {/,/^}/p; /^job_body() {/,/^}/p' "$src")"
job_body "$1" ""`
	cmd := exec.Command("bash", "-c", fns, "job_body", payload)
	cmd.Env = append(cmd.Environ(), "JOBS_API_KEY="+jobsKey)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("job_body: %v", err)
	}
	return bytes.TrimSpace(out)
}

func receiveTaskStatus(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, task string) (int, string) {
	t.Helper()
	// each request has its own one-time body
	body := jobsScriptBody(t, `{"server":"127.0.0.1:3306"}`, cl.GetJobsKey())
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/c1/servers/127.0.0.1/3306/actions/receive-task/"+task, bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"clusterName": "c1", "serverName": "127.0.0.1", "serverPort": "3306", "taskname": task})
	w := httptest.NewRecorder()
	repman.handlerMuxServerReceiveTask(w, req)
	return w.Code, strings.TrimSpace(w.Body.String())
}

func sstReceiverPorts() map[int]bool {
	cluster.SSTs.Lock()
	defer cluster.SSTs.Unlock()
	ports := make(map[int]bool, len(cluster.SSTs.SSTconnections))
	for p := range cluster.SSTs.SSTconnections {
		ports[p] = true
	}
	return ports
}

// TestReceiveTaskMariabackupAPIMode drives handlerMuxServerReceiveTask for a physical backup
// the way the jobs script does in API mode: it answers the receiver the running backup opened,
// as many times as it is asked, and opens no other; with no backup waiting it refuses and
// opens nothing.
func TestReceiveTaskMariabackupAPIMode(t *testing.T) {
	const task = config.ConstBackupPhysicalTypeMariaBackup
	wd := t.TempDir()
	cl := &cluster.Cluster{
		Name: "c1",
		Conf: &config.Config{
			WorkingDir: wd, SchedulerJobsMode: "api", CompressBackups: true,
			BackupPhysicalType: task, APISecretLoginWindow: 300,
			SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		},
		StateMachine:         &state.StateMachine{},
		SecurityStateMachine: &state.StateMachine{},
		Logrus:               log.New(),
		BackupMetaMap:        backupmgr.NewBackupMetaMap(),
		DiskStatManager:      misc.NewDiskStatManager(),
		VersionsMap:          config.NewVersionsMap(),
		SstAvailablePorts:    make(map[string]string),
	}
	cl.StateMachine.Init()
	cl.SecurityStateMachine.Init()
	node := &cluster.ServerMonitor{Host: "127.0.0.1", Port: "3306", URL: "127.0.0.1:3306", Pass: "s3cr3t", ClusterGroup: cl}
	node.JobResults = config.NewTasksMap()
	node.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	node.Datadir = filepath.Join(wd, "node1_3306")
	if err := os.MkdirAll(node.Datadir, 0755); err != nil {
		t.Fatal(err)
	}
	cl.Servers = []*cluster.ServerMonitor{node}
	repman := &ReplicationManager{Conf: &config.Config{}, Logrus: log.New(), Clusters: map[string]*cluster.Cluster{"c1": cl}}

	before := sstReceiverPorts()

	// no backup waits: refused, nothing opened, in particular nothing on the backup file
	code, body := receiveTaskStatus(t, repman, cl, task)
	if code != 500 || !strings.Contains(body, "No physical backup") {
		t.Fatalf("no backup waiting: %d %q, want 500 refusal", code, body)
	}
	if n := len(sstReceiverPorts()) - len(before); n != 0 {
		t.Fatalf("a refused request opened %d receivers", n)
	}

	// the backup starts and opens its receiver
	if err := node.JobBackupPhysicalWithOptions(cluster.BackupRunOptions{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var port string
	for p := range sstReceiverPorts() {
		if !before[p] {
			port = strconv.Itoa(p)
		}
	}
	if port == "" {
		t.Fatal("the backup opened no receiver")
	}
	t.Cleanup(func() { // end the receiver whatever happens
		node.JobsUpdateState(task, "error", cluster.JobStateErrorExec, 1)
		if c, err := net.Dial("tcp", "127.0.0.1:"+port); err == nil {
			c.Close()
		}
	})

	for i := 0; i < 3; i++ {
		code, body = receiveTaskStatus(t, repman, cl, task)
		if code != 200 || body != "RECEIVER_PORT="+port {
			t.Fatalf("ask %d: %d %q, want 200 RECEIVER_PORT=%s", i+1, code, body, port)
		}
	}
	if n := len(sstReceiverPorts()) - len(before); n != 1 {
		t.Fatalf("%d receivers open after three asks, want the backup's one", n)
	}

	// the stream the jobs script then sends goes to that receiver and completes the backup
	payload := bytes.Repeat([]byte("mariabackup stream "), 4096)
	node.JobsUpdateState(task, "completed", cluster.JobStateSuccess, 1)
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	// the jobs script signs its stream once its login named the key (job_stream_signed)
	header, err := jobapi.StreamHeader(cl.GetJobsKey(), jobapi.StreamToMonitor, "127.0.0.1:3306", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c.Write(header)
	c.Write(payload)
	c.Close()
	// the end of the receiver marks the backup received: its cookie is set after the metadata
	deadline := time.Now().Add(10 * time.Second)
	for !node.HasBackupMariabackupCookie() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !node.HasBackupMariabackupCookie() {
		t.Fatal("the backup whose stream was received got no cookie")
	}
	dest := node.GetMyBackupDirectory() + task + ".xbtream.gz"
	if st, err := os.Stat(dest); err != nil || st.Size() == 0 {
		t.Fatalf("backup file %s: %v", dest, err)
	}

	// the receiver ended: asking again finds no backup waiting
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ = receiveTaskStatus(t, repman, cl, task); code == 500 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code != 500 {
		t.Fatal("after its receiver ended, receive-task must refuse again")
	}
}
