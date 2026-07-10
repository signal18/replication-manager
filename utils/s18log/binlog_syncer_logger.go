// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s18log

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// isBenignCloseError matches go-mysql's close-time KILL failure: when a
// syncer closes it tries to KILL its own dump thread, which the server has
// often already reaped ("kill connection N error ... Unknown thread id" /
// error 1094). It is harmless — the connection is going away regardless —
// but replication-manager opens+closes a syncer per tick, so at Error level
// it floods. Downgraded to DEBUG.
func isBenignCloseError(msg string) bool {
	return strings.Contains(msg, "kill connection") &&
		(strings.Contains(msg, "Unknown thread id") || strings.Contains(msg, "1094"))
}

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
// regardless of the module gate. NewBinlogSyncer calls Logger.Fatal when
// ServerID==0, and nothing else in the monitoring loop captures that failure
// — silently dropping the line would turn a misconfiguration into an
// unexplained process exit. Fatal* panics with the FatalError sentinel below
// instead of calling os.Exit(1), so a recover()-based wrapper around the
// syncer constructor (cluster.newSafeBinlogSyncer) can turn it into an
// ordinary error instead of killing the whole replication-manager process.
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

func (l *BinlogSyncerLogger) errorOrDebug(msg string) {
	if isBenignCloseError(msg) {
		l.log(false, config.LvlDbg, msg)
		return
	}
	l.log(false, config.LvlErr, msg)
}
func (l *BinlogSyncerLogger) Error(args ...interface{}) {
	l.errorOrDebug(fmt.Sprint(args...))
}
func (l *BinlogSyncerLogger) Errorf(f string, args ...interface{}) {
	l.errorOrDebug(fmt.Sprintf(f, args...))
}
func (l *BinlogSyncerLogger) Errorln(args ...interface{}) {
	l.errorOrDebug(fmt.Sprintln(args...))
}

// FatalError is the panic value used by Fatal*/Fatalf/Fatalln in place of
// os.Exit(1). go-mysql's replication.NewBinlogSyncer calls Logger.Fatal(...)
// when its ServerID is 0, with no way to recover short of intercepting the
// panic.
//
// Recovery policy: cluster.newSafeBinlogSyncer is the sole, narrow boundary
// that recovers this. It deliberately recovers ALL panics raised during
// construction, not just *FatalError — a single misconfigured or unexpected
// binlog syncer must never be allowed to exit the process or take down other
// monitored clusters, so the boundary fails safe regardless of the panic's
// type. *FatalError is distinguished from other panics only to produce a
// clearer wrapped error message; both are contained the same way.
type FatalError struct {
	msg string
}

func (e *FatalError) Error() string { return e.msg }

// Recovered force-logs msg at Error level, unconditionally (like Fatal/Panic
// — see the type doc comment for why). It is meant to be called by a
// recover()-based wrapper (cluster.newSafeBinlogSyncer) right after it
// catches a panic raised during construction, so the recovery itself is
// always high-visibility — independent of whatever level a caller further
// up the stack chooses to log its returned error at.
func (l *BinlogSyncerLogger) Recovered(msg string) {
	l.log(true, config.LvlErr, msg)
}

// Fatal* mirrors go-log's default logger in that it always logs as error
// first, but panics with FatalError instead of calling os.Exit(1) — see the
// FatalError doc comment for why. Always force-logged — see the type doc
// comment for why.
func (l *BinlogSyncerLogger) Fatal(args ...interface{}) {
	msg := fmt.Sprint(args...)
	l.log(true, config.LvlErr, msg)
	panic(&FatalError{msg: msg})
}
func (l *BinlogSyncerLogger) Fatalf(f string, args ...interface{}) {
	msg := fmt.Sprintf(f, args...)
	l.log(true, config.LvlErr, msg)
	panic(&FatalError{msg: msg})
}
func (l *BinlogSyncerLogger) Fatalln(args ...interface{}) {
	msg := fmt.Sprintln(args...)
	l.log(true, config.LvlErr, msg)
	panic(&FatalError{msg: msg})
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
