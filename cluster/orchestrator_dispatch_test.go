package cluster

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The lock of the orchestrator migration (doc/implementation/cluster/ORCHESTRATOR_INTERFACE.md,
// section 5.3): the files that may still dispatch on the orchestrator type, and how many
// lines of each do. A dispatch line is a line that compares to a config.ConstOrchestrator*
// constant, compares ProvOrchestrator to a string, or calls a backend-prefixed method
// (OpenSVC*, K8S*, k8s*, OnPremise*, Localhost*, SlapOS*) through a selector.
//
// The counts are the inventory of the design document (section 1) plus the adapters and the
// backend files themselves. A new dispatch line anywhere fails the test; a line removed by a
// migration step fails it too, so the step lowers the count here in the same pull request
// and the list only ever shrinks. At the last step the list is the adapters and the
// registry alone.
//
// Measured at migration step 1: 710 lines in 75 files. The grep of the design document
// (227 lines) counts the type comparisons only; this measure adds the calls to the
// backend-prefixed methods, which are the dispatch the grep could not see (the 27 direct
// calls from server/ and mcp/, and the backends calling each other).
var orchestratorDispatchAllowed = map[string]int{
	// the interface, its registry and its adapters: the one place that may name a type
	"cluster/orchestrator_k8s.go":       18,
	"cluster/orchestrator_localhost.go": 10,
	"cluster/orchestrator_onpremise.go": 12,
	"cluster/orchestrator_opensvc.go":   32,
	"cluster/orchestrator_proxmox.go":   1, // step 3b: its Type()
	"cluster/orchestrator_registry.go":  8, // step 3b: the proxmox case, serviceOrchestratorType
	"cluster/orchestrator_slapos.go":    8,
	// the backends themselves, until they move behind the adapters
	"cluster/app_resize_dynamic.go":      7, // develop at the 2026-11-23 merge (app live resize, #2417)
	"cluster/cluster_resize_k8s.go":      10,
	"cluster/prov_image.go":              1,
	"cluster/prov_k8s.go":                2,
	"cluster/prov_k8s_db.go":             64, // develop at the 2026-11-23 merge (Kubernetes work landed after step 1)
	"cluster/prov_k8s_jobs_key.go":       3,  // develop at the 2026-11-23 merge
	"cluster/prov_k8s_manifest.go":       2,
	"cluster/prov_k8s_prx.go":            18,
	"cluster/prov_localhost_db.go":       4,
	"cluster/prov_localhost_haproxy.go":  2,
	"cluster/prov_localhost_prx.go":      10,
	"cluster/prov_localhost_proxysql.go": 2,
	"cluster/prov_onpremise_db.go":       6,
	"cluster/prov_onpremise_haproxy.go":  4,
	"cluster/prov_onpremise_prx.go":      12,
	"cluster/prov_onpremise_proxysql.go": 4,
	"cluster/prov_opensvc.go":            7,
	"cluster/prov_opensvc_app.go":        45,
	"cluster/prov_opensvc_db.go":         49,
	"cluster/prov_opensvc_haproxy.go":    1,
	"cluster/prov_opensvc_prx.go":        46,
	"cluster/store_orchestrator.go":      9,
	"cluster/store_orchestrator_k8s.go":  3,
	// the dispatch sites of the inventory (design section 1), by file
	"cluster/app_get.go":                   4,
	"cluster/app_has.go":                   1,
	"cluster/bootstrap_dr.go":              2,
	"cluster/cluster.go":                   12,
	"cluster/cluster_app.go":               1,
	"cluster/cluster_chk.go":               9,
	"cluster/cluster_compute_sensor.go":    2,
	"cluster/cluster_del.go":               1,
	"cluster/cluster_get.go":               6,
	"cluster/cluster_has.go":               1,
	"cluster/cluster_resize_dynamic.go":    14,
	"cluster/cluster_roll.go":              2,
	"cluster/cluster_roll_plan.go":         5,
	"cluster/cluster_sec_galera_sst.go":    1,
	"cluster/cluster_sec_job_login.go":     2,
	"cluster/cluster_secret_kv.go":         3,
	"cluster/cluster_set.go":               16,
	"cluster/cluster_tst.go":               2,
	"cluster/cluster_user_rotate.go":       2,
	"cluster/configurator/configurator.go": 4,
	"cluster/configurator/distribution.go": 2,
	"cluster/prov.go":                      156,
	"cluster/prx.go":                       2,
	"cluster/prx_get.go":                   4,
	"cluster/prx_haproxy.go":               2,
	"cluster/prx_has.go":                   1,
	"cluster/prx_maxscale.go":              1,
	"cluster/prx_proxysql.go":              1,
	"cluster/srv.go":                       4,
	"cluster/srv_binlog_budget.go":         1,
	"cluster/srv_cnf.go":                   10,
	"cluster/srv_get.go":                   3,
	"cluster/srv_job.go":                   1,
	"cluster/srv_job_backup.go":            1,
	"cluster/srv_job_postgres.go":          3,
	"cluster/srv_set.go":                   1,
	"cluster/srv_wal_archive.go":           2,
	"mcp/tools_app.go":                     6,
	"server/api.go":                        1,
	"server/api_app.go":                    10,
	"server/api_cluster.go":                3,
	"server/api_database.go":               7,
	"server/api_proxy.go":                  2,
	"server/server.go":                     6,
	"server/server_child_cluster.go":       1,
	"server/server_secret_kv.go":           2,
	"server/server_selfservice.go":         3,
	"server/server_tty.go":                 5,
}

var backendMethodRe = regexp.MustCompile(`^(OpenSVC|K8S|k8s|OnPremise|Localhost|SlapOS)[A-Z]`)

// orchestratorDispatchLines returns the dispatch lines of one Go file, as distinct line numbers.
func orchestratorDispatchLines(t *testing.T, path string) map[int]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	lines := map[int]bool{}
	mark := func(n ast.Node) { lines[fset.Position(n.Pos()).Line] = true }
	isConstOrchestrator := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "config" && strings.HasPrefix(sel.Sel.Name, "ConstOrchestrator")
	}
	isProvOrchestrator := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "ProvOrchestrator"
	}
	isString := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if isConstOrchestrator(x) {
				mark(x)
			}
		case *ast.BinaryExpr:
			if (x.Op == token.EQL || x.Op == token.NEQ) &&
				((isProvOrchestrator(x.X) && isString(x.Y)) || (isProvOrchestrator(x.Y) && isString(x.X))) {
				mark(x)
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && backendMethodRe.MatchString(sel.Sel.Name) {
				mark(x)
			}
		}
		return true
	})
	return lines
}

// orchestratorDispatchFiles lists the non-test Go files of the packages the lock covers.
func orchestratorDispatchFiles(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, dir := range []string{"cluster", "cluster/configurator", "server", "mcp"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			files[dir+"/"+name] = filepath.Join(root, dir, name)
		}
	}
	return files
}

func TestNoOrchestratorDispatchOutsideRegistry(t *testing.T) {
	files := orchestratorDispatchFiles(t)
	counts := map[string]int{}
	for rel, path := range files {
		if n := len(orchestratorDispatchLines(t, path)); n > 0 {
			counts[rel] = n
		}
	}
	var problems []string
	for rel, got := range counts {
		want, ok := orchestratorDispatchAllowed[rel]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %d dispatch lines, not in the allowed list: route the operation through cluster.Orchestrator() instead of a switch on the type", rel, got))
		case got > want:
			problems = append(problems, fmt.Sprintf("%s: %d dispatch lines, %d allowed: a new switch on the orchestrator type was added", rel, got, want))
		case got < want:
			problems = append(problems, fmt.Sprintf("%s: %d dispatch lines, %d allowed: lower the allowed count in this pull request so the list keeps shrinking", rel, got, want))
		}
	}
	for rel, want := range orchestratorDispatchAllowed {
		if _, ok := files[rel]; !ok {
			problems = append(problems, fmt.Sprintf("%s: in the allowed list (%d) but no such file: remove it", rel, want))
		} else if _, ok := counts[rel]; !ok {
			problems = append(problems, fmt.Sprintf("%s: in the allowed list (%d) but has no dispatch line left: remove it", rel, want))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if testing.Verbose() {
		total := 0
		rels := make([]string, 0, len(counts))
		for rel := range counts {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for _, rel := range rels {
			t.Logf("%-45s %d", rel, counts[rel])
			total += counts[rel]
		}
		t.Logf("total dispatch lines: %d in %d files", total, len(counts))
	}
}
