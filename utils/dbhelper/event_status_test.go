package dbhelper

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// An event is disabled on the replicas with DISABLE ON SLAVE, DISABLE ON REPLICA on
// MySQL/Percona from 8.2 (MySQL 8.0.35 refuses it: lab, 2026-10-15); a definer host such as '%' is quoted, not refused. The event
// scheduler of PostgreSQL is not read.
func TestSetEventStatus(t *testing.T) {
	ev := Event{Db: "app", Name: "nightly", Definer: "root@%"}
	for _, tc := range []struct {
		flavor, release, want string
		status                int64
	}{
		{"MariaDB", "10.11.19-MariaDB", "ALTER /\\*replication-manager\\*/ DEFINER=`root`@`%` EVENT `app`.`nightly` DISABLE ON SLAVE$", 3},
		{"MySQL", "8.0.35", "DISABLE ON SLAVE$", 3},
		{"Percona", "8.0.46-37", "DISABLE ON SLAVE$", 3},
		{"MySQL", "8.2.0", "DISABLE ON REPLICA$", 3},
		{"Percona", "8.4.11-11", "DISABLE ON REPLICA$", 3},
		{"MySQL", "8.4.11", "EVENT `app`.`nightly` ENABLE$", 1},
	} {
		v, _ := version.NewVersionFromString(tc.flavor, tc.release)
		db, mock := freshMock(t)
		mock.ExpectExec(tc.want).WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := SetEventStatus(db, ev, tc.status, v); err != nil {
			t.Fatalf("%s %s: %v", tc.flavor, tc.release, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("%s %s: %v", tc.flavor, tc.release, err)
		}
	}

	db, mock := freshMock(t)
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	if _, _, err := GetEventScheduler(db, pg); !errors.Is(err, ErrEventsUnsupported) {
		t.Fatalf("PostgreSQL scheduler: %v", err)
	}
	if _, err := SetEventStatus(db, Event{Db: "app", Name: "e", Definer: "root@"}, 3, nil); err == nil {
		t.Fatal("a definer without host is refused")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetEventScheduler(t *testing.T) {
	maria, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	db, mock := freshMock(t)
	mock.ExpectQuery("@@GLOBAL.event_scheduler").WillReturnRows(sqlmock.NewRows([]string{"s"}).AddRow("on"))
	state, _, err := GetEventScheduler(db, maria)
	if err != nil || state != "ON" {
		t.Fatalf("state %q err %v", state, err)
	}
}
