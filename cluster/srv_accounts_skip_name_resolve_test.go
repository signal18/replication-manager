package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// GH-2046: with skip_name_resolve the server ignores accounts with a DNS host, and the
// account dump of a logical backup stops on them: the backup is refused up front with
// the accounts named, so ERR00118 shows why.
func TestLogicalBackupAccountsBlocker(t *testing.T) {
	c := &Cluster{Name: "curepipe", Conf: &config.Config{BackupMysqldumpOptions: "--single-transaction --all-databases --system=all"}}
	db2 := &ServerMonitor{URL: "db2.curepipe.svc.cloud18:3306", ClusterGroup: c, Variables: config.NewStringsMap(), Users: dbhelper.NewGrantsMap()}
	c.Servers = []*ServerMonitor{db2}
	c.master = db2
	db2.Users.Set("a", &dbhelper.Grant{User: "forgejo1", Host: "repman.s18.svc.cloud18"})
	db2.Users.Set("b", &dbhelper.Grant{User: "app", Host: "%"})
	db2.Users.Set("c", &dbhelper.Grant{User: "repl", Host: "10.60.%"})

	db2.Variables.Set("SKIP_NAME_RESOLVE", "OFF")
	if got := db2.AccountsIgnoredBySkipNameResolve(); len(got) != 0 {
		t.Fatalf("skip_name_resolve off: nothing hidden, got %v", got)
	}
	if err := db2.logicalBackupAccountsBlocker(config.ConstBackupLogicalTypeMysqldump); err != nil {
		t.Fatalf("skip_name_resolve off: no refusal, got %s", err)
	}

	db2.Variables.Set("SKIP_NAME_RESOLVE", "ON")
	if got := db2.AccountsIgnoredBySkipNameResolve(); len(got) != 1 || got[0] != "'forgejo1'@'repman.s18.svc.cloud18'" {
		t.Fatalf("only the DNS-host account is hidden: %v", got)
	}
	err := db2.logicalBackupAccountsBlocker(config.ConstBackupLogicalTypeMysqldump)
	if err == nil || !strings.Contains(err.Error(), "'forgejo1'@'repman.s18.svc.cloud18'") || !strings.Contains(err.Error(), "1133") {
		t.Fatalf("a mysqldump --system=all is refused with the account named: %v", err)
	}

	// no account dump: mydumper without the users sidecar, or mysqldump without --system
	if err := db2.logicalBackupAccountsBlocker(config.ConstBackupLogicalTypeMydumper); err != nil {
		t.Fatalf("mydumper without the users sidecar dumps no account: %s", err)
	}
	c.Conf.BackupMysqldumpOptions = "--single-transaction --all-databases"
	if err := db2.logicalBackupAccountsBlocker(config.ConstBackupLogicalTypeMysqldump); err != nil {
		t.Fatalf("mysqldump without --system dumps no account: %s", err)
	}
	// the users sidecar dumps the accounts whatever the tool
	c.Conf.BackupSplitMysqlUser = true
	if err := db2.logicalBackupAccountsBlocker(config.ConstBackupLogicalTypeMydumper); err == nil {
		t.Fatal("the users sidecar (--system=user) is refused too")
	}
}
