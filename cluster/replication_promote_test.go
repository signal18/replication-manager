package cluster

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// recordingReplication records the verbs the promotion calls on it.
type recordingReplication struct {
	replicationBase
	calls *[]string
}

func (r *recordingReplication) Promote(reason string) (string, error) {
	*r.calls = append(*r.calls, "promote")
	return "", nil
}

// The promotion of a candidate keeps its order: the link ends (RESET SLAVE ALL), the switchover
// reads the new master's position, then read_only goes off. A position read after read_only off
// could miss a direct write, which positional replicas would then skip.
func TestOpenNewMasterOrder(t *testing.T) {
	for _, c := range []struct {
		name             string
		promote, refresh bool
		want             []string
	}{
		{"switchover", true, true, []string{"promote", "refresh", "read-write"}},
		{"failover", true, false, []string{"promote", "read-write"}},
		{"multi-master switchover", false, true, []string{"refresh", "read-write"}},
	} {
		var calls []string
		link := &recordingReplication{replicationBase: replicationBase{manifest: mariadbNativeManifest, name: "belair"}, calls: &calls}
		rwErr := errors.New("read only refused")
		err := openNewMaster(link, LinkBySwitchover, c.promote, c.refresh,
			func() error { calls = append(calls, "refresh"); return nil },
			func() error { calls = append(calls, "read-write"); return rwErr })
		if !errors.Is(err, rwErr) {
			t.Errorf("%s: the read-write error is returned, got %v", c.name, err)
		}
		if len(calls) != len(c.want) {
			t.Fatalf("%s: calls %v, want %v", c.name, calls, c.want)
		}
		for i := range calls {
			if calls[i] != c.want[i] {
				t.Fatalf("%s: calls %v, want %v", c.name, calls, c.want)
			}
		}
	}
}

// On MySQL SetPosition is GTID_PURGED, the server's GTID state: refused, never sent, on a server
// whose gtid_executed is not empty; set on a server just reset.
func TestSetPositionMySQLRefusesAGTIDState(t *testing.T) {
	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 40}
	server, mock := newMyLoaderPositionServer(t, mysql, true)
	mock.ExpectQuery("SELECT Variable_Value AS Value FROM performance_schema.global_variables WHERE Variable_Name = ?").
		WithArgs("GTID_EXECUTED").
		WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5"))
	if _, err := server.ReplicationLink().SetPosition(Position{GTID: "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-9"}, LinkByTest); !errors.Is(err, ErrReplicationPositionNotEmpty) {
		t.Fatalf("a server with a GTID state: want ErrReplicationPositionNotEmpty, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT Variable_Value AS Value FROM performance_schema.global_variables WHERE Variable_Name = ?").
		WithArgs("GTID_EXECUTED").
		WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow(""))
	mock.ExpectExec("SET @@GLOBAL.GTID_PURGED = '3e11fa47-71ca-11e1-9e33-c80aa9429562:1-9'").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := server.ReplicationLink().SetPosition(Position{GTID: "3e11fa47-71ca-11e1-9e33-c80aa9429562:1-9"}, LinkByTest); err != nil {
		t.Fatalf("a server just reset: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
