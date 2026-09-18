// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestK8SProvisionSchedulerVolumeBinding reprovisions a Kubernetes-orchestrated
// slave and verifies its PVC actually binds and its pod actually reaches
// Running, rather than just checking that the Create() calls succeeded.
//
// This regresses a real bug found via live-cluster testing (not caught by the
// fake-client unit tests in cluster/prov_k8s_test.go, since the fake API
// server doesn't simulate the scheduler or the volume-binding controller):
// pinning the Deployment's pod via Spec.NodeName bypasses the scheduler
// entirely, so a WaitForFirstConsumer StorageClass (the default for most
// dynamic provisioners, including kind's local-path-provisioner and typical
// cloud CSI drivers) never binds the PVC, since that only happens during
// scheduling — the pod hangs in Init forever. See
// doc/implementation/cluster/KUBERNETES_PROVISIONING.md ("Node discovery").
//
// This test requires:
//   - Kubernetes orchestrator (prov-orchestrator = kube)
//   - At least one slave
//   - A real Kubernetes API connection (cl.K8SConnectAPI()) — this cannot run
//     against a fake/mocked cluster, only a real one (kind, minikube, or a
//     real cluster), which is exactly the T13 gap this test is meant to close
//     once a Kubernetes-capable CI harness exists.
func (regtest *RegTest) TestK8SProvisionSchedulerVolumeBinding(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if cl.GetOrchestrator() != config.ConstOrchestratorKubernetes {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: requires Kubernetes orchestrator")
		return true
	}

	// Prefer a slave, to avoid disrupting a live master — but the
	// scheduling/PVC-binding behavior under test is per-server and doesn't
	// depend on replication role, so fall back to any server if no
	// replication topology has been established yet (e.g. SQL connectivity
	// to the pod isn't reachable from wherever repman itself is running,
	// which is a deployment-topology concern orthogonal to what this test
	// checks).
	var slave *cluster.ServerMonitor
	if slaves := cl.GetSlaves(); len(slaves) > 0 {
		slave = slaves[0]
	} else if servers := cl.GetServers(); len(servers) > 0 {
		slave = servers[0]
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: no servers available")
		return true
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Testing Kubernetes scheduler/PVC-binding provisioning for %s", slave.URL)

	client, err := cl.K8SConnectAPI()
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot connect to Kubernetes API: %s", err)
		return false
	}

	// Reprovision from scratch so this exercises the exact scheduling path a
	// real provision goes through, not whatever state the slave was already
	// left in (e.g. already Running from initial cluster bootstrap, in which
	// case the scheduling/PVC-binding step being tested wouldn't happen at all).
	if err := cl.UnprovisionDatabaseService(slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"Unprovision before reprovision reported: %s (continuing)", err)
	}
	time.Sleep(5 * time.Second)

	if err := cl.InitDatabaseService(slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: provisioning failed: %s", err)
		return false
	}

	pvcName := cl.Name + "-" + slave.Name + "-claim"
	pvcBound := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		pvc, getErr := client.CoreV1().PersistentVolumeClaims(cl.Name).Get(context.TODO(), pvcName, metav1.GetOptions{})
		if getErr == nil && pvc.Status.Phase == apiv1.ClaimBound {
			pvcBound = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !pvcBound {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: PVC %s did not bind within 90s — this is the WaitForFirstConsumer/scheduler-bypass regression", pvcName)
		return false
	}

	podRunning := false
	for deadline := time.Now().Add(120 * time.Second); time.Now().Before(deadline); {
		pods, listErr := client.CoreV1().Pods(cl.Name).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "tag=" + slave.Name,
		})
		if listErr == nil {
			for _, p := range pods.Items {
				if p.Status.Phase == apiv1.PodRunning {
					podRunning = true
					break
				}
			}
		}
		if podRunning {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !podRunning {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: pod for %s did not reach Running within 120s", slave.Name)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"PASS: %s provisioned, PVC bound, pod Running", slave.Name)
	return true
}
