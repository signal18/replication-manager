package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// GH-2044: a reseed of db1 from db2 is recorded on db1 while the dump ran against db2;
// db2's plugins get that job, marked with db2 as its source, and db1's own job stays.
func TestSnapshotJobsIncludesDumpsOfThisServer(t *testing.T) {
	c := &Cluster{Name: "curepipe", Conf: &config.Config{}}
	db1 := &ServerMonitor{URL: "db1.curepipe.svc.cloud18:3306", ClusterGroup: c, JobResults: config.NewTasksMap()}
	db2 := &ServerMonitor{URL: "db2.curepipe.svc.cloud18:3306", ClusterGroup: c, JobResults: config.NewTasksMap()}
	db3 := &ServerMonitor{URL: "db3.curepipe.svc.cloud18:3306", ClusterGroup: c, JobResults: config.NewTasksMap()}
	c.Servers = []*ServerMonitor{db1, db2, db3}

	db1.JobResults.Set("direct", &config.Task{Task: "direct", Done: 1, State: 5, End: 1791973763,
		Result: "application restore: Reseed failed: mysqldump on db2.curepipe.svc.cloud18:3306: exit status 2 | stderr: Column count of mysql.proc is wrong."})
	db2.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 1, State: 4, End: 1791900000, Result: "Backup completed"})

	jobs := snapshotJobs(db2)
	var own, fromDb1 int
	for _, j := range jobs {
		switch {
		case j.Task == "mysqldump" && j.Source == "":
			own++
		case j.Task == "direct" && j.Source == db2.URL:
			fromDb1++
		default:
			t.Fatalf("unexpected job for db2: %+v", j)
		}
	}
	if own != 1 || fromDb1 != 1 {
		t.Fatalf("db2 must see its own backup and db1's reseed dump of it: %+v", jobs)
	}

	for _, j := range snapshotJobs(db3) {
		if j.Task == "direct" {
			t.Fatalf("db3 was not dumped by db1's reseed: %+v", j)
		}
	}
	for _, j := range snapshotJobs(db1) {
		if j.Task == "direct" && j.Source != "" {
			t.Fatalf("db1's own job keeps no source mark (the plugin reads its result): %+v", j)
		}
	}
}
