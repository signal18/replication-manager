package dbhelper

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
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
	opt.SSLCa = "/etc/mysql/ssl/ca-cert.pem"
	if cm := changeMasterSQL(t, opt); strings.Contains(cm, "SSL") {
		t.Fatalf("no TLS material without replication SSL: %s", cm)
	}
}
