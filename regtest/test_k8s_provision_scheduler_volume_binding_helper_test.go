// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// These tests cover the error reporting of k8sReplacementPod, the lookup of the volume-binding
// regtest: a refusal by the API server is returned at once, with the verb and the resource; a
// ReplicaSet that is not there yet stays "not yet". They run on a fake client: they prove the
// helper's logic, not the controller (the regtest itself does that on a real cluster).

const (
	hlpNamespace = "t"
	hlpServer    = "db1"
	hlpDepUID    = types.UID("dep-new")
)

func hlpReplicaSet(depUID types.UID) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "rs-new", Namespace: hlpNamespace,
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: hlpServer, UID: depUID}},
	}}
}

func hlpPod(uid types.UID, name string, ready bool, deleting bool) *apiv1.Pod {
	p := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: hlpNamespace, UID: uid,
		Labels:          map[string]string{"tag": hlpServer},
		OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs-new"}},
	}, Status: apiv1.PodStatus{
		Phase:             apiv1.PodRunning,
		ContainerStatuses: []apiv1.ContainerStatus{{Name: hlpServer, Ready: ready}},
	}}
	if deleting {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
	return p
}

func hlpRefuse(client *fake.Clientset, verb, resource string) {
	client.PrependReactor(verb, resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: resource}, "x",
			context.DeadlineExceeded)
	})
}

func TestK8SReplacementPodMatchesOwnedReadyPod(t *testing.T) {
	client := fake.NewSimpleClientset(hlpReplicaSet(hlpDepUID), hlpPod("new", "db1-new", true, false))
	pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if err != nil || pod == nil || pod.UID != "new" {
		t.Fatalf("want the owned, ready pod, got pod=%v err=%v", pod, err)
	}
}

func TestK8SReplacementPodKeepsOwnershipAndOldPodChecks(t *testing.T) {
	// a ReplicaSet owned by another Deployment, an old pod, a terminating pod and a pod that is not ready
	other := hlpReplicaSet("dep-other")
	client := fake.NewSimpleClientset(other, hlpPod("p1", "db1-1", true, false))
	if pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{}); pod != nil || err != nil {
		t.Fatalf("a pod owned through another Deployment must not match: pod=%v err=%v", pod, err)
	}
	client = fake.NewSimpleClientset(hlpReplicaSet(hlpDepUID),
		hlpPod("old", "db1-old", true, false), hlpPod("term", "db1-term", true, true), hlpPod("nr", "db1-nr", false, false))
	if pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{"old": "db1-old"}); pod != nil || err != nil {
		t.Fatalf("old, terminating and not ready pods must not match: pod=%v err=%v", pod, err)
	}
}

func TestK8SReplacementPodReplicaSetNotFoundIsNotYet(t *testing.T) {
	// the pod exists, its ReplicaSet does not (yet): "not yet", no error, and it matches once the ReplicaSet exists
	client := fake.NewSimpleClientset(hlpPod("new", "db1-new", true, false))
	pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if pod != nil || err != nil {
		t.Fatalf("a missing ReplicaSet must be retried, not reported: pod=%v err=%v", pod, err)
	}
	if _, err := client.AppsV1().ReplicaSets(hlpNamespace).Create(context.TODO(), hlpReplicaSet(hlpDepUID), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod, err = k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if err != nil || pod == nil || pod.UID != "new" {
		t.Fatalf("want the pod once its ReplicaSet exists, got pod=%v err=%v", pod, err)
	}
}

func TestK8SReplacementPodReplicaSetGetForbiddenIsReported(t *testing.T) {
	client := fake.NewSimpleClientset(hlpReplicaSet(hlpDepUID), hlpPod("new", "db1-new", true, false))
	hlpRefuse(client, "get", "replicasets")
	pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if pod != nil || err == nil {
		t.Fatalf("a refused ReplicaSets.Get must be an error: pod=%v err=%v", pod, err)
	}
	if !k8sRefusedByAPI(err) {
		t.Fatalf("the error must still be recognisable as a refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "get replicasets.apps rs-new") {
		t.Fatalf("the error must name the verb and the resource: %v", err)
	}
}

func TestK8SReplacementPodPodsListForbiddenIsReported(t *testing.T) {
	client := fake.NewSimpleClientset()
	hlpRefuse(client, "list", "pods")
	pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if pod != nil || err == nil || !k8sRefusedByAPI(err) {
		t.Fatalf("a refused Pods.List must be reported as a refusal: pod=%v err=%v", pod, err)
	}
	if !strings.Contains(err.Error(), "list pods in namespace "+hlpNamespace) {
		t.Fatalf("the error must name the verb and the resource: %v", err)
	}
}

func TestK8SReplacementPodUnauthorizedIsAlsoARefusal(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewUnauthorized("token expired")
	})
	_, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if err == nil || !k8sRefusedByAPI(err) {
		t.Fatalf("Unauthorized must be a refusal: %v", err)
	}
}

func TestK8SReplacementPodTransientErrorIsNotARefusal(t *testing.T) {
	client := fake.NewSimpleClientset(hlpReplicaSet(hlpDepUID), hlpPod("new", "db1-new", true, false))
	client.PrependReactor("get", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("etcd restarting")
	})
	pod, err := k8sReplacementPod(client, hlpNamespace, hlpServer, hlpDepUID, map[types.UID]string{})
	if pod != nil || err == nil {
		t.Fatalf("a failed call must be returned: pod=%v err=%v", pod, err)
	}
	if k8sRefusedByAPI(err) {
		t.Fatalf("a transient failure must not be taken for a refusal: %v", err)
	}
}
