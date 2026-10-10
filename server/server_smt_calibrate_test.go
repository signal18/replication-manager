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

func TestSmtGainSetterValidation(t *testing.T) {
	for _, bad := range []string{"NaN", "nan", "Inf", "-Inf", "-0.5", "abc"} {
		if _, err := parseSmtGain(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	// no upper bound of 2: an SMT4 host is clamped by the node, not refused here
	for _, ok := range []string{"1.15", "0", "3.2"} {
		if _, err := parseSmtGain(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func TestParseFileioIops(t *testing.T) {
	out := []byte(`File operations:
    reads/s:                      5123.45
    writes/s:                     3415.63
    fsyncs/s:                     0.00

Throughput:
    read, MiB/s:                  80.05`)
	r, w, err := parseFileioIops(out)
	if err != nil || r != 5123.45 || w != 3415.63 {
		t.Fatalf("reads %v writes %v err %v", r, w, err)
	}
	if _, _, err := parseFileioIops([]byte("FATAL: cannot open file")); err == nil {
		t.Fatal("an output without rates must be an error")
	}
}
