package cluster

import "testing"

// No node, ALL or * restart every instance; a node name only that one (#2223).
func TestAppRestartAllNodes(t *testing.T) {
	for _, n := range []string{"", "ALL", "all", "*"} {
		if !AppRestartAllNodes(n) {
			t.Fatalf("%q: every instance", n)
		}
	}
	if AppRestartAllNodes("node-1") {
		t.Fatal("a node name: that instance only")
	}
}
