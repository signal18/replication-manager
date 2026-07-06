// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
)

// Config event log — replay side.
// Design: doc/implementation/config/CONFIG_EVENT_LOG.md.
//
// Peers publish their locally-born config mutations in
// event-changed.<RRBid>.log files inside the shared config git repo. On
// every git-sync cycle this instance fetches those logs (remote-tracking
// refs only, the working tree is never merged into), applies the events
// past its per-log cursor in (ts, author) order under per-key LWW, and
// records provenance so the next save-diff does not re-emit what was just
// replayed. Cursors and provenance are instance-local state, persisted in
// event-log-state.json (gitignored).

type peerEventLogState struct {
	// Cursors maps event log filename → number of lines already consumed.
	Cursors map[string]int `json:"cursors"`
	// Provenance maps cluster name → key → last applied change origin.
	Provenance map[string]map[string]cluster.EventProvenanceEntry `json:"provenance"`
}

func (repman *ReplicationManager) eventLogStatePath() string {
	return filepath.Join(repman.Conf.WorkingDir, "event-log-state.json")
}

func (repman *ReplicationManager) loadEventLogState() *peerEventLogState {
	st := &peerEventLogState{
		Cursors:    make(map[string]int),
		Provenance: make(map[string]map[string]cluster.EventProvenanceEntry),
	}
	data, err := os.ReadFile(repman.eventLogStatePath())
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot parse %s, starting fresh: %s", repman.eventLogStatePath(), err)
		return &peerEventLogState{
			Cursors:    make(map[string]int),
			Provenance: make(map[string]map[string]cluster.EventProvenanceEntry),
		}
	}
	if st.Cursors == nil {
		st.Cursors = make(map[string]int)
	}
	if st.Provenance == nil {
		st.Provenance = make(map[string]map[string]cluster.EventProvenanceEntry)
	}
	return st
}

func (repman *ReplicationManager) saveEventLogState(st *peerEventLogState) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := repman.eventLogStatePath() + ".new"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot write state: %s", err)
		return
	}
	if err := os.Rename(tmp, repman.eventLogStatePath()); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot rename state: %s", err)
	}
}

// ReplayPeerConfigEvents fetches peer event logs from the config repo remote
// and applies the not-yet-consumed events. Called from the detached git-sync
// task before the push, so replayed changes land in the same save cycle.
func (repman *ReplicationManager) ReplayPeerConfigEvents() {
	if repman.Conf.GitUrl == "" || repman.ConfigManager == nil {
		return
	}
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	if tok == "" {
		return
	}

	st := repman.loadEventLogState()

	// Seed cluster provenance tables from persisted state (no-op for keys
	// already tracked in memory — memory is newer).
	for name, prov := range st.Provenance {
		if cl := repman.getClusterByName(name); cl != nil {
			cl.ImportEventProvenance(prov)
		}
	}

	// Fetch under the git lock: read-only for the working tree, but the
	// object store is shared with concurrent commit/push operations.
	var files map[string][]byte
	var ferr error
	repman.ConfigManager.WithGitLock(func() {
		files, ferr = repman.Conf.FetchRemoteRootFiles(repman.Conf.WorkingDir, repman.Conf.GitUsername, tok, "event-changed.", ".log")
	})
	if ferr != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Event log: cannot fetch peer logs: %s", ferr)
		return
	}

	selfName := fmt.Sprintf("event-changed.%d.log", repman.Conf.ArbitrationSasUniqueId)
	type pendingEvent struct {
		ev cluster.ConfigChangeEvent
	}
	var todo []pendingEvent
	for fname, data := range files {
		if fname == selfName {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		cur := st.Cursors[fname]
		if cur > len(lines) {
			// The peer log shrank: it was rotated. Replay from the start —
			// per-key LWW skips anything older than what we already hold.
			cur = 0
		}
		for i := cur; i < len(lines); i++ {
			l := strings.TrimSpace(lines[i])
			if l == "" {
				continue
			}
			var ev cluster.ConfigChangeEvent
			if err := json.Unmarshal([]byte(l), &ev); err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: skipping unparsable line %d of %s: %s", i+1, fname, err)
				continue
			}
			todo = append(todo, pendingEvent{ev: ev})
		}
		st.Cursors[fname] = len(lines)
	}

	if len(todo) > 0 {
		// Apply in (ts, author) order across all peer logs so every instance
		// resolves concurrent writes the same way.
		sort.SliceStable(todo, func(i, j int) bool {
			ti, tj := todo[i].ev.Ts, todo[j].ev.Ts
			if ti.Equal(tj) {
				return todo[i].ev.Author < todo[j].ev.Author
			}
			return ti.Before(tj)
		})
		applied := 0
		for _, p := range todo {
			if repman.applyPeerConfigEvent(p.ev) {
				applied++
			}
		}
		if applied > 0 {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Event log: replayed %d peer config change event(s)", applied)
		}
	}

	// Persist cursors and the provenance tables of every cluster.
	for _, name := range repman.ClusterList {
		if cl := repman.getClusterByName(name); cl != nil {
			st.Provenance[name] = cl.ExportEventProvenance()
		}
	}
	repman.saveEventLogState(st)
}

// applyPeerConfigEvent applies one peer event under per-key LWW. Returns
// true when the event mutated the cluster config. Provenance is recorded
// with the event's original value form (ciphertext for secrets) so the next
// save-diff recognizes and subtracts the echo.
func (repman *ReplicationManager) applyPeerConfigEvent(ev cluster.ConfigChangeEvent) bool {
	cl := repman.getClusterByName(ev.Cluster)
	if cl == nil {
		return false
	}
	// Instance-local keys are never applied, even from logs written before
	// the exclusion existed.
	if cluster.IsInstanceLocalConfigKey(ev.Key) {
		return false
	}
	// LWW: never let a stale event overwrite a newer value; author id breaks
	// timestamp ties so all peers pick the same winner.
	if p, ok := cl.GetEventProvenance(ev.Key); ok {
		if p.Ts.After(ev.Ts) || (p.Ts.Equal(ev.Ts) && p.Author >= ev.Author) {
			return false
		}
	}

	value := ev.Value
	if ev.Action == "unset" {
		def, ok := cl.Conf.DefaultFlagMap[ev.Key]
		if !ok {
			// Unknown default: record the provenance so LWW ordering holds,
			// but there is nothing to apply.
			cl.RecordEventProvenance(ev.Key, ev.Value, ev.Ts, ev.Author)
			return false
		}
		value = fmt.Sprintf("%v", def)
	}

	applyVal := value
	if _, isSecret := cl.Conf.Secrets[ev.Key]; isSecret && strings.Contains(applyVal, "hash_") {
		// Peers share the encryption key through the config exchange, so the
		// ciphertext decrypts here; the plaintext feeds the normal setter.
		applyVal = cl.Conf.DecryptSecretValue(ev.Key, applyVal)
	}

	err := repman.setClusterSetting(cl, ev.Key, applyVal)
	if err != nil {
		// Keys without a dedicated setter case (bool switches, rarely-set
		// values) are applied generically through the same viper/mapstructure
		// path the config loader uses.
		if gerr := repman.applyGenericClusterKey(cl, ev.Key, applyVal); gerr != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Event log: cannot apply %s=%s on %s: %s / %s", ev.Key, cl.Conf.PrintSecret(applyVal), ev.Cluster, err, gerr)
			// Record provenance anyway: the event was consumed, LWW ordering
			// must not regress, and the save-diff must not re-emit it as ours.
			cl.RecordEventProvenance(ev.Key, ev.Value, ev.Ts, ev.Author)
			return false
		}
	}

	if _, isSecret := cl.Conf.Secrets[ev.Key]; isSecret && ev.Action == "set" && strings.Contains(ev.Value, "hash_") {
		// Seed the ciphertext cache with the peer's exact ciphertext so our
		// next save writes the identical bytes: both peers' saved configs
		// converge and the echo subtraction matches on the first save.
		sec := cl.Conf.Secrets[ev.Key]
		sec.Crypted = ev.Value
		cl.Conf.Secrets[ev.Key] = sec
	}

	cl.RecordEventProvenance(ev.Key, ev.Value, ev.Ts, ev.Author)
	return true
}

// applyGenericClusterKey sets a single config key through viper/mapstructure
// — the same decoding path the config loader uses — for keys that have no
// dedicated case in setClusterSetting.
func (repman *ReplicationManager) applyGenericClusterKey(cl *cluster.Cluster, key string, value string) error {
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader(key + " = " + strconv.Quote(value))); err != nil {
		return err
	}
	if err := v.Unmarshal(cl.Conf, func(dc *mapstructure.DecoderConfig) {
		dc.WeaklyTypedInput = true
	}); err != nil {
		return err
	}
	if cl.Conf.DynamicFlagMap != nil {
		cl.Conf.DynamicFlagMap[key] = value
	}
	return nil
}
