package cluster

import (
	"strings"
	"testing"
)

func TestVariableDiff_Changed(t *testing.T) {
	prev := map[string]string{
		"MAX_CONNECTIONS":         "500",
		"INNODB_BUFFER_POOL_SIZE": "4G",
	}
	current := map[string]string{
		"MAX_CONNECTIONS":         "1000",
		"INNODB_BUFFER_POOL_SIZE": "4G",
	}
	diff := buildVariableDiff("db1:3306", prev, current)

	if !strings.Contains(diff, "- MAX_CONNECTIONS = 500") {
		t.Error("missing old MAX_CONNECTIONS")
	}
	if !strings.Contains(diff, "+ MAX_CONNECTIONS = 1000") {
		t.Error("missing new MAX_CONNECTIONS")
	}
	if strings.Contains(diff, "INNODB_BUFFER_POOL_SIZE") {
		t.Error("unchanged variable should not appear")
	}
}

func TestVariableDiff_Removed(t *testing.T) {
	prev := map[string]string{
		"MAX_CONNECTIONS": "500",
		"OLD_VAR":         "old_value",
	}
	current := map[string]string{
		"MAX_CONNECTIONS": "500",
	}
	diff := buildVariableDiff("db1:3306", prev, current)

	if !strings.Contains(diff, "- OLD_VAR = old_value") {
		t.Error("missing removed OLD_VAR")
	}
	if strings.Contains(diff, "MAX_CONNECTIONS") {
		t.Error("unchanged variable should not appear")
	}
}

func TestVariableDiff_New(t *testing.T) {
	prev := map[string]string{
		"MAX_CONNECTIONS": "500",
	}
	current := map[string]string{
		"MAX_CONNECTIONS": "500",
		"NEW_VAR":         "new_value",
	}
	diff := buildVariableDiff("db1:3306", prev, current)

	if !strings.Contains(diff, "+ NEW_VAR = new_value") {
		t.Error("missing new NEW_VAR")
	}
	if strings.Contains(diff, "MAX_CONNECTIONS") {
		t.Error("unchanged variable should not appear")
	}
}

func TestVariableDiff_NoChange(t *testing.T) {
	vars := map[string]string{
		"MAX_CONNECTIONS": "500",
		"PORT":            "3306",
	}
	diff := buildVariableDiff("db1:3306", vars, vars)

	if strings.Contains(diff, "\n- ") || strings.Contains(diff, "\n+ ") {
		t.Errorf("identical vars should produce no diff lines, got: %q", diff)
	}
}

func TestVariableDiff_EmptyBoth(t *testing.T) {
	diff := buildVariableDiff("db1:3306", nil, nil)
	lines := strings.Split(strings.TrimSpace(diff), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 header lines, got %d", len(lines))
	}
}

func TestVariableDiff_AllNew(t *testing.T) {
	current := map[string]string{
		"VAR_A": "1",
		"VAR_B": "2",
	}
	diff := buildVariableDiff("db1:3306", nil, current)

	if !strings.Contains(diff, "+ VAR_A = 1") {
		t.Error("missing VAR_A")
	}
	if !strings.Contains(diff, "+ VAR_B = 2") {
		t.Error("missing VAR_B")
	}
}

func TestVariableDiff_AllRemoved(t *testing.T) {
	prev := map[string]string{
		"VAR_A": "1",
		"VAR_B": "2",
	}
	diff := buildVariableDiff("db1:3306", prev, nil)

	if !strings.Contains(diff, "- VAR_A = 1") {
		t.Error("missing VAR_A")
	}
	if !strings.Contains(diff, "- VAR_B = 2") {
		t.Error("missing VAR_B")
	}
}

// buildVariableDiff is the same logic as MonitorVariablesChange, extracted for testing.
func buildVariableDiff(serverURL string, prevVars, currentVars map[string]string) string {
	var sb strings.Builder
	sb.WriteString("--- " + serverURL + " (before)\n")
	sb.WriteString("+++ " + serverURL + " (after)\n")

	for k, old := range prevVars {
		if v, exists := currentVars[k]; !exists {
			sb.WriteString("- " + k + " = " + old + "\n")
		} else if old != v {
			sb.WriteString("- " + k + " = " + old + "\n")
			sb.WriteString("+ " + k + " = " + v + "\n")
		}
	}
	for k, v := range currentVars {
		if _, exists := prevVars[k]; !exists {
			sb.WriteString("+ " + k + " = " + v + "\n")
		}
	}

	return sb.String()
}
