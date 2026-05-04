package configurator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/share"
)

// TestParseVariableNamesFromCnf verifies that variable names are correctly
// extracted and normalised from my.cnf content.
func TestParseVariableNamesFromCnf(t *testing.T) {
	tests := []struct {
		name    string
		cnf     string
		want    []string
		wantLen int
	}{
		{
			name: "basic variables",
			cnf: `[mysqld]
innodb_buffer_pool_size=128M
max_connections = 200
log-bin=mysql-bin
`,
			want: []string{"innodb_buffer_pool_size", "max_connections", "log_bin"},
		},
		{
			name: "skip comments and sections",
			cnf: `# This is a comment
[mysqld]
# another comment
server_id=1
!includedir /etc/mysql/conf.d
`,
			want:    []string{"server_id"},
			wantLen: 1,
		},
		{
			name:    "empty content",
			cnf:     "",
			wantLen: 0,
		},
		{
			name: "hyphens normalised to underscores",
			cnf: `[mysqld]
innodb-buffer-pool-size=128M
skip-name-resolve
`,
			want: []string{"innodb_buffer_pool_size", "skip_name_resolve"},
		},
		{
			name: "deduplication",
			cnf: `[mysqld]
innodb_buffer_pool_size=128M
innodb_buffer_pool_size=256M
`,
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseVariableNamesFromCnf(tt.cnf)
			if tt.wantLen > 0 && len(got) != tt.wantLen {
				t.Errorf("got %d variables, want %d: %v", len(got), tt.wantLen, got)
			}
			if tt.want != nil {
				if len(got) != len(tt.want) {
					t.Errorf("got %d variables, want %d\n  got:  %v\n  want: %v", len(got), len(tt.want), got, tt.want)
					return
				}
				for i, w := range tt.want {
					if got[i] != w {
						t.Errorf("variable[%d] = %q, want %q", i, got[i], w)
					}
				}
			}
		})
	}
}

// TestDocHelpLookupVariables verifies that the embedded dochelp database
// can look up common MySQL/MariaDB variables.
func TestDocHelpLookupVariables(t *testing.T) {
	dh := NewDocHelp("") // no disk override — use embedded default
	matched, unknown := dh.LookupVariables([]string{
		"innodb_buffer_pool_size",
		"max_connections",
		"server_id",
		"this_variable_does_not_exist_xyz",
	})

	if len(matched) < 3 {
		t.Errorf("expected at least 3 matched variables, got %d", len(matched))
	}
	for _, m := range matched {
		if m.MariaDBURL == "" && m.MySQLURL == "" {
			t.Errorf("variable %q has no documentation URL", m.Name)
		}
	}
	if len(unknown) != 1 || unknown[0] != "this_variable_does_not_exist_xyz" {
		t.Errorf("expected 1 unknown variable, got %v", unknown)
	}
}

// TestDocHelpCoverageAgainstComplianceModule loads the compliance module,
// extracts all variable names from cnf file content, and checks what
// percentage is covered by the dochelp database. This is a coverage
// report, not a pass/fail test — it logs the match rate and any gaps.
func TestDocHelpCoverageAgainstComplianceModule(t *testing.T) {
	// Load the compliance module
	raw, err := share.EmbededDbModuleFS.ReadFile("opensvc/moduleset_mariadb.svc.mrm.db.json")
	if err != nil {
		t.Skipf("cannot read compliance module: %v", err)
	}

	type compliance struct {
		Rulesets []struct {
			Name      string `json:"ruleset_name"`
			Filter    string `json:"fset_name"`
			Variables []struct {
				Value string `json:"var_value"`
				Class string `json:"var_class"`
				Name  string `json:"var_name"`
			} `json:"variables"`
		} `json:"rulesets"`
	}
	var comp compliance
	if err := json.Unmarshal(raw, &comp); err != nil {
		t.Fatalf("cannot parse compliance module: %v", err)
	}

	// Extract variable names only from .cnf file content (skip scripts, launchers, etc.)
	type fileVar struct {
		Path string `json:"path"`
		Fmt  string `json:"fmt"`
	}
	allVars := make(map[string]bool)
	for _, rs := range comp.Rulesets {
		if !strings.Contains(rs.Name, "mariadb.svc.mrm.db.cnf") {
			continue
		}
		for _, v := range rs.Variables {
			if v.Class != "file" {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(v.Value), &fv); err != nil {
				continue
			}
			// Only parse .cnf files — skip scripts, launchers, etc.
			if !strings.HasSuffix(fv.Path, ".cnf") {
				continue
			}
			for _, name := range ParseVariableNamesFromCnf(fv.Fmt) {
				allVars[name] = true
			}
		}
	}

	if len(allVars) == 0 {
		t.Skip("no variables extracted from compliance module")
	}

	// Look them up in the dochelp database
	names := make([]string, 0, len(allVars))
	for name := range allVars {
		names = append(names, name)
	}

	dh := NewDocHelp("")
	matched, unknown := dh.LookupVariables(names)

	matchRate := float64(len(matched)) / float64(len(names)) * 100

	t.Logf("Compliance module variables: %d", len(names))
	t.Logf("Matched in dochelp DB:       %d (%.1f%%)", len(matched), matchRate)
	t.Logf("Not found in dochelp DB:     %d", len(unknown))

	if len(unknown) > 0 && len(unknown) <= 20 {
		t.Logf("Missing variables: %v", unknown)
	} else if len(unknown) > 20 {
		t.Logf("First 20 missing: %v", unknown[:20])
	}

	// Warn if coverage drops below 50% — something is wrong with the DB
	if matchRate < 50 {
		t.Errorf("dochelp coverage is only %.1f%% — expected at least 50%%", matchRate)
	}
}
