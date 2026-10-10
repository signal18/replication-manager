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

// A short or common password must not comment out lines that only share its letters
// (review of #1962): user names, paths, comments and section headers stay.
func TestFilterRootPasswordCommonPasswordNoOverMatch(t *testing.T) {
	in := "[mysqld]\nuser=root\nwsrep_sst_user=root\ndatadir=/var/lib/mysql\n# root is the admin\nsocket=/run/mysqld/mysqld.sock\n" +
		"wsrep_sst_auth=root:root\n[client]\npassword=root\n[xtrabackup]\npassword = \"root\"\n"
	out, changed := FilterRootPassword("/etc/mysql/conf.d/x.cnf", in, "root")
	for _, keep := range []string{"user=root", "wsrep_sst_user=root", "datadir=/var/lib/mysql", "# root is the admin"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q must stay:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "password=root") || strings.Contains(out, "\"root\"") || strings.Contains(out, "wsrep_sst_auth=root") {
		t.Errorf("the password values must be removed:\n%s", out)
	}
	if len(changed) != 3 {
		t.Errorf("changed %v, want wsrep_sst_auth and the two password lines", changed)
	}
	// a script line passing it as -p<password> is still caught
	if out, _ := FilterRootPassword("/x/dbjob.sh", "mysql -uroot -proot -e 'select 1'\n", "root"); strings.Contains(out, "-proot") {
		t.Errorf("-p<password> must be removed: %s", out)
	}
}

// A password holding a separator is still found in the value.
func TestFilterRootPasswordWithSeparator(t *testing.T) {
	pw := "s3:cr et"
	out, changed := FilterRootPassword("/x/x.cnf", "[client]\nuser=root\npassword="+pw+"\n", pw)
	if strings.Contains(out, pw) || len(changed) != 1 || !strings.Contains(out, "user=root") {
		t.Fatalf("password with separators: %v\n%s", changed, out)
	}
}
