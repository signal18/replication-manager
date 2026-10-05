package cluster

import (
	"os/exec"
	"strings"
	"testing"
)

// TestK8SDatabaseInitOwnershipCommandInAlpine runs the ownership command that the
// Kubernetes init container ends with (k8sDBIdentity, generated for the
// configured pair) in the very image of that init container, because a string
// comparison cannot tell whether BusyBox's find -exec chown -h behaves as intended:
//
//   - numeric ids with no passwd entry, a mixed UID/GID, nested directories;
//   - symlinks are chowned themselves and their target is left alone;
//   - a path that cannot be chowned (read-only) is reported on stderr and does not
//     change the exit status, which stays the mkdir status;
//   - a volume that already has the right owner is not touched (ctime unchanged).
//
// It needs a Docker daemon and the init image (it skips otherwise, and under -short).
func TestK8SDatabaseInitOwnershipCommandInAlpine(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("no Docker daemon")
	}
	probe := newTestCluster("k8stest")
	image := probe.k8sDatabaseDeployment(&ServerMonitor{Name: "db1", Port: "3306"}, 3306, "node-a").Spec.Template.Spec.InitContainers[0].Image
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		if out, err := exec.Command("docker", "pull", "-q", image).CombinedOutput(); err != nil {
			t.Skipf("init image %q is not available: %s", image, strings.TrimSpace(string(out)))
		}
	}

	// the generated fragment for a pair, ready to be placed after "MKDIR_STATUS=$?"
	fragment := func(uid, gid string) string {
		c := newTestCluster("k8stest")
		c.Conf.ProvDBVolumeUID = uid + ":" + gid
		_, cmd, _ := c.k8sDBIdentity()
		return cmd
	}
	run := func(t *testing.T, script string, docker ...string) string {
		t.Helper()
		args := append([]string{"run", "--rm", "--network", "none"}, docker...)
		args = append(args, image, "sh", "-c", script)
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run failed: %v\n%s", err, out)
		}
		return string(out)
	}
	const prologue = "mkdir -p /var/lib/mysql; MKDIR_STATUS=$?; cd /var/lib/mysql; "

	t.Run("numeric ids without a passwd entry, nested, root and foreign owners", func(t *testing.T) {
		out := run(t, prologue+`mkdir -p d/sub .system/jobs; touch f d/sub/g .system/jobs/j; chown -R 0:0 .; chown 999:999 d d/sub`+
			fragment("1234", "1235")+`; echo "bad=$(find /var/lib/mysql \( ! -user 1234 -o ! -group 1235 \) | wc -l) exit=$MKDIR_STATUS"`)
		if !strings.Contains(out, "bad=0 exit=0") || strings.Contains(out, "WARNING") {
			t.Fatalf("every entry must be 1234:1235 with exit 0 and no warning, got:\n%s", out)
		}
	})
	t.Run("mixed uid and gid", func(t *testing.T) {
		out := run(t, prologue+`mkdir -p d; touch d/f; chown -R 1001:1001 .`+
			fragment("999", "1001")+`; echo "bad=$(find /var/lib/mysql \( ! -user 999 -o ! -group 1001 \) | wc -l) exit=$MKDIR_STATUS"`)
		if !strings.Contains(out, "bad=0 exit=0") {
			t.Fatalf("every entry must be 999:1001, got:\n%s", out)
		}
	})
	t.Run("root", func(t *testing.T) {
		out := run(t, prologue+`mkdir -p d; touch d/f; chown -R 1001:1001 .`+
			fragment("0", "0")+`; echo "bad=$(find /var/lib/mysql \( ! -user 0 -o ! -group 0 \) | wc -l) exit=$MKDIR_STATUS"`)
		if !strings.Contains(out, "bad=0 exit=0") {
			t.Fatalf("every entry must be 0:0, got:\n%s", out)
		}
	})
	t.Run("symlinks are chowned and their targets left alone", func(t *testing.T) {
		out := run(t, prologue+`ln -s /etc/passwd lnk; ln -s /nonexistent dangling; chown -h 0:0 lnk dangling; touch f; chown 0:0 f`+
			fragment("1234", "1234")+`; echo "lnk=$(stat -c %u:%g lnk) dangling=$(stat -c %u:%g dangling) passwd=$(stat -c %u:%g /etc/passwd) exit=$MKDIR_STATUS"`)
		if !strings.Contains(out, "lnk=1234:1234 dangling=1234:1234 passwd=0:0 exit=0") {
			t.Fatalf("links must be 1234:1234 and /etc/passwd untouched, got:\n%s", out)
		}
	})
	t.Run("a path that cannot be chowned is reported and does not fail the init container", func(t *testing.T) {
		out := run(t, prologue+"true"+fragment("1234", "1234")+`; echo "exit=$MKDIR_STATUS"`, "-v", t.TempDir()+":/var/lib/mysql/ro:ro")
		// an empty read-only mount is owned by the test's own uid, which differs from 1234
		if !strings.Contains(out, "WARNING: could not set the owner of /var/lib/mysql to 1234:1234") || !strings.Contains(out, "exit=0") {
			t.Fatalf("expected the warning and exit=0, got:\n%s", out)
		}
	})
	t.Run("an already correct volume is not touched", func(t *testing.T) {
		out := run(t, prologue+`mkdir -p d; touch f d/g; chown -R 1234:1234 .; stat -c %Z f d/g > /tmp/before; sleep 1.2`+
			fragment("1234", "1234")+`; stat -c %Z f d/g > /tmp/after; cmp -s /tmp/before /tmp/after && echo untouched=yes || echo untouched=NO; echo "exit=$MKDIR_STATUS"`)
		if !strings.Contains(out, "untouched=yes") || strings.Contains(out, "WARNING") {
			t.Fatalf("a correct volume must not be rewritten, got:\n%s", out)
		}
	})
}
