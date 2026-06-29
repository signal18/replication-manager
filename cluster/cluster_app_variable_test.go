package cluster

import (
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// proxySub is a substitution JSON that provides one proxy host, enough to resolve
// {{proxies.#.host}} to "192.168.1.10".
const proxySub = `{"proxies":[{"host":"192.168.1.10"}]}`

func newTestClusterForVarTest(t *testing.T) *Cluster {
	t.Helper()
	return &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}
}

func newTestAppWithSub(name, sub string) *App {
	return &App{
		Name:                 name,
		Mutex:                &sync.Mutex{},
		AppClusterSubstitute: sub,
		AppConfig: &config.AppConfig{
			Deployment: config.NewDeploymentConfig(),
		},
	}
}

// --- ResolveEnvVariableValue ---

func TestResolveEnvVariableValue_FullyResolvable(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	got := cl.ResolveEnvVariableValue(app, "{{proxies.#.host}}")
	if got != "192.168.1.10" {
		t.Errorf("expected resolved host, got %q", got)
	}
}

func TestResolveEnvVariableValue_UnresolvablePlaceholder_KeepsRaw(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	raw := "{{nonexistent.key}}"
	got := cl.ResolveEnvVariableValue(app, raw)
	if got != raw {
		t.Errorf("expected raw value %q to be kept, got %q", raw, got)
	}
}

func TestResolveEnvVariableValue_PartiallyResolvable_KeepsRaw(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	raw := "{{proxies.#.host}}-{{nonexistent.key}}"
	got := cl.ResolveEnvVariableValue(app, raw)
	if got != raw {
		t.Errorf("expected raw value %q to be kept when partially unresolvable, got %q", raw, got)
	}
}

func TestResolveEnvVariableValue_NoSubstitutionData_KeepsRaw(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	// AppClusterSubstitute is empty; GetAppsSubstitutionJSon will fail on a minimal cluster
	app := newTestAppWithSub("app1", "")
	app.ClusterGroup = cl

	raw := "{{proxies.#.host}}"
	got := cl.ResolveEnvVariableValue(app, raw)
	if got != raw {
		t.Errorf("expected raw value %q to be kept when no sub data, got %q", raw, got)
	}
}

func TestResolveEnvVariableValue_PlainString_Unchanged(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	got := cl.ResolveEnvVariableValue(app, "no-placeholders-here")
	if got != "no-placeholders-here" {
		t.Errorf("expected plain string unchanged, got %q", got)
	}
}

// --- SetAppVariableValue ---

func TestSetAppVariableValue_EnvAdd_ResolvesTemplate(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	v := config.VariableMapping{
		Name:  "DB_HOST",
		Value: "{{proxies.#.host}}",
		Type:  config.VariableTypeEnv,
	}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := app.AppConfig.Deployment.Variables[0].Value
	if got != "192.168.1.10" {
		t.Errorf("expected resolved value, got %q", got)
	}
}

func TestSetAppVariableValue_EnvAdd_UnresolvablePlaceholder_KeepsRaw(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	raw := "{{nonexistent.key}}"
	v := config.VariableMapping{Name: "DB_HOST", Value: raw, Type: config.VariableTypeEnv}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := app.AppConfig.Deployment.Variables[0].Value
	if got != raw {
		t.Errorf("expected raw value %q kept for unresolvable placeholder, got %q", raw, got)
	}
}

func TestSetAppVariableValue_EnvAdd_ConditionalResolved(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	v := config.VariableMapping{
		Name:  "DB_HOST",
		Value: "{{proxies.#.host}}",
		Type:  config.VariableTypeEnv,
		Conditional: config.AVSlice{
			{Agent: "agent1", Value: "{{proxies.#.host}}"},
		},
	}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	variable := app.AppConfig.Deployment.Variables[0]
	if variable.Value != "192.168.1.10" {
		t.Errorf("expected resolved main value, got %q", variable.Value)
	}
	if len(variable.Conditional) != 1 {
		t.Fatalf("expected one conditional, got %d", len(variable.Conditional))
	}
	if variable.Conditional[0].Value != "192.168.1.10" {
		t.Errorf("expected resolved conditional value, got %q", variable.Conditional[0].Value)
	}
}

func TestSetAppVariableValue_EnvAdd_ConditionalUnresolvable_KeepsRaw(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	raw := "{{bad.key}}"
	v := config.VariableMapping{
		Name:  "DB_HOST",
		Value: "static",
		Type:  config.VariableTypeEnv,
		Conditional: config.AVSlice{
			{Agent: "agent1", Value: raw},
		},
	}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := app.AppConfig.Deployment.Variables[0].Conditional[0].Value
	if got != raw {
		t.Errorf("expected raw conditional value %q kept, got %q", raw, got)
	}
}

func TestSetAppVariableValue_SecretAdd_NotResolved(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	raw := "{{proxies.#.host}}"
	v := config.VariableMapping{Name: "MY_SECRET", Value: raw, Type: config.VariableTypeSecret}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := app.AppConfig.Deployment.Variables[0].Value
	// Secrets go through encryption only; the placeholder must NOT be template-expanded.
	if got == "192.168.1.10" {
		t.Errorf("secret value must not be template-resolved, but got resolved value %q", got)
	}
}

func TestSetAppVariableValue_EnvAdd_DoesNotMutateInputConditional(t *testing.T) {
	cl := newTestClusterForVarTest(t)
	app := newTestAppWithSub("app1", proxySub)

	origCond := config.AVSlice{{Agent: "a1", Value: "{{proxies.#.host}}"}}
	v := config.VariableMapping{
		Name:        "DB_HOST",
		Value:       "{{proxies.#.host}}",
		Type:        config.VariableTypeEnv,
		Conditional: origCond,
	}
	if err := cl.SetAppVariableValue(app, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The caller's original slice must not be mutated.
	if origCond[0].Value != "{{proxies.#.host}}" {
		t.Errorf("input conditional slice was mutated: got %q", origCond[0].Value)
	}
}
