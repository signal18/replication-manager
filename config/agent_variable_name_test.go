package config

import "testing"

// The per-agent key is shared by provisioning and the credential stores (#2306): both must
// name it the same way, or the vault keys of the two writers drift apart.
func TestAgentVariableName(t *testing.T) {
	if got := AgentVariableName("APP_SECRET", "opensvc-node2"); got != "APP_SECRET@opensvc-node2" {
		t.Fatalf("AgentVariableName = %q", got)
	}
}
