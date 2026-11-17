package cluster

import (
	"os"
	"strings"
	"testing"
)

// RollingRestart runs on Kubernetes the sequence it runs on OpenSVC for every replica and for the original
// primary: stop, wait until the monitor has seen the database Failed, refresh the definition, start and wait
// for the rejoin of the returning database, then synchronize. The Kubernetes-only rolling pod replacement
// (K8SRestartDatabaseServiceWaitRejoin) has no way to tell the restarted database's rejoin from another one
// and completes on Pod Ready, before SQL is available: it must not come back in RollingRestart.
func TestRollingRestartRunsTheSameSequenceOnEveryOrchestrator(t *testing.T) {
	src, err := os.ReadFile("cluster_roll.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (cluster *Cluster) RollingRestart() error {")
	end := strings.Index(text, "func (cluster *Cluster) RollingOptimize()")
	if start < 0 || end < start {
		t.Fatal("RollingRestart not found in cluster_roll.go")
	}
	body := text[start:end]
	if strings.Contains(body, "K8SRestartDatabaseServiceWaitRejoin") || strings.Contains(body, "ConstOrchestratorKubernetes") {
		t.Error("RollingRestart must not special-case Kubernetes: the stop, wait Failed, refresh, start sequence dispatches by orchestrator")
	}
	// the same ordered steps, once for the replicas and once for the original primary
	steps := []string{
		"cluster.FreezeDatabaseService(",
		"cluster.StopDatabaseService(",
		"cluster.WaitDatabaseFailed(",
		"cluster.UpgradeDatabaseDeploymentOnStart(",
		"cluster.StartDatabaseWaitRejoin(",
	}
	for _, who := range []string{"slave", "master"} {
		pos := 0
		for _, step := range steps {
			i := strings.Index(body[pos:], step+who)
			if i < 0 {
				t.Fatalf("RollingRestart: %s%s is missing or out of order for the %s", step, who, who)
			}
			pos += i + len(step)
		}
	}
	// the replicas synchronize after the restart, the original primary too
	for _, sync := range []string{"slave.WaitSyncToMaster(currentMaster)", "master.WaitSyncToMaster(cluster.master)"} {
		if !strings.Contains(body, sync) {
			t.Errorf("RollingRestart lost %s", sync)
		}
	}
}

// The definition refresh of Kubernetes patches the pod template and must find the Deployment at zero replicas:
// the sequence above stops the database (scale to zero) and waits for the monitor to see it Failed first.
func TestK8SDefinitionRefreshNeedsZeroReplicasSoItFollowsTheStop(t *testing.T) {
	src, err := os.ReadFile("prov_k8s_db.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	i := strings.Index(text, "func (cluster *Cluster) k8sUpdateDatabaseServiceConfigWithClientImage(")
	if i < 0 {
		t.Fatal("k8sUpdateDatabaseServiceConfigWithClientImage not found")
	}
	if !strings.Contains(text[i:i+3000], "is not scaled to 0 replicas") {
		t.Error("the Kubernetes definition refresh no longer refuses a Deployment that is not at zero replicas")
	}
	stop := text[strings.Index(text, "func (cluster *Cluster) k8sStopDatabaseServiceWithClient("):]
	if !strings.Contains(stop[:400], `{"spec":{"replicas":0}}`) {
		t.Error("the Kubernetes Stop no longer scales the Deployment to zero")
	}
}
