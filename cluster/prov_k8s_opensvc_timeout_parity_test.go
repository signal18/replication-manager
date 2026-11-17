package cluster

// Kubernetes follows the OpenSVC timeout contract (PR #2390), including where OpenSVC leaves a value
// unspecified: nothing is configured on the Kubernetes side that OpenSVC does not configure. The offline
// tests prove repman's own manifests and wiring; the kubelet's behavior is for the lab runs.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// OpenSVC writes stop_timeout for ColumnStore only (prov_opensvc_db.go) and Kubernetes provisioning has no
// ColumnStore support: so no database Pod, new or patched, carries a termination grace period
func TestK8SParity_NoExplicitStopTimeoutWhereOpenSVCHasNone(t *testing.T) {
	osvc := readSource(t, "prov_opensvc_db.go")
	if strings.Count(osvc, `"stop_timeout"`) != 1 {
		t.Fatal("OpenSVC writes another stop_timeout: Kubernetes must be reviewed against it")
	}
	i := strings.Index(osvc, `svccontainer["stop_timeout"]`)
	if !strings.Contains(osvc[max(0, i-400):i], "HasConfigTopoColumnstore()") {
		t.Fatal("OpenSVC's stop_timeout is no longer ColumnStore-only: Kubernetes must be reviewed against it")
	}
	k8s := readSource(t, "prov_k8s_db.go")
	if strings.Contains(strings.ToLower(k8s), "columnstore") {
		t.Error("Kubernetes provisioning handles ColumnStore now: apply OpenSVC's explicit stop_timeout for it")
	}
	if strings.Contains(k8s, "TerminationGracePeriodSeconds") || strings.Contains(k8s, "terminationGracePeriodSeconds") {
		t.Error("an explicit termination grace period where OpenSVC sets no stop_timeout")
	}

	for _, topo := range []string{"", "mariadb", "mysql"} {
		c := newTestCluster(swNS)
		c.Conf.TopologyTarget = topo
		dep := c.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")
		if g := dep.Spec.Template.Spec.TerminationGracePeriodSeconds; g != nil {
			t.Errorf("a new database Deployment must leave the grace period unset, got %d", *g)
		}
	}
}

// the Pod-replacing operations leave an existing Deployment's grace period exactly as it is
func TestK8SParity_LifecyclePatchesDoNotTouchTheGracePeriod(t *testing.T) {
	for _, op := range swMigrationOps {
		t.Run(op.name, func(t *testing.T) {
			for _, start := range []*int64{nil, ptrInt64(60)} {
				d := swDeployment(op.replic, swRecreate())
				d.Spec.Template.Spec.TerminationGracePeriodSeconds = start
				client := fake.NewSimpleClientset(d)
				if err := op.run(newTestCluster(swNS), client); err != nil {
					t.Fatal(err)
				}
				dep, _ := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
				got := dep.Spec.Template.Spec.TerminationGracePeriodSeconds
				if (start == nil) != (got == nil) || (start != nil && *start != *got) {
					t.Errorf("grace period changed by %s: %v -> %v", op.name, start, got)
				}
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

// prov-db-start-timeout is OpenSVC's pull_timeout and start_timeout; prov-timeout is read by nothing.
// Kubernetes has no per-container mechanism for either phase, so neither is consumed.
func TestK8SParity_StartAndEventTimeoutsAreNotInventedOnKubernetes(t *testing.T) {
	osvc := readSource(t, "prov_opensvc_db.go")
	for _, kw := range []string{`svccontainer["pull_timeout"] = server.ClusterGroup.dbStartTimeout()`, `svccontainer["start_timeout"] = server.ClusterGroup.dbStartTimeout()`} {
		if !strings.Contains(osvc, kw) {
			t.Errorf("OpenSVC no longer writes %s: re-check the Kubernetes mapping", kw)
		}
	}
	k8s := readSource(t, "prov_k8s_db.go")
	for _, forbidden := range []string{"ProvDbStartTimeout", "dbStartTimeout(", "ProvEventTimeout", "ProgressDeadlineSeconds", "k8sWaitDatabaseContainerStarted"} {
		if strings.Contains(k8s, forbidden) {
			t.Errorf("Kubernetes provisioning uses %s: OpenSVC has no equivalent failure condition for it", forbidden)
		}
	}
}

// the provisioning of a server ends as OpenSVC's does: objects created, then WaitDatabaseStart, whose result
// is not the provisioning's, then the result; the only gate is WaitDatabaseCanConn in ProvisionServices
func TestK8SParity_ProvisioningEndsLikeOpenSVC(t *testing.T) {
	k8s := readSource(t, "prov_k8s_db.go")
	start := strings.Index(k8s, "func (cluster *Cluster) K8SProvisionDatabaseService(")
	end := strings.Index(k8s, "// k8sStopDatabaseServiceWithClient scales")
	if start < 0 || end < start {
		t.Fatal("K8SProvisionDatabaseService not found")
	}
	body := k8s[start:end]
	pos := 0
	for _, step := range []string{"servicesClient.Create(", "\tcluster.WaitDatabaseStart(s)\n", "cluster.errorChan <- nil"} {
		i := strings.Index(body[pos:], step)
		if i < 0 {
			t.Fatalf("%q missing or out of order", strings.TrimSpace(step))
		}
		pos += i + len(step)
	}
	if strings.Contains(body, "= cluster.WaitDatabaseStart(s)") || strings.Contains(body, "if err := cluster.WaitDatabaseStart(s)") {
		t.Error("OpenSVC ignores the result of WaitDatabaseStart: so must Kubernetes")
	}
	osvc := readSource(t, "prov_opensvc_db.go")
	i := strings.Index(osvc, "func (cluster *Cluster) OpenSVCProvisionDatabaseService(")
	tail := osvc[i : i+3000]
	if !strings.Contains(tail, "\tcluster.WaitDatabaseStart(s)\n\n\tcluster.errorChan <- nil") {
		t.Error("OpenSVC's provisioning no longer ends with an unchecked WaitDatabaseStart: re-check Kubernetes")
	}
	prov := readSource(t, "prov.go")
	if !strings.Contains(prov, "err := cluster.WaitDatabaseCanConn()") {
		t.Error("ProvisionServices no longer gates on WaitDatabaseCanConn")
	}
}

// the #2390 guard is unchanged: the 90s bound, used by its three waits, fail-closed (the behavior is
// covered by the TestK8SStart_*/TestK8SUnprovision_* single-writer tests)
func TestK8SParity_GuardKeepsItsBound(t *testing.T) {
	if k8sLifecycleWaitTimeout != 90*time.Second {
		t.Errorf("guard bound %s, want the 90s of #2390", k8sLifecycleWaitTimeout)
	}
	k8s := readSource(t, "prov_k8s_db.go")
	if n := strings.Count(k8s, "k8sLifecycleWaitTimeout, k8sRolloutPollInterval"); n != 3 {
		t.Errorf("start, creation over a retained PVC and unprovision must use the guard bound, found %d", n)
	}
	if strings.Contains(k8s, "k8sLifecycleTimeout") || strings.Contains(k8s, "k8sStopGraceMargin") {
		t.Error("the guard's deadline is derived from something: no such policy is in the OpenSVC contract")
	}
}
