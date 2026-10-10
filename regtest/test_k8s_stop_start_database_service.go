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
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestK8SStopStartDatabaseService verifies that StopDatabaseService and
// StartDatabaseService actually perform a real scale-to-0/scale-to-1 cycle
// on Kubernetes (k8sStopDatabaseServiceWithClient/
// k8sStartDatabaseServiceWithClient, cluster/prov_k8s_db.go), not the
// previous behavior where Stop always returned an error and Start was only
// a state check -- neither of which ever touched the Deployment.
//
// This can't be exercised by the fake-client unit tests in
// cluster/prov_k8s_test.go, since the fake clientset doesn't run a real
// ReplicaSet controller: patching replicas there never actually creates or
// deletes a pod, so those tests can only assert the patch request itself,
// not that the pod lifecycle genuinely follows it. Requires a real
// Kubernetes API connection (kind, minikube, or a real cluster) --
// the same T13 gap TestK8SProvisionSchedulerVolumeBinding documents.
func (regtest *RegTest) TestK8SStopStartDatabaseService(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if cl.GetOrchestrator() != config.ConstOrchestratorKubernetes {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: requires Kubernetes orchestrator")
		return true
	}

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
		"Testing Kubernetes stop/start scale cycle for %s", slave.URL)

	client, err := cl.K8SConnectAPI()
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot connect to Kubernetes API: %s", err)
		return false
	}
	deploymentsClient := client.AppsV1().Deployments(cl.Name)

	if err := cl.StopDatabaseService(slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: StopDatabaseService returned an error: %s", err)
		return false
	}

	stopped := false
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		dep, getErr := deploymentsClient.Get(context.TODO(), slave.Name, metav1.GetOptions{})
		if getErr == nil && dep.Status.Replicas == 0 {
			stopped = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !stopped {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: Deployment %s did not scale down to 0 running replicas within 60s -- Stop did not actually stop the pod", slave.Name)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Stop confirmed: %s scaled to 0 replicas", slave.Name)

	if err := cl.StartDatabaseService(slave); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: StartDatabaseService returned an error: %s", err)
		return false
	}

	var started *appsv1.Deployment
	for deadline := time.Now().Add(120 * time.Second); time.Now().Before(deadline); {
		dep, getErr := deploymentsClient.Get(context.TODO(), slave.Name, metav1.GetOptions{})
		if getErr == nil && dep.Status.ReadyReplicas >= 1 {
			started = dep
			break
		}
		time.Sleep(2 * time.Second)
	}
	if started == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: Deployment %s did not reach 1 ready replica within 120s after Start", slave.Name)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Start confirmed: %s has a ready replica again", slave.Name)

	// Confirm repman itself, not just Kubernetes, sees the server as healthy
	// again -- a Ready pod doesn't guarantee replication actually reconnected.
	reconnected := false
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		slave.Refresh()
		if !slave.IsDown() {
			reconnected = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !reconnected {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: %s did not report healthy to repman within 60s of the pod becoming ready", slave.URL)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"PASS: %s stopped and started via a real scale-to-0/scale-to-1 cycle, healthy afterward", slave.URL)
	return true
}
