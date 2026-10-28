package dbhelper

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// changeMasterSQL runs ChangeMaster against a mock that accepts any statement and
// returns the statement issued.
func changeMasterSQL(t *testing.T, opt ChangeMasterOpt) string {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
	cm, err := ChangeMaster(sqlx.NewDb(db, "sqlmock"), opt, mariaDBVersionForTest())
	if err != nil {
		t.Fatal(err)
	}
	return cm
}

// A positional CHANGE turns GTID positioning off: MariaDB MASTER_USE_GTID=NO, MySQL
// SOURCE_AUTO_POSITION=0 (a channel left on auto-position refuses a file/position, error 1776).
func TestChangeMasterPositionalTurnsGTIDOff(t *testing.T) {
	opt := ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", Mode: "POSITIONAL", Logfile: "binlog.000002", Logpos: "93156"}
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_log_pos=93156, MASTER_USE_GTID=NO") {
		t.Fatalf("MariaDB: %s", cm)
	}
	for _, v := range []*version.Version{{Flavor: "MySQL", Major: 8, Minor: 0, Release: 35}, {Flavor: "Percona", Major: 5, Minor: 7, Release: 44}} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
		cm, err := ChangeMaster(sqlx.NewDb(db, "sqlmock"), opt, v)
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cm, "_log_pos=93156, ") || !strings.Contains(strings.ToUpper(cm), "_AUTO_POSITION=0") {
			t.Fatalf("%s %d.%d: %s", v.Flavor, v.Major, v.Minor, cm)
		}
	}
}

// With replication SSL and a CA the replica verifies its primary against the CA, quoted;
// without one, or without SSL, no CA is emitted (#1933).
func TestChangeMasterSSLCa(t *testing.T) {
	base := ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", Mode: "SLAVE_POS"}

	opt := base
	opt.SSL, opt.SSLCa = true, "/etc/mysql/ssl/ca-cert.pem"
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_SSL=1, MASTER_SSL_CA='/etc/mysql/ssl/ca-cert.pem'") {
		t.Fatalf("SSL with a CA must emit MASTER_SSL_CA: %s", cm)
	}

	opt.SSLCa = "/etc/o'brien/ca.pem"
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_SSL_CA='/etc/o''brien/ca.pem'") {
		t.Fatalf("the CA path must be quoted: %s", cm)
	}

	opt.SSLCa = ""
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_SSL=1") || strings.Contains(cm, "SSL_CA") {
		t.Fatalf("SSL without a CA keeps MASTER_SSL=1 only: %s", cm)
	}

	opt = base
	if cm := changeMasterSQL(t, opt); strings.Contains(cm, "SSL") {
		t.Fatalf("no TLS material named: none emitted: %s", cm)
	}

	opt = base
	opt.SSLCaClear = true
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_SSL_CA=''") || strings.Contains(cm, "MASTER_SSL=") {
		t.Fatalf("an emptied CA without touching MASTER_SSL: %s", cm)
	}

	opt = base
	opt.SSLOff = true
	if cm := changeMasterSQL(t, opt); !strings.Contains(cm, "MASTER_SSL=0") {
		t.Fatalf("TLS off towards a primary without TLS: %s", cm)
	}
}

// MySQL 8.4 refuses SOURCE_RETRY_COUNT on the group replication recovery channel (Error
// 3139): the recovery channel never names it, an ordinary channel still does (#2164).
func TestChangeMasterRetryCountNotOnGroupRecovery(t *testing.T) {
	mysql84 := &version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}
	run := func(opt ChangeMasterOpt) string {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
		cm, err := ChangeMaster(sqlx.NewDb(db, "sqlmock"), opt, mysql84)
		if err != nil {
			t.Fatal(err)
		}
		return cm
	}
	group := run(ChangeMasterOpt{User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", RetryCount: "100000", Mode: "GROUP_REPL", Channel: "group_replication_recovery"})
	if strings.Contains(group, "RETRY_COUNT") || !strings.Contains(group, "FOR CHANNEL 'group_replication_recovery'") {
		t.Fatalf("group recovery channel: %s", group)
	}
	replica := run(ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", RetryCount: "100000", Mode: "MASTER_AUTO_POSITION"})
	if !strings.Contains(replica, "SOURCE_RETRY_COUNT=100000") {
		t.Fatalf("an ordinary channel keeps its retry count on 8.4: %s", replica)
	}
}

// MySQL 8's caching_sha2_password replication user needs the source's public key without
// TLS (error 2061): GET_MASTER_PUBLIC_KEY / GET_SOURCE_PUBLIC_KEY, never with TLS, on the
// group replication recovery channel, on MariaDB or before MySQL 8.0 (#2218).
func TestChangeMasterGetPublicKey(t *testing.T) {
	run := func(opt ChangeMasterOpt, v *version.Version) string {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
		cm, err := ChangeMaster(sqlx.NewDb(db, "sqlmock"), opt, v)
		if err != nil {
			t.Fatal(err)
		}
		return cm
	}
	base := ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "3", Mode: "MASTER_AUTO_POSITION", Channel: "bench"}
	for _, c := range []struct {
		v    *version.Version
		want string
	}{
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 22}, "GET_MASTER_PUBLIC_KEY=1"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 41}, "GET_SOURCE_PUBLIC_KEY=1"},
		{&version.Version{Flavor: "Percona", Major: 8, Minor: 4, Release: 3}, "GET_SOURCE_PUBLIC_KEY=1"},
	} {
		if cm := run(base, c.v); !strings.Contains(cm, c.want) || !strings.HasSuffix(cm, "FOR CHANNEL 'bench'") {
			t.Errorf("%s %d.%d.%d: %s", c.v.Flavor, c.v.Major, c.v.Minor, c.v.Release, cm)
		}
	}
	mysql80 := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 41}
	tls := base
	tls.SSL = true
	group := ChangeMasterOpt{User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", Mode: "GROUP_REPL", Channel: "group_replication_recovery"}
	for name, cm := range map[string]string{
		"TLS":            run(tls, mysql80),
		"group recovery": run(group, mysql80),
		"MariaDB":        run(base, mariaDBVersionForTest()),
		"MySQL 5.7":      run(base, &version.Version{Flavor: "MySQL", Major: 5, Minor: 7, Release: 44}),
	} {
		if strings.Contains(cm, "PUBLIC_KEY") {
			t.Errorf("%s: no public key option, got %s", name, cm)
		}
	}
}
