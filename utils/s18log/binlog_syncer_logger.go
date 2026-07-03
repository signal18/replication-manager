// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s18log

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/config"
)

// ModulePrintf is the subset of the cluster's module-based logger that
// BinlogSyncerLogger needs. *cluster.Cluster satisfies this via its existing
// LogModulePrintf method, so no import back into the cluster package is required.
type ModulePrintf interface {
	LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int
}

// BinlogSyncerLogger adapts go-mysql's replication.BinlogSyncer logger
// (siddontang/go-log/loggers.Advanced) to a cluster's module-based logger.
//
// go-mysql's default logger writes every connection lifecycle event
// ("create BinlogSyncer with config ...", "rotate to ...", "syncer is
// closing...") to stdout at Info level, unconditionally. replication-manager
// opens one of these syncers for binlog metadata refresh on every monitoring
// tick, plus query-event scanning each tick when MonitorBinlogEvents is
// enabled, plus timestamp lookups during PITR binlog replay — frequent
// enough in the common case that this floods the logs with routine noise.
//
// All call sites use config.ConstLogModPurge, so every level is mapped
// (never dropped) and gated purely by the existing log-binlog-purge /
// log-level-binlog-purge knobs — the same knobs that already control the
// rest of the binlog purge logging. Upstream Info is deliberately downgraded
// to DEBUG here (see the Info* methods below) since it is otherwise the
// dominant source of noise; Warn/Error/Fatal/Panic keep their original
// severity. Raising log-level-binlog-purge to DEBUG re-enables the routine
// chatter for troubleshooting without any new config. This intentionally
// does NOT honor cluster.Conf.Verbose as a force-print override (the
// repo-wide convention elsewhere): verbose mode would otherwise make this
// logger impossible to fully suppress.
//
// Fatal/Panic are the one deliberate exception: they are always force-logged
// regardless of the module gate. NewBinlogSyncer calls Logger.Fatal (then
// os.Exit(1)) when ServerID==0, and nothing else in the monitoring loop
// captures that failure — silently dropping the line would turn a
// misconfiguration into an unexplained process exit.
type BinlogSyncerLogger struct {
	printer   ModulePrintf
	serverURL string
	context   string // short tag identifying the call site, e.g. "binlog-meta"
	module    int    // config.ConstLogMod* to attribute the message to
}

// NewBinlogSyncerLogger builds a BinlogSyncerLogger scoped to a single server
// and call site.
func NewBinlogSyncerLogger(printer ModulePrintf, serverURL, context string, module int) *BinlogSyncerLogger {
	return &BinlogSyncerLogger{printer: printer, serverURL: serverURL, context: context, module: module}
}

func (l *BinlogSyncerLogger) format(msg string) string {
	return fmt.Sprintf("[%s] %s: %s", l.context, l.serverURL, msg)
}

func (l *BinlogSyncerLogger) log(forcingLog bool, level, msg string) {
	l.printer.LogModulePrintf(forcingLog, l.module, level, "%s", l.format(msg))
}

func (l *BinlogSyncerLogger) Debug(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Debugf(f string, args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Debugln(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintln(args...))
}

// Info* is downgraded to DEBUG here: go-mysql logs routine connection
// lifecycle events (create/open/close, rotate, kill last connection) at Info
// unconditionally, and replication-manager opens a syncer per monitoring
// tick, so passing these through at Info would flood the logs. Raise
// log-level-binlog-purge to DEBUG to see them again. This does not affect
// replication-manager's own binlog-purge Info logs (e.g. "Refreshed oldest
// timestamp..."), which are logged directly via cluster.LogModulePrintf, not
// through this adapter.
func (l *BinlogSyncerLogger) Info(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Infof(f string, args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Infoln(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintln(args...))
}

// Print* is never actually called by go-mysql's BinlogSyncer; mapped to
// Debug level to keep it consistent with the rest of the adapter should any
// caller reach it via the loggers.Standard interface.
func (l *BinlogSyncerLogger) Print(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Printf(f string, args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Println(args ...interface{}) {
	l.log(false, config.LvlDbg, fmt.Sprintln(args...))
}

func (l *BinlogSyncerLogger) Warn(args ...interface{}) {
	l.log(false, config.LvlWarn, fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Warnf(f string, args ...interface{}) {
	l.log(false, config.LvlWarn, fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Warnln(args ...interface{}) {
	l.log(false, config.LvlWarn, fmt.Sprintln(args...))
}

func (l *BinlogSyncerLogger) Error(args ...interface{}) {
	l.log(false, config.LvlErr, fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Errorf(f string, args ...interface{}) {
	l.log(false, config.LvlErr, fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Errorln(args ...interface{}) {
	l.log(false, config.LvlErr, fmt.Sprintln(args...))
}

// Fatal* mirrors go-log's default logger (log as error, then os.Exit(1)).
// Always force-logged — see the type doc comment for why.
func (l *BinlogSyncerLogger) Fatal(args ...interface{}) {
	l.log(true, config.LvlErr, fmt.Sprint(args...))
	os.Exit(1)
}
func (l *BinlogSyncerLogger) Fatalf(f string, args ...interface{}) {
	l.log(true, config.LvlErr, fmt.Sprintf(f, args...))
	os.Exit(1)
}
func (l *BinlogSyncerLogger) Fatalln(args ...interface{}) {
	l.log(true, config.LvlErr, fmt.Sprintln(args...))
	os.Exit(1)
}

// Panic* mirrors go-log's default logger (log as error, then panic). Always
// force-logged — see the type doc comment for why.
func (l *BinlogSyncerLogger) Panic(args ...interface{}) {
	msg := fmt.Sprint(args...)
	l.log(true, config.LvlErr, msg)
	panic(msg)
}
func (l *BinlogSyncerLogger) Panicf(f string, args ...interface{}) {
	msg := fmt.Sprintf(f, args...)
	l.log(true, config.LvlErr, msg)
	panic(msg)
}
func (l *BinlogSyncerLogger) Panicln(args ...interface{}) {
	msg := fmt.Sprintln(args...)
	l.log(true, config.LvlErr, msg)
	panic(msg)
}
