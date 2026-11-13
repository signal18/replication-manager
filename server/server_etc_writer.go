// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/etcwriter"
	"github.com/signal18/replication-manager/utils/state"
)

// etcWriteTimeout bounds one write of the /etc writer: a rotation never waits longer (F2).
const etcWriteTimeout = 10 * time.Second

// etcCredentials is the /etc writer (CREDENTIAL_STORES.md "The /etc writer") and, per server
// credential, why its last write failed (GWARN025 until a write succeeds).
type etcCredentials struct {
	mu     sync.Mutex
	writer *etcwriter.Writer
	// why is a server credential's last failure, "" or absent when the file holds its value
	why map[string]string
}

// etcConfigFiles are the configuration files the start read outside the working directory:
// /etc/replication-manager/config.toml and its cluster.d include files, never the files
// replication-manager writes itself (default.toml, the clusters' saved files).
func (repman *ReplicationManager) etcConfigFiles() []string {
	work := filepath.Clean(repman.Conf.WorkingDir) + string(filepath.Separator)
	seen := map[string]bool{}
	var out []string
	for _, f := range repman.ConfigPathList {
		f = filepath.Clean(f)
		if f == "" || seen[f] || strings.HasPrefix(f, work) {
			continue
		}
		if fi, err := os.Lstat(f); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// startEtcWriter is called while replication-manager is still root, before it drops root
// (LimitPrivileges): dropping, a child that stays root writes the files; not dropping, this
// process writes them itself. Off (monitoring-etc-credential-writer) or no file: no writer.
func (repman *ReplicationManager) startEtcWriter(dropping bool) {
	if !repman.Conf.MonitoringEtcCredentialWriter {
		return
	}
	files := repman.etcConfigFiles()
	if len(files) == 0 {
		return
	}
	k := &repman.etcCredentials
	k.mu.Lock()
	defer k.mu.Unlock()
	if !dropping {
		k.writer = etcwriter.Local(files, isServerSecretKey)
		return
	}
	w, err := etcwriter.Start(files)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The /etc credential writer did not start: %s", err)
		return
	}
	k.writer = w
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "The /etc credential writer runs as root for %s", strings.Join(files, ", "))
}

// syncEtcCredential writes server credential key's current value to the configuration files
// under /etc that set it, after a rotation or a change took it in the versioned store. It
// returns the files written, and why not (also kept for GWARN025). A key no file sets: nothing.
func (repman *ReplicationManager) syncEtcCredential(key string) ([]string, string) {
	if _, inFile := repman.Conf.GetImmuableFlag(key); !inFile || !isServerSecretKey(key) {
		return nil, ""
	}
	k := &repman.etcCredentials
	k.mu.Lock()
	w := k.writer
	k.mu.Unlock()
	var why string
	var files []string
	switch {
	case !repman.Conf.MonitoringEtcCredentialWriter:
		why = "the /etc writer is off (monitoring-etc-credential-writer)"
	case w == nil:
		why = "no /etc writer: replication-manager did not start as root, or the writer was off at the start"
	default:
		plain := repman.serverSecretVersion(key).Value
		var err error
		files, err = w.Replace(key, plain, repman.Conf.GetEncryptedString(plain), etcWriteTimeout)
		if err != nil {
			why = err.Error()
		} else if len(files) > 0 {
			// the file now holds the current version: the next start must not take it as an
			// operator's edit of the file
			if repman.serverVersionStore().MarkConfigFileValue(key, plain) {
				repman.writeServerSecretFile()
			}
			repman.Conf.SetImmuableFlag(key, plain)
			repman.logSecurityEvent("etc_credential_written", key, "", fmt.Sprintf("Rotated %s written to %s", key, strings.Join(files, ", ")))
		}
	}
	k.mu.Lock()
	if k.why == nil {
		k.why = map[string]string{}
	}
	if why == "" {
		delete(k.why, key)
	} else {
		k.why[key] = why
		repman.Logrus.Warnf("The configuration file under /etc keeps the old %s: %s", key, why)
	}
	k.mu.Unlock()
	return files, why
}

// etcCredentialStatus: whether a file under /etc sets server credential key, and why its last
// write there failed ("" when the file holds the current value).
func (repman *ReplicationManager) etcCredentialStatus(key string) (bool, string) {
	if _, inFile := repman.Conf.GetImmuableFlag(key); !inFile {
		return false, ""
	}
	k := &repman.etcCredentials
	k.mu.Lock()
	defer k.mu.Unlock()
	return true, k.why[key]
}

// checkEtcCredentials raises GWARN025 for each server credential whose file under /etc still
// holds an old value.
func (repman *ReplicationManager) checkEtcCredentials() {
	k := &repman.etcCredentials
	k.mu.Lock()
	why := make(map[string]string, len(k.why))
	for key, w := range k.why {
		why[key] = w
	}
	k.mu.Unlock()
	for key, w := range why {
		repman.SetState("GWARN025@"+key, state.State{ErrType: "WARNING", ErrKey: "GWARN025", ErrFrom: "REPMAN",
			ErrDesc: fmt.Sprintf(config.GlobalError["GWARN025"], key, w)})
	}
}
