// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"fmt"
	"strings"
	"testing"
)

// cpuinfo builds a /proc/cpuinfo for sockets x cores x threads.
func cpuinfo(sockets, cores, threads int, topology bool) string {
	var b strings.Builder
	n := 0
	for s := 0; s < sockets; s++ {
		for t := 0; t < threads; t++ {
			for c := 0; c < cores; c++ {
				fmt.Fprintf(&b, "processor\t: %d\nmodel name\t: Xeon\n", n)
				if topology {
					fmt.Fprintf(&b, "physical id\t: %d\ncore id\t\t: %d\n", s, c)
				}
				b.WriteString("\n")
				n++
			}
		}
	}
	return b.String()
}

func TestHostCoresAndThreads(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		info                 string
		wantCores, wantThrds int
	}{
		{"BSO-like 2 sockets x 24 cores x 2 threads", cpuinfo(2, 24, 2, true), 48, 96},
		{"no SMT 1 socket x 32 cores", cpuinfo(1, 32, 1, true), 32, 32},
		{"VM without topology", cpuinfo(1, 8, 1, false), 8, 8},
	} {
		c, th := hostCoresAndThreads(tt.info)
		if c != tt.wantCores || th != tt.wantThrds {
			t.Errorf("%s: %d cores / %d threads, want %d / %d", tt.name, c, th, tt.wantCores, tt.wantThrds)
		}
	}
}

func TestSysbenchOutputParsing(t *testing.T) {
	cpuOut := "CPU speed:\n    events per second: 49579.11\n\nGeneral statistics:\n"
	memOut := "Total operations: 1 (  1.00 per second)\n\n43666.50 MiB transferred (1455.55 MiB/sec)\n"
	if m := sysbenchEventsRe.FindStringSubmatch(cpuOut); m == nil || m[1] != "49579.11" {
		t.Fatalf("cpu events: %v", m)
	}
	if m := sysbenchMemRe.FindStringSubmatch(memOut); m == nil || m[1] != "1455.55" {
		t.Fatalf("memory MiB/s: %v", m)
	}
}
