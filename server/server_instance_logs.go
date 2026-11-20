// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

// The instance-level log rings of the Security Logs and Access Logs tabs of the instance
// Dashboard (GUI: Pages/GlobalItems). See doc/implementation/ui-components/CLUSTER_LOGS_TABS.md.
//
// Each ring is an s18log.HttpLog: a hard max of entries (log-security-length, log-api-access-length,
// a non-zero default when 0 or unset), drop-oldest in a fresh backing array, and the
// number of entries dropped since start counted and returned with the entries (T18). A
// display ring wraps all the time on a busy instance, so the drop count is not raised as a
// state: the full history is in the log files.

const (
	DefaultSecurityLogLength  = 200
	DefaultAPIAccessLogLength = 500
	timestampFormat           = "2006/01/02 15:04:05"
)

type instanceLogRing struct {
	log     s18log.HttpLog
	dropped atomic.Uint64
	// the collapse of identical consecutive entries (addCollapsing): the key and text of the
	// newest entry and how many times it came in a row
	mu       sync.Mutex
	lastKey  string
	lastText string
	repeats  int
}

func newInstanceLogRing(size, defaultSize int) *instanceLogRing {
	if size <= 0 {
		size = defaultSize
	}
	return &instanceLogRing{log: s18log.NewHttpLog(size)}
}

// add records one entry; a nil ring (before the server init) records nothing.
func (r *instanceLogRing) add(level string, module int, text string) {
	if r == nil {
		return
	}
	if r.log.AddReportDrop(s18log.HttpMessage{
		Group:     s18log.GroupNone,
		Level:     level,
		Timestamp: time.Now().Format(timestampFormat),
		Text:      text,
		Module:    module,
	}) {
		r.dropped.Add(1)
	}
}

// addCollapsing records an entry unless it repeats the newest one (same key): then the
// newest entry takes the count and the time of the last repeat instead of a new slot, so a
// caller in a loop never floods the ring (F4) nor pushes the other entries out (T4).
func (r *instanceLogRing) addCollapsing(key, level string, module int, text string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().Format(timestampFormat)
	if key != "" && key == r.lastKey {
		r.repeats++
		r.log.Update(0, s18log.HttpMessage{
			Group:     s18log.GroupNone,
			Level:     level,
			Timestamp: now,
			Text:      fmt.Sprintf("%s (repeated %d times)", r.lastText, r.repeats),
			Module:    module,
		})
		return
	}
	r.lastKey, r.lastText, r.repeats = key, text, 1
	r.add(level, module, text)
}

func (r *instanceLogRing) resize(size, defaultSize int) {
	if r == nil {
		return
	}
	if size <= 0 {
		size = defaultSize
	}
	r.log.Resize(size)
}

// snapshot returns the ring's entries, newest first, in the JSON shape of
// /api/global/http-logs, plus the drop count.
func (r *instanceLogRing) snapshot() globalLogsSnapshot {
	if r == nil {
		return globalLogsSnapshot{Buffer: []s18log.HttpMessage{}}
	}
	entries, size := r.log.EntriesAndLen()
	return globalLogsSnapshot{Buffer: entries, Len: size, Dropped: r.dropped.Load()}
}

// addSecurityEvent records one security event (logSecurityEvent): the same fields as the
// security log file, never a secret (the callers pass an event name, a user, an address
// and a message).
func (r *instanceLogRing) addSecurityEvent(event, user, remoteAddr, msg string) {
	level := config.LvlWarn
	if strings.HasSuffix(event, "_success") {
		level = config.LvlInfo
	}
	r.add(level, config.ConstLogModGeneral, fmt.Sprintf("[security] %s: %s (user %q, from %s)", event, msg, user, remoteAddr))
}

// accessLogLoggedVars are the route variables whose value is written to the access log;
// any other variable stays as its placeholder. An allowlist, so a new route carrying a
// secret under a new name ({hash}, {otp}) is never logged in clear: names (cluster,
// server, proxy, app, setting...), ports, and a few action words.
var accessLogLoggedVars = map[string]bool{
	"action": true, "task": true, "level": true, "logType": true, "topology": true,
	"state": true, "index": true, "field": true, "month": true, "taskname": true,
}

func accessLogVarLogged(name string) bool {
	if accessLogLoggedVars[name] {
		return true
	}
	// clusterName, serverName, settingName, serverPort...; never a value, token or id
	return strings.HasSuffix(name, "Name") || strings.HasSuffix(name, "Port")
}

// accessLogClean drops the control characters of a logged field (a URL-decoded path may
// carry a %0a newline, a user name comes from token claims).
func accessLogClean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// accessLogPath builds the logged path from the route template, never from the raw URL:
// the query string is never in it, a variable whose name may carry a secret stays as its
// placeholder ({settingValue}), and the other variables take their value
// (/api/clusters/{clusterName} -> /api/clusters/dev3). Without a template (no route
// matched) only the path, without query, is used.
func accessLogPath(template string, vars map[string]string, rawPath string) string {
	if template == "" {
		return rawPath
	}
	var out strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' {
			out.WriteByte(template[i])
			i++
			continue
		}
		// a placeholder is {name} or {name:regexp}; a regexp may hold braces ({1,3})
		depth, j := 0, i
		for ; j < len(template); j++ {
			if template[j] == '{' {
				depth++
			} else if template[j] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if j >= len(template) {
			out.WriteString(template[i:])
			break
		}
		name := template[i+1 : j]
		if colon := strings.Index(name, ":"); colon >= 0 {
			name = name[:colon]
		}
		if value, ok := vars[name]; ok && accessLogVarLogged(name) {
			out.WriteString(value)
		} else {
			out.WriteString("{" + name + "}")
		}
		i = j + 1
	}
	return out.String()
}

// accessStatusWriter records the status a handler answers with; Flush and Hijack pass
// through, so streaming answers and websockets behave as before.
type accessStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *accessStatusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *accessStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *accessStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *accessStatusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("access log: the response writer cannot be hijacked")
}

// accessLogProtocolRoutes are the routes the jobs containers poll in a loop and that answer
// "no" with a 500 "false" on purpose (the compatibility contract of older dbjobs scripts).
// They are recorded at DEBUG only, under the task module's verbosity (log-task-level at
// debug): at the default level they leave no trace; at debug their entries name the task
// and collapse like any repeated entry.
var accessLogProtocolRoutes = map[string]bool{
	"/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/needs/{taskname}": true,
	"/api/clusters/{clusterName}/jobs-log-level/{task}/{level}":                      true,
}

// apiAccessLogMiddleware records the API calls that matter in the Access Logs ring: every
// call that changes something (any method but GET, HEAD, OPTIONS) and every failed call
// (status 400 and above, refusals first). A successful read is not recorded: the GUI polls
// dozens of them per refresh and would push everything else out of the ring within
// minutes. An entry: method, path (see accessLogPath), status, duration, user and remote
// address; never the query string, a header, a body or a token (F10). Off with
// log-api-access = false (T14). Nothing here may break a request: a panic in the recording
// is recovered and the entry is lost.
func (repman *ReplicationManager) apiAccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !repman.Conf.LogAPIAccess || repman.APIAccessLogs == nil || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &accessStatusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if status < 400 && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions) {
			return
		}
		if route := mux.CurrentRoute(r); route != nil {
			if template, err := route.GetPathTemplate(); err == nil && accessLogProtocolRoutes[template] {
				if repman.Conf.IsEligibleForPrinting(config.ConstLogModTask, config.LvlDbg) {
					repman.recordAPIAccessAt(r, status, time.Since(start), config.LvlDbg, config.ConstLogModTask)
				}
				return
			}
		}
		repman.recordAPIAccess(r, status, time.Since(start))
	})
}

func (repman *ReplicationManager) recordAPIAccess(r *http.Request, status int, duration time.Duration) {
	level := config.LvlInfo
	switch {
	case status >= 500:
		level = config.LvlErr
	case status >= 400:
		level = config.LvlWarn
	}
	repman.recordAPIAccessAt(r, status, duration, level, config.ConstLogModGeneral)
}

// recordAPIAccessAt records one call at the given level and module.
func (repman *ReplicationManager) recordAPIAccessAt(r *http.Request, status int, duration time.Duration, level string, module int) {
	defer func() {
		// nothing in the recording may cost the request
		_ = recover()
	}()
	template := ""
	if route := mux.CurrentRoute(r); route != nil {
		template, _ = route.GetPathTemplate()
	}
	user := ""
	if r.Header.Get("Authorization") != "" {
		// GetUserFromRequest asserts claim types: a malformed token loses only its user, the
		// call (often the refusal worth keeping) is still recorded with user "-"
		user = func() (u string) {
			defer func() { _ = recover() }()
			return repman.GetUserFromRequest(r)
		}()
	}
	if user == "" {
		user = "-"
	}
	method, path, remote := accessLogClean(r.Method), accessLogClean(accessLogPath(template, mux.Vars(r), r.URL.Path)), accessLogClean(r.RemoteAddr)
	user = accessLogClean(user)
	// identical calls in a row collapse into one entry: same method, path, status, user and
	// host (not the source port, which changes with each connection)
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	repman.APIAccessLogs.addCollapsing(fmt.Sprintf("%s %s %d %s %s", method, path, status, user, host), level, module,
		fmt.Sprintf("[access] %s %s %d %dms user %s from %s", method, path, status, duration.Milliseconds(), user, remote))
}
