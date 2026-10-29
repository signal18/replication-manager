package cluster

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// The sysbench command line of a MariaDB/MySQL cluster is the one 3.1.42 issued, option for
// option; a PostgreSQL master switches the driver and the option prefix.
func TestSysbenchConnectionArgsByFlavor(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{Secrets: config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "app:secret"}})}, VersionsMap: config.NewVersionsMap()}
	prx := &Proxy{Host: "prx1", WritePort: 3306}
	want := "--db-driver=mysql --mysql-db=replication_manager_schema --mysql-user=app --mysql-password=secret --mysql-host=prx1 --mysql-port=3306"
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != want {
		t.Fatalf("MariaDB/MySQL without master:\n got  %s\n want %s", got, want)
	}
	m := &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "MariaDB", Major: 11}}
	cl.master = m
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != want {
		t.Fatalf("MariaDB master:\n got  %s\n want %s", got, want)
	}
	pg := &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "PostgreSQL", Major: 17}, PostgressDB: "postgres"}
	cl.master = pg
	prx.WritePort = 5432
	wantPg := "--db-driver=pgsql --pgsql-db=postgres --pgsql-user=app --pgsql-password=secret --pgsql-host=prx1 --pgsql-port=5432"
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != wantPg {
		t.Fatalf("PostgreSQL master:\n got  %s\n want %s", got, wantPg)
	}
}

// sysbench 1.1 connects with TLS when the master needs it (#2235): skip-verify without a
// client certificate when the master requires TLS or is monitored over it, the CA when the
// client TLS decision verifies the server; sysbench 1.0 keeps its plain arguments.
func TestSysbenchTLS(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{Secrets: config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "app:secret"}})}, VersionsMap: config.NewVersionsMap()}
	m := &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}, Variables: config.NewStringsMap()}
	cl.master = m
	tls := func() string {
		args, env := cl.sysbenchTLS()
		return strings.Join(args, " ") + "|" + strings.Join(env, " ")
	}
	cl.VersionsMap.Set("sysbench", &version.Version{Flavor: "sysbench", Major: 1, Minor: 0, Release: 20})
	m.Variables.Set("REQUIRE_SECURE_TRANSPORT", "ON")
	if got := tls(); got != "|" {
		t.Fatalf("sysbench 1.0: got %q, want no TLS option", got)
	}
	cl.VersionsMap.Set("sysbench", &version.Version{Flavor: "sysbench", Major: 1, Minor: 1})
	skipVerify := "--mysql-ssl=on|MARIADB_TLS_DISABLE_PEER_VERIFICATION=1"
	if got := tls(); got != skipVerify {
		t.Fatalf("require_secure_transport: got %q, want %q", got, skipVerify)
	}
	m.Variables.Set("REQUIRE_SECURE_TRANSPORT", "OFF")
	if got := tls(); got != "|" {
		t.Fatalf("plain master: got %q, want no TLS option", got)
	}
	m.ForceTLSSkipVerify = true // the monitor fell back to TLS on error 3159
	if got := tls(); got != skipVerify {
		t.Fatalf("monitored over TLS: got %q, want %q", got, skipVerify)
	}
	if got := strings.Join(cl.sysbenchConnectionArgs(&Proxy{Host: "prx1", WritePort: 3306}), " "); !strings.HasSuffix(got, " --mysql-port=3306 --mysql-ssl=on") {
		t.Fatalf("connection args do not end with the TLS option: %s", got)
	}
	cl.master = &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "PostgreSQL", Major: 17}, ForceTLSSkipVerify: true}
	if got := tls(); got != "|" {
		t.Fatalf("PostgreSQL: got %q, want no mysql TLS option", got)
	}
}

// The client TLS decision is read back from either syntax GetSSLClientParam writes.
func TestSSLClientMode(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--skip-ssl"}, "DISABLED"},
		{[]string{"--ssl=FALSE"}, "DISABLED"},
		{[]string{"--ssl", "--ssl-verify-server-cert=false"}, "REQUIRED"},
		{[]string{"--ssl", "--ssl-verify-server-cert", "--ssl-ca=/w/ca-cert.pem"}, "VERIFY_CA /w/ca-cert.pem"},
		{[]string{"--ssl", "--ssl-verify-server-cert", "--ssl-ca=/w/ca.pem", "--ssl-cert=/w/c.pem", "--ssl-key=/w/k.pem"}, "VERIFY_IDENTITY /w/ca.pem"},
		{[]string{"--ssl-mode=verify_ca", "--ssl-ca=/w/ca.pem"}, "VERIFY_CA /w/ca.pem"},
	} {
		mode, ca, _, _ := sslClientMode(c.args)
		if got := strings.TrimSpace(mode + " " + ca); got != c.want {
			t.Errorf("sslClientMode(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// A failed sysbench command is reported by its FATAL lines, the password masked, and a
// failed benchmark is WARN0270 until one runs through.
func TestSysbenchFailureState(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{Secrets: config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "app:secret"}})}, VersionsMap: config.NewVersionsMap()}
	cmd := exec.Command("sh", "-c", "echo 'sysbench 1.1.0'; echo 'FATAL: unable to connect, password secret'; echo 'FATAL: error 3159: insecure transport'; exit 1")
	_, err := cl.sysbenchOutput("cleanup", cmd)
	want := "sysbench cleanup: FATAL: unable to connect, password XXXX FATAL: error 3159: insecure transport"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	cl.setBenchmarkFailure(errors.New("sysbench run: FATAL: x"))
	if failure, _ := cl.benchmarkFailure.Load().(string); !strings.HasSuffix(failure, ": sysbench run: FATAL: x") {
		t.Fatalf("failure not recorded: %q", failure)
	}
	cl.setBenchmarkFailure(nil)
	if failure, _ := cl.benchmarkFailure.Load().(string); failure != "" {
		t.Fatalf("failure not cleared by a run that went through: %q", failure)
	}
}
