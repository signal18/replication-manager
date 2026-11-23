package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestK8SJobsKeyInitCommandInAlpine runs the init container's command, generated with the jobs key, in the very image of
// that init container (BusyBox sh and wget), because a string comparison cannot tell whether the composition behaves:
// stub wget and CLI stand for replication-manager, the key is a file at /credentials/JOBS_API_KEY as the Secret volume
// projects it. It checks, with the real shell:
//
//   - the CLI is downloaded first, runs, and fetches the config (job config) with the names of the server and the key it
//     finds in the file; the archive replaces conf.d and init, the CLI is kept for the dbjobs container; no wget call
//     carries an Authorization header or reaches the config route;
//   - a refused login is written to the container log and leaves the files as they were (exit 0, as before);
//   - a download that fails uses the copy a previous start left on the volume; with none, the config is not fetched.
//
// It needs a Docker daemon and the init image (it skips otherwise, and under -short).
func TestK8SJobsKeyInitCommandInAlpine(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("no Docker daemon")
	}
	c := jobsKeyTestCluster(true)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	image := dep.Spec.Template.Spec.InitContainers[0].Image
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		if out, err := exec.Command("docker", "pull", "-q", image).CombinedOutput(); err != nil {
			t.Skipf("init image %q is not available: %s", image, strings.TrimSpace(string(out)))
		}
	}
	initScript := dep.Spec.Template.Spec.InitContainers[0].Command[2]

	write := func(t *testing.T, path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	const stubWget = `#!/bin/sh
echo "wget $*" >> /stub/wget.log
out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in -qO) out=$2; shift 2;; -T) shift 2;; --header=*|--no-check-certificate) shift;; *) url=$1; shift;; esac
done
case "$url" in
  */static/configurator/bin/replication-manager-cli) [ -f /stub/download ] && cp /stub/cli "$out"; exit $? ;;
esac
exit 1
`
	const stubCLI = `#!/bin/sh
case "$1" in
  version) echo stub-cli; exit 0 ;;
  job)
    [ "$2" = config ] || exit 9
    echo "url=$REPLICATION_MANAGER_URL cluster=$REPLICATION_MANAGER_CLUSTER_NAME server=$REPLICATION_MANAGER_HOST_NAME port=$REPLICATION_MANAGER_HOST_PORT key=$(cut -c1-4 /credentials/JOBS_API_KEY)" >> /stub/cli.log
    [ -f /stub/refuse ] && { echo "replication-manager-cli: jobs login refused" >&2; exit 2; }
    mkdir -p /tmp/cx/etc/mysql/conf.d /tmp/cx/init
    echo "[mysqld]" > /tmp/cx/etc/mysql/conf.d/fetched.cnf
    echo "#!/bin/sh" > /tmp/cx/init/dbjobs_new
    (cd /tmp/cx && tar czf "$3" etc init)
    exit 0 ;;
esac
exit 9
`
	// scenario: which stub files exist, and whether a CLI is already on the volume
	run := func(t *testing.T, download, refuse, cachedCLI bool) (out, wgetLog, cliLog string) {
		t.Helper()
		dir := t.TempDir()
		write(t, filepath.Join(dir, "wget"), stubWget, 0755)
		write(t, filepath.Join(dir, "cli"), stubCLI, 0755)
		write(t, filepath.Join(dir, "key"), c.GetJobsKey()+"\n", 0400)
		if download {
			write(t, filepath.Join(dir, "download"), "", 0644)
		}
		if refuse {
			write(t, filepath.Join(dir, "refuse"), "", 0644)
		}
		script := "mkdir -p /etc/mysql/conf.d /docker-entrypoint-initdb.d /var/lib/mysql; echo old > /etc/mysql/conf.d/old.cnf; "
		if cachedCLI {
			script += "cp /stub/cli /docker-entrypoint-initdb.d/replication-manager-cli; "
		}
		script += "( " + initScript + " ); echo \"exit=$?\"; echo \"conf.d: $(ls /etc/mysql/conf.d | tr '\\n' ' ')\"; " +
			"echo \"init: $(ls /docker-entrypoint-initdb.d | tr '\\n' ' ')\""
		args := []string{"run", "--rm", "--network", "none"}
		// what Kubernetes injects from the Deployment: the init container's own environment, no secret
		for _, e := range dep.Spec.Template.Spec.InitContainers[0].Env {
			if e.ValueFrom != nil {
				t.Fatalf("env %s is not a plain value: %+v", e.Name, e.ValueFrom)
			}
			args = append(args, "-e", e.Name+"="+e.Value)
		}
		args = append(args,
			"-v", dir+":/stub",
			"-v", filepath.Join(dir, "wget")+":/usr/local/bin/wget:ro",
			"-v", filepath.Join(dir, "key")+":/credentials/JOBS_API_KEY:ro",
			image, "sh", "-c", script)
		b, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run failed: %v\n%s", err, b)
		}
		wl, _ := os.ReadFile(filepath.Join(dir, "wget.log"))
		cl, _ := os.ReadFile(filepath.Join(dir, "cli.log"))
		return string(b), string(wl), string(cl)
	}

	t.Run("the CLI is downloaded and fetches the config with the key", func(t *testing.T) {
		out, wgetLog, cliLog := run(t, true, false, false)
		if !strings.Contains(out, "exit=0") {
			t.Fatalf("the init container exits 0:\n%s", out)
		}
		if !strings.Contains(out, "conf.d: fetched.cnf") || strings.Contains(out, "old.cnf") {
			t.Fatalf("the fetched config replaces conf.d:\n%s", out)
		}
		if !strings.Contains(out, "dbjobs_new") || !strings.Contains(out, "replication-manager-cli") {
			t.Fatalf("the init directory holds the config's scripts and the CLI:\n%s", out)
		}
		wantCLI := "url=https://repman.s18.svc.cloud18:10005 cluster=k8stest server=db1 port=3306 key=" + c.GetJobsKey()[:4]
		if !strings.Contains(cliLog, wantCLI) {
			t.Fatalf("the CLI is run with the server's names and finds the key file, want %q in:\n%s", wantCLI, cliLog)
		}
		if strings.Count(wgetLog, "wget") != 1 || !strings.Contains(wgetLog, "/static/configurator/bin/replication-manager-cli") ||
			strings.Contains(wgetLog, "Authorization") || strings.Contains(wgetLog, "/config") && !strings.Contains(wgetLog, "/static/configurator") {
			t.Fatalf("one public download of the CLI and no authenticated wget, got:\n%s", wgetLog)
		}
		if strings.Contains(out+wgetLog+cliLog, c.GetJobsKey()) {
			t.Fatalf("the key is written in the logs")
		}
	})
	t.Run("a refused login is in the container log and changes nothing", func(t *testing.T) {
		out, _, _ := run(t, true, true, false)
		if !strings.Contains(out, "jobs login refused") {
			t.Fatalf("the CLI's reason is in the output:\n%s", out)
		}
		if !strings.Contains(out, "exit=0") || !strings.Contains(out, "conf.d: old.cnf") || strings.Contains(out, "fetched.cnf") {
			t.Fatalf("the files stay as they were and the init exits 0:\n%s", out)
		}
	})
	t.Run("a failed download uses the CLI a previous start left on the volume", func(t *testing.T) {
		out, _, cliLog := run(t, false, false, true)
		if !strings.Contains(out, "conf.d: fetched.cnf") || !strings.Contains(cliLog, "cluster=k8stest") {
			t.Fatalf("the cached CLI fetches the config:\n%s\n%s", out, cliLog)
		}
	})
	t.Run("no CLI at all: nothing is fetched and the files stay", func(t *testing.T) {
		out, _, cliLog := run(t, false, false, false)
		if !strings.Contains(out, "exit=0") || !strings.Contains(out, "conf.d: old.cnf") || cliLog != "" {
			t.Fatalf("no fetch without a CLI:\n%s\n%s", out, cliLog)
		}
	})
}
