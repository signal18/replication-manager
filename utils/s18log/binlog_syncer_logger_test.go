// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s18log

import (
	"strings"
	"testing"
)

type recordingPrinter struct {
	forcedLevels []string
}

func (p *recordingPrinter) LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int {
	if forcingLog {
		p.forcedLevels = append(p.forcedLevels, level)
	}
	return 0
}

// TestBinlogSyncerLogger_FatalPanicsWithFatalErrorSentinel guards against a
// regression back to os.Exit(1): go-mysql's NewBinlogSyncer calls
// Logger.Fatal on ServerID==0, and cluster.newSafeBinlogSyncer depends on
// Fatal* panicking with *FatalError (recoverable) rather than exiting the
// process (unrecoverable) so one bad binlog syncer can't kill the daemon.
func TestBinlogSyncerLogger_FatalPanicsWithFatalErrorSentinel(t *testing.T) {
	cases := []struct {
		name string
		call func(l *BinlogSyncerLogger)
	}{
		{"Fatal", func(l *BinlogSyncerLogger) { l.Fatal("boom") }},
		{"Fatalf", func(l *BinlogSyncerLogger) { l.Fatalf("boom %d", 1) }},
		{"Fatalln", func(l *BinlogSyncerLogger) { l.Fatalln("boom") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingPrinter{}
			l := NewBinlogSyncerLogger(p, "127.0.0.1:3306", "test", 0)

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected Fatal* to panic instead of calling os.Exit(1)")
				}
				fe, ok := r.(*FatalError)
				if !ok {
					t.Fatalf("expected panic value of type *FatalError, got %T: %v", r, r)
				}
				if !strings.Contains(fe.Error(), "boom") {
					t.Fatalf("expected FatalError message to contain original message, got %q", fe.Error())
				}
				if len(p.forcedLevels) == 0 {
					t.Fatal("expected Fatal* to force-log before panicking")
				}
			}()

			tc.call(l)
		})
	}
}
