//go:build kindsmoke

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// Manual smoke test against a real Kubernetes API server (kind), not the
// fake clientset every other test in this package uses. Run explicitly:
//
//	go test ./cluster -tags kindsmoke -run TestKindSmoke_ProxyProvisioning -v -timeout 120s
//
// Requires a reachable cluster via the current kubeconfig context (this was
// run against the pre-existing "kind-repman" cluster/context). Creates and
// deletes its own namespace ("repman-smoketest"); never touches any other
// namespace. Excluded from normal `go test ./...` by the kindsmoke build
// tag, since it needs a real cluster this repo's CI doesn't have (see
// "No Kubernetes-capable regtest/CI harness exists" in
// doc/implementation/cluster/KUBERNETES_PROVISIONING.md).
package cluster

import (
	"context"
	"hash/crc64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestKindSmoke_ProxyProvisioning(t *testing.T) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("cannot resolve home dir for default kubeconfig: %s", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	kconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("cannot load kubeconfig %q (current context): %s", kubeconfig, err)
	}
	client, err := kubernetes.NewForConfig(kconfig)
	if err != nil {
		t.Fatalf("cannot init Kubernetes client: %s", err)
	}

	const ns = "repman-smoketest"
	t.Cleanup(func() {
		_ = client.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	cl := &Cluster{Name: ns, Conf: &config.Config{
		ProvProxHaproxyImg:  "signal18/haproxy:2.6",
		ProvProxProxysqlImg: "signal18/proxysql:2.4",
		ProvProxDisk:        "1G",
		HaproxyReadPort:     3307,
		HaproxyStatPort:     1988,
	}}
	cl.crcTable = crc64.MakeTable(crc64.ECMA)

	haproxy := &fakeProxy{cluster: cl, name: "smoke-colocated", port: "1999", proxyType: config.ConstProxyHaproxy, writePort: 3306, readPort: 3307}
	proxysql := &fakeProxy{cluster: cl, name: "smoke-colocated", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}
	dotted := &fakeProxy{cluster: cl, name: "10.0.0.5", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6034}

	// 1. Fresh-namespace provision (k8sEnsureNamespace against a namespace
	// that doesn't exist yet -- the real failure mode a fake clientset can't
	// exercise, since it never enforces namespace existence).
	if err := cl.k8sProvisionProxyServiceWithClient(client, haproxy); err != nil {
		t.Fatalf("provision haproxy into fresh namespace %q: %s", ns, err)
	}
	if _, err := client.AppsV1().Deployments(ns).Get(context.Background(), k8sProxyDeploymentName(ns, "smoke-colocated"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected haproxy Deployment to exist: %s", err)
	}
	t.Log("PASS: fresh-namespace provision")

	// 2. Cross-type collision while haproxy's Deployment/Service are still
	// live -- must be refused by the real API server's response to our
	// preflight check, not just the fake clientset's in-memory tracker.
	if err := cl.k8sProvisionProxyServiceWithClient(client, proxysql); err == nil {
		t.Fatal("expected cross-type name collision (live Deployment/Service) to be refused, got nil")
	} else {
		t.Logf("PASS: cross-type collision refused: %s", err)
	}

	// 3. DNS-1035 Service name validation -- this is the one check whose
	// correctness genuinely depends on matching the real Kubernetes API
	// server's own validation.IsDNS1035Label, not just our own copy of it.
	if err := cl.k8sProvisionProxyServiceWithClient(client, dotted); err == nil {
		t.Fatal("expected dotted proxy name to be refused, got nil")
	} else {
		t.Logf("PASS: dotted name refused pre-Create: %s", err)
	}
	if _, err := client.AppsV1().Deployments(ns).Get(context.Background(), k8sProxyDeploymentName(ns, "10.0.0.5"), metav1.GetOptions{}); err == nil {
		t.Fatal("expected no Deployment to exist for the rejected dotted name")
	}

	// 4. Unprovision haproxy, then confirm the retained PVC alone still
	// blocks a different-type proxy from reusing the name -- the real-world
	// version of TestK8SProvisionProxy_RefusesNameOwnedByDifferentTypeViaRetainedPVC.
	if err := cl.k8sUnprovisionProxyServiceWithClient(client, haproxy); err != nil {
		t.Fatalf("unprovision haproxy: %s", err)
	}
	// Delete() returning success only means deletion was accepted, not
	// completed -- DeletePropagationForeground waits on the garbage
	// collector to remove dependents (the ReplicaSet/Pod) first, so the
	// Deployment stays Get()-able ("Terminating") for a moment after this
	// call returns. The fake clientset every other test in this package
	// uses deletes synchronously and would never surface this gap.
	deploymentGone := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := client.AppsV1().Deployments(ns).Get(context.Background(), k8sProxyDeploymentName(ns, "smoke-colocated"), metav1.GetOptions{}); apierrors.IsNotFound(err) {
			deploymentGone = true
			break
		}
	}
	if !deploymentGone {
		t.Fatal("expected haproxy Deployment to be deleted after unprovision (timed out waiting)")
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(ns).Get(context.Background(), k8sProxyPVCName(ns, "smoke-colocated"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected haproxy's PVC to be retained after unprovision: %s", err)
	}
	if err := cl.k8sProvisionProxyServiceWithClient(client, proxysql); err == nil {
		t.Fatal("expected retained-PVC collision to be refused, got nil")
	} else {
		t.Logf("PASS: retained-PVC collision refused: %s", err)
	}
}
