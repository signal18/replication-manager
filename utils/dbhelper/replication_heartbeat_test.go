// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// MySQL 8 records the last heartbeat and the last queued event per channel: the newer one,
// aged on the replica's clock (#2208).
func TestGetReplicationLastReceivedMySQL(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := sqlx.NewDb(raw, "mysql")
	mock.ExpectQuery(regexp.QuoteMeta("GREATEST(s.LAST_HEARTBEAT_TIMESTAMP, s.LAST_QUEUED_TRANSACTION_END_QUEUE_TIMESTAMP)")).WithArgs("bench").
		WillReturnRows(sqlmock.NewRows([]string{"age", "HEARTBEAT_INTERVAL"}).AddRow(1500000, "3.000"))
	lr, _, err := GetReplicationLastReceived(db, "bench", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 41})
	if err != nil || !lr.Received || lr.Age != 1500*time.Millisecond || lr.HeartbeatPeriod != 3 {
		t.Fatalf("%+v %v", lr, err)
	}
	// a channel that never received: NULL age
	mock.ExpectQuery(regexp.QuoteMeta("s.LAST_HEARTBEAT_TIMESTAMP")).WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"age", "HEARTBEAT_INTERVAL"}).AddRow(nil, "30.000"))
	lr, query, err := GetReplicationLastReceived(db, "", &version.Version{Flavor: "MySQL", Major: 5, Minor: 7, Release: 44})
	if err != nil || lr.Received || lr.HeartbeatPeriod != 30 {
		t.Fatalf("%+v %v", lr, err)
	}
	if regexp.MustCompile("LAST_QUEUED").MatchString(query) {
		t.Fatal("5.7 has no LAST_QUEUED_TRANSACTION_END_QUEUE_TIMESTAMP")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// MariaDB, MySQL before 5.7 and PostgreSQL record no such time: an error, no query.
func TestGetReplicationLastReceivedUnsupported(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := sqlx.NewDb(raw, "mysql")
	for _, v := range []*version.Version{
		{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9},
		{Flavor: "MySQL", Major: 5, Minor: 6, Release: 51},
		{Flavor: "PostgreSQL", Major: 17},
	} {
		if _, _, err := GetReplicationLastReceived(db, "", v); err == nil {
			t.Errorf("%s %d.%d: want an error", v.Flavor, v.Major, v.Minor)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The heartbeat period goes to the named channel, the flavor's way.
func TestSetSlaveHeartbeatStatement(t *testing.T) {
	for _, c := range []struct {
		v       *version.Version
		channel string
		want    string
	}{
		{&version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}, "bench", "CHANGE MASTER 'bench' TO MASTER_HEARTBEAT_PERIOD=3"},
		{&version.Version{Flavor: "MariaDB", Major: 10, Minor: 6, Release: 1}, "", "CHANGE MASTER TO MASTER_HEARTBEAT_PERIOD=3"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 22}, "ch1", "CHANGE MASTER TO MASTER_HEARTBEAT_PERIOD=3 FOR CHANNEL 'ch1'"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 3}, "", "CHANGE REPLICATION SOURCE TO SOURCE_HEARTBEAT_PERIOD=3"},
		{&version.Version{Flavor: "Percona", Major: 8, Minor: 0, Release: 41}, "ch1", "CHANGE REPLICATION SOURCE TO SOURCE_HEARTBEAT_PERIOD=3 FOR CHANNEL 'ch1'"},
	} {
		got, err := setSlaveHeartbeatStatement("3", c.channel, c.v)
		if err != nil || got != c.want {
			t.Errorf("%s %d.%d.%d %q: %q %v, want %q", c.v.Flavor, c.v.Major, c.v.Minor, c.v.Release, c.channel, got, err, c.want)
		}
	}
	for _, bad := range []struct {
		v                 *version.Version
		interval, channel string
	}{
		{&version.Version{Flavor: "PostgreSQL", Major: 17}, "3", ""},
		{&version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}, "3; DROP", ""},
		{&version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}, "3", "x' TO"},
	} {
		if got, err := setSlaveHeartbeatStatement(bad.interval, bad.channel, bad.v); err == nil {
			t.Errorf("%q %q on %s: %q, want an error", bad.interval, bad.channel, bad.v.Flavor, got)
		}
	}
}
