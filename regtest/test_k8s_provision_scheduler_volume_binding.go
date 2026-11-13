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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TestK8SProvisionSchedulerVolumeBinding reprovisions a Kubernetes-orchestrated
// slave and verifies its PVC actually binds and a REPLACEMENT pod actually reaches
// Running, rather than just checking that the Create() calls succeeded.
//
// It regresses two real bugs found via live-cluster testing (neither caught by the
// fake-client unit tests in cluster/prov_k8s_test.go, since the fake API server
// simulates neither the scheduler nor the Deployment controller):
//
//   - pinning the Deployment's pod via Spec.NodeName bypasses the scheduler
//     entirely, so a WaitForFirstConsumer StorageClass (the default for most
//     dynamic provisioners, including kind's local-path-provisioner and typical
//     cloud CSI drivers) never binds the PVC, since that only happens during
//     scheduling -- the pod hangs in Init forever. See
//     doc/implementation/cluster/KUBERNETES_PROVISIONING.md ("Node discovery").
//   - a reprovision that returned before the foreground deletion of the old
//     Deployment had completed: the provisioning Create returned AlreadyExists
//     (accepted as success), the OLD pod, still Running while it stopped, satisfied a
//     "some pod is Running" check, and the Deployment then disappeared. The test
//     therefore follows identities, not names: the old Deployment and pods are
//     recorded first; Unprovision returning success must mean the old Deployment is
//     already gone (no sleep stands in for it); the replacement must be a different
//     Deployment, with a different pod owned by it, ready, and must still be there
//     after a stability window.
//
// This test requires:
//   - Kubernetes orchestrator (prov-orchestrator = kube)
//   - At least one slave
//   - A real Kubernetes API connection (cl.K8SConnectAPI()) -- this cannot run
//     against a fake/mocked cluster, only a real one (kind, minikube, or a
//     real cluster), which is exactly the T13 gap this test is meant to close
//     once a Kubernetes-capable CI harness exists.
//   - A DISPOSABLE cluster: it unprovisions and reprovisions a database server.
func (regtest *RegTest) TestK8SProvisionSchedulerVolumeBinding(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	// Prefer a slave, to avoid disrupting a live master -- but the
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
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: "+format, args...)
		return false
	}

	// The generation being replaced: the Deployment and every pod of it, by UID.
	oldDeploymentUID := types.UID("")
	if dep, getErr := client.AppsV1().Deployments(cl.Name).Get(context.TODO(), slave.Name, metav1.GetOptions{}); getErr == nil {
		oldDeploymentUID = dep.UID
	} else if !apierrors.IsNotFound(getErr) {
		return fail("cannot read the Deployment %s before reprovisioning: %s", slave.Name, getErr)
	}
	oldPods := map[types.UID]string{}
	if pods, listErr := client.CoreV1().Pods(cl.Name).List(context.TODO(), metav1.ListOptions{LabelSelector: "tag=" + slave.Name}); listErr == nil {
		for _, p := range pods.Items {
			oldPods[p.UID] = p.Name
		}
	} else {
		return fail("cannot list the pods of %s before reprovisioning: %s", slave.Name, listErr)
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Replacing Deployment %s (uid %q) and %d pod(s)", slave.Name, oldDeploymentUID, len(oldPods))

	// Reprovision from scratch so this exercises the exact scheduling path a
	// real provision goes through, not whatever state the slave was already
	// left in (e.g. already Running from initial cluster bootstrap, in which
	// case the scheduling/PVC-binding step being tested wouldn't happen at all).
	if err := cl.UnprovisionDatabaseService(slave); err != nil {
		return fail("unprovision: %s", err)
	}
	// A successful unprovision means the old generation is gone: checked at once, not after a sleep.
	if dep, getErr := client.AppsV1().Deployments(cl.Name).Get(context.TODO(), slave.Name, metav1.GetOptions{}); getErr == nil {
		return fail("unprovision returned success while Deployment %s (uid %s, deletion timestamp %v) still exists", dep.Name, dep.UID, dep.DeletionTimestamp)
	} else if !apierrors.IsNotFound(getErr) {
		return fail("cannot read the Deployment %s after unprovisioning: %s", slave.Name, getErr)
	}

	if err := cl.InitDatabaseService(slave); err != nil {
		return fail("provisioning failed: %s", err)
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
		return fail("PVC %s did not bind within 90s -- this is the WaitForFirstConsumer/scheduler-bypass regression", pvcName)
	}

	// The replacement: a Deployment that is not the old one and is not being deleted ...
	dep, getErr := client.AppsV1().Deployments(cl.Name).Get(context.TODO(), slave.Name, metav1.GetOptions{})
	if getErr != nil {
		return fail("no Deployment %s after provisioning: %s", slave.Name, getErr)
	}
	if dep.DeletionTimestamp != nil {
		return fail("Deployment %s (uid %s) is being deleted: provisioning was accepted on a deleting object", dep.Name, dep.UID)
	}
	if oldDeploymentUID != "" && dep.UID == oldDeploymentUID {
		return fail("Deployment %s still is the old one (uid %s): it was not replaced", dep.Name, dep.UID)
	}

	// ... with a new pod, owned by it, that runs and is ready.
	var replacement *apiv1.Pod
	for deadline := time.Now().Add(120 * time.Second); time.Now().Before(deadline); {
		if pod := k8sReplacementPod(client, cl.Name, slave.Name, dep.UID, oldPods); pod != nil {
			replacement = pod
			break
		}
		time.Sleep(2 * time.Second)
	}
	if replacement == nil {
		return fail("no new, ready pod owned by Deployment %s (uid %s) reached Running within 120s", slave.Name, dep.UID)
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Replacement pod %s (uid %s) is running and ready", replacement.Name, replacement.UID)

	// The old pods are gone: the replacement did not simply sit next to them.
	for uid, name := range oldPods {
		pods, listErr := client.CoreV1().Pods(cl.Name).List(context.TODO(), metav1.ListOptions{LabelSelector: "tag=" + slave.Name})
		if listErr != nil {
			return fail("cannot list the pods of %s: %s", slave.Name, listErr)
		}
		for _, p := range pods.Items {
			if p.UID == uid {
				return fail("old pod %s (uid %s) still exists next to the replacement", name, uid)
			}
		}
	}

	// The replacement persists: a late deletion of the old generation would take it away.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		time.Sleep(2 * time.Second)
		cur, curErr := client.AppsV1().Deployments(cl.Name).Get(context.TODO(), slave.Name, metav1.GetOptions{})
		if curErr != nil {
			return fail("Deployment %s disappeared after provisioning: %s", slave.Name, curErr)
		}
		if cur.UID != dep.UID || cur.DeletionTimestamp != nil {
			return fail("Deployment %s changed identity or is being deleted after provisioning (uid %s, was %s)", slave.Name, cur.UID, dep.UID)
		}
		pod, podErr := client.CoreV1().Pods(cl.Name).Get(context.TODO(), replacement.Name, metav1.GetOptions{})
		if podErr != nil || pod.UID != replacement.UID || pod.DeletionTimestamp != nil {
			return fail("replacement pod %s did not persist (%v)", replacement.Name, podErr)
		}
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"PASS: %s replaced (Deployment %s -> %s), PVC bound, new pod %s Running and persistent", slave.Name, oldDeploymentUID, dep.UID, replacement.Name)
	return true
}

// k8sReplacementPod returns a pod of the server that is running, not terminating, whose
// container named after the server is ready, that is not one of the old pods and that is owned (through
// its ReplicaSet) by the Deployment with the given UID; nil when there is none yet.
func k8sReplacementPod(client kubernetes.Interface, namespace, server string, deploymentUID types.UID, oldPods map[types.UID]string) *apiv1.Pod {
	pods, err := client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "tag=" + server})
	if err != nil {
		return nil
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if _, old := oldPods[pod.UID]; old || pod.DeletionTimestamp != nil || pod.Status.Phase != apiv1.PodRunning {
			continue
		}
		ready := false
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == server && cs.Ready {
				ready = true
			}
		}
		if !ready {
			continue
		}
		for _, ref := range pod.OwnerReferences {
			if ref.Kind != "ReplicaSet" {
				continue
			}
			rs, rsErr := client.AppsV1().ReplicaSets(namespace).Get(context.TODO(), ref.Name, metav1.GetOptions{})
			if rsErr != nil {
				continue
			}
			for _, rsRef := range rs.OwnerReferences {
				if rsRef.UID == deploymentUID {
					return pod
				}
			}
		}
	}
	return nil
}
