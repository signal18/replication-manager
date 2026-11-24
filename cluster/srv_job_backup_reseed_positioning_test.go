// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// seenStatements records every statement the server receives, in order, whether the mock expected
// it or not: an unexpected statement fails nothing by itself in the code under test (its error is
// logged), so the tests read the record.
type seenStatements struct {
	mu   sync.Mutex
	list []string
}

func (s *seenStatements) add(sql string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, strings.Join(strings.Fields(sql), " "))
}

func (s *seenStatements) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.list...)
}

func (s *seenStatements) has(re string) bool {
	r := regexp.MustCompile(re)
	for _, q := range s.all() {
		if r.MatchString(q) {
			return true
		}
	}
	return false
}

// reseedPositioningEnv is a replica that reseeds from a mydumper backup of its primary: the
// statements it receives are recorded, myloader is a script that leaves a marker when it runs.
type reseedPositioningEnv struct {
	cl      *Cluster
	server  *ServerMonitor
	mock    sqlmock.Sqlmock
	seen    *seenStatements
	backup  string
	marker  string
	task    string
	closeDB func()
}

func newReseedPositioningEnv(t *testing.T, flavor string, major, minor int) *reseedPositioningEnv {
	t.Helper()
	cl, server := newTestRuntimeOnlyClusterServer(t, "reseed-positioning", "replica", "3306")
	// a failed statement is logged to the cluster's SQL logs
	cl.SqlErrorLog, cl.SqlGeneralLog = logrus.New(), logrus.New()
	seen := &seenStatements{}
	matcher := sqlmock.QueryMatcherFunc(func(expectedSQL, actualSQL string) error {
		seen.add(actualSQL)
		return sqlmock.QueryMatcherRegexp.Match(expectedSQL, actualSQL)
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatal(err)
	}
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = &version.Version{Flavor: flavor, Major: major, Minor: minor}
	// a replication password, as in a running cluster (an empty one makes the log masking garble every line)
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"replication-credential": {Value: "repl:s3cr3t"}})
	cl.master = &ServerMonitor{Id: "primary", Host: "primary", Port: "3306", URL: "primary:3306", ClusterGroup: cl,
		DBVersion: &version.Version{Flavor: flavor, Major: major, Minor: minor}}
	server.Variables.Set("GTID_MODE", "ON")
	cl.Conf.BackupLogicalType = config.ConstBackupLogicalTypeMydumper

	dir := t.TempDir()
	backup := filepath.Join(dir, "mydumper")
	if err := os.MkdirAll(backup, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "metadata"), []byte("Started dump at: 2026-11-24 00:00:00\nSHOW MASTER STATUS:\n\tLog: bin.000001\n\tPos: 4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "myloader-ran")
	loader := filepath.Join(dir, "myloader")
	if err := os.WriteFile(loader, []byte("#!/bin/sh\necho ran > "+marker+"\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cl.Conf.BackupMyLoaderPath = loader

	task := "reseed" + config.ConstBackupLogicalTypeMydumper
	server.SetInReseedBackup(task)
	server.JobsUpdatePayloadRuntimeOnly(task, fmt.Sprintf(`{"backup_type":"mydumper","backup_path":%q}`, backup))
	return &reseedPositioningEnv{cl: cl, server: server, mock: mock, seen: seen, backup: backup, marker: marker, task: task, closeDB: func() { db.Close() }}
}

// fileSet is every file of dir with its size, to prove a directory was not touched.
func fileSet(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out = append(out, fmt.Sprintf("%s:%d:%x", p, info.Size(), b))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// A mydumper reseed whose positioning statement fails with error 3079 (a MySQL/Percona replica with
// several channels, the default one not named: CHANGE REPLICATION SOURCE is refused) ends before the restore:
// no myloader, nothing loaded, no reset of the binary logs or GTIDs, no restart of the replication,
// the job failed, the reseed state cleared, the stopped link recorded for WARN0252.
func TestProcessReseedLogical_MydumperPositioningFailureStopsBeforeRestore(t *testing.T) {
	for _, tc := range []struct {
		name          string
		flavor        string
		major, minor  int
		change        string
		forceNoGtid   bool
		wantStatement string
	}{
		{"mysql gtid auto-position", "MySQL", 8, 4, "CHANGE REPLICATION SOURCE", false, `CHANGE REPLICATION SOURCE TO .*SOURCE_AUTO_POSITION=1`},
		{"percona gtid auto-position", "Percona", 8, 4, "CHANGE REPLICATION SOURCE", false, `CHANGE REPLICATION SOURCE TO .*SOURCE_AUTO_POSITION=1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newReseedPositioningEnv(t, tc.flavor, tc.major, tc.minor)
			defer env.closeDB()
			env.cl.Conf.ForceSlaveNoGtid = tc.forceNoGtid
			// the error the server answers to the statement: a typed *mysql.MySQLError, which
			// dbhelper.ChangeMaster wraps with %w, so the number is read with errors.As
			failure := &mysql.MySQLError{Number: 3079, Message: "Multiple channels exist on the replica. Please provide channel name as an argument."}
			env.mock.MatchExpectationsInOrder(true)
			env.mock.ExpectExec("STOP (SLAVE|REPLICA)").WillReturnResult(sqlmock.NewResult(0, 0))
			env.mock.ExpectExec(tc.change).WillReturnError(failure)

			before := fileSet(t, env.backup)
			err := env.server.ProcessReseedLogical(env.task)

			if err == nil {
				t.Fatal("expected the reseed to fail on the positioning error")
			}
			if !strings.Contains(err.Error(), "Multiple channels exist") || !strings.Contains(err.Error(), "before the restore") {
				t.Errorf("the error must name the cause and that nothing was restored, got: %v", err)
			}
			// before MyLoader execution
			if _, statErr := os.Stat(env.marker); statErr == nil {
				t.Error("myloader was executed after the positioning failure")
			}
			// the statements the server received: stop, then the failed positioning, nothing else
			got := env.seen.all()
			if len(got) != 2 {
				t.Fatalf("want exactly STOP and the failed positioning, got %d statements: %q", len(got), got)
			}
			if !regexp.MustCompile(tc.wantStatement).MatchString(got[1]) {
				t.Errorf("the positioning statement was not the existing strategy %q, got %q", tc.wantStatement, got[1])
			}
			for _, forbidden := range []string{`RESET BINARY LOGS`, `RESET MASTER`, `GTID_PURGED`, `gtid_slave_pos`, `START (SLAVE|REPLICA)`, `RESET (SLAVE|REPLICA)`, `DROP `, `SET GLOBAL`} {
				if env.seen.has(forbidden) {
					t.Errorf("statement %q was sent after the positioning failure: %q", forbidden, env.seen.all())
				}
			}
			// job failure and cleanup
			task := env.server.JobResults.Get(env.task)
			if task == nil || task.State != 5 || task.Done != 1 || !strings.Contains(task.Result, "Multiple channels exist") {
				t.Errorf("want the job failed (state 5, done 1) with the error, got %+v", task)
			}
			if env.server.HasAnyReseedingState() {
				t.Errorf("the reseeding state must be cleared, got %q", env.server.IsReseeding)
			}
			if msg := env.server.ReseedFailedSince(0); !strings.Contains(msg, "Multiple channels exist") {
				t.Errorf("MarkReseedFailed was not recorded, got %q", msg)
			}
			// the link stays as it was found, stopped, and WARN0252 knows
			if ch := env.server.reseedStoppedLinkList(); len(ch) != 1 || ch[0] != env.cl.Conf.MasterConn {
				t.Errorf("the stopped managed channel must stay recorded, got %q", ch)
			}
			// the backup is untouched
			if fileSet(t, env.backup) != before {
				t.Error("the backup directory changed")
			}
			if err := env.mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

// Any other positioning failure is logged and the reseed goes on as it always did: a restore that
// stopped half-way can leave the replica's replication metadata missing (error 1146 on
// mysql.slave_relay_log_info), and the next restore is what repairs it. Only the typed error 3079 on
// MySQL/Percona ends the reseed: not another error number, not a message that merely contains
// "3079", not the same number on MariaDB.
func TestProcessReseedLogical_MydumperOtherPositioningErrorsGoOn(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flavor       string
		major, minor int
		change       string
		failure      error
	}{
		{"mysql 1146 on the replication metadata table", "MySQL", 8, 4, "CHANGE REPLICATION SOURCE",
			&mysql.MySQLError{Number: 1146, Message: "Table 'mysql.slave_relay_log_info' doesn't exist"}},
		{"mysql untyped error carrying the text 3079", "MySQL", 8, 4, "CHANGE REPLICATION SOURCE",
			fmt.Errorf("Error 3079 (HY000): Multiple channels exist on the replica")},
		{"mariadb with a 3079 error number", "MariaDB", 11, 8, "CHANGE MASTER",
			&mysql.MySQLError{Number: 3079, Message: "Multiple channels exist on the replica."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newReseedPositioningEnv(t, tc.flavor, tc.major, tc.minor)
			defer env.closeDB()
			env.mock.ExpectExec("STOP (SLAVE|REPLICA)").WillReturnResult(sqlmock.NewResult(0, 0))
			env.mock.ExpectExec(tc.change).WillReturnError(tc.failure)

			_ = env.server.ProcessReseedLogical(env.task) // the fake myloader exits 1: the restore fails, which is not under test

			if _, statErr := os.Stat(env.marker); statErr != nil {
				t.Errorf("the reseed must go on to the restore after this positioning error, myloader did not run: %v", statErr)
			}
			if task := env.server.JobResults.Get(env.task); task != nil && strings.Contains(task.Result, "cancelled before the restore") {
				t.Errorf("the reseed was cancelled before the restore: %+v", task)
			}
		})
	}
}

// With the positioning accepted (a single channel, or a named managed channel) the reseed goes on
// to the restore as before: myloader runs after exactly the same two statements.
func TestProcessReseedLogical_MydumperPositioningSuccessGoesOnToRestore(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flavor       string
		major, minor int
		change       string
		channel      string
	}{
		{"mysql gtid single channel", "MySQL", 8, 4, "CHANGE REPLICATION SOURCE", ""},
		{"mysql gtid named managed channel", "MySQL", 8, 4, "CHANGE REPLICATION SOURCE .*FOR CHANNEL 'managed'", "managed"},
		{"mariadb gtid", "MariaDB", 11, 8, "CHANGE MASTER", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newReseedPositioningEnv(t, tc.flavor, tc.major, tc.minor)
			defer env.closeDB()
			env.cl.Conf.MasterConn = tc.channel
			env.mock.ExpectExec("STOP (SLAVE|REPLICA)").WillReturnResult(sqlmock.NewResult(0, 0))
			env.mock.ExpectExec(tc.change).WillReturnResult(sqlmock.NewResult(0, 0))

			_ = env.server.ProcessReseedLogical(env.task) // the fake myloader exits 1: the restore fails, which is not under test

			got := env.seen.all()
			if len(got) < 2 || !strings.HasPrefix(got[0], "STOP ") || !strings.HasPrefix(got[1], "CHANGE ") {
				t.Fatalf("want STOP then the positioning first, got %q", got)
			}
			if _, statErr := os.Stat(env.marker); statErr != nil {
				t.Errorf("myloader did not run after an accepted positioning: %v", statErr)
			}
		})
	}
}
