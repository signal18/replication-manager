package config

import (
	"reflect"
	"testing"
)

// A listed task that can not be forced remote is reported, whatever the reason
// (local-only, remote-only, unknown); a TaskCapBoth task is not.
func TestJobsExecRemoteIgnored(t *testing.T) {
	conf := &Config{SchedulerJobsExecRemote: " mysqldump, reseedmysqldump,optimize, mydumper ,xtrabackup,nosuchtask,,"}
	ignored, allowed := conf.JobsExecRemoteIgnored()
	if want := []string{"mydumper", "nosuchtask", "reseedmysqldump", "xtrabackup"}; !reflect.DeepEqual(ignored, want) {
		t.Errorf("ignored = %v, want %v", ignored, want)
	}
	for _, task := range allowed {
		if TaskRegistry[TaskName(task)].Capability != TaskCapBoth {
			t.Errorf("allowed lists %s, not a TaskCapBoth task", task)
		}
	}
	conf.ParseJobsExecOverrides()
	if want := map[TaskName]bool{ConstTaskDump: true, ConstTaskOptimize: true}; !reflect.DeepEqual(conf.SchedulerJobsExecOverrides, want) {
		t.Errorf("overrides = %v, want %v", conf.SchedulerJobsExecOverrides, want)
	}

	conf.SchedulerJobsExecRemote = "mysqldump,optimize"
	if ignored, _ := conf.JobsExecRemoteIgnored(); len(ignored) != 0 {
		t.Errorf("ignored = %v, want none", ignored)
	}
}
