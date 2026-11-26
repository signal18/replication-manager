package selfexec

import (
	"reflect"
	"testing"
)

// The child gets the parent's environment followed by the install profile, which wins on a
// duplicate, and the caller's slice is not modified.
func TestChildEnv(t *testing.T) {
	saved := ProfileEnv
	defer func() { ProfileEnv = saved }()
	ProfileEnv = []string{"REPLICATION_MANAGER_INSTALL_PROFILE=osc", "REPLICATION_MANAGER_INSTALL_LAYOUT=package"}
	env := []string{"PATH=/usr/bin", "REPLICATION_MANAGER_INSTALL_PROFILE=pro"}
	got := ChildEnv(env)
	want := []string{"PATH=/usr/bin", "REPLICATION_MANAGER_INSTALL_PROFILE=pro", "REPLICATION_MANAGER_INSTALL_PROFILE=osc", "REPLICATION_MANAGER_INSTALL_LAYOUT=package"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(env) != 2 {
		t.Fatalf("caller's slice modified: %v", env)
	}
}
