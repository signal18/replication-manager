package cluster

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// The logged template keeps its settings and drops the embedded payload, shown by its size.
func TestTemplateForLog(t *testing.T) {
	script := []byte(strings.Repeat("#!/bin/sh\necho xtrabackup bundle\n", 40))
	payload := base64.StdEncoding.EncodeToString(script)
	template := "[container#03]\ncommand = -c \"echo " + payload + " | base64 -d | XB_IMAGE=percona/percona-xtrabackup:8.4 sh\"\nimage = percona/percona-xtrabackup:8.4\n" +
		"[env]\ndocker_image = mysql:8.4.11\n"
	got := templateForLog(template)
	if strings.Contains(got, payload[:64]) {
		t.Fatalf("the payload is still logged:\n%s", got)
	}
	if !strings.Contains(got, "<base64, "+strconv.Itoa(len(script))+" bytes>") {
		t.Fatalf("the payload size is not shown:\n%s", got)
	}
	for _, keep := range []string{"[container#03]", "| base64 -d | XB_IMAGE=percona/percona-xtrabackup:8.4 sh", "docker_image = mysql:8.4.11"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("%q must stay in the logged template:\n%s", keep, got)
		}
	}
	if plain := "[env]\nnodes = opensvc-node1\n"; templateForLog(plain) != plain {
		t.Fatal("a template without a payload must be logged unchanged")
	}
}
