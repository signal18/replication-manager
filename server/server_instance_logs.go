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
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

// The instance-level log rings of the Security Logs and Access Logs tabs of the instance
// Dashboard (GUI: Pages/GlobalItems). See doc/implementation/ui-components/CLUSTER_LOGS_TABS.md.
//
// Each ring is an s18log.HttpLog: a hard max (log-security-length, log-api-access-length,
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
	entries := r.log.Entries()
	return globalLogsSnapshot{Buffer: entries, Len: r.log.Len, Dropped: r.dropped.Load()}
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

// accessLogSecretVar tells the route variables whose value is never written to the access
// log: a setting value, a token, a secret or a password can travel in an API path.
func accessLogSecretVar(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"value", "token", "secret", "pass", "key", "cred", "code", "route", "peer"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
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
		if value, ok := vars[name]; ok && !accessLogSecretVar(name) {
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

// apiAccessLogMiddleware records each API call in the Access Logs ring: method, path (see
// accessLogPath), status, duration, user and remote address. Never the query string, a
// header, a body or a token (F10). Off with log-api-access = false (T14). The calls that
// read the access log itself are not recorded, or the tab would fill with its own polling.
func (repman *ReplicationManager) apiAccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !repman.Conf.LogAPIAccess || repman.APIAccessLogs == nil || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/global/access-logs" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &accessStatusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		repman.recordAPIAccess(r, sw.status, time.Since(start))
	})
}

func (repman *ReplicationManager) recordAPIAccess(r *http.Request, status int, duration time.Duration) {
	if status == 0 {
		status = http.StatusOK
	}
	template := ""
	if route := mux.CurrentRoute(r); route != nil {
		template, _ = route.GetPathTemplate()
	}
	user := ""
	if r.Header.Get("Authorization") != "" {
		user = repman.GetUserFromRequest(r)
	}
	if user == "" {
		user = "-"
	}
	level := config.LvlInfo
	switch {
	case status >= 500:
		level = config.LvlErr
	case status >= 400:
		level = config.LvlWarn
	}
	repman.APIAccessLogs.add(level, config.ConstLogModGeneral, fmt.Sprintf("[access] %s %s %d %dms user %s from %s",
		r.Method, accessLogPath(template, mux.Vars(r), r.URL.Path), status, duration.Milliseconds(), user, r.RemoteAddr))
}
