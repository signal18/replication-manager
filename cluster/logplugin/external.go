// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// External binary plugin support.
//
// External plugins are standalone executables placed by the back office under
//
//	<cluster.WorkingDir>/plugins/
//
// via the normal per-cluster GitLab pull.  The directory layout is:
//
//	/var/lib/replication-manager/
//	└── mycluster/                         ← cluster.WorkingDir
//	    ├── mycluster.toml
//	    └── plugins/
//	        ├── plugin-innodb-corruption   ← executable dropped by back office
//	        └── plugin-replication-drift
//
// Wire protocol (JSON over stdin/stdout):
//
//	stdin  ← {"server_url":"h:3306","error_log":[...],"sql_error_log":[...],"slow_log":[...]}
//	stdout → {"findings":[{"err_key":"WARN0300","severity":"WARNING","description":"..."}]}
//	exit 0  = evaluation complete (findings may be empty — means "all clear")
//	exit ≠0 = plugin error; repman logs a WARN0203 and skips state injection
package logplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

const (
	// DefaultPluginTimeout is the maximum time a plugin binary may run per evaluation.
	DefaultPluginTimeout = 5 * time.Second

	// PluginDirName is the subdirectory inside cluster.WorkingDir where
	// the back office drops plugin binaries via the GitLab pull.
	PluginDirName = "plugins"
)

// stdioRequest is the JSON payload written to a plugin's stdin.
type stdioRequest struct {
	ServerURL   string     `json:"server_url"`
	ErrorLog    []stdioMsg `json:"error_log"`
	SqlErrorLog []stdioMsg `json:"sql_error_log"`
	SlowLog     []stdioMsg `json:"slow_log"`
}

// stdioMsg is the wire representation of an s18log.HttpMessage.
// Kept flat and dependency-free so plugin authors need no replication-manager imports.
type stdioMsg struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

// stdioResponse is the JSON payload read from a plugin's stdout.
type stdioResponse struct {
	Findings []stdioFinding `json:"findings"`
}

// stdioFinding is the wire representation of a Finding returned by a plugin.
type stdioFinding struct {
	ErrKey      string `json:"err_key"`
	Severity    string `json:"severity"`    // "WARNING" or "ERROR"
	Description string `json:"description"`
}

// ExternalLogPlugin wraps a downloaded plugin binary as a LogPlugin.
// Each Evaluate() call spawns an independent child process so multiple
// servers can be evaluated in parallel without races.
type ExternalLogPlugin struct {
	name    string
	binPath string
	timeout time.Duration
}

// NewExternalLogPlugin creates a wrapper for the executable at binPath.
// name is the stable identifier used in config and log tags.
func NewExternalLogPlugin(name, binPath string, timeout time.Duration) *ExternalLogPlugin {
	if timeout <= 0 {
		timeout = DefaultPluginTimeout
	}
	return &ExternalLogPlugin{name: name, binPath: binPath, timeout: timeout}
}

func (p *ExternalLogPlugin) Name() string { return p.name }

func (p *ExternalLogPlugin) Evaluate(src LogSource) []Finding {
	req := stdioRequest{
		ServerURL:   src.ServerURL,
		ErrorLog:    msgsToWire(src.ErrorLog),
		SqlErrorLog: msgsToWire(src.SqlErrorLog),
		SlowLog:     msgsToWire(src.SlowLog),
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("marshal error: %v", err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.binPath) // #nosec G204 — path validated in LoadPluginsFromDir
	cmd.Stdin = bytes.NewReader(payload)

	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return pluginErrFinding(p.name, src.ServerURL, "plugin timed out")
	}
	if err != nil {
		return pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("exec error: %v", err))
	}

	var resp stdioResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("bad JSON response: %v", err))
	}

	findings := make([]Finding, 0, len(resp.Findings))
	for _, sf := range resp.Findings {
		sev := Severity(strings.ToUpper(sf.Severity))
		if sev != SeverityWarning && sev != SeverityError {
			sev = SeverityWarning
		}
		findings = append(findings, Finding{
			ErrKey:      sf.ErrKey,
			Severity:    sev,
			Description: sf.Description,
		})
	}
	return findings
}

// LoadPluginsFromDir scans pluginDir for executable files, creates an
// ExternalLogPlugin for each one, and registers/replaces it in reg.
//
//   - Non-executable files and dotfiles are silently skipped.
//   - If a plugin with the same name is already registered it is replaced,
//     so that a git pull delivering a new binary takes effect on the next tick
//     without restarting repman.
//   - If pluginDir does not exist the function returns (0, nil) — not an error.
func LoadPluginsFromDir(pluginDir string, reg *Registry) (int, error) {
	entries, err := os.ReadDir(pluginDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("logplugin: cannot read plugin dir %q: %w", pluginDir, err)
	}

	loaded := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Require at least one execute bit to be set.
		if info.Mode()&0111 == 0 {
			continue
		}

		binPath := filepath.Join(pluginDir, e.Name())
		name := pluginNameFromBinary(e.Name())
		reg.replace(name, NewExternalLogPlugin(name, binPath, DefaultPluginTimeout))
		loaded++
	}
	return loaded, nil
}

// PluginDir returns the canonical plugin directory for a cluster given its
// WorkingDir (= Conf.WorkingDir + "/" + cluster.Name).
func PluginDir(clusterWorkingDir string) string {
	return filepath.Join(clusterWorkingDir, PluginDirName)
}

// pluginNameFromBinary derives a stable name from a binary filename.
//
//	"plugin-innodb-corruption" → "innodb-corruption"
//	"plugin-foo.exe"          → "foo"
//	"myplugin"                → "myplugin"
func pluginNameFromBinary(filename string) string {
	name := strings.TrimPrefix(filename, "plugin-")
	name = strings.TrimSuffix(name, ".exe")
	return name
}

// replace swaps the existing plugin with the same Name(), or appends.
func (r *Registry) replace(name string, p LogPlugin) {
	for i, existing := range r.plugins {
		if existing.Name() == name {
			r.plugins[i] = p
			return
		}
	}
	r.plugins = append(r.plugins, p)
}

// msgsToWire converts []s18log.HttpMessage to the over-the-wire slice.
func msgsToWire(msgs []s18log.HttpMessage) []stdioMsg {
	out := make([]stdioMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, stdioMsg{
			Level:     m.Level,
			Timestamp: m.Timestamp,
			Text:      m.Text,
		})
	}
	return out
}

// pluginErrFinding returns a single WARNING Finding describing an execution
// error so the operator sees it in the state machine UI.
// WARN0203 is reserved for plugin execution failures.
func pluginErrFinding(pluginName, serverURL, msg string) []Finding {
	return []Finding{{
		ErrKey:   "WARN0203",
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"log plugin %q failed on %s: %s", pluginName, serverURL, msg),
	}}
}
