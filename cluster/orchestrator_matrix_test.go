package cluster

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The backend x method matrix of doc/implementation/cluster/ORCHESTRATOR_INTERFACE.md,
// section 2.5, as a test. For every method of the Orchestrator interface and every backend,
// the table says what the adapter does today:
//   - implemented: the adapter calls a backend method (the AST of its file declares it);
//   - notSupported: the adapter declares no method and the embedded unsupportedOrchestrator
//     answers ErrOrchestratorNotSupported, which the test calls and checks;
//   - legacyDiffers: implemented, and the switch statement of prov.go does something else
//     today (the localhost stop); listed so the difference is a fact of the matrix, not a
//     surprise of migration step 2.
//
// A method of the interface without a row fails the test, so the matrix and the interface
// cannot drift apart. The implemented cells are not called: they reach real orchestrators
// and wait on their answers; their behaviour is the regtest suite's to prove (T13).
type matrixStatus string

const (
	implemented   matrixStatus = "implemented"
	notSupportedS matrixStatus = "notSupported"
	legacyDiffers matrixStatus = "legacyDiffers"
)

// orchestratorMatrix: method -> backend type -> status. A method absent for a type is a
// test failure, not a default.
var orchestratorMatrix = map[string]map[string]matrixStatus{
	// OrchestratorInfo: every backend answers.
	"Name":        allBackends(implemented),
	"Type":        allBackends(implemented),
	"Traits":      allBackends(implemented),
	"Layout":      allBackends(implemented),
	"ProxyLayout": allBackends(implemented),
	"Check":       allBackends(implemented),
	// NodeInventory
	"Nodes":     allBackends(implemented),
	"Storages":  backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"NodeStats": backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	// DatabaseOrchestrator
	"ProvisionDatabaseService":         allBackends(implemented),
	"UnprovisionDatabaseService":       allBackends(implemented),
	"StartDatabaseService":             allBackends(implemented),
	"StopDatabaseService":              backends(implemented, implemented, implemented, legacyDiffers, implemented),
	"RestartDatabaseService":           backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"FreezeDatabaseService":            backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"UnfreezeDatabaseService":          backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"UpdateDatabaseServiceConfig":      backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"RefreshDatabaseServiceDefinition": backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"UpgradeDatabaseService":           backends(notSupportedS, notSupportedS, implemented, notSupportedS, notSupportedS),
	"DatabaseServiceDefinition":        backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"DatabaseServiceImage":             backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"DatabaseWorkingNode":              backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"AbortDatabaseService":             backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"ClearDatabaseInstanceState":       backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	// ProxyOrchestrator
	"ProvisionProxyService":         backends(implemented, implemented, implemented, implemented, notSupportedS),
	"UnprovisionProxyService":       backends(implemented, implemented, implemented, implemented, notSupportedS),
	"StartProxyService":             allBackends(implemented),
	"StopProxyService":              allBackends(implemented),
	"RefreshProxyServiceDefinition": allBackends(notSupportedS),
	"ProxyWorkingNode":              backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"AbortProxyService":             backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	"ClearProxyInstanceState":       backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	// AppOrchestrator: OpenSVC only today.
	"ProvisionAppService":         openSVCOnly(),
	"UnprovisionAppService":       openSVCOnly(),
	"StartAppService":             openSVCOnly(),
	"StopAppService":              openSVCOnly(),
	"RestartAppService":           openSVCOnly(),
	"UpdateAppConfig":             openSVCOnly(),
	"RefreshAppServiceDefinition": openSVCOnly(),
	"ProvisionAppRoutes":          openSVCOnly(),
	"WithdrawAppRoutes":           openSVCOnly(),
	"AbortAppService":             openSVCOnly(),
	"ClearAppInstanceState":       openSVCOnly(),
	// SecretOrchestrator
	"SecretStore":            backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"ServerSecretStore":      backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"InitCredentials":        backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"RotateDatabasePassword": backends(implemented, implemented, notSupportedS, notSupportedS, notSupportedS),
	"UnprovisionSecret":      backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
	// TerminalOrchestrator
	"TerminalCommand":  backends(implemented, notSupportedS, implemented, notSupportedS, notSupportedS),
	"DefaultContainer": backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS),
}

// matrixBackends is the column order of backends(): OpenSVC, Kubernetes, on-premise,
// localhost, SlapOS.
var matrixBackends = []string{
	config.ConstOrchestratorOpenSVC,
	config.ConstOrchestratorKubernetes,
	config.ConstOrchestratorOnPremise,
	config.ConstOrchestratorLocalhost,
	config.ConstOrchestratorSlapOS,
}

// matrixFiles is the adapter file of each backend.
var matrixFiles = map[string]string{
	config.ConstOrchestratorOpenSVC:    "orchestrator_opensvc.go",
	config.ConstOrchestratorKubernetes: "orchestrator_k8s.go",
	config.ConstOrchestratorOnPremise:  "orchestrator_onpremise.go",
	config.ConstOrchestratorLocalhost:  "orchestrator_localhost.go",
	config.ConstOrchestratorSlapOS:     "orchestrator_slapos.go",
}

func backends(opensvc, kube, onpremise, local, slapos matrixStatus) map[string]matrixStatus {
	return map[string]matrixStatus{
		config.ConstOrchestratorOpenSVC:    opensvc,
		config.ConstOrchestratorKubernetes: kube,
		config.ConstOrchestratorOnPremise:  onpremise,
		config.ConstOrchestratorLocalhost:  local,
		config.ConstOrchestratorSlapOS:     slapos,
	}
}

func allBackends(s matrixStatus) map[string]matrixStatus { return backends(s, s, s, s, s) }

func openSVCOnly() map[string]matrixStatus {
	return backends(implemented, notSupportedS, notSupportedS, notSupportedS, notSupportedS)
}

// methodsNotReturningError are the interface methods whose answer carries no error; a
// notSupported cell of theirs is the zero value, checked as such.
var methodsNotReturningError = map[string]bool{
	"Name": true, "Type": true, "Traits": true, "Layout": true, "ProxyLayout": true,
	"SecretStore": true, "InitCredentials": true, "DefaultContainer": true,
}

// declaredMethods are the methods an adapter file declares on its receiver type.
func declaredMethods(t *testing.T, file string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	methods := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		methods[fn.Name.Name] = true
	}
	return methods
}

func matrixCluster(t *testing.T, orchestrator string) *Cluster {
	t.Helper()
	conf := &config.Config{ProvOrchestrator: orchestrator}
	return &Cluster{Name: "matrix", Conf: conf, errorChan: make(chan error)}
}

func TestOrchestratorMatrix(t *testing.T) {
	ifaceType := reflect.TypeOf((*Orchestrator)(nil)).Elem()
	methods := make([]string, 0, ifaceType.NumMethod())
	for i := 0; i < ifaceType.NumMethod(); i++ {
		methods = append(methods, ifaceType.Method(i).Name)
	}
	sort.Strings(methods)

	// every method of the interface has a row, and every row names a method of the interface
	for _, method := range methods {
		if _, ok := orchestratorMatrix[method]; !ok {
			t.Errorf("method %s of the Orchestrator interface has no row in orchestratorMatrix: add it to the matrix and to the design document", method)
		}
	}
	for method := range orchestratorMatrix {
		if _, ok := ifaceType.MethodByName(method); !ok {
			t.Errorf("orchestratorMatrix names %s, which the Orchestrator interface does not have", method)
		}
	}

	for _, backend := range matrixBackends {
		declared := declaredMethods(t, matrixFiles[backend])
		cl := matrixCluster(t, backend)
		orch := cl.Orchestrator()
		if orch.Type() != backend {
			t.Fatalf("%s: registry built a %q orchestrator", backend, orch.Type())
		}
		value := reflect.ValueOf(orch)
		for _, method := range methods {
			want, ok := orchestratorMatrix[method][backend]
			if !ok {
				t.Errorf("%s.%s: no cell in the matrix", backend, method)
				continue
			}
			// the AST says what the adapter does: a method it declares is implemented, one
			// it leaves to unsupportedOrchestrator is not supported
			got := notSupportedS
			if declared[method] {
				got = implemented
			}
			wantDeclared := want == implemented || want == legacyDiffers
			if (got == implemented) != wantDeclared {
				t.Errorf("%s.%s: the matrix says %s, the adapter file %s says %s", backend, method, want, matrixFiles[backend], got)
			}
			if want != notSupportedS {
				continue
			}
			// a not-supported cell is called: it answers the sentinel (or the zero value when
			// the method carries no error) without touching its arguments
			m := value.MethodByName(method)
			args := make([]reflect.Value, m.Type().NumIn())
			for i := range args {
				args[i] = reflect.Zero(m.Type().In(i))
			}
			out := m.Call(args)
			if methodsNotReturningError[method] {
				if method == "SecretStore" {
					store := out[0].Interface().(orchestratorStore)
					if _, _, err := store.ReadCurrent("k"); !errors.Is(err, ErrOrchestratorNotSupported) {
						t.Errorf("%s.SecretStore().ReadCurrent: got %v, want ErrOrchestratorNotSupported", backend, err)
					}
				}
				continue
			}
			last := out[len(out)-1]
			err, _ := last.Interface().(error)
			if !errors.Is(err, ErrOrchestratorNotSupported) {
				t.Errorf("%s.%s: got %v, want ErrOrchestratorNotSupported", backend, method, err)
			}
		}
	}
}

// The null orchestrator of an unknown type answers the sentinel on every method that
// carries an error and never panics.
func TestOrchestratorNullAnswersEveryMethod(t *testing.T) {
	cl := matrixCluster(t, "nothing-like-this")
	orch := cl.Orchestrator()
	if orch.Type() != "" || orch.Name() != "nothing-like-this" {
		t.Fatalf("null orchestrator: type %q name %q", orch.Type(), orch.Name())
	}
	value := reflect.ValueOf(orch)
	ifaceType := reflect.TypeOf((*Orchestrator)(nil)).Elem()
	for i := 0; i < ifaceType.NumMethod(); i++ {
		method := ifaceType.Method(i).Name
		if methodsNotReturningError[method] {
			continue
		}
		m := value.MethodByName(method)
		args := make([]reflect.Value, m.Type().NumIn())
		for j := range args {
			args[j] = reflect.Zero(m.Type().In(j))
		}
		out := m.Call(args)
		err, _ := out[len(out)-1].Interface().(error)
		if !errors.Is(err, ErrOrchestratorNotSupported) {
			t.Errorf("null.%s: got %v, want ErrOrchestratorNotSupported", method, err)
		}
	}
}
