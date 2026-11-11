// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// physicalReceiverPayload is the stream a physical backup sends: incompressible enough
// that the gzip member is longer than the 20 bytes an empty member takes.
func physicalReceiverPayload() []byte {
	b := make([]byte, 64*1024)
	x := uint32(2463534242)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

func gunzipFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip header of %s: %v", path, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	return out
}

// waitReceiverGone waits for the receiver on port to leave SSTs (its file is closed by then).
func waitReceiverGone(t *testing.T, port string) {
	t.Helper()
	p, _ := strconv.Atoi(port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		SSTs.Lock()
		_, open := SSTs.SSTconnections[p]
		SSTs.Unlock()
		if !open {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("receiver on port %s did not end", port)
}

// expireReceiver ends the accept wait of the receiver on port the way its one-hour deadline
// does, without waiting an hour: the deadline moves to now.
func expireReceiver(t *testing.T, port string) {
	t.Helper()
	p, _ := strconv.Atoi(port)
	SSTs.Lock()
	sst := SSTs.SSTconnections[p]
	SSTs.Unlock()
	if sst == nil {
		t.Fatalf("no receiver on port %s", port)
	}
	sst.tcplistener.SetDeadline(time.Now())
}

// waitBackupStateReleased waits for the end of the physical branch of JobFinishReceiveFile: the
// live progress is ended last, and it is a synchronized map, so what the branch wrote before is
// visible after it.
func waitBackupStateReleased(t *testing.T, cl *Cluster, server *ServerMonitor) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for cl.backupProgressFor(server, "physical") != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cl.backupProgressFor(server, "physical") != nil {
		t.Fatal("the ended receiver did not release the physical backup state")
	}
}

func sendToReceiver(t *testing.T, port string, payload []byte) {
	t.Helper()
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Write(payload)
	c.Close()
}

// TestUnusedPhysicalReceiverDoesNotCorruptBackup reproduces the API-mode double receiver
// (Kubernetes deployment, scheduler-jobs-mode=api): JobBackupPhysicalWithOptions opens a
// receiver on the backup file, then the jobs script asks receive-task/mariabackup and gets a
// second one on the same file; the stream goes to the second. When the first reaches its
// accept deadline its cleanup must leave the finished backup alone.
func TestUnusedPhysicalReceiverDoesNotCorruptBackup(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string)
	cl.DiskStatManager = misc.NewDiskStatManager() // WriteBackupMetadata records the disk usage
	server.JobResults = config.NewTasksMap()
	server.JobResults.Set(config.ConstBackupPhysicalTypeMariaBackup, &config.Task{Task: config.ConstBackupPhysicalTypeMariaBackup, State: JobStateSuccess, Done: 1}) // WriteBackupMetadata waits for the end of the job
	cl.Conf.CompressBackups = true
	task := config.ConstBackupPhysicalTypeMariaBackup
	dest := filepath.Join(t.TempDir(), "mariabackup.xbtream.gz")

	// what the backup opens at dispatch
	portA, err := server.openPhysicalBackupReceiver(true, dest, task)
	if err != nil {
		t.Fatalf("receiver of the backup: %v", err)
	}
	// what the receive-task handler opens when the jobs script asks (api_database.go)
	portB, err := cl.SSTRunReceiverToGZip(server, dest, ConstJobCreateFile, task)
	if err != nil {
		t.Fatalf("receiver of the handler: %v", err)
	}

	payload := physicalReceiverPayload()
	cl.StartBackupProgress(server, "physical", task)
	sendToReceiver(t, portB, payload)
	waitReceiverGone(t, portB)
	waitBackupStateReleased(t, cl, server) // the receivers' ends write shared state: one at a time
	if got := gunzipFile(t, dest); !bytes.Equal(got, payload) {
		t.Fatalf("before the unused receiver ends: backup holds %d bytes, want %d", len(got), len(payload))
	}

	cl.StartBackupProgress(server, "physical", task)
	expireReceiver(t, portA) // the hour is over, nobody connected to it
	waitReceiverGone(t, portA)
	waitBackupStateReleased(t, cl, server)

	if got := gunzipFile(t, dest); !bytes.Equal(got, payload) {
		t.Fatalf("the unused receiver's end rewrote the finished backup: it now gunzips to %d bytes, want %d", len(got), len(payload))
	}
}

// TestUnusedPhysicalReceiverDoesNotCompleteBackup: a receiver nobody streamed to must not
// mark the backup completed (and so publish, archive, restic it).
func TestUnusedPhysicalReceiverDoesNotCompleteBackup(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string)
	cl.DiskStatManager = misc.NewDiskStatManager() // WriteBackupMetadata records the disk usage
	server.JobResults = config.NewTasksMap()
	server.JobResults.Set(config.ConstBackupPhysicalTypeMariaBackup, &config.Task{Task: config.ConstBackupPhysicalTypeMariaBackup, State: JobStateSuccess, Done: 1}) // WriteBackupMetadata waits for the end of the job
	cl.Conf.CompressBackups = true
	task := config.ConstBackupPhysicalTypeMariaBackup
	dest := filepath.Join(t.TempDir(), "mariabackup.xbtream.gz")
	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{Id: 1, BackupTool: task, Dest: dest, Source: server.URL}
	cl.SetInPhysicalBackupState(true)
	cl.StartBackupProgress(server, "physical", task)

	port, err := server.openPhysicalBackupReceiver(true, dest, task)
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	expireReceiver(t, port)
	waitReceiverGone(t, port)

	// the end of JobFinishReceiveFile's physical branch: the progress is ended last
	waitBackupStateReleased(t, cl, server)
	server.backupMetaMutex.Lock()
	completed := server.LastBackupMeta.Physical.Completed
	server.backupMetaMutex.Unlock()
	if completed {
		t.Fatal("a receiver that got no stream marked the physical backup completed")
	}
}

// TestPhysicalBackupReceiverAdoptedByReceiveTask: what receive-task answers in API mode is the
// receiver the backup opened, so one backup has one receiver on its file.
func TestPhysicalBackupReceiverAdoptedByReceiveTask(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string)
	cl.DiskStatManager = misc.NewDiskStatManager()
	server.JobResults = config.NewTasksMap()
	task := config.ConstBackupPhysicalTypeMariaBackup
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateSuccess, Done: 1})
	dest := filepath.Join(t.TempDir(), "mariabackup.xbtream.gz")
	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{Id: 1, BackupTool: task, Dest: dest, Source: server.URL}
	cl.StartBackupProgress(server, "physical", task)

	if _, err := server.PhysicalBackupReceiverPort(task); err == nil {
		t.Fatal("no backup waits: receive-task must refuse, not open a receiver on the backup file")
	}

	port, err := server.openPhysicalBackupReceiver(true, dest, task)
	if err != nil {
		t.Fatalf("receiver of the backup: %v", err)
	}
	server.registerPhysicalBackupReceiver(task, port)

	got, err := server.PhysicalBackupReceiverPort(task)
	if err != nil || got != port {
		t.Fatalf("receive-task answers %q, %v; want the backup's receiver %q", got, err, port)
	}
	// a second ask (a retry of the jobs script) gets the same receiver
	if again, _ := server.PhysicalBackupReceiverPort(task); again != port {
		t.Fatalf("second ask answers %q, want %q", again, port)
	}
	// the end of another, older receiver never drops this registration
	server.releasePhysicalBackupReceiver(task, "1")
	if _, err := server.PhysicalBackupReceiverPort(task); err != nil {
		t.Fatalf("an older receiver's end dropped the registration: %v", err)
	}

	payload := physicalReceiverPayload()
	sendToReceiver(t, got, payload)
	waitReceiverGone(t, port)
	waitBackupStateReleased(t, cl, server)

	if out := gunzipFile(t, dest); !bytes.Equal(out, payload) {
		t.Fatalf("backup holds %d bytes, want %d", len(out), len(payload))
	}
	server.backupMetaMutex.Lock()
	completed := server.LastBackupMeta.Physical.Completed
	server.backupMetaMutex.Unlock()
	if !completed {
		t.Fatal("a backup whose stream was received must be marked completed")
	}
	if _, err := server.PhysicalBackupReceiverPort(task); err == nil {
		t.Fatal("the registration must end with its receiver")
	}
}

// TestUnusedPhysicalReceiverRollsBackKeepUntilValid: with backup-keep-until-valid the previous
// backup is renamed .old when a new one starts. A receiver that got no stream is a failed
// backup whatever the job reported: the previous backup comes back, it is not deleted for a
// file nothing was streamed to.
func TestUnusedPhysicalReceiverRollsBackKeepUntilValid(t *testing.T) {
	for name, jobState := range map[string]int{"job error": JobStateErrorExec, "job success": JobStateSuccess} {
		t.Run(name, func(t *testing.T) {
			cl, server := newTestClusterServer(t)
			cl.SstAvailablePorts = make(map[string]string)
			cl.DiskStatManager = misc.NewDiskStatManager()
			cl.Conf.BackupKeepUntilValid = true
			task := config.ConstBackupPhysicalTypeMariaBackup
			server.JobResults = config.NewTasksMap()
			server.JobResults.Set(task, &config.Task{Task: task, State: jobState, Done: 1})
			dest := filepath.Join(t.TempDir(), "mariabackup.xbtream.gz")
			previous := []byte("previous good backup")
			if err := os.WriteFile(dest+".old", previous, 0600); err != nil {
				t.Fatal(err)
			}
			server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{Id: 2, BackupTool: task, Dest: dest, Source: server.URL}
			cl.BackupMetaMap.Set(2, server.LastBackupMeta.Physical)
			cl.StartBackupProgress(server, "physical", task)

			port, err := server.openPhysicalBackupReceiver(true, dest, task)
			if err != nil {
				t.Fatalf("receiver: %v", err)
			}
			expireReceiver(t, port)
			waitReceiverGone(t, port)
			waitBackupStateReleased(t, cl, server)

			got, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(got, previous) {
				t.Fatalf("the previous backup must be back under its name: %q, %v", got, err)
			}
			if _, err := os.Stat(dest + ".old"); err == nil {
				t.Fatal("the previous backup is still renamed .old after the rollback")
			}
		})
	}
}

// newPhysicalBackupFixture is a server a physical backup can be dispatched on, in the jobs mode
// asked: what JobBackupPhysicalWithOptions and the end of its receiver (JobFinishReceiveFile)
// need, none of it a live database.
func newPhysicalBackupFixture(t *testing.T, mode string) (*Cluster, *ServerMonitor) {
	t.Helper()
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string)
	cl.DiskStatManager = misc.NewDiskStatManager()
	cl.Conf.SchedulerJobsMode = mode
	cl.Conf.CompressBackups = true
	cl.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	server.JobResults = config.NewTasksMap()
	cl.VersionsMap = config.NewVersionsMap() // WriteBackupMetadata checks the tool version
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	server.Datadir = filepath.Join(cl.Conf.WorkingDir, "node1_3306") // the cookies live there
	if err := os.MkdirAll(server.Datadir, 0755); err != nil {
		t.Fatal(err)
	}
	return cl, server
}

// closeAllNewReceivers ends the receivers a test left open, so none outlives it.
func closeAllNewReceivers(t *testing.T, server *ServerMonitor, before map[int]bool) {
	t.Helper()
	// the end of a receiver waits for the job's terminal state (WriteBackupMetadata)
	task := config.ConstBackupPhysicalTypeMariaBackup
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateErrorExec, Done: 1})
	closed := false
	for _, port := range newSSTPorts(before, sstConnectionPorts(t)) {
		closeSSTReceiverAndWait(t, port)
		closed = true
	}
	// the end of the receiver goes on after it left SSTs (metadata files in the test's directory):
	// the progress is ended last, wait for it so the directory is not removed under it
	cl := server.ClusterGroup
	for deadline := time.Now().Add(5 * time.Second); closed && cl.backupProgressFor(server, "physical") != nil && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
}

// TestJobBackupPhysicalAPIModeRegistersReceiver: in API mode the receiver the backup opens is the
// one receive-task hands out; in SQL mode the jobs table carries its port and nothing is registered.
func TestJobBackupPhysicalAPIModeRegistersReceiver(t *testing.T) {
	for _, mode := range []string{"api", "sql"} {
		t.Run(mode, func(t *testing.T) {
			cl, server := newPhysicalBackupFixture(t, mode)
			task := config.ConstBackupPhysicalTypeMariaBackup
			before := sstConnectionPorts(t)
			t.Cleanup(func() { closeAllNewReceivers(t, server, before) })

			if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			added := newSSTPorts(before, sstConnectionPorts(t))
			if len(added) != 1 {
				t.Fatalf("the backup opened %d receivers (%v), want exactly one", len(added), added)
			}
			port := strconv.Itoa(added[0])

			got, err := server.PhysicalBackupReceiverPort(task)
			if mode == "sql" {
				if err == nil {
					t.Fatalf("SQL mode registered receiver %s: its port travels in the jobs table, receive-task is not used", got)
				}
				return
			}
			if err != nil || got != port {
				t.Fatalf("receive-task would answer %q, %v; want the backup's receiver %s", got, err, port)
			}
			// asking again opens nothing: still the one receiver, the same port
			for i := 0; i < 3; i++ {
				if again, err := server.PhysicalBackupReceiverPort(task); err != nil || again != port {
					t.Fatalf("ask %d answers %q, %v; want %s", i+2, again, err, port)
				}
			}
			if n := len(newSSTPorts(before, sstConnectionPorts(t))); n != 1 {
				t.Fatalf("asking again left %d receivers, want 1", n)
			}
			if !server.HasWaitMariabackupCookie() {
				t.Fatal("API mode: the wait cookie dbjobs polls must be set")
			}
			server.backupMetaMutex.Lock()
			meta := server.LastBackupMeta.Physical
			server.backupMetaMutex.Unlock()
			if meta == nil || meta.Completed || !strings.HasSuffix(meta.Dest, "mariabackup.xbtream.gz") {
				t.Fatalf("metadata of the running backup: %+v", meta)
			}
			_ = cl
		})
	}
}

// sendReset sends part of payload then resets the connection: a stream cut mid-way.
func sendReset(t *testing.T, port string, payload []byte) {
	t.Helper()
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Write(payload)
	c.(*net.TCPConn).SetLinger(0)
	c.Close()
}

// TestPhysicalBackupAPIModeStreamOutcomes runs an API-mode physical backup (the real dispatch,
// the receiver receive-task hands out, the end of that receiver, and the job's "done" report,
// MarkBackupPhysicalDone) through every way its stream can end. Only a stream received whole, of
// a job that succeeded, leaves a completed backup and the cookie that reloads it after a
// restart; every other outcome leaves neither, gives the previous backup back
// (backup-keep-until-valid) and never starts restic.
func TestPhysicalBackupAPIModeStreamOutcomes(t *testing.T) {
	payload := physicalReceiverPayload()
	tests := []struct {
		name      string
		stream    func(t *testing.T, port string)
		jobState  int
		doneFirst bool // the job's "done" report arrives before the receiver ends
		want      bool // completed, cookie set, previous backup retired
	}{
		{"whole stream, job success", func(t *testing.T, p string) { sendToReceiver(t, p, payload) }, JobStateSuccess, false, true},
		{"whole stream, done reported first", func(t *testing.T, p string) { sendToReceiver(t, p, payload) }, JobStateSuccess, true, true},
		{"whole stream, job error", func(t *testing.T, p string) { sendToReceiver(t, p, payload) }, JobStateErrorExec, false, false},
		{"no sender, job reports done", func(t *testing.T, p string) { expireReceiver(t, p) }, JobStateSuccess, false, false},
		{"no sender, done reported first", func(t *testing.T, p string) { expireReceiver(t, p) }, JobStateSuccess, true, false},
		{"zero-byte stream, job reports done", func(t *testing.T, p string) { sendToReceiver(t, p, nil) }, JobStateSuccess, false, false},
		{"stream reset midway, job reports done", func(t *testing.T, p string) { sendReset(t, p, payload[:len(payload)/2]) }, JobStateSuccess, false, false},
		{"stream reset midway, job error", func(t *testing.T, p string) { sendReset(t, p, payload[:len(payload)/2]) }, JobStateErrorExec, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, server := newPhysicalBackupFixture(t, "api")
			cl.Conf.BackupKeepUntilValid = true
			task := config.ConstBackupPhysicalTypeMariaBackup
			dest := server.GetMyBackupDirectory() + task + ".xbtream.gz"
			previous := []byte("previous good backup")
			if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, previous, 0600); err != nil {
				t.Fatal(err)
			}
			before := sstConnectionPorts(t)
			t.Cleanup(func() { closeAllNewReceivers(t, server, before) })

			if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			server.backupMetaMutex.Lock()
			server.LastBackupMeta.Physical.ResticEnabled = true // restic must not start unless the backup completed
			server.backupMetaMutex.Unlock()
			// the job's terminal state, as its report sets it (WriteBackupMetadata waits for it)
			server.JobResults.Set(task, &config.Task{Task: task, State: tt.jobState, Done: 1})

			port, err := server.PhysicalBackupReceiverPort(task) // what receive-task answers
			if err != nil {
				t.Fatalf("receive-task: %v", err)
			}
			if tt.doneFirst {
				server.MarkBackupPhysicalDone(task)
			}
			tt.stream(t, port)
			waitReceiverGone(t, port)
			waitBackupStateReleased(t, cl, server)
			if !tt.doneFirst && tt.jobState == JobStateSuccess {
				server.MarkBackupPhysicalDone(task)
			}

			server.backupMetaMutex.Lock()
			// a failed backup is rolled back: no metadata left, or one that is not completed
			completed := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
			server.backupMetaMutex.Unlock()
			if completed != tt.want {
				t.Errorf("Completed = %v, want %v", completed, tt.want)
			}
			if cookie := server.HasBackupMariabackupCookie(); cookie != tt.want {
				t.Errorf("backup cookie = %v, want %v", cookie, tt.want)
			}
			if !tt.want {
				if got, err := os.ReadFile(dest); err != nil || !bytes.Equal(got, previous) {
					t.Errorf("the previous backup must be back under its name, got %d bytes, %v", len(got), err)
				}
				if cl.InResticPhysicalBackup {
					t.Error("restic started for a backup that did not complete")
				}
			} else if out := gunzipFile(t, dest); !bytes.Equal(out, payload) {
				t.Errorf("the completed backup holds %d bytes, want %d", len(out), len(payload))
			}
		})
	}
}

// TestPhysicalBackupAPIModeEncryptionStaging: with backup-encryption on the receiver writes a
// plaintext staging file; only a whole stream of a job that succeeded is encrypted and
// published. Every other outcome removes the staging, publishes nothing, completes nothing.
func TestPhysicalBackupAPIModeEncryptionStaging(t *testing.T) {
	payload := physicalReceiverPayload()
	tests := []struct {
		name     string
		stream   func(t *testing.T, port string)
		jobState int
		want     bool
	}{
		{"whole stream, job success", func(t *testing.T, p string) { sendToReceiver(t, p, payload) }, JobStateSuccess, true},
		{"whole stream, job error reported first", func(t *testing.T, p string) { sendToReceiver(t, p, payload) }, JobStateErrorExec, false},
		{"no sender", func(t *testing.T, p string) { expireReceiver(t, p) }, JobStateSuccess, false},
		{"zero-byte stream", func(t *testing.T, p string) { sendToReceiver(t, p, nil) }, JobStateSuccess, false},
		{"stream reset midway", func(t *testing.T, p string) { sendReset(t, p, payload[:len(payload)/2]) }, JobStateSuccess, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, server := newPhysicalBackupFixture(t, "api")
			cl.Conf.BackupEncryption = true
			cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:s3cr3t"}})
			task := config.ConstBackupPhysicalTypeMariaBackup
			logical := server.GetMyBackupDirectory() + task + ".xbtream.gz"
			before := sstConnectionPorts(t)
			t.Cleanup(func() { closeAllNewReceivers(t, server, before) })

			if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			port, err := server.PhysicalBackupReceiverPort(task)
			if err != nil {
				t.Fatalf("receive-task: %v", err)
			}
			// the report of the job, as the job-state callback records it
			if tt.jobState == JobStateErrorExec {
				server.JobsUpdateState(task, "error", JobStateErrorExec, 1)
			} else {
				server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
			}
			tt.stream(t, port)
			waitReceiverGone(t, port)
			waitBackupStateReleased(t, cl, server)
			if tt.jobState == JobStateSuccess {
				server.MarkBackupPhysicalDone(task)
			}

			server.backupMetaMutex.Lock()
			meta := server.LastBackupMeta.Physical
			completed := meta != nil && meta.Completed
			server.backupMetaMutex.Unlock()
			if completed != tt.want {
				t.Errorf("Completed = %v, want %v", completed, tt.want)
			}
			if cookie := server.HasBackupMariabackupCookie(); cookie != tt.want {
				t.Errorf("backup cookie = %v, want %v", cookie, tt.want)
			}
			if _, err := os.Stat(logical + partialSuffixForCleanup); err == nil {
				t.Error("the plaintext staging is still on disk")
			}
			if _, err := os.Stat(logical); err == nil {
				t.Error("a plaintext artifact was published under the backup name")
			}
			_, encErr := os.Stat(logical + ".enc")
			if published := encErr == nil; published != tt.want {
				t.Errorf("encrypted artifact published = %v, want %v", published, tt.want)
			}
		})
	}
}

// A job that reports an error after its encrypted artifact was published (API mode's
// job-state callback, JobsUpdateState) withdraws that artifact and leaves a backup that is not
// completed, as JobsCheckErrors does from the jobs table in SQL mode.
func TestPhysicalBackupAPIModeLateJobErrorWithdrawsEncryptedArtifact(t *testing.T) {
	cl, server := newPhysicalBackupFixture(t, "api")
	cl.Conf.BackupEncryption = true
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:s3cr3t"}})
	task := config.ConstBackupPhysicalTypeMariaBackup
	logical := server.GetMyBackupDirectory() + task + ".xbtream.gz"
	before := sstConnectionPorts(t)
	t.Cleanup(func() { closeAllNewReceivers(t, server, before) })

	if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	port, _ := server.PhysicalBackupReceiverPort(task)
	server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
	sendToReceiver(t, port, physicalReceiverPayload())
	waitReceiverGone(t, port)
	waitBackupStateReleased(t, cl, server)
	if _, err := os.Stat(logical + ".enc"); err != nil {
		t.Fatalf("the encrypted artifact must be published first: %v", err)
	}

	server.JobsUpdateState(task, "error", JobStateErrorExec, 1)

	if _, err := os.Stat(logical + ".enc"); err == nil {
		t.Error("the artifact of a job that reported an error is still published")
	}
	server.backupMetaMutex.Lock()
	completed := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
	server.backupMetaMutex.Unlock()
	if completed {
		t.Error("the backup of a job that reported an error is still completed")
	}
}

// xbChunk is one chunk of an xbstream/mbstream: magic, flags, type, path, then for a payload
// chunk its length, offset, checksum and bytes. The layout is the one mbstream writes and reads
// (TestXbstreamChunksAreMbstream checks it against the real tool when it is installed).
func xbChunk(typ byte, path string, payload []byte) []byte {
	b := []byte("XBSTCK01")
	b = append(b, 0, typ)
	var n4 [4]byte
	binary.LittleEndian.PutUint32(n4[:], uint32(len(path)))
	b = append(b, n4[:]...)
	b = append(b, path...)
	if typ == 'P' {
		var h [20]byte
		binary.LittleEndian.PutUint64(h[0:], uint64(len(payload)))
		binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(payload)) // the offset stays 0
		b = append(b, h[:]...)
		b = append(b, payload...)
	}
	return b
}

// xbFile is a file in a stream: its payload chunk, then its EOF chunk.
func xbFile(path string, payload []byte) []byte {
	return append(xbChunk('P', path, payload), xbChunk('E', path, nil)...)
}

func mbstreamExtract(t *testing.T, stream []byte) error {
	t.Helper()
	cmd := exec.Command("mbstream", "-x")
	cmd.Dir = t.TempDir()
	cmd.Stdin = bytes.NewReader(stream)
	return cmd.Run()
}

// The test streams are real xbstreams: what the tool that restores a backup accepts, cut in
// the middle of a chunk it refuses; cut after a payload chunk, or after a whole file, it takes
// without a word (the stream has no end marker of its own).
func TestXbstreamChunksAreMbstream(t *testing.T) {
	if _, err := exec.LookPath("mbstream"); err != nil {
		t.Skip("mbstream not installed")
	}
	data := physicalReceiverPayload()
	stream := append(xbFile("db/t1.ibd", data), xbFile("xtrabackup_info", []byte("info"))...)
	if err := mbstreamExtract(t, stream); err != nil {
		t.Fatalf("a whole stream must extract: %v", err)
	}
	if err := mbstreamExtract(t, stream[:len(stream)/3]); err == nil {
		t.Error("a stream cut in the middle of a chunk must be refused by mbstream")
	}
	firstFile := len(xbFile("db/t1.ibd", data))
	if err := mbstreamExtract(t, stream[:firstFile]); err != nil {
		t.Errorf("a stream cut after a whole file is taken by mbstream: %v", err)
	}
	if err := mbstreamExtract(t, stream[:firstFile-len(xbChunk('E', "db/t1.ibd", nil))]); err != nil {
		t.Errorf("a stream cut after a payload chunk, its EOF chunk missing, is taken by mbstream: %v", err)
	}
}

// TestPhysicalBackupTruncatedStreamEndingCleanlyIsNotDetected records what the receiver can and
// cannot know. Transport completion (the connection ended with EOF, no read or write error),
// producer success (the job reported done: the script only greps mariabackup's "completed OK!",
// the exit status of socat in the pipe is not looked at), gzip integrity (the receiver writes a
// valid gzip of whatever arrived) and backup validity (the stream is a whole backup) are four
// different things. A stream cut in the middle of a chunk and ended with EOF, of a job that
// reported done, is today a completed backup with its cookie. This predates the receiver
// changes (the same test passes on the code before them) and nothing in the stream ends it, so
// the receiver cannot tell; the restore of such a backup is refused by mbstream. When a check
// is added (the script failing the job on a non-zero socat status), this test is the one to flip.
func TestPhysicalBackupTruncatedStreamEndingCleanlyIsNotDetected(t *testing.T) {
	data := physicalReceiverPayload()
	stream := append(xbFile("db/t1.ibd", data), xbFile("xtrabackup_info", []byte("info"))...)
	for name, cut := range map[string]int{
		"cut in the middle of a chunk":        len(stream) / 2,
		"cut after a payload, before its EOF": len(xbFile("db/t1.ibd", data)) - len(xbChunk('E', "db/t1.ibd", nil)),
	} {
		t.Run(name, func(t *testing.T) {
			cl, server := newPhysicalBackupFixture(t, "api")
			task := config.ConstBackupPhysicalTypeMariaBackup
			before := sstConnectionPorts(t)
			t.Cleanup(func() { closeAllNewReceivers(t, server, before) })
			if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			port, _ := server.PhysicalBackupReceiverPort(task)
			server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
			sendToReceiver(t, port, stream[:cut]) // a clean close: FIN, no reset
			waitReceiverGone(t, port)
			waitBackupStateReleased(t, cl, server)
			server.MarkBackupPhysicalDone(task)

			server.backupMetaMutex.Lock()
			completed := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
			server.backupMetaMutex.Unlock()
			if !completed || !server.HasBackupMariabackupCookie() {
				t.Fatalf("known limit changed: completed=%v cookie=%v; a check for a cleanly ended truncated stream now exists, flip this test", completed, server.HasBackupMariabackupCookie())
			}
		})
	}
}

// producerTail runs the end of the jobs script's physical backup pipeline: what the backup
// tool writes goes through job_stream_signed (the header replication-manager's receiver checks)
// and a real socat to the receiver (dbjobs_new.sh: `... | job_stream_signed | socat -u stdio TCP:$ADDRESS`).
func producerTail(t *testing.T, producer, port, jobsKey string) *exec.Cmd {
	t.Helper()
	for _, bin := range []string{"bash", "openssl", "sha256sum", "socat"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	fns := `src=../share/scripts/dbjobs_new.sh
eval "$(sed -n '/^derive_key() {/,/^}/p; /^jobs_key() {/,/^}/p; /^readonly JOB_STREAM_WINDOW=/p; /^job_stream_signed() {/,/^}/p; /^job_stream_mac() {/,/^}/p' "$src")"
` + producer + ` | job_stream_signed | socat -u stdio TCP:127.0.0.1:` + port
	cmd := exec.Command("bash", "-c", fns)
	cmd.Env = append(cmd.Environ(), "JOBS_API_KEY="+jobsKey, "MYSQL_ROOT_PASSWORD=s3cr3t", "MYSQL_SERVER=127.0.0.1", "MYSQL_PORT=3306", "LOG_DIR="+t.TempDir())
	return cmd
}

// newSignedStreamFixture is a dispatched API-mode physical backup whose server logged in with
// the jobs key, so its receiver demands the signed stream header.
func newSignedStreamFixture(t *testing.T) (*Cluster, *ServerMonitor, string, string) {
	t.Helper()
	cl, server := newPhysicalBackupFixture(t, "api")
	cl.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	cl.Conf.APISecretLoginWindow = 300
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	server.Pass = "s3cr3t"
	jk := cl.GetJobsKey()
	if _, _, code, err := server.OpenJobBody(scriptJobBodyKey(t, `{"server":"127.0.0.1:3306"}`, server.Pass, jk), time.Now(), false); code != 200 {
		t.Fatalf("login with the jobs key: %d %v", code, err)
	}
	before := sstConnectionPorts(t)
	t.Cleanup(func() { closeAllNewReceivers(t, server, before) })
	if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	port, err := server.PhysicalBackupReceiverPort(config.ConstBackupPhysicalTypeMariaBackup)
	if err != nil {
		t.Fatal(err)
	}
	return cl, server, port, jk
}

func receiverOf(t *testing.T, port string) *SST {
	t.Helper()
	p, _ := strconv.Atoi(port)
	SSTs.Lock()
	defer SSTs.Unlock()
	return SSTs.SSTconnections[p]
}

// TestPhysicalBackupRealProducerTail: the way socat ends its stream is what the receiver calls a
// complete one. A socat that finishes shuts the connection down with EOF after every byte: no
// read error, the backup completes. A socat killed in the middle of the stream also ends it with
// EOF (the receiver cannot tell it from the end); the producer then fails (SIGPIPE), the job
// reports an error, and the backup is not completed and the previous one comes back.
func TestPhysicalBackupRealProducerTail(t *testing.T) {
	t.Run("socat finishes", func(t *testing.T) {
		cl, server, port, jk := newSignedStreamFixture(t)
		task := config.ConstBackupPhysicalTypeMariaBackup
		server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
		payload := bytes.Repeat(physicalReceiverPayload(), 80) // 5 MB, over the pipe and socket buffers
		file := filepath.Join(t.TempDir(), "stream")
		if err := os.WriteFile(file, payload, 0600); err != nil {
			t.Fatal(err)
		}
		sst := receiverOf(t, port)
		if out, err := producerTail(t, "cat "+file, port, jk).CombinedOutput(); err != nil {
			t.Fatalf("producer tail: %v: %s", err, out)
		}
		waitReceiverGone(t, port)
		waitBackupStateReleased(t, cl, server)
		server.MarkBackupPhysicalDone(task)

		if sst.rcvErr != nil || sst.rcvBytes != int64(len(payload)) {
			t.Fatalf("receiver saw %d of %d bytes, read error %v", sst.rcvBytes, len(payload), sst.rcvErr)
		}
		server.backupMetaMutex.Lock()
		completed := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
		server.backupMetaMutex.Unlock()
		if !completed || !server.HasBackupMariabackupCookie() {
			t.Fatalf("a stream ended by a finished socat must complete the backup: completed=%v cookie=%v", completed, server.HasBackupMariabackupCookie())
		}
		dest := server.GetMyBackupDirectory() + task + ".xbtream.gz"
		if out := gunzipFile(t, dest); !bytes.Equal(out, payload) {
			t.Fatalf("backup holds %d bytes, want %d", len(out), len(payload))
		}
	})

	t.Run("socat killed midway", func(t *testing.T) {
		cl, server, port, jk := newSignedStreamFixture(t)
		task := config.ConstBackupPhysicalTypeMariaBackup
		cl.Conf.BackupKeepUntilValid = false
		server.JobsUpdateState(task, "error", JobStateErrorExec, 1) // the producer fails on SIGPIPE
		sst := receiverOf(t, port)
		cmd := producerTail(t, "dd if=/dev/zero bs=1M count=2000 2>/dev/null", port, jk)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		dest := server.GetMyBackupDirectory() + task + ".xbtream.gz"
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		exec.Command("pkill", "-KILL", "-f", "socat -u stdio TCP:127.0.0.1:"+port).Run()
		cmd.Wait()
		waitReceiverGone(t, port)
		waitBackupStateReleased(t, cl, server)

		if sst.rcvErr != nil {
			t.Logf("the receiver saw a read error: %v", sst.rcvErr)
		} else {
			t.Log("the receiver saw a clean EOF from the killed socat")
		}
		server.backupMetaMutex.Lock()
		completed := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
		server.backupMetaMutex.Unlock()
		if completed || server.HasBackupMariabackupCookie() {
			t.Fatalf("a stream cut by a killed socat, its job in error, must not complete the backup: completed=%v cookie=%v", completed, server.HasBackupMariabackupCookie())
		}
	})
}

// encryptingBackup is a physical backup whose stream ended cleanly and whose completion
// (completeReceivedPhysicalBackup) is held inside the encryption of its staging file: the staging
// path is a named pipe, which EncryptFile opens before it reads, so the completion is provably
// inside the encryption when the test's open for writing returns.
type encryptingBackup struct {
	t       *testing.T
	cl      *Cluster
	server  *ServerMonitor
	meta    *backupmgr.BackupMetadata
	logical string
	w       *os.File
	done    chan struct{}
}

func newEncryptingBackup(t *testing.T) *encryptingBackup {
	t.Helper()
	cl, server := newPhysicalBackupFixture(t, "api")
	cl.Conf.BackupEncryption = true
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:s3cr3t"}})
	task := config.ConstBackupPhysicalTypeMariaBackup
	logical := server.GetMyBackupDirectory() + task + ".xbtream.gz"
	if err := os.MkdirAll(filepath.Dir(logical), 0750); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(logical+partialSuffixForCleanup, 0600); err != nil {
		t.Skipf("no named pipe here: %v", err)
	}
	meta := &backupmgr.BackupMetadata{Id: 1, BackupTool: task, Dest: logical + partialSuffixForCleanup, Source: server.URL, Compressed: true}
	server.LastBackupMeta.Physical = meta
	cl.BackupMetaMap.Set(meta.Id, meta)
	e := &encryptingBackup{t: t, cl: cl, server: server, meta: meta, logical: logical, done: make(chan struct{})}
	go func() {
		defer close(e.done)
		server.completeReceivedPhysicalBackup(meta)
	}()
	opened := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(logical+partialSuffixForCleanup, os.O_WRONLY, 0)
		e.w = w
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("open the staging pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the completion did not reach the encryption of the staging file")
	}
	return e
}

// finish lets the encryption read its source to the end and waits for the completion to return.
func (e *encryptingBackup) finish() {
	e.t.Helper()
	e.w.Write(physicalReceiverPayload())
	e.w.Close()
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		e.t.Fatal("the completion did not return")
	}
}

func (e *encryptingBackup) state() (completed bool) {
	e.server.backupMetaMutex.Lock()
	defer e.server.backupMetaMutex.Unlock()
	return e.meta.Completed
}

// The job's error arrives while the artifact is being encrypted: the failure is not seen by the
// withdraw (nothing is published yet), the artifact is published after it. Once published, an
// artifact of a job that failed is withdrawn and the backup is not completed.
func TestPhysicalBackupJobErrorDuringEncryptionWithdrawsArtifact(t *testing.T) {
	e := newEncryptingBackup(t)
	task := config.ConstBackupPhysicalTypeMariaBackup
	e.server.JobsUpdateState(task, "error", JobStateErrorExec, 1) // inside the encryption
	e.finish()

	if _, err := os.Stat(e.logical + ".enc"); err == nil {
		t.Error("the artifact of a job that reported an error during its encryption is published")
	}
	if e.state() {
		t.Error("the backup of a job that reported an error during its encryption is completed")
	}
}

// The job's done arrives while the artifact is being encrypted: nothing is received yet, so
// no cookie; the backup is completed, and the cookie set, once it is published.
func TestPhysicalBackupDoneDuringEncryptionSetsCookieOnlyOncePublished(t *testing.T) {
	e := newEncryptingBackup(t)
	task := config.ConstBackupPhysicalTypeMariaBackup
	e.server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
	e.server.MarkBackupPhysicalDone(task) // inside the encryption
	if e.server.HasBackupMariabackupCookie() {
		t.Error("the cookie is set while the artifact is still being encrypted")
	}
	e.finish()
	if !e.state() {
		t.Fatal("the backup is not completed once its artifact is published")
	}
	e.server.MarkBackupPhysicalDone(task)
	if !e.server.HasBackupMariabackupCookie() {
		t.Error("no cookie once the artifact is published")
	}
	if _, err := os.Stat(e.logical + ".enc"); err != nil {
		t.Errorf("the artifact is not published: %v", err)
	}
}

// The job's done arrives while the artifact is being encrypted, then the encryption fails: the
// failed backup has no cookie of its own (a cookie of the previous backup, if any, is not
// touched: it describes the artifact that is still there).
func TestPhysicalBackupEncryptionFailureAfterDoneLeavesNoCookie(t *testing.T) {
	e := newEncryptingBackup(t)
	task := config.ConstBackupPhysicalTypeMariaBackup
	e.server.JobsUpdateState(task, "completed", JobStateSuccess, 1)
	e.server.MarkBackupPhysicalDone(task)                    // inside the encryption
	if err := os.Mkdir(e.logical+".enc", 0700); err != nil { // the artifact can not be put in place
		t.Fatal(err)
	}
	e.finish()
	e.server.MarkBackupPhysicalDone(task) // the receiver's end, the done callback again

	if e.state() {
		t.Error("a backup whose encryption failed is completed")
	}
	if e.server.HasBackupMariabackupCookie() {
		t.Error("a backup whose encryption failed has the backup cookie")
	}
	if _, err := os.Stat(e.logical + partialSuffixForCleanup); err == nil {
		t.Error("the plaintext staging of a failed encryption is still on disk")
	}
}

// With backup-keep-until-valid and an encrypted previous backup (renamed .enc.old when this
// run started), a job that reports an error while its artifact is being encrypted gives the
// previous backup back: the new artifact is withdrawn, not left beside it or over it.
func TestPhysicalBackupJobErrorDuringEncryptionKeepUntilValidRestoresPrevious(t *testing.T) {
	e := newEncryptingBackup(t)
	task := config.ConstBackupPhysicalTypeMariaBackup
	e.cl.Conf.BackupKeepUntilValid = true
	previous := []byte("previous encrypted backup")
	if err := os.WriteFile(e.logical+".enc.old", previous, 0600); err != nil {
		t.Fatal(err)
	}
	e.server.JobsUpdateState(task, "error", JobStateErrorExec, 1) // inside the encryption
	e.finish()
	e.server.writeBackupMetadata(backupmgr.BackupMethodPhysical, true) // what the receiver's end does next

	got, err := os.ReadFile(e.logical + ".enc")
	if err != nil || !bytes.Equal(got, previous) {
		t.Fatalf("the previous encrypted backup must be back under its name: %q, %v", got, err)
	}
	if _, err := os.Stat(e.logical + ".enc.old"); err == nil {
		t.Error("the previous backup is still renamed .enc.old")
	}
	if e.server.HasBackupMariabackupCookie() {
		t.Error("the failed backup has the backup cookie")
	}
}
