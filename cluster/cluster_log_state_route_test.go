package cluster

import (
	"bytes"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// The ring buffers are pre-sized: count the entries really written.
func httpCount(l *s18log.HttpLog) int {
	n := 0
	for _, m := range l.Buffer {
		if m.Text != "" {
			n++
		}
	}
	return n
}

func termCount(l *s18log.TermLog) int {
	n := 0
	for _, m := range l.Buffer {
		if m != "" {
			n++
		}
	}
	return n
}

func newBufLogger(buf *bytes.Buffer) *log.Logger {
	l := log.New()
	l.SetOutput(buf)
	l.SetFormatter(&log.TextFormatter{DisableTimestamp: true})
	return l
}

// A workload, security or schema state transition is written to its own daemon log and
// buffer only: the general cluster log (live buffer, terminal buffer, main log file that
// feeds the GUI history) carries HA/operational states only.
func TestStatePrinterRoutesByBuffer(t *testing.T) {
	var mainOut, workOut, secOut, schemaOut bytes.Buffer
	c := &Cluster{Name: "t", Conf: &config.Config{Daemon: true, HttpServ: true}}
	c.Logrus = newBufLogger(&mainOut)
	c.WorkloadLogrus = newBufLogger(&workOut)
	c.SecurityLogrus = newBufLogger(&secOut)
	c.SchemaLogrus = newBufLogger(&schemaOut)
	c.Log = s18log.NewHttpLog(50)
	c.LogWorkload = s18log.NewHttpLog(50)
	c.LogSecurity = s18log.NewHttpLog(50)
	c.LogSchema = s18log.NewHttpLog(50)
	general := s18log.NewHttpLog(50)
	c.htlog = &general
	tl := s18log.NewTermLog(50)
	c.tlog = &tl

	c.logPrintStateTo(state.State{ErrKey: "WTAG0210", ErrDesc: "Full scan (type=ALL) row cost 100%"}, false, &c.LogWorkload)
	if mainOut.Len() != 0 || httpCount(&c.Log) != 0 || httpCount(&general) != 0 || termCount(c.tlog) != 0 {
		t.Fatalf("a workload state must not reach the general log: main=%q live=%d general=%d term=%d", mainOut.String(), httpCount(&c.Log), httpCount(&general), termCount(c.tlog))
	}
	if !strings.Contains(workOut.String(), "Full scan") || !strings.Contains(workOut.String(), "status=OPENED") || httpCount(&c.LogWorkload) != 1 {
		t.Fatalf("a workload state must land in workload.log and its buffer: %q buffer=%d", workOut.String(), httpCount(&c.LogWorkload))
	}
	c.logPrintStateTo(state.State{ErrKey: "SEC0001", ErrDesc: "weak auth"}, true, &c.LogSecurity)
	c.logPrintStateTo(state.State{ErrKey: "SCH0001", ErrDesc: "row size"}, false, &c.LogSchema)
	if mainOut.Len() != 0 || !strings.Contains(secOut.String(), "status=RESOLV") || !strings.Contains(schemaOut.String(), "row size") {
		t.Fatalf("security and schema states go to their own logs: main=%q sec=%q schema=%q", mainOut.String(), secOut.String(), schemaOut.String())
	}
	// General states keep today's path: main log, live buffer, terminal buffer.
	c.logPrintStateTo(state.State{ErrKey: "ERR00010", ErrDesc: "master down"}, false, c.htlog)
	if !strings.Contains(mainOut.String(), "master down") || httpCount(&c.Log) != 1 || httpCount(&general) != 1 || termCount(c.tlog) != 1 {
		t.Fatalf("a general state must reach the main log and both general buffers: main=%q live=%d general=%d term=%d", mainOut.String(), httpCount(&c.Log), httpCount(&general), termCount(c.tlog))
	}
	// No dedicated logger: fall back to the main one so nothing is lost.
	c.WorkloadLogrus = nil
	c.logPrintStateTo(state.State{ErrKey: "WTAG0211", ErrDesc: "sorted rows"}, false, &c.LogWorkload)
	if !strings.Contains(mainOut.String(), "sorted rows") {
		t.Fatalf("without a workload logger the state must fall back to the main log")
	}
}
