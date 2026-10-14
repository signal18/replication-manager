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
