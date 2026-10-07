// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Scheduled database events in the schema drift detection
// (monitoring-schema-events): the schema scan collects, per server, the events
// with the CRC64 of their definition (EventSchema); compareEventSchema is the
// one implementation of the drift rules, consumed by the schema diff
// (WARN0164, the authoritative signal) and by the /schema/events view.

// Collection state of the events of a server.
const (
	EventCollectionChecked     = "checked"     // collected by the last schema scan
	EventCollectionUnavailable = "unavailable" // the last schema scan could not read them
	EventCollectionUnsupported = "unsupported" // the engine has no EVENT objects (PostgreSQL)
	EventCollectionNotChecked  = "not-checked" // not collected yet
)

// Result of the comparison of a replica with the master.
const (
	EventComparisonConsistent = "consistent"
	EventComparisonDifferent  = "different"
	EventComparisonNotChecked = "not-checked" // one side is not checked: nothing is concluded
)

// Drift kinds, in the order they are reported.
const (
	EventDriftMissing    = "missing"    // on the master, not on the replica
	EventDriftExtra      = "extra"      // on the replica, not on the master
	EventDriftDefinition = "definition" // the definition CRC64 differs
	EventDriftDefiner    = "definer"    // the definer differs
	EventDriftStatus     = "status"     // active on one side, disabled on the other
)

var eventDriftOrder = []string{EventDriftMissing, EventDriftExtra, EventDriftDefinition, EventDriftDefiner, EventDriftStatus}

// eventDriftMaxNames bounds the event names per drift kind in a WARN0164 line.
const eventDriftMaxNames = 10

// EventSchema is the events of a server as one schema scan observed them. A
// snapshot is never modified once stored.
type EventSchema struct {
	Collection  string
	CollectedAt int64
	Events      []dbhelper.EventChecksum
}

// EventDrift is one difference of one event between a replica and the master.
type EventDrift struct {
	Db    string
	Name  string
	Drift string
}

// EventSchemaDiff is the comparison of a replica with the master.
type EventSchemaDiff struct {
	Comparison string
	Drifts     []EventDrift
}

// GetEventSchema returns the events of the last schema scan, nil when none
// was collected.
func (server *ServerMonitor) GetEventSchema() *EventSchema {
	return server.eventSchema.Load()
}

// eventSchemaFile is the file the events of the last schema scan are kept in,
// next to dicttables.json, so they survive a restart as the tables do.
const eventSchemaFile = "eventschema.json"

// eventSchemaSaved is the saved form of an EventSchema. Unlike the API, it
// keeps the definer: a reloaded snapshot is compared with fresh ones.
type eventSchemaSaved struct {
	Collection  string                  `json:"collection"`
	CollectedAt int64                   `json:"collectedAt"`
	Events      []eventSchemaSavedEvent `json:"events"`
}

type eventSchemaSavedEvent struct {
	Db              string `json:"db"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Definer         string `json:"definer"`
	DefinitionCrc64 uint64 `json:"definitionCrc64,string"`
}

// SaveEventSchema writes the events of the last schema scan to
// eventschema.json (SaveInfos, as SaveDictTables), or removes the file when
// there is none (monitoring-schema-events off).
func (server *ServerMonitor) SaveEventSchema() {
	path := server.Datadir + "/" + eventSchemaFile
	snap := server.GetEventSchema()
	if snap == nil {
		os.Remove(path)
		return
	}
	saved := eventSchemaSaved{Collection: snap.Collection, CollectedAt: snap.CollectedAt, Events: make([]eventSchemaSavedEvent, 0, len(snap.Events))}
	for _, e := range snap.Events {
		saved.Events = append(saved.Events, eventSchemaSavedEvent{Db: e.Db, Name: e.Name, Status: e.Status, Definer: e.Definer, DefinitionCrc64: e.DefinitionCrc64})
	}
	data, err := json.MarshalIndent(saved, "", "\t")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0644)
}

// ReloadEventSchema restores the events of the last schema scan from
// eventschema.json at startup (ReloadSaveInfosVariables, as ReloadDictTables),
// with their original collection time. Nothing is restored with
// monitoring-schema-events off, or from an unreadable file.
func (server *ServerMonitor) ReloadEventSchema() {
	cluster := server.ClusterGroup
	if !cluster.Conf.MonitorSchemaEvents {
		return
	}
	data, err := os.ReadFile(server.Datadir + "/" + eventSchemaFile)
	if err != nil {
		return
	}
	var saved eventSchemaSaved
	if err := json.Unmarshal(data, &saved); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Error parsing %s for server %s: %v", eventSchemaFile, server.URL, err)
		return
	}
	switch saved.Collection {
	case EventCollectionChecked, EventCollectionUnavailable, EventCollectionUnsupported:
	default:
		return
	}
	snap := &EventSchema{Collection: saved.Collection, CollectedAt: saved.CollectedAt, Events: make([]dbhelper.EventChecksum, 0, len(saved.Events))}
	for _, e := range saved.Events {
		snap.Events = append(snap.Events, dbhelper.EventChecksum{Db: e.Db, Name: e.Name, Status: e.Status, Definer: e.Definer, DefinitionCrc64: e.DefinitionCrc64})
	}
	server.eventSchema.Store(snap)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Restored %d scheduled database events from cache for server %s", len(snap.Events), server.URL)
}

// MonitorEventSchema collects the scheduled database events of the master and,
// with monitoring-schema-on-replicas, of the replicas. Called by MonitorSchema;
// with monitoring-schema-events off it drops the collected events instead.
func (cluster *Cluster) MonitorEventSchema() {
	if !cluster.Conf.MonitorSchemaEvents {
		for _, srv := range cluster.GetServers() {
			if srv != nil {
				srv.eventSchema.Store(nil)
			}
		}
		return
	}
	if master := cluster.GetMaster(); master != nil {
		cluster.collectEventSchema(master)
	}
	if cluster.Conf.MonitorSchemaOnReplicas {
		for _, sl := range cluster.GetSlaves() {
			cluster.collectEventSchema(sl)
		}
	}
}

// collectEventSchema stores the events of the server: checked, unsupported
// (PostgreSQL) or unavailable when they cannot be read. A failure is logged,
// never turned into a state: an unavailable server is not compared.
func (cluster *Cluster) collectEventSchema(server *ServerMonitor) {
	if server == nil {
		return
	}
	snap := &EventSchema{CollectedAt: time.Now().Unix()}
	switch {
	case server.DBVersion != nil && server.DBVersion.IsPostgreSQL():
		snap.Collection = EventCollectionUnsupported
	case server.Conn == nil || server.State == stateFailed || server.State == stateMaintenance || server.State == stateUnconn:
		snap.Collection = EventCollectionUnavailable
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
			"Scheduled database events not collected on %s: server state %s", server.URL, server.State)
	default:
		events, logs, err := dbhelper.GetEventChecksums(server.Conn, server.DBVersion, cluster.Conf.MonitorSchemaScanTimeout)
		switch {
		case errors.Is(err, dbhelper.ErrEventsUnsupported):
			snap.Collection = EventCollectionUnsupported
		case err != nil:
			snap.Collection = EventCollectionUnavailable
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlWarn, "Could not collect scheduled database events on %s: %s", server.URL, err)
		default:
			snap.Collection = EventCollectionChecked
			snap.Events = events
		}
	}
	server.eventSchema.Store(snap)
}

// eventKey identifies an event: a struct, not "schema.name", since either
// identifier may contain a dot.
type eventKey struct{ db, name string }

// compareEventSchema compares the events of a replica with those of the
// master: the drift rules of monitoring-schema-events. When either side is not
// checked, nothing is concluded (never "missing"). The status is compared by
// class, and only when both sides carry their own status: replica-side
// disabled (what a replica gives every replicated event, whether the master
// has it enabled or disabled) is never a status drift.
func compareEventSchema(master, replica *EventSchema) EventSchemaDiff {
	if master == nil || replica == nil || master.Collection != EventCollectionChecked || replica.Collection != EventCollectionChecked {
		return EventSchemaDiff{Comparison: EventComparisonNotChecked}
	}
	key := func(e dbhelper.EventChecksum) eventKey { return eventKey{e.Db, e.Name} }
	onReplica := make(map[eventKey]dbhelper.EventChecksum, len(replica.Events))
	for _, e := range replica.Events {
		onReplica[key(e)] = e
	}
	onMaster := make(map[eventKey]bool, len(master.Events))
	var drifts []EventDrift
	for _, m := range master.Events {
		onMaster[key(m)] = true
		r, ok := onReplica[key(m)]
		if !ok {
			drifts = append(drifts, EventDrift{m.Db, m.Name, EventDriftMissing})
			continue
		}
		if m.DefinitionCrc64 != r.DefinitionCrc64 {
			drifts = append(drifts, EventDrift{m.Db, m.Name, EventDriftDefinition})
		}
		if m.Definer != r.Definer {
			drifts = append(drifts, EventDrift{m.Db, m.Name, EventDriftDefiner})
		}
		if m.Status != r.Status && m.Status != dbhelper.EventStatusReplicaSide && r.Status != dbhelper.EventStatusReplicaSide {
			drifts = append(drifts, EventDrift{m.Db, m.Name, EventDriftStatus})
		}
	}
	for _, r := range replica.Events {
		if !onMaster[key(r)] {
			drifts = append(drifts, EventDrift{r.Db, r.Name, EventDriftExtra})
		}
	}
	sort.SliceStable(drifts, func(i, j int) bool {
		if drifts[i].Db != drifts[j].Db {
			return drifts[i].Db < drifts[j].Db
		}
		if drifts[i].Name != drifts[j].Name {
			return drifts[i].Name < drifts[j].Name
		}
		return driftRank(drifts[i].Drift) < driftRank(drifts[j].Drift)
	})
	if len(drifts) == 0 {
		return EventSchemaDiff{Comparison: EventComparisonConsistent}
	}
	return EventSchemaDiff{Comparison: EventComparisonDifferent, Drifts: drifts}
}

func driftRank(drift string) int {
	for i, d := range eventDriftOrder {
		if d == drift {
			return i
		}
	}
	return len(eventDriftOrder)
}

// CompareEventSchemaBetweenMasterAndSlave compares the collected events of the
// replica with those of the master (compareEventSchema), for the schema diff
// and the /schema/events view alike.
func (cluster *Cluster) CompareEventSchemaBetweenMasterAndSlave(sl *ServerMonitor) EventSchemaDiff {
	master := cluster.GetMaster()
	if master == nil || sl == nil {
		return EventSchemaDiff{Comparison: EventComparisonNotChecked}
	}
	return compareEventSchema(master.GetEventSchema(), sl.GetEventSchema())
}

// formatEventSchemaDiff returns the WARN0164 line of a replica's event drift:
// one line, names grouped by drift kind and bounded per kind, or nothing.
func formatEventSchemaDiff(url string, diff EventSchemaDiff) []string {
	if diff.Comparison != EventComparisonDifferent {
		return nil
	}
	byKind := make(map[string][]string)
	for _, d := range diff.Drifts {
		byKind[d.Drift] = append(byKind[d.Drift], d.Db+"."+d.Name)
	}
	var parts []string
	for _, kind := range eventDriftOrder {
		names := byKind[kind]
		if len(names) == 0 {
			continue
		}
		text := strings.Join(names, ", ")
		if len(names) > eventDriftMaxNames {
			text = strings.Join(names[:eventDriftMaxNames], ", ") + fmt.Sprintf(" (+%d more)", len(names)-eventDriftMaxNames)
		}
		parts = append(parts, kind+": "+text)
	}
	return []string{fmt.Sprintf("Events differ on slave %s -> %s", url, strings.Join(parts, "; "))}
}

// eventSchemaDiffLines compares the events of the replica with the master for
// the schema diff (MonitorTableSchemaDiff): each drift is logged at debug
// level, and the aggregated line is returned for WARN0164.
func (cluster *Cluster) eventSchemaDiffLines(sl *ServerMonitor) []string {
	if !cluster.Conf.MonitorSchemaEvents {
		return nil
	}
	diff := cluster.CompareEventSchemaBetweenMasterAndSlave(sl)
	if diff.Comparison == EventComparisonNotChecked {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
			"Scheduled database events of %s not compared: not collected on the master or the replica", sl.URL)
	}
	for _, d := range diff.Drifts {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
			"Scheduled database event %s.%s on %s: %s drift", d.Db, d.Name, sl.URL, d.Drift)
	}
	return formatEventSchemaDiff(sl.URL, diff)
}

// EventSchemaView is the /schema/events answer: the event consistency of the
// cluster, derived from the collected events. It carries no definition: names,
// presence, status class, definition CRC64 and drift kinds only.
type EventSchemaView struct {
	Enabled bool                    `json:"enabled"`
	Servers []EventSchemaServerView `json:"servers"`
	Events  []EventSchemaEventView  `json:"events"`
}

// EventSchemaServerView is one server of the view. Comparison is set on
// replicas only.
type EventSchemaServerView struct {
	Id          string `json:"id"`
	URL         string `json:"url"`
	IsMaster    bool   `json:"isMaster"`
	Collection  string `json:"collection"`
	CollectedAt int64  `json:"collectedAt,omitempty"`
	Comparison  string `json:"comparison,omitempty"`
}

// EventSchemaEventView is one event found on any checked server. Nodes holds
// the servers whose events are checked, by server id: a server absent from
// Nodes is not checked, never "missing".
type EventSchemaEventView struct {
	Db     string                         `json:"db"`
	Name   string                         `json:"name"`
	Nodes  map[string]EventSchemaNodeView `json:"nodes"`
	Drifts []EventSchemaDriftView         `json:"drifts"`
}

// EventSchemaNodeView is one event on one checked server.
type EventSchemaNodeView struct {
	Present         bool   `json:"present"`
	Status          string `json:"status,omitempty"`
	DefinitionCrc64 uint64 `json:"definitionCrc64,omitempty,string"`
}

// EventSchemaDriftView is one drift of an event on a replica.
type EventSchemaDriftView struct {
	ServerId string `json:"serverId"`
	Drift    string `json:"drift"`
}

// GetEventSchemaView builds the /schema/events view from the collected events
// and CompareEventSchemaBetweenMasterAndSlave. It reads no database and
// changes no state.
func (cluster *Cluster) GetEventSchemaView() EventSchemaView {
	view := EventSchemaView{Enabled: cluster.Conf.MonitorSchemaEvents, Servers: []EventSchemaServerView{}, Events: []EventSchemaEventView{}}
	if !view.Enabled {
		return view
	}
	master := cluster.GetMaster()
	var servers []*ServerMonitor
	if master != nil {
		servers = append(servers, master)
	}
	for _, sl := range cluster.GetSlaves() {
		if sl != nil && sl != master {
			servers = append(servers, sl)
		}
	}

	byKey := map[eventKey]*EventSchemaEventView{}
	event := func(db, name string) *EventSchemaEventView {
		k := eventKey{db, name}
		if byKey[k] == nil {
			byKey[k] = &EventSchemaEventView{Db: db, Name: name, Nodes: map[string]EventSchemaNodeView{}, Drifts: []EventSchemaDriftView{}}
		}
		return byKey[k]
	}
	var checked []*ServerMonitor
	for _, srv := range servers {
		sv := EventSchemaServerView{Id: srv.Id, URL: srv.URL, IsMaster: srv == master, Collection: EventCollectionNotChecked}
		if snap := srv.GetEventSchema(); snap != nil {
			sv.Collection, sv.CollectedAt = snap.Collection, snap.CollectedAt
			if snap.Collection == EventCollectionChecked {
				checked = append(checked, srv)
				for _, e := range snap.Events {
					event(e.Db, e.Name).Nodes[srv.Id] = EventSchemaNodeView{Present: true, Status: e.Status, DefinitionCrc64: e.DefinitionCrc64}
				}
			}
		}
		if srv != master {
			diff := cluster.CompareEventSchemaBetweenMasterAndSlave(srv)
			sv.Comparison = diff.Comparison
			for _, d := range diff.Drifts {
				ev := event(d.Db, d.Name)
				ev.Drifts = append(ev.Drifts, EventSchemaDriftView{ServerId: srv.Id, Drift: d.Drift})
			}
		}
		view.Servers = append(view.Servers, sv)
	}
	for _, ev := range byKey {
		for _, srv := range checked {
			if _, ok := ev.Nodes[srv.Id]; !ok {
				ev.Nodes[srv.Id] = EventSchemaNodeView{Present: false}
			}
		}
		view.Events = append(view.Events, *ev)
	}
	sort.Slice(view.Events, func(i, j int) bool {
		if view.Events[i].Db != view.Events[j].Db {
			return view.Events[i].Db < view.Events[j].Db
		}
		return view.Events[i].Name < view.Events[j].Name
	})
	return view
}
