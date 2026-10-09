package configurator

import (
	"strings"
	"testing"
)

const testRootPw = "S3cr3t-Root-Pw"

func TestFilterRootPasswordGaleraSST(t *testing.T) {
	in := "[mysqld]\nwsrep_on=ON\nwsrep_sst_method=mariabackup\nwsrep_sst_auth=root:" + testRootPw + "\n"
	out, changed := FilterRootPassword("/data/init/etc/mysql/replication-manager.d/with_rep_wsrep.cnf", in, testRootPw)
	if strings.Contains(out, testRootPw) {
		t.Fatalf("password left in the SST config:\n%s", out)
	}
	if !strings.Contains(out, "\nwsrep_sst_auth=mysql:\n") || !strings.Contains(out, "wsrep_sst_method=mariabackup") {
		t.Fatalf("SST auth not switched to the socket account:\n%s", out)
	}
	if len(changed) != 1 || changed[0] != "wsrep_sst_auth" {
		t.Fatalf("changed = %v", changed)
	}
}

func TestFilterRootPasswordOtherFragments(t *testing.T) {
	cases := map[string]string{
		"with_xtrabackup.cnf": "[xtrabackup]\nparallel=4\nuser=root\npassword=" + testRootPw + "\n",
		"dbjob.cnf":           "[client]\nuser=root\npassword = " + testRootPw + "\nsocket=/run/mysqld/mysqld.sock",
		// a fragment the moduleset might add one day, under any name and key
		"future.cnf": "[mysqld]\nsome-plugin-secret=" + testRootPw + "\n",
	}
	for name, in := range cases {
		out, changed := FilterRootPassword("/data/init/etc/mysql/"+name, in, testRootPw)
		if strings.Contains(out, testRootPw) {
			t.Errorf("%s: password left:\n%s", name, out)
		}
		if len(changed) != 1 {
			t.Errorf("%s: changed = %v", name, changed)
		}
	}
	out, _ := FilterRootPassword("/x/future.cnf", cases["future.cnf"], testRootPw)
	if !strings.Contains(out, "# some_plugin_secret: removed by replication-manager") {
		t.Errorf("removed line not named:\n%s", out)
	}
}

func TestFilterRootPasswordLeavesTheRest(t *testing.T) {
	secret := "export MYSQL_ROOT_PASSWORD=" + testRootPw + "\n"
	if out, changed := FilterRootPassword("/data/init/init/MYSQL_ROOT_PASSWORD", secret, testRootPw); out != secret || changed != nil {
		t.Errorf("init secret file changed: %v", changed)
	}
	clean := "[mysqld]\nmax_connections=500\n"
	if out, changed := FilterRootPassword("/x/a.cnf", clean, testRootPw); out != clean || changed != nil {
		t.Errorf("clean file changed")
	}
	if out, changed := FilterRootPassword("/x/a.cnf", clean, ""); out != clean || changed != nil {
		t.Errorf("empty password must not filter")
	}
}
