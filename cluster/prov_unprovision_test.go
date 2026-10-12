// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// #1994: a service the orchestrator no longer has counts as removed; any other error
// does not.
func TestOrchestratorNotFound(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New(`unexpected status code: 404, body: {"detail":"","status":404,"title":"object not found"}`), true},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "statefulsets"}, "db1"), true},
		{errors.New("Agent not found in orechestrator node list"), false},
		{errors.New("unexpected status code: 500, body: {}"), false},
		{errors.New("unexpected status code: 404, body: 404 page not found"), false}, // a wrong API path is not a removal
	} {
		if got := orchestratorNotFound(c.err); got != c.want {
			t.Errorf("orchestratorNotFound(%v) = %t, want %t", c.err, got, c.want)
		}
	}
}

// #1995: a database or proxy service is removed with its volume and its -sec volume.
func TestOpenSVCServiceObjects(t *testing.T) {
	c := &Cluster{Name: "dev3-staging"}
	got := c.openSVCServiceObjects("dev3-staging/svc/db1", "db1")
	want := []string{"dev3-staging/svc/db1", "dev3-staging/vol/db1", "dev3-staging/vol/db1-sec"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
