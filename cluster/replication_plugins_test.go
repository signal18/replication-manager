package cluster

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// Each replication the monitor reads gets the native plugin of its target's engine, the
// cluster's topology and its source's engine; the object of that plugin answers only the verbs
// its manifest lists.
func TestNativePluginChoice(t *testing.T) {
	maria := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 40}
	pg := &version.Version{Flavor: "PostgreSQL", Major: 17}
	for _, c := range []struct {
		name          string
		target        *version.Version
		source        *version.Version
		topology      string
		want          string
		postgresqlObj bool
	}{
		{"mariadb from mariadb", maria, maria, "", ReplicationPluginMariaDBNative, false},
		{"mariadb from an unmonitored source", maria, nil, "", ReplicationPluginMariaDBNative, false},
		{"mariadb from mysql", maria, mysql, "", ReplicationPluginMySQLMariaDBNative, false},
		{"mysql from mysql", mysql, mysql, "", ReplicationPluginMySQLNative, false},
		{"columnstore", maria, maria, config.TopoColumnstore, ReplicationPluginColumnStoreNative, false},
		{"postgresql", pg, pg, config.TopoMasterSlavePgLog, ReplicationPluginPostgreSQLNative, true},
		{"unknown engine", nil, nil, "", ReplicationPluginMariaDBNative, false},
	} {
		cl, sv := reseedLinkFixture()
		cl.Conf.TopologyTarget = c.topology
		sv.DBVersion = c.target
		src := &ServerMonitor{Host: "db2.belair", Port: "3306", URL: "db2.belair:3306", ClusterGroup: cl, DBVersion: c.source}
		if c.source != nil {
			cl.Servers = serverList{sv, src}
		}
		r := sv.newNativeReplication("belair", channel("belair", "db2.belair", "Yes", "Yes"))
		if r.Plugin().Name != c.want {
			t.Errorf("%s: plugin %s, want %s", c.name, r.Plugin().Name, c.want)
		}
		if _, ok := r.(*postgresReplication); ok != c.postgresqlObj {
			t.Errorf("%s: object %T", c.name, r)
		}
		if KindOf(r) != ReplicationKindNative || !CanFailover(r) {
			t.Errorf("%s: kind %s, failover %t", c.name, KindOf(r), CanFailover(r))
		}
		// the JSON names the plugin, and decodes back to an object of that plugin
		b, _ := json.Marshal(ReplicationList{r})
		var back ReplicationList
		if err := json.Unmarshal(b, &back); err != nil || back[0].Plugin().Name != c.want {
			t.Errorf("%s: decoded %v %+v", c.name, err, back)
		}
	}

	// PostgreSQL: no copy or apply apart, no skip of an event, nothing sent (no connection needed)
	_, sv := reseedLinkFixture()
	sv.DBVersion = pg
	r := sv.ReplicationChannelLink("alltables")
	for name, op := range map[string]func() (string, error){
		"SkipEvent":      func() (string, error) { return r.SkipEvent(LinkByTest) },
		"StopCopyEvents": func() (string, error) { return r.StopCopyEvents(LinkByTest) },
		"Configure":      func() (string, error) { return r.Configure(LinkOptions{ExecMode: "IDEMPOTENT"}, LinkByTest) },
		"SetPosition":    func() (string, error) { return r.SetPosition(Position{}, LinkByTest) },
	} {
		if _, err := op(); !errors.Is(err, ErrReplicationNotSupported) {
			t.Errorf("postgresql %s: want ErrReplicationNotSupported, got %v", name, err)
		}
		if r.Plugin().Can(map[string]string{"SkipEvent": ReplicationActionSkipEvent, "StopCopyEvents": ReplicationActionStopCopyEvents, "Configure": ReplicationActionConfigure, "SetPosition": ReplicationActionSetPosition}[name]) {
			t.Errorf("postgresql manifest lists %s", name)
		}
	}
}

// The manifests: Galera on the MariaDB plugin only, Group Replication on the MySQL one only,
// the PostgreSQL topologies physical and logical, every topology with its transport and
// consistency.
func TestNativeManifests(t *testing.T) {
	has := func(m ReplicationManifest, topo string) bool {
		for _, tp := range m.Topologies {
			if tp.Name == topo {
				return true
			}
		}
		return false
	}
	for name, p := range nativePlugins {
		m := p.Manifest()
		if m.Name != name || m.Type != string(ReplicationKindNative) || len(m.Source) == 0 || len(m.Target) == 0 {
			t.Errorf("%s: %+v", name, m)
		}
		for _, tp := range m.Topologies {
			if tp.Transport == "" || tp.Consistency == "" {
				t.Errorf("%s: topology %+v without its transport or consistency", name, tp)
			}
		}
		if has(m, config.TopoMultiMasterWsrep) != (name == ReplicationPluginMariaDBNative) {
			t.Errorf("%s: galera", name)
		}
		if has(m, config.TopoMultiMasterGrouprep) != (name == ReplicationPluginMySQLNative) {
			t.Errorf("%s: group replication", name)
		}
	}
	pgm := nativePlugins[ReplicationPluginPostgreSQLNative].Manifest()
	if pgm.Topologies[0].Name != config.TopoMasterSlavePgStream || pgm.Topologies[0].Transport != ReplicationTransportPhysical ||
		pgm.Topologies[1].Name != config.TopoMasterSlavePgLog || pgm.Topologies[1].Transport != ReplicationTransportLogical {
		t.Errorf("postgresql topologies %+v", pgm.Topologies)
	}
	if !has(nativePlugins[ReplicationPluginColumnStoreNative].Manifest(), config.TopoColumnstore) {
		t.Error("columnstore topology")
	}
	_, sv := reseedLinkFixture()
	if g := sv.GroupReplication(); g.Plugin().Name != ReplicationPluginMySQLNative || g.Name() != groupReplicationName {
		t.Errorf("group membership: %s %s", g.Plugin().Name, g.Name())
	}
}
