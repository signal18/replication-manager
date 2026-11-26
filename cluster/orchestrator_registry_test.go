package cluster

import (
	"errors"
	"reflect"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A flat configuration (prov-orchestrator and the orchestrator's keys at cluster level) and
// the same keys as a named block build the same orchestrator: same name, type and traits,
// and the synthesized configuration carries every key of the block. This is the
// compatibility rule of the design (section 3.2): an existing configuration file keeps
// working unchanged.
func TestOrchestratorSynthesized(t *testing.T) {
	flat := &config.Config{
		ProvOrchestrator:            config.ConstOrchestratorOpenSVC,
		ProvHost:                    "collector.example:1215",
		ProvAdminUser:               "root:secret",
		ProvUser:                    "repman:secret",
		ProvCodeApp:                 "repman",
		ProvOpensvcP12Certificate:   "/etc/replication-manager/cert.p12",
		ProvOpensvcUseCollectorAPI:  false,
		ProvOpensvcV3ProvisionDelay: 3,
		ProvOrchestratorCluster:     "infra1",
	}
	block := orchestratorConfig{
		Name:                        config.ConstOrchestratorOpenSVC,
		Type:                        config.ConstOrchestratorOpenSVC,
		ProvHost:                    "collector.example:1215",
		ProvAdminUser:               "root:secret",
		ProvUser:                    "repman:secret",
		ProvCodeApp:                 "repman",
		ProvOpensvcP12Certificate:   "/etc/replication-manager/cert.p12",
		ProvOpensvcUseCollectorAPI:  false,
		ProvOpensvcV3ProvisionDelay: 3,
		ProvOrchestratorCluster:     "infra1",
	}
	synthesized := synthesizedOrchestratorConfig(flat)
	if !reflect.DeepEqual(synthesized, block) {
		t.Fatalf("synthesized configuration differs from the block:\n got %+v\nwant %+v", synthesized, block)
	}

	fromFlat := &Cluster{Name: "flat", Conf: flat}
	if err := fromFlat.newOrchestratorList(); err != nil {
		t.Fatal(err)
	}
	fromBlock := &Cluster{Name: "block", Conf: &config.Config{ProvOrchestrator: block.Type}}
	orchBlock, ok := fromBlock.newOrchestrator(block)
	if !ok {
		t.Fatal("block of a known type refused")
	}
	orchFlat := fromFlat.Orchestrator()
	if len(fromFlat.Orchestrators) != 1 {
		t.Fatalf("registry holds %d orchestrators, want the synthesized one", len(fromFlat.Orchestrators))
	}
	if orchFlat.Name() != orchBlock.Name() || orchFlat.Type() != orchBlock.Type() {
		t.Fatalf("flat %s/%s, block %s/%s", orchFlat.Name(), orchFlat.Type(), orchBlock.Name(), orchBlock.Type())
	}
	if orchFlat.Traits() != orchBlock.Traits() {
		t.Fatalf("traits differ: flat %+v, block %+v", orchFlat.Traits(), orchBlock.Traits())
	}
	if orchFlat.Name() != config.ConstOrchestratorOpenSVC {
		t.Fatalf("a synthesized orchestrator is named after its type, got %q", orchFlat.Name())
	}
}

// Every type the configuration accepts today builds its backend, and each backend reports
// the type it was built for.
func TestOrchestratorRegistryBuildsEveryType(t *testing.T) {
	for _, typ := range []string{
		config.ConstOrchestratorOpenSVC,
		config.ConstOrchestratorKubernetes,
		config.ConstOrchestratorOnPremise,
		config.ConstOrchestratorLocalhost,
		config.ConstOrchestratorSlapOS,
		config.ConstOrchestratorProxmox,
	} {
		cl := &Cluster{Name: "types", Conf: &config.Config{ProvOrchestrator: typ}}
		if err := cl.newOrchestratorList(); err != nil {
			t.Fatal(err)
		}
		orch, ok := cl.Orchestrators[typ]
		if !ok {
			t.Fatalf("%s: not registered", typ)
		}
		if orch.Type() != typ || orch.Name() != typ {
			t.Fatalf("%s: built %s/%s", typ, orch.Name(), orch.Type())
		}
	}
}

// cluster.orchestrator(server) is the cluster's orchestrator for every server until the
// per-server override exists; an unknown type leaves the registry empty and answers a null
// orchestrator (never nil) whose methods return the sentinel; a type changed at runtime is
// built on the spot.
func TestOrchestratorResolvesClusterOrchestrator(t *testing.T) {
	cl := &Cluster{Name: "resolve", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOnPremise}}
	if err := cl.newOrchestratorList(); err != nil {
		t.Fatal(err)
	}
	servers := []*ServerMonitor{{Name: "db1", ClusterGroup: cl}, {Name: "db2", ClusterGroup: cl}, nil}
	for _, server := range servers {
		if cl.orchestrator(server) != cl.Orchestrator() {
			t.Fatalf("server %v does not resolve to the cluster's orchestrator", server)
		}
	}

	unknown := &Cluster{Name: "unknown", Conf: &config.Config{ProvOrchestrator: "mesos"}}
	if err := unknown.newOrchestratorList(); err != nil {
		t.Fatal(err)
	}
	if len(unknown.Orchestrators) != 0 {
		t.Fatalf("registry of an unknown type holds %d orchestrators", len(unknown.Orchestrators))
	}
	orch := unknown.Orchestrator()
	if orch == nil {
		t.Fatal("Orchestrator() returned nil")
	}
	if _, ok := orch.(*nullOrchestrator); !ok {
		t.Fatalf("Orchestrator() of an unknown type is a %T, want *nullOrchestrator", orch)
	}
	if err := orch.StartDatabaseService(nil); !errors.Is(err, ErrOrchestratorNotSupported) {
		t.Fatalf("null orchestrator start: %v", err)
	}
	if len(unknown.Orchestrators) != 0 {
		t.Fatal("the null orchestrator must not be registered")
	}

	// the type changes at runtime (SetProvOrchestrator): the next call builds the new backend
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorLocalhost
	if got := cl.Orchestrator().Type(); got != config.ConstOrchestratorLocalhost {
		t.Fatalf("after a type change Orchestrator() is %q", got)
	}
	if len(cl.Orchestrators) != 2 {
		t.Fatalf("registry holds %d orchestrators after a type change, want 2", len(cl.Orchestrators))
	}

	// a cluster without configuration (a test fixture) answers a null orchestrator too
	bare := &Cluster{}
	if _, ok := bare.Orchestrator().(*nullOrchestrator); !ok {
		t.Fatal("a cluster without Conf must answer a null orchestrator")
	}
}

// notSupported wraps the sentinel with the operation and the orchestrator, and the two
// OpenSVC API-version sentinels are the interface's sentinel through the adapter.
func TestOrchestratorNotSupportedWraps(t *testing.T) {
	cl := &Cluster{Name: "wrap", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	orch := cl.Orchestrator()
	err := notSupported("FreezeDatabaseService", orch)
	if !errors.Is(err, ErrOrchestratorNotSupported) {
		t.Fatalf("notSupported does not wrap the sentinel: %v", err)
	}
	want := "operation not supported by this orchestrator: FreezeDatabaseService on opensvc (opensvc)"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
	wrapped := openSVCNotSupported(ErrOpenSVCAbortNotSupported)
	if !errors.Is(wrapped, ErrOrchestratorNotSupported) || !errors.Is(wrapped, ErrOpenSVCAbortNotSupported) {
		t.Fatalf("the OpenSVC abort refusal must be both sentinels: %v", wrapped)
	}
	other := errors.New("daemon down")
	if openSVCNotSupported(other) != other {
		t.Fatal("an ordinary error must pass through unchanged")
	}
}
