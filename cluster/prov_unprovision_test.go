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
	} {
		if got := orchestratorNotFound(c.err); got != c.want {
			t.Errorf("orchestratorNotFound(%v) = %t, want %t", c.err, got, c.want)
		}
	}
}
