package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// Every task the user may send to dbjobs (TaskCapBoth) needs a wait cookie, or the
// API-mode dispatch in jobInsertTask fails with "no cookie mapping for task".
func TestBothCapabilityTasksHaveACookie(t *testing.T) {
	s := postgresJobTestServer(t)
	for name, def := range config.TaskRegistry {
		if def.Capability != config.TaskCapBoth {
			continue
		}
		if err := s.setTaskCookie(string(name)); err != nil {
			t.Errorf("task %s can run remotely but has no cookie: %v", name, err)
		}
	}
}

// Listing a logical restore task in scheduler-jobs-exec-remote must not make it remote:
// repman runs it, dbjobs never does.
func TestLogicalRestoreTasksStayLocal(t *testing.T) {
	conf := &config.Config{SchedulerJobsExecRemote: "reseedmysqldump, mydumper, flashbackmysqldump, mysqldump"}
	conf.ParseJobsExecOverrides()
	for _, task := range []config.TaskName{config.ConstTaskReseedDump, config.ConstTaskMydumper, config.ConstTaskFlashDump} {
		if config.IsRemoteTask(task, conf.SchedulerJobsExecOverrides) {
			t.Errorf("task %s must stay local", task)
		}
	}
	if !config.IsRemoteTask(config.ConstTaskDump, conf.SchedulerJobsExecOverrides) {
		t.Error("mysqldump is still allowed to run remotely")
	}
}
