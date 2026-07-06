// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// Config event log — save-diff authoring side.
// Design: doc/implementation/config/CONFIG_EVENT_LOG.md.
//
// Every config save writes its file crash-safe (content built in memory,
// written to <file>.new, renamed over the old file) and key-diffs the new
// content against the previous save. Changes that were not just replayed
// from a peer (provenance subtraction) are locally-born mutations and are
// appended to this instance's event-changed.<RRBid>.log, which travels to
// peers through the regular config git push.

// ConfigChangeEvent is one line of event-changed.<RRBid>.log.
type ConfigChangeEvent struct {
	Ts      time.Time `json:"ts"`
	Cluster string    `json:"cluster"`
	Author  int       `json:"author"` // arbitration-external-unique-id of the authoring instance
	Action  string    `json:"action"` // "set", or "unset" when the key returned to its default
	Key     string    `json:"key"`
	Value   string    `json:"value,omitempty"` // secrets travel as hash_ ciphertext only
}

// EventProvenanceEntry records where the current value of a key came from.
// It is both the echo-subtraction table (a save-diff candidate whose value
// was just replayed from a peer is not re-emitted) and the LWW table (a
// stale peer event must not overwrite a newer value).
type EventProvenanceEntry struct {
	Value  string    `json:"value"`
	Ts     time.Time `json:"ts"`
	Author int       `json:"author"`
}

// EventLogPath returns the event log location for the given author id inside
// the config git working directory.
func EventLogPath(workingDir string, author int) string {
	return filepath.Join(workingDir, fmt.Sprintf("event-changed.%d.log", author))
}

// eventLogWriteLock serializes appends to this instance's own event log:
// cluster saves are queued but defensive against future parallel callers.
var eventLogWriteLock sync.Mutex

func (cluster *Cluster) RecordEventProvenance(key string, value string, ts time.Time, author int) {
	cluster.eventProvLock.Lock()
	defer cluster.eventProvLock.Unlock()
	if cluster.eventProv == nil {
		cluster.eventProv = make(map[string]EventProvenanceEntry)
	}
	cluster.eventProv[key] = EventProvenanceEntry{Value: value, Ts: ts, Author: author}
}

func (cluster *Cluster) GetEventProvenance(key string) (EventProvenanceEntry, bool) {
	cluster.eventProvLock.Lock()
	defer cluster.eventProvLock.Unlock()
	p, ok := cluster.eventProv[key]
	return p, ok
}

// ExportEventProvenance snapshots the provenance table for persistence in
// the instance-local event-log state file.
func (cluster *Cluster) ExportEventProvenance() map[string]EventProvenanceEntry {
	cluster.eventProvLock.Lock()
	defer cluster.eventProvLock.Unlock()
	out := make(map[string]EventProvenanceEntry, len(cluster.eventProv))
	for k, v := range cluster.eventProv {
		out[k] = v
	}
	return out
}

// ImportEventProvenance seeds the provenance table from the persisted state
// file at startup. Existing in-memory entries win: they are newer than
// anything read from disk.
func (cluster *Cluster) ImportEventProvenance(m map[string]EventProvenanceEntry) {
	cluster.eventProvLock.Lock()
	defer cluster.eventProvLock.Unlock()
	if cluster.eventProv == nil {
		cluster.eventProv = make(map[string]EventProvenanceEntry)
	}
	for k, v := range m {
		if _, ok := cluster.eventProv[k]; !ok {
			cluster.eventProv[k] = v
		}
	}
}

// flattenTomlValues parses toml content into a flat key → printed-value map,
// recursing into sections ("section.key").
func flattenTomlValues(data []byte) (map[string]string, error) {
	t, err := toml.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	var walk func(prefix string, tree *toml.Tree)
	walk = func(prefix string, tree *toml.Tree) {
		for _, k := range tree.Keys() {
			v := tree.GetPath([]string{k})
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if sub, ok := v.(*toml.Tree); ok {
				walk(key, sub)
			} else {
				out[key] = fmt.Sprintf("%v", v)
			}
		}
	}
	walk("", t)
	return out, nil
}

// stripTomlSection reduces a flattened map to the keys under the given
// section, with the section prefix removed. Empty section returns the map
// unchanged (root-level files like immutable.toml).
func stripTomlSection(m map[string]string, section string) map[string]string {
	if section == "" {
		return m
	}
	out := make(map[string]string)
	pre := section + "."
	for k, v := range m {
		if strings.HasPrefix(k, pre) {
			key := strings.TrimPrefix(k, pre)
			if key == "title" {
				continue
			}
			out[key] = v
		}
	}
	return out
}

// emitConfigChangeEvents key-diffs the previous and new content of a saved
// config file and appends locally-born changes to this instance's event log.
// Changes whose value was just replayed from a peer (provenance author is
// not us and the value matches) are echoes of the replay and are subtracted,
// never re-emitted — this is what makes ping-pong impossible.
func (cluster *Cluster) emitConfigChangeEvents(oldData []byte, newData []byte, section string) {
	oldFlat, err := flattenTomlValues(oldData)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot parse previous config for diff: %s", err)
		return
	}
	newFlat, err := flattenTomlValues(newData)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot parse new config for diff: %s", err)
		return
	}
	o := stripTomlSection(oldFlat, section)
	n := stripTomlSection(newFlat, section)

	keys := make(map[string]bool, len(o)+len(n))
	for k := range o {
		keys[k] = true
	}
	for k := range n {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	self := cluster.Conf.ArbitrationSasUniqueId
	now := time.Now()
	var events []ConfigChangeEvent
	for _, k := range sorted {
		ov, ook := o[k]
		nv, nok := n[k]
		if ook && nok && ov == nv {
			continue
		}
		action, val := "set", nv
		if !nok {
			action, val = "unset", ""
		}
		// Echo subtraction: this value was just replayed from a peer, it is
		// not a locally-born change.
		if p, ok := cluster.GetEventProvenance(k); ok && p.Author != self && p.Value == val {
			continue
		}
		events = append(events, ConfigChangeEvent{Ts: now, Cluster: cluster.Name, Author: self, Action: action, Key: k, Value: val})
		cluster.RecordEventProvenance(k, val, now, self)
	}
	if len(events) == 0 {
		return
	}

	eventLogWriteLock.Lock()
	defer eventLogWriteLock.Unlock()
	f, err := os.OpenFile(EventLogPath(cluster.Conf.WorkingDir, self), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Event log: cannot open %s: %s", EventLogPath(cluster.Conf.WorkingDir, self), err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Event log: cannot append event: %s", err)
			return
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Event log: %d config change event(s) recorded for cluster %s", len(events), cluster.Name)
}

// saveConfigArtifact writes a config file crash-safe and derives config
// change events from the save diff. Content is compared with the previous
// file first: identical content is not rewritten (stable mtimes, no phantom
// git changes). On change, events are emitted from the key diff (skipped
// when no previous file exists — a first save is a baseline, not a set of
// changes), then the content is written to <path>.new and renamed over the
// old file so a crash mid-save can never leave a truncated config.
func (cluster *Cluster) saveConfigArtifact(filePath string, content []byte, section string, emitEvents bool) (bool, error) {
	oldData, rerr := os.ReadFile(filePath)
	hadOld := rerr == nil
	if hadOld && bytes.Equal(oldData, content) {
		return false, nil
	}
	if emitEvents && hadOld {
		cluster.emitConfigChangeEvents(oldData, content, section)
	}
	tmp := filePath + ".new"
	if err := os.WriteFile(tmp, content, 0666); err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", tmp)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error writing file %s: %s", tmp, err)
		}
		return true, err
	}
	if err := os.Rename(tmp, filePath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error renaming %s over %s: %s", tmp, filePath, err)
		return true, err
	}
	return true, nil
}
