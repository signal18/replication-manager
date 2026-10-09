package cluster

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func otpTestCluster(t *testing.T) (*Cluster, map[string]string) {
	t.Helper()
	pushed := map[string]string{}
	cl := &Cluster{Name: "c1", Conf: &config.Config{}}
	cl.bootstrapOTPPusher = func(key, value string) error { pushed[key] = value; return nil }
	return cl, pushed
}

// The first password is delivered to the per-service key and only its hash is kept; a
// second ensure does not replace it.
func TestBootstrapOTPEnsure(t *testing.T) {
	cl, pushed := otpTestCluster(t)
	dir := t.TempDir()
	if cl.BootstrapOTPDelivered(dir) {
		t.Fatal("nothing delivered yet")
	}
	if err := cl.EnsureBootstrapOTP("c1/svc/db1", dir); err != nil {
		t.Fatal(err)
	}
	otp := pushed["bootstrap-otp-db1"]
	if len(otp) < 40 || !cl.BootstrapOTPDelivered(dir) {
		t.Fatalf("a random value goes to bootstrap-otp-db1 and is marked delivered: %q", otp)
	}
	stored, _ := os.ReadFile(bootstrapOTPFile(dir))
	if strings.Contains(string(stored), otp) || string(stored) != hashBootstrapOTP(otp) {
		t.Fatal("only the hash of the password is stored")
	}
	if fi, _ := os.Stat(bootstrapOTPFile(dir)); fi.Mode().Perm() != 0600 {
		t.Fatalf("hash file must be 0600, got %v", fi.Mode().Perm())
	}
	if err := cl.EnsureBootstrapOTP("c1/svc/db1", dir); err != nil || pushed["bootstrap-otp-db1"] != otp {
		t.Fatal("an existing password is not replaced by ensure")
	}
}

// A password works once: it is rotated on use, the old one is refused afterwards, the
// new one works; a wrong or empty one is refused and rotates nothing.
func TestBootstrapOTPConsumeRotates(t *testing.T) {
	cl, pushed := otpTestCluster(t)
	dir := t.TempDir()
	if err := cl.EnsureBootstrapOTP("c1/svc/db1", dir); err != nil {
		t.Fatal(err)
	}
	first := pushed["bootstrap-otp-db1"]
	for _, bad := range []string{"", "nope", first + "x"} {
		if cl.ConsumeBootstrapOTP("c1/svc/db1", dir, bad) {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if pushed["bootstrap-otp-db1"] != first {
		t.Fatal("a refused password rotates nothing")
	}
	if !cl.ConsumeBootstrapOTP("c1/svc/db1", dir, first) {
		t.Fatal("the delivered password is accepted")
	}
	second := pushed["bootstrap-otp-db1"]
	if second == first {
		t.Fatal("a used password is rotated")
	}
	if cl.ConsumeBootstrapOTP("c1/svc/db1", dir, first) {
		t.Fatal("a used password is refused the second time")
	}
	if !cl.ConsumeBootstrapOTP("c1/svc/db1", dir, second) {
		t.Fatal("the rotated password is accepted")
	}
	// another service's password never opens this one
	other := t.TempDir()
	if err := cl.EnsureBootstrapOTP("c1/svc/db2", other); err != nil {
		t.Fatal(err)
	}
	if cl.ConsumeBootstrapOTP("c1/svc/db1", dir, pushed["bootstrap-otp-db2"]) {
		t.Fatal("db2's password must not open db1's config")
	}
}

// When the next password cannot be delivered, the presented one stays valid: a restart is
// never left without a credential.
func TestBootstrapOTPRotationFailureKeepsCurrent(t *testing.T) {
	cl, pushed := otpTestCluster(t)
	dir := t.TempDir()
	if err := cl.EnsureBootstrapOTP("c1/svc/prx1", dir); err != nil {
		t.Fatal(err)
	}
	cur := pushed["bootstrap-otp-prx1"]
	cl.bootstrapOTPPusher = func(string, string) error { return errors.New("orchestrator down") }
	if !cl.ConsumeBootstrapOTP("c1/svc/prx1", dir, cur) {
		t.Fatal("the current password is accepted even when the rotation fails")
	}
	if !cl.ConsumeBootstrapOTP("c1/svc/prx1", dir, cur) {
		t.Fatal("an undelivered rotation keeps the current password valid")
	}
	if err := cl.EnsureBootstrapOTP("c1/svc/prx2", t.TempDir()); err == nil {
		t.Fatal("a first delivery that fails is an error (the definition keeps the admin login)")
	}
}

// The init container maps only the service's password, no admin login.
func TestApplyBootstrapOTPEnv(t *testing.T) {
	init := map[string]string{
		"secrets_environment": "env/REPLICATION_MANAGER_PASSWORD",
		"configs_environment": "env/REPLICATION_MANAGER_USER env/REPLICATION_MANAGER_URL",
	}
	applyBootstrapOTPEnv(init, "c1/svc/db1")
	if init["secrets_environment"] != "REPLICATION_MANAGER_OTP=env/bootstrap-otp-db1" {
		t.Fatalf("secrets_environment: %q", init["secrets_environment"])
	}
	if strings.Contains(init["configs_environment"], "REPLICATION_MANAGER_USER") || !strings.Contains(init["configs_environment"], "REPLICATION_MANAGER_URL") {
		t.Fatalf("configs_environment: %q", init["configs_environment"])
	}
	if bootstrapOTPKey("db1") != "bootstrap-otp-db1" {
		t.Fatal("a bare name is its own key suffix")
	}
}

// The proxy section maps the password once delivered, the admin login before.
func TestProxyInitContainerUsesOTPOnceDelivered(t *testing.T) {
	cl := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cl)
	cl.Conf = &config.Config{ProvType: "docker", ProvProxType: "docker", ProvProxDiskType: "volume"}
	pushed := map[string]string{}
	cl.bootstrapOTPPusher = func(key, value string) error { pushed[key] = value; return nil }
	prx := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cl, Name: "prx1", ServiceName: "c1/svc/prx1", Datadir: t.TempDir()}}
	if got := cl.OpenSVCGetProxyTemplateSectionMap("db1:3306", prx)["container#02"]["secrets_environment"]; got != "env/REPLICATION_MANAGER_PASSWORD" {
		t.Fatalf("before delivery the admin login stays: %q", got)
	}
	if err := cl.EnsureBootstrapOTP(prx.GetServiceName(), prx.GetDatadir()); err != nil {
		t.Fatal(err)
	}
	if got := cl.OpenSVCGetProxyTemplateSectionMap("db1:3306", prx)["container#02"]["secrets_environment"]; got != "REPLICATION_MANAGER_OTP=env/bootstrap-otp-prx1" {
		t.Fatalf("after delivery the one-time password: %q", got)
	}
}
