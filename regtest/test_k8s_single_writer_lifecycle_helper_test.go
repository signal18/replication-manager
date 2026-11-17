// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// These tests need no Kubernetes cluster and create no workload: they check the decisions of
// testK8SSingleWriterLifecycle (eligibility, ordering analysis, watch synchronisation, the held-PVC
// observer, failure classification, patches, restoration) against plain values and a fake clientset, and
// its registration.

const swNS = "ns"

func TestK8SSWTopologyEligibility(t *testing.T) {
	for _, c := range []struct {
		name         string
		orchestrator string
		servers      int
		skip         bool
	}{
		{"kubernetes, one server", config.ConstOrchestratorKubernetes, 1, false},
		{"opensvc", config.ConstOrchestratorOpenSVC, 1, true},
		{"on premise", config.ConstOrchestratorOnPremise, 1, true},
		{"kubernetes, two servers", config.ConstOrchestratorKubernetes, 2, true},
		{"kubernetes, no server", config.ConstOrchestratorKubernetes, 0, true},
	} {
		reason := k8sSWTopologyEligibility(c.orchestrator, c.servers)
		if (reason != "") != c.skip {
			t.Errorf("%s: reason %q, skip wanted %t", c.name, reason, c.skip)
		}
	}
}

// An absent or wrong label is never eligible: the test would stop and reprovision a database.
func TestK8SSWNamespaceEligibility(t *testing.T) {
	for _, c := range []struct {
		name   string
		labels map[string]string
		ok     bool
	}{
		{"nil labels", nil, false},
		{"no label", map[string]string{"other": "true"}, false},
		{"false", map[string]string{k8sSWDisposableLabel: "false"}, false},
		{"empty", map[string]string{k8sSWDisposableLabel: ""}, false},
		{"upper case", map[string]string{k8sSWDisposableLabel: "True"}, false},
		{"explicit true", map[string]string{k8sSWDisposableLabel: "true"}, true},
	} {
		reason := k8sSWNamespaceEligibility(swNS, c.labels)
		if c.ok && reason != "" {
			t.Errorf("%s: refused: %s", c.name, reason)
		}
		if !c.ok && !strings.Contains(reason, k8sSWDisposableLabel+"=true") {
			t.Errorf("%s: want a refusal that names the label, got %q", c.name, reason)
		}
	}
}

// A cluster that is not eligible is SKIP before anything is read or changed: the test returns with the
// reason set (the runner records SKIP, never PASS) and no Kubernetes call is made.
func TestK8SSingleWriterLifecycleSkipsBeforeAnyChange(t *testing.T) {
	for _, c := range []struct {
		name         string
		orchestrator string
		servers      int
	}{
		{"opensvc", config.ConstOrchestratorOpenSVC, 1},
		{"two servers", config.ConstOrchestratorKubernetes, 2},
	} {
		cl := &cluster.Cluster{Conf: new(config.Config)}
		cl.Conf.ProvOrchestrator = c.orchestrator
		cl.Servers = make([]*cluster.ServerMonitor, c.servers)
		test := &cluster.Test{}
		new(RegTest).TestK8SSingleWriterLifecycle(cl, "", test)
		if test.SkipReason == "" {
			t.Errorf("%s: no skip reason: the result would be reported as a result, not SKIP", c.name)
		}
	}
}

func TestK8SSWPermissions(t *testing.T) {
	allow := func(deny string, fail bool) *fake.Clientset {
		cs := fake.NewSimpleClientset()
		cs.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
			if fail {
				return true, nil, errors.New("connection refused")
			}
			attrs := action.(ktesting.CreateAction).GetObject().(*authv1.SelfSubjectAccessReview).Spec.ResourceAttributes
			ok := attrs.Verb+" "+attrs.Resource != deny
			return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: ok}}, nil
		})
		return cs
	}
	missing, err := k8sSWMissingPermissions(context.Background(), allow("", false), swNS)
	if err != nil || len(missing) != 0 {
		t.Fatalf("all allowed: missing %v, error %v", missing, err)
	}
	missing, err = k8sSWMissingPermissions(context.Background(), allow("create pods", false), swNS)
	if err != nil || len(missing) != 1 || missing[0] != "create pods" {
		t.Fatalf("create pods denied: missing %v, error %v", missing, err)
	}
	// a review that cannot be made is an error (HARNESS), never an empty list that looks like "allowed"
	if missing, err = k8sSWMissingPermissions(context.Background(), allow("", true), swNS); err == nil || missing != nil {
		t.Fatalf("a failed review must be an error, got missing %v, error %v", missing, err)
	}
}

func swPod(name, uid, claim string, labels map[string]string) apiv1.Pod {
	return apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid), Labels: labels},
		Spec:       apiv1.PodSpec{Volumes: []apiv1.Volume{{Name: "d", VolumeSource: apiv1.VolumeSource{PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}}},
	}
}

func swDataDeployment() *appsv1.Deployment {
	dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)
	dep.Spec.Template.Spec.Volumes = []apiv1.Volume{
		{Name: "run", VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}}},
		{Name: "other", VolumeSource: apiv1.VolumeSource{PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: "ns-other-claim"}}},
		{Name: "data", VolumeSource: apiv1.VolumeSource{PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: "ns-db1-claim"}}},
	}
	dep.Spec.Template.Spec.Containers[0].VolumeMounts = []apiv1.VolumeMount{
		{Name: "run", MountPath: "/run/mysqld"},
		{Name: "other", MountPath: "/backup"},
		{Name: "data", MountPath: "/var/lib/mysql"},
		{Name: "data", MountPath: "/etc/mysql/conf.d", SubPath: "conf.d"},
	}
	return dep
}

// The data claim is the one mounted at /var/lib/mysql by the database container, not the first PVC of the
// template; an unidentifiable or ambiguous claim is a precondition problem.
func TestK8SSWDataClaim(t *testing.T) {
	if claim, problem := k8sSWDataClaim(swDataDeployment(), "db1"); claim != "ns-db1-claim" || problem != "" {
		t.Fatalf("claim %q problem %q: the data claim is not the first PVC of the template", claim, problem)
	}
	for name, mutate := range map[string]func(d *appsv1.Deployment){
		"no mount of the data directory": func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].VolumeMounts = d.Spec.Template.Spec.Containers[0].VolumeMounts[:2]
		},
		"two mounts of the data directory": func(d *appsv1.Deployment) {
			c := &d.Spec.Template.Spec.Containers[0]
			c.VolumeMounts = append(c.VolumeMounts, apiv1.VolumeMount{Name: "other", MountPath: "/var/lib/mysql"})
		},
		"the volume is not a PVC": func(d *appsv1.Deployment) { d.Spec.Template.Spec.Volumes[2].PersistentVolumeClaim = nil },
		"the volume does not exist": func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].VolumeMounts[2].Name = "missing"
		},
		"no database container": func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers[0].Name = "x" },
	} {
		d := swDataDeployment()
		mutate(d)
		if claim, problem := k8sSWDataClaim(d, "db1"); problem == "" || claim != "" {
			t.Errorf("%s: claim %q problem %q", name, claim, problem)
		}
	}
	// a mount of the data directory by the sidecar only does not count
	d := swDataDeployment()
	d.Spec.Template.Spec.Containers[0].VolumeMounts = nil
	d.Spec.Template.Spec.Containers[1].VolumeMounts = []apiv1.VolumeMount{{Name: "data", MountPath: "/var/lib/mysql"}}
	if _, problem := k8sSWDataClaim(d, "db1"); problem == "" {
		t.Error("only the database container's mount identifies the data claim")
	}
}

func TestK8SSWForeignConsumers(t *testing.T) {
	pods := []apiv1.Pod{
		swPod("db1-x", "1", "ns-db1-claim", map[string]string{"tag": "db1"}),
		swPod("stranger", "2", "ns-db1-claim", map[string]string{"app": "x"}),
		swPod("other-db", "3", "ns-db2-claim", map[string]string{"tag": "db2"}),
		// the label of the database does not make a Pod the database: only its identity does
		swPod("db1-impostor", "4", "ns-db1-claim", map[string]string{"tag": "db1"}),
	}
	foreign := k8sSWForeignConsumers(pods, "ns-db1-claim", "1")
	if !slices.Equal(foreign, []string{"stranger", "db1-impostor"}) {
		t.Fatalf("foreign consumers %v", foreign)
	}
	if len(k8sSWForeignConsumers(pods[:1], "ns-db1-claim", "1")) != 0 {
		t.Fatal("the baseline Pod is not foreign")
	}
}

func controlled(kind, name, uid string) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, UID: types.UID(uid), Controller: &yes}}
}

// a Pod whose database container mounts the data directory from the given claim
func withDataClaim(p apiv1.Pod, claim string) apiv1.Pod {
	p.Spec.Containers = []apiv1.Container{{Name: "db1", VolumeMounts: []apiv1.VolumeMount{{Name: "data", MountPath: "/var/lib/mysql"}}}}
	p.Spec.Volumes = []apiv1.Volume{{Name: "data", VolumeSource: apiv1.VolumeSource{PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}}
	return p
}

// Exactly one database Pod, running and ready, owned by the Deployment through its ReplicaSet (owner kind,
// name and UID, the ReplicaSet's own UID included) and mounting the data claim the Deployment names.
func TestK8SSWBaselinePod(t *testing.T) {
	dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "db1-abc", UID: "rs-uid", OwnerReferences: controlled("Deployment", "db1", "dep-uid")}}
	getRS := func(name string) (*appsv1.ReplicaSet, error) {
		if name != "db1-abc" {
			return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "replicasets"}, name)
		}
		return rs.DeepCopy(), nil
	}
	good := func() apiv1.Pod {
		p := withDataClaim(readyPod("p1"), "ns-db1-claim")
		p.Status.Phase = apiv1.PodRunning
		p.OwnerReferences = controlled("ReplicaSet", "db1-abc", "rs-uid")
		return p
	}
	if p, problem := k8sSWBaselinePod([]apiv1.Pod{good()}, "db1", "ns-db1-claim", dep, getRS); problem != "" || p == nil || p.UID != "p1" {
		t.Fatalf("a good baseline: %v %q", p, problem)
	}
	now := metav1.Now()
	notController := []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "db1-abc", UID: "rs-uid"}}
	one := func(f func(p *apiv1.Pod)) func() []apiv1.Pod {
		return func() []apiv1.Pod { p := good(); f(&p); return []apiv1.Pod{p} }
	}
	for name, c := range map[string]struct {
		pods func() []apiv1.Pod
		rs   func(r *appsv1.ReplicaSet)
	}{
		"no Pod":   {func() []apiv1.Pod { return nil }, nil},
		"two Pods": {func() []apiv1.Pod { a, b := good(), good(); b.UID, b.Name = "p2", "db1-p2"; return []apiv1.Pod{a, b} }, nil},
		"a failed Pod too": {func() []apiv1.Pod {
			a, b := good(), good()
			b.UID = "p2"
			b.Status.Phase = apiv1.PodFailed
			return []apiv1.Pod{a, b}
		}, nil},
		"terminating":                     {one(func(p *apiv1.Pod) { p.DeletionTimestamp = &now }), nil},
		"not running":                     {one(func(p *apiv1.Pod) { p.Status.Phase = apiv1.PodPending }), nil},
		"not ready":                       {one(func(p *apiv1.Pod) { p.Status.ContainerStatuses[0].Ready = false }), nil},
		"no owner":                        {one(func(p *apiv1.Pod) { p.OwnerReferences = nil }), nil},
		"owner is not a controller":       {one(func(p *apiv1.Pod) { p.OwnerReferences = notController }), nil},
		"owner not a ReplicaSet":          {one(func(p *apiv1.Pod) { p.OwnerReferences = controlled("StatefulSet", "db1-abc", "rs-uid") }), nil},
		"owner name unknown":              {one(func(p *apiv1.Pod) { p.OwnerReferences = controlled("ReplicaSet", "other", "rs-uid") }), nil},
		"owner UID differs":               {one(func(p *apiv1.Pod) { p.OwnerReferences = controlled("ReplicaSet", "db1-abc", "stale-uid") }), nil},
		"ReplicaSet of another name":      {one(func(p *apiv1.Pod) {}), func(r *appsv1.ReplicaSet) { r.OwnerReferences = controlled("Deployment", "db2", "dep-uid") }},
		"Deployment UID differs":          {one(func(p *apiv1.Pod) {}), func(r *appsv1.ReplicaSet) { r.OwnerReferences = controlled("Deployment", "db1", "old-dep") }},
		"ReplicaSet owner not Deployment": {one(func(p *apiv1.Pod) {}), func(r *appsv1.ReplicaSet) { r.OwnerReferences = controlled("Job", "db1", "dep-uid") }},
		"ReplicaSet without owner":        {one(func(p *apiv1.Pod) {}), func(r *appsv1.ReplicaSet) { r.OwnerReferences = nil }},
		"mounts another claim":            {one(func(p *apiv1.Pod) { *p = withDataClaim(*p, "ns-other-claim") }), nil},
		"no data mount":                   {one(func(p *apiv1.Pod) { p.Spec.Containers[0].VolumeMounts = nil }), nil},
		"two data mounts": {one(func(p *apiv1.Pod) {
			p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, apiv1.VolumeMount{Name: "data", MountPath: "/var/lib/mysql", SubPath: "x"})
		}), nil},
	} {
		saved := rs
		if c.rs != nil {
			rs = rs.DeepCopy()
			c.rs(rs)
		}
		if p, problem := k8sSWBaselinePod(c.pods(), "db1", "ns-db1-claim", dep, getRS); problem == "" || p != nil {
			t.Errorf("%s: accepted (%v)", name, p)
		}
		rs = saved
	}
}

// The Pod's own data claim is resolved like the Deployment's.
func TestK8SSWPodDataClaim(t *testing.T) {
	p := withDataClaim(readyPod("p1"), "ns-db1-claim")
	if c, problem := k8sSWPodDataClaim(&p, "db1"); c != "ns-db1-claim" || problem != "" {
		t.Fatalf("%q %q", c, problem)
	}
	if _, problem := k8sSWPodDataClaim(&p, "other"); problem == "" {
		t.Fatal("no container of that name")
	}
}

// S3 refuses the next mutation unless the Deployment is explicitly stopped, has no database Pod and has
// the expected strategy.
func TestK8SSWStoppedProblem(t *testing.T) {
	stopped := func(t appsv1.DeploymentStrategyType) *appsv1.Deployment {
		return swStopped(swDeployment("db1", strategyOf(t), nil))
	}
	rec, roll := appsv1.RecreateDeploymentStrategyType, appsv1.RollingUpdateDeploymentStrategyType
	if p := k8sSWStoppedProblem(stopped(rec), nil, rec, false); p != "" {
		t.Fatalf("stopped under Recreate: %s", p)
	}
	if p := k8sSWStoppedProblem(stopped(roll), nil, roll, true); p != "" {
		t.Fatalf("stopped under the injected legacy strategy: %s", p)
	}
	unset := stopped(rec)
	unset.Spec.Replicas = nil
	running := swDeployment("db1", strategyOf(rec), nil)
	terminating := readyPod("p1")
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	for name, c := range map[string]struct {
		dep  *appsv1.Deployment
		pods []apiv1.Pod
		want appsv1.DeploymentStrategyType
		ru   bool
	}{
		"replicas not set":            {unset, nil, rec, false},
		"one replica":                 {running, nil, rec, false},
		"a database Pod remains":      {stopped(rec), []apiv1.Pod{readyPod("p1")}, rec, false},
		"a terminating Pod remains":   {stopped(rec), []apiv1.Pod{terminating}, rec, false},
		"strategy type differs":       {stopped(roll), nil, rec, false},
		"legacy not injected":         {stopped(rec), nil, roll, true},
		"rollingUpdate block differs": {stopped(roll), nil, roll, false},
	} {
		if k8sSWStoppedProblem(c.dep, c.pods, c.want, c.ru) == "" {
			t.Errorf("%s: accepted", name)
		}
	}
}

// --- ordering analysis ---------------------------------------------------------------------------

var swBase = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func swT(sec int) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return swBase.Add(time.Duration(sec) * time.Second)
}

// ev builds an event of the database container: state is "run", "term" (terminated) or "" (no state).
func ev(typ watch.EventType, uid, node, state string, started, finished int, deleting bool) k8sSWPodEvent {
	e := k8sSWPodEvent{Type: typ, UID: types.UID(uid), Name: "db1-" + uid, Node: node, Deleting: deleting, DBStarted: swT(started), DBFinished: swT(finished)}
	switch state {
	case "run":
		e.DBRunning = true
	case "term":
		e.DBTerminated = true
	}
	return e
}

func oldRunning(uid, node string) []k8sSWOldPod {
	return []k8sSWOldPod{{UID: types.UID(uid), Name: "db1-" + uid, Node: node, DBRunning: true}}
}

// the events of a clean Recreate: the old Pod terminates and goes, then the new one is created and runs
func swSerialized() []k8sSWPodEvent {
	return []k8sSWPodEvent{
		ev(watch.Modified, "old", "n1", "run", 1, 0, true),
		ev(watch.Modified, "old", "n1", "term", 1, 20, true),
		ev(watch.Deleted, "old", "n1", "term", 1, 20, true),
		ev(watch.Added, "new", "n1", "", 0, 0, false),
		ev(watch.Modified, "new", "n1", "run", 25, 0, false),
	}
}

func TestK8SSWAnalyzeWindowSerialized(t *testing.T) {
	v := k8sSWAnalyzeWindow(swSerialized(), 0, oldRunning("old", "n1"), 1)
	if !v.ok() || v.NewPods != 1 || len(v.NewUIDs) != 1 || v.NewUIDs[0] != "new" {
		t.Fatalf("serialized: %+v", v)
	}
	if !strings.Contains(v.Detail, "no overlap was observed") || !strings.Contains(v.Detail, "not excluded") {
		t.Errorf("the verdict states what was observed and that a process-level overlap is not excluded: %q", v.Detail)
	}
}

// A replacement that was required but not observed is INCONCLUSIVE, never a pass: a watch that lags or
// misses events must not turn into a PASS.
func TestK8SSWAnalyzeWindowZeroNewPodWhereOneIsRequired(t *testing.T) {
	// nothing at all happened in the window
	if v := k8sSWAnalyzeWindow(nil, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive {
		t.Fatalf("no events, one replacement required: %+v", v)
	}
	// the old Pod's end was seen but the new Pod was not
	events := swSerialized()[:3]
	if v := k8sSWAnalyzeWindow(events, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive {
		t.Fatalf("the replacement was not seen: %+v", v)
	}
	// events from before the mark do not count
	if v := k8sSWAnalyzeWindow(swSerialized(), 5, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive {
		t.Fatalf("all events precede the window: %+v", v)
	}
}

// Zero replacements are a legitimate expectation (a strategy-only patch), and distinct from a missing
// observation: it needs the old Pods unchanged, not an empty window.
func TestK8SSWAnalyzeWindowExpectingNoReplacement(t *testing.T) {
	old := oldRunning("old", "n1")
	v := k8sSWAnalyzeWindow(nil, 0, old, 0)
	if !v.ok() || !strings.Contains(v.Detail, "unchanged") {
		t.Fatalf("nothing changed, nothing expected: %+v", v)
	}
	if v := k8sSWAnalyzeWindow(swSerialized(), 0, old, 0); v.Class != k8sSWProduct {
		t.Fatalf("a Pod was created and replaced where none was expected: %+v", v)
	}
	onlyNew := []k8sSWPodEvent{ev(watch.Added, "new", "n1", "", 0, 0, false)}
	if v := k8sSWAnalyzeWindow(onlyNew, 0, old, 0); v.Class != k8sSWProduct || !strings.Contains(v.Detail, "new Pod") {
		t.Fatalf("a new Pod appeared: %+v", v)
	}
	terminating := []k8sSWPodEvent{ev(watch.Modified, "old", "n1", "run", 1, 0, true)}
	if v := k8sSWAnalyzeWindow(terminating, 0, old, 0); v.Class != k8sSWProduct {
		t.Fatalf("the running Pod started terminating: %+v", v)
	}
	// the same empty window where one replacement is required is a missing observation, not a pass
	if v := k8sSWAnalyzeWindow(nil, 0, old, 1); v.ok() {
		t.Fatalf("a missing observation must differ from an expected zero: %+v", v)
	}
}

// An old Pod that the API showed running is never ignored because the watch missed its events.
func TestK8SSWAnalyzeWindowOldPodEvidence(t *testing.T) {
	newOnly := []k8sSWPodEvent{
		ev(watch.Added, "new", "n1", "", 0, 0, false),
		ev(watch.Modified, "new", "n1", "run", 25, 0, false),
	}
	if v := k8sSWAnalyzeWindow(newOnly, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive || !strings.Contains(v.Detail, "not observed") {
		t.Fatalf("the end of the old Pod was never seen: %+v", v)
	}
	// the end was seen but its finish time was not reported
	noFinish := swSerialized()
	noFinish[1].DBFinished, noFinish[2].DBFinished = time.Time{}, time.Time{}
	if v := k8sSWAnalyzeWindow(noFinish, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive || !strings.Contains(v.Detail, "finish time") {
		t.Fatalf("a missing finish time must be INCONCLUSIVE: %+v", v)
	}
	// the new database container has no start time
	noStart := swSerialized()
	noStart[4].DBStarted = time.Time{}
	if v := k8sSWAnalyzeWindow(noStart, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive {
		t.Fatalf("a missing start time must be INCONCLUSIVE: %+v", v)
	}
	// an old Pod that was not running (Pending, never ran) does not constrain the replacement
	notRunning := []k8sSWOldPod{{UID: "old", Name: "db1-old", Node: "n1", DBRunning: false}}
	if v := k8sSWAnalyzeWindow(newOnly, 0, notRunning, 1); !v.ok() {
		t.Fatalf("an old Pod that never ran must not block: %+v", v)
	}
	// a Pod the snapshot did not know and the watch never saw created
	stray := []k8sSWPodEvent{ev(watch.Modified, "ghost", "n1", "run", 3, 0, false)}
	if v := k8sSWAnalyzeWindow(stray, 0, nil, 1); v.Class != k8sSWInconclusive {
		t.Fatalf("an unknown identity: %+v", v)
	}
}

func TestK8SSWAnalyzeWindowOverlap(t *testing.T) {
	// the new Pod appears before the old database container is seen terminated
	early := []k8sSWPodEvent{
		ev(watch.Modified, "old", "n1", "run", 1, 0, true),
		ev(watch.Added, "new", "n1", "", 0, 0, false),
		ev(watch.Modified, "old", "n1", "term", 1, 12, true),
		ev(watch.Deleted, "old", "n1", "term", 1, 12, true),
		ev(watch.Modified, "new", "n1", "run", 14, 0, false),
	}
	if v := k8sSWAnalyzeWindow(early, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWProduct {
		t.Fatalf("a Pod created before the old process ended: %+v", v)
	}
	// two database containers reported running at the same time
	both := []k8sSWPodEvent{
		ev(watch.Modified, "old", "n1", "run", 1, 0, true),
		ev(watch.Added, "new", "n1", "", 0, 0, false),
		ev(watch.Modified, "new", "n1", "run", 5, 0, false),
	}
	if v := k8sSWAnalyzeWindow(both, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWProduct || !strings.Contains(v.Detail, "same time") {
		t.Fatalf("two running containers: %+v", v)
	}
	// two replacements where one is expected
	two := append(swSerialized(), ev(watch.Added, "third", "n1", "", 0, 0, false), ev(watch.Modified, "third", "n1", "run", 30, 0, false))
	if v := k8sSWAnalyzeWindow(two, 0, oldRunning("old", "n1"), 1); v.Class != k8sSWProduct {
		t.Fatalf("two new Pods: %+v", v)
	}
}

// The timestamps are compared on one node; across nodes a contradiction is INCONCLUSIVE.
func TestK8SSWAnalyzeWindowTimestamps(t *testing.T) {
	contradict := func(oldNode, newNode string) []k8sSWPodEvent {
		return []k8sSWPodEvent{
			ev(watch.Modified, "old", oldNode, "term", 1, 30, true),
			ev(watch.Deleted, "old", oldNode, "term", 1, 30, true),
			ev(watch.Added, "new", newNode, "", 0, 0, false),
			ev(watch.Modified, "new", newNode, "run", 20, 0, false),
		}
	}
	if v := k8sSWAnalyzeWindow(contradict("n1", "n1"), 0, oldRunning("old", "n1"), 1); v.Class != k8sSWProduct {
		t.Fatalf("same node, the new container started before the old one finished: %+v", v)
	}
	if v := k8sSWAnalyzeWindow(contradict("n1", "n2"), 0, oldRunning("old", "n1"), 1); v.Class != k8sSWInconclusive || !strings.Contains(v.Detail, "different nodes") {
		t.Fatalf("different nodes: clocks may differ: %+v", v)
	}
	// started in the same second the old one finished: the resolution does not allow to call it an overlap
	same := swSerialized()
	same[4].DBStarted = swT(20)
	if v := k8sSWAnalyzeWindow(same, 0, oldRunning("old", "n1"), 1); !v.ok() {
		t.Fatalf("the same second: %+v", v)
	}
}

// --- watch synchronisation -------------------------------------------------------------------------

// swFactsPod is a listed Pod whose database container is in the given state.
func swFactsPod(name, uid, state string, finished int, deleting bool) apiv1.Pod {
	p := apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid), ResourceVersion: "999"}}
	st := apiv1.ContainerStatus{Name: "db1"}
	switch state {
	case "run":
		st.State.Running = &apiv1.ContainerStateRunning{StartedAt: metav1.NewTime(swT(1))}
	case "term":
		st.State.Terminated = &apiv1.ContainerStateTerminated{StartedAt: metav1.NewTime(swT(1)), FinishedAt: metav1.NewTime(swT(finished))}
	}
	p.Status.ContainerStatuses = []apiv1.ContainerStatus{st}
	if deleting {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
	return p
}

// The watch is synchronised with a List by the facts the analysis reads, not by resourceVersions (opaque,
// and changed by any unrelated update of a Pod).
func TestK8SSWSynced(t *testing.T) {
	run := swFactsPod("a", "a", "run", 0, false)
	evA := k8sSWEventOf(watch.Modified, &run, "db1")
	evB := k8sSWEventOf(watch.Added, ptr(swFactsPod("b", "b", "run", 0, false)), "db1")
	evC := k8sSWPodEvent{Type: watch.Deleted, UID: "c", Name: "c"}
	events := []k8sSWPodEvent{{Type: k8sSWSync, UID: "a", Name: "a"}, evA, evB, evC}

	// a different resourceVersion (an annotation changed) is still the same state
	listedA, listedB := swFactsPod("a", "a", "run", 0, false), swFactsPod("b", "b", "run", 0, false)
	listedA.ResourceVersion = "1"
	if r := k8sSWSynced(events, []apiv1.Pod{listedA, listedB}, "db1"); r != "" {
		t.Fatalf("converged although the versions differ: %s", r)
	}
	for name, listed := range map[string][]apiv1.Pod{
		"the List shows the Pod terminated":    {swFactsPod("a", "a", "term", 20, true), listedB},
		"the List shows the Pod terminating":   {swFactsPod("a", "a", "run", 0, true), listedB},
		"a Pod the watch never saw":            {listedA, listedB, swFactsPod("z", "z", "run", 0, false)},
		"alive in the watch, absent from List": {listedA},
		"listed but deleted in the watch":      {listedA, listedB, swFactsPod("c", "c", "run", 0, false)},
		"another finish time":                  {swFactsPod("a", "a", "term", 21, false), listedB},
	} {
		if k8sSWSynced(events, listed, "db1") == "" {
			t.Errorf("%s: reported as converged", name)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func swMust(p []apiv1.Pod, err error) []apiv1.Pod {
	if err != nil {
		panic(err)
	}
	return p
}

func TestK8SSWWatcherSynchronisesWithAList(t *testing.T) {
	cs := fake.NewSimpleClientset()
	w, err := startK8sSWWatcher(cs, swNS, "db1")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	list := func() ([]apiv1.Pod, error) {
		l, err := cs.CoreV1().Pods(swNS).List(context.TODO(), metav1.ListOptions{LabelSelector: "tag=db1"})
		return l.Items, err
	}
	mark := w.mark()
	pod := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db1-a", Namespace: swNS, UID: "u1", Labels: map[string]string{"tag": "db1"}}}
	if _, err := cs.CoreV1().Pods(swNS).Create(context.TODO(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := w.waitSynced(list, 3*time.Second); err != nil {
		t.Fatalf("the watch must catch up with the List: %v", err)
	}
	events, werr := w.snapshot()
	if werr != nil || len(events)-mark != 1 || events[mark].Type != watch.Added || events[mark].UID != "u1" {
		t.Fatalf("events %+v, error %v", events, werr)
	}
	// a List that holds a Pod the watch never delivers is not synchronised: INCONCLUSIVE, not a pass
	extra := func() ([]apiv1.Pod, error) {
		return append(swMust(list()), swFactsPod("ghost", "g1", "", 0, false)), nil
	}
	if err := w.waitSynced(extra, 400*time.Millisecond); err == nil || !strings.Contains(err.Error(), "did not catch up") {
		t.Fatalf("an undelivered Pod must time out, got %v", err)
	}
}

// --- the held-PVC observer ---------------------------------------------------------------------------

func TestK8SSWWindowJudge(t *testing.T) {
	held := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "h", UID: "hu"}}
	terminating := held.DeepCopy()
	now := metav1.Now()
	terminating.DeletionTimestamp = &now

	w := &k8sSWWindow{}
	w.judge(false, 0, 0, terminating, "hu") // before Start was invoked: not a proof of anything
	w.judge(true, 0, 0, terminating, "hu")
	w.judge(true, 0, 0, held, "hu")
	w.judge(true, 0, 0, nil, "hu") // gone: nothing to judge
	polls, present, term, violation := w.result()
	if polls != 4 || present != 2 || term != 1 || violation != "" {
		t.Fatalf("polls %d present %d terminating %d violation %q", polls, present, term, violation)
	}
	// a scale-up while the held Pod is still there
	w = &k8sSWWindow{}
	w.judge(true, 1, 0, terminating, "hu")
	if _, _, _, v := w.result(); !strings.Contains(v, "1 replica") {
		t.Fatalf("a scale-up while held must be a violation, got %q", v)
	}
	// a second database Pod while the held Pod is still there
	w = &k8sSWWindow{}
	w.judge(true, 0, 1, held, "hu")
	if _, _, _, v := w.result(); v == "" {
		t.Fatal("another database Pod while held must be a violation")
	}
	// a scale-up after the held Pod is gone is the Start succeeding, not a violation
	w = &k8sSWWindow{}
	w.judge(true, 1, 1, nil, "hu")
	if _, _, _, v := w.result(); v != "" {
		t.Fatalf("after the held Pod is gone: %q", v)
	}
	// a Pod of the same name but another identity is not the held Pod
	w = &k8sSWWindow{}
	other := held.DeepCopy()
	other.UID = "other"
	w.judge(true, 1, 0, other, "hu")
	if _, _, _, v := w.result(); v != "" {
		t.Fatalf("another identity: %q", v)
	}
}

func swDBPod(name, uid string) *apiv1.Pod {
	return &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: swNS, UID: types.UID(uid), Labels: map[string]string{"tag": "db1"}}}
}

func TestK8SSWPollOnce(t *testing.T) {
	zero, one := int32(0), int32(1)
	dep := func(r *int32) *appsv1.Deployment {
		d := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)
		d.Spec.Replicas = r
		return d
	}
	holder := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "holder", Namespace: swNS, UID: "hu"}}
	now := metav1.Now()
	holder.DeletionTimestamp = &now
	holder.Finalizers = []string{"test/keep"}

	// zero replicas, no database Pod, holder Terminating: the window is observed
	w := &k8sSWWindow{}
	if err := k8sSWPollOnce(fake.NewSimpleClientset(dep(&zero), holder.DeepCopy()), swNS, "db1", "holder", "hu", true, w); err != nil {
		t.Fatal(err)
	}
	if _, _, term, v := w.result(); term != 1 || v != "" {
		t.Fatalf("terminating %d violation %q", term, v)
	}
	// a scale-up while the holder is there
	w = &k8sSWWindow{}
	_ = k8sSWPollOnce(fake.NewSimpleClientset(dep(&one), holder.DeepCopy()), swNS, "db1", "holder", "hu", true, w)
	if _, _, _, v := w.result(); v == "" {
		t.Fatal("scale-up while the holder is there must be a violation")
	}
	// a database Pod while the holder is there
	w = &k8sSWWindow{}
	_ = k8sSWPollOnce(fake.NewSimpleClientset(dep(&zero), holder.DeepCopy(), swDBPod("db1-x", "x")), swNS, "db1", "holder", "hu", true, w)
	if _, _, _, v := w.result(); v == "" {
		t.Fatal("a database Pod while the holder is there must be a violation")
	}
	// the held Pod is itself a database Pod (S5, S6): it is not "another" one
	w = &k8sSWWindow{}
	_ = k8sSWPollOnce(fake.NewSimpleClientset(dep(&zero), swDBPod("db1-old", "ou")), swNS, "db1", "db1-old", "ou", true, w)
	if _, present, _, v := w.result(); present != 1 || v != "" {
		t.Fatalf("held database Pod: present %d violation %q", present, v)
	}
	// a read that fails is an error, not a poll
	w = &k8sSWWindow{}
	if err := k8sSWPollOnce(fake.NewSimpleClientset(), swNS, "db1", "holder", "hu", true, w); err == nil {
		t.Fatal("a missing Deployment must be an error")
	}
	if polls, _, _, _ := w.result(); polls != 0 {
		t.Fatalf("a failed poll counted: %d", polls)
	}
}

// A failed poll is recorded, never discarded, and a stretch without a successful poll is insufficient
// evidence: INCONCLUSIVE, however clean the polls that did succeed.
func TestK8SSWObserverCoverage(t *testing.T) {
	clock := swBase
	w := &k8sSWWindow{now: func() time.Time { return clock }}
	held := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "h", UID: "hu"}}
	if w.coverage(clock) == "" {
		t.Fatal("a Start that was never marked has no coverage")
	}
	w.markInvoked()
	for i := 0; i < 4; i++ { // successful polls every 2 s
		clock = clock.Add(2 * time.Second)
		w.judge(true, 0, 0, held, "hu")
	}
	if c := w.coverage(clock.Add(time.Second)); c != "" {
		t.Fatalf("regular polls: %s", c)
	}
	// the polls stop succeeding for 20 s: the tail of the interval is not observed
	if c := w.coverage(clock.Add(20 * time.Second)); c == "" || !strings.Contains(c, "without a successful observation") {
		t.Fatalf("a long stretch without a poll: %q", c)
	}
	// a gap in the middle is counted too, even when polls resume
	clock = clock.Add(25 * time.Second)
	w.judge(true, 0, 0, held, "hu")
	clock = clock.Add(time.Second)
	w.judge(true, 0, 0, held, "hu")
	if c := w.coverage(clock); c == "" {
		t.Fatal("the middle gap must be remembered")
	}
	// the first poll after Start counts from the call: a delayed first observation is a gap
	late := &k8sSWWindow{now: func() time.Time { return clock }}
	late.markInvoked()
	clock = clock.Add(15 * time.Second)
	late.judge(true, 0, 0, held, "hu")
	if late.coverage(clock) == "" {
		t.Fatal("a first successful poll 15 s after Start is a delayed observation")
	}
}

func TestK8SSWPollErrorsAreRecorded(t *testing.T) {
	cs := fake.NewSimpleClientset(swDataDeploymentZero())
	cs.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})
	w := &k8sSWWindow{}
	if err := k8sSWPollOnce(cs, swNS, "db1", "holder", "hu", true, w); err == nil {
		t.Fatal("the failed read must be returned")
	}
	if polls, _, _, _ := w.result(); polls != 0 {
		t.Fatalf("a failed poll counted as a poll: %d", polls)
	}
	if w.fails != 1 || !strings.Contains(w.lastErr, "apiserver unavailable") {
		t.Fatalf("the failure was discarded: fails %d, last %q", w.fails, w.lastErr)
	}
	w.markInvoked()
	if c := w.coverage(time.Now().Add(30 * time.Second)); !strings.Contains(c, "1 poll(s) failed") || !strings.Contains(c, "apiserver unavailable") {
		t.Fatalf("the coverage must report the failures: %q", c)
	}
}

func swDataDeploymentZero() *appsv1.Deployment {
	zero := int32(0)
	d := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)
	d.Spec.Replicas = &zero
	return d
}

// Start is not called without one successful observation of the starting state. The initial observation
// may be delayed (retried within a bound); a state that never becomes observable ends in an error.
func TestK8SSWAwaitInitialObservation(t *testing.T) {
	holder := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "holder", Namespace: swNS, UID: "hu"}}
	fails := 0
	delayed := fake.NewSimpleClientset(swDataDeploymentZero(), holder.DeepCopy())
	delayed.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		if fails < 2 {
			fails++
			return true, nil, errors.New("timeout")
		}
		return false, nil, nil
	})
	if err := k8sSWAwaitInitial(delayed, swNS, "db1", "holder", "hu", 5*time.Second, 10*time.Millisecond); err != nil || fails != 2 {
		t.Fatalf("a delayed initial observation must succeed after retries: %v (failures %d)", err, fails)
	}
	never := fake.NewSimpleClientset(swDataDeploymentZero(), holder.DeepCopy())
	never.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("timeout")
	})
	if err := k8sSWAwaitInitial(never, swNS, "db1", "holder", "hu", 100*time.Millisecond, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "no successful initial observation") {
		t.Fatalf("an observation that never succeeds: %v", err)
	}
	// the held Pod is not there: the scenario cannot start
	if err := k8sSWAwaitInitial(fake.NewSimpleClientset(swDataDeploymentZero()), swNS, "db1", "holder", "hu", 100*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("no holder, no initial observation")
	}
	// a violation already in the starting state (a database is running)
	one := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)
	if err := k8sSWAwaitInitial(fake.NewSimpleClientset(one, holder.DeepCopy()), swNS, "db1", "holder", "hu", time.Second, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "violation") {
		t.Fatalf("a running database in the starting state: %v", err)
	}
}

// --- the decision on a Start made while a Pod holds the PVC ------------------------------------------

func TestK8SSWJudgeHeldStart(t *testing.T) {
	inconclusive := k8sSWVerdictOf(k8sSWInconclusive, "refused")
	for _, c := range []struct {
		name        string
		start       *k8sSWVerdict
		violation   string
		coverage    string
		heldThere   bool
		terminating int
		want        k8sSWClass
	}{
		{"guard held, window exercised", nil, "", "", false, 3, ""},
		{"scale-up seen while held", nil, "scaled", "", false, 3, k8sSWProduct},
		{"scale-up seen, coverage also poor", nil, "scaled", "gap", false, 3, k8sSWProduct},
		{"Start succeeded with the held Pod still there", nil, "", "", true, 3, k8sSWProduct},
		{"window not exercised", nil, "", "", false, 0, k8sSWInconclusive},
		{"insufficient coverage is never a pass", nil, "", "a gap of 30s", false, 3, k8sSWInconclusive},
		{"Start refused, nothing violated", &inconclusive, "", "", true, 2, k8sSWInconclusive},
		{"Start refused but scale-up seen", &inconclusive, "scaled", "", true, 2, k8sSWProduct},
	} {
		if got := k8sSWJudgeHeldStart(c.start, c.violation, c.coverage, c.heldThere, c.terminating, "the holder"); got.Class != c.want {
			t.Errorf("%s: class %q (%s), want %q", c.name, got.Class, got.Detail, c.want)
		}
	}
}

func TestK8SSWClassifyGuardProbe(t *testing.T) {
	holder := "regtest-holder-ab12cd34-1"
	refused := fmt.Errorf("Pod(s) still use PVC ns-db1-claim after 1m30s, not scaling up or creating the Deployment and nothing deleted: %s (Running)", holder)
	for _, c := range []struct {
		name     string
		err      error
		elapsed  time.Duration
		replicas int32
		pods     int
		want     k8sSWClass
	}{
		{"guard held", refused, 91 * time.Second, 0, 0, ""},
		{"baseline: Start scaled up, no error", nil, 2 * time.Second, 1, 1, k8sSWProduct},
		{"error but the scale-up was sent", refused, 91 * time.Second, 1, 1, k8sSWProduct},
		{"permission missing", errors.New("not allowed to list Pods in namespace ns (needs `list` on `pods`): pods is forbidden"), time.Second, 0, 0, k8sSWHarness},
		{"api forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no")), time.Second, 0, 0, k8sSWHarness},
		{"other refusal", errors.New("cannot read its Deployment: timeout"), time.Second, 0, 0, k8sSWInconclusive},
		{"refusal without the holder", errors.New("Pod(s) still use PVC x after 1m30s: another-pod (Running)"), 91 * time.Second, 0, 0, k8sSWInconclusive},
		{"refused at once, not by the wait", refused, 3 * time.Second, 0, 0, k8sSWInconclusive},
		{"wait beyond its bound", refused, 200 * time.Second, 0, 0, k8sSWProduct},
	} {
		if got := k8sSWClassifyGuardProbe(c.err, c.elapsed, c.replicas, c.pods, holder); got.Class != c.want {
			t.Errorf("%s: class %q (%s), want %q", c.name, got.Class, got.Detail, c.want)
		}
	}
}

// A guard refusal while an earlier Pod still holds the PVC is the guard working: not a product failure
// unless the test established a violation (a scale-up despite the refusal).
func TestK8SSWClassifyStartError(t *testing.T) {
	refused := errors.New("Pod(s) still use PVC ns-db1-claim after 1m30s, not scaling up: db1-old (Running, terminating)")
	for _, c := range []struct {
		name      string
		err       error
		replicas  int32
		others    int
		heldThere bool
		want      k8sSWClass
	}{
		{"guard refused, old Pod still there", refused, 0, 0, true, k8sSWInconclusive},
		{"guard refused, old Pod gone now", refused, 0, 0, false, k8sSWInconclusive},
		{"refused but scaled up", refused, 1, 0, true, k8sSWProduct},
		{"refused with another database Pod", refused, 0, 1, true, k8sSWProduct},
		{"forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no")), 0, 0, false, k8sSWHarness},
		{"permission message", errors.New("not allowed to list Pods in namespace ns: x"), 0, 0, false, k8sSWHarness},
		{"unknown failure", errors.New("connection reset"), 0, 0, false, k8sSWInconclusive},
	} {
		if got := k8sSWClassifyStartError(c.err, c.replicas, c.others, c.heldThere); got.Class != c.want {
			t.Errorf("%s: class %q (%s), want %q", c.name, got.Class, got.Detail, c.want)
		}
	}
}

// The database errors are classified by their structure (error number, kind), not their text.
func TestK8SSWClassifySQLError(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		verified bool
		want     k8sSWClass
	}{
		{"no such table, fixture verified", &mysql.MySQLError{Number: 1146, Message: "Table 'x.t' doesn't exist"}, true, k8sSWProduct},
		{"unknown database, fixture verified", fmt.Errorf("digest: %w", &mysql.MySQLError{Number: 1049, Message: "Unknown database"}), true, k8sSWProduct},
		{"no such table before the fixture", &mysql.MySQLError{Number: 1146}, false, k8sSWHarness},
		{"access denied", &mysql.MySQLError{Number: 1045}, true, k8sSWHarness},
		{"no privilege", &mysql.MySQLError{Number: 1142}, true, k8sSWHarness},
		{"read only", &mysql.MySQLError{Number: 1290}, true, k8sSWInconclusive},
		{"bad connection", driver.ErrBadConn, true, k8sSWInconclusive},
		{"invalid connection", mysql.ErrInvalidConn, true, k8sSWInconclusive},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("refused")}, true, k8sSWInconclusive},
		{"deadline", context.DeadlineExceeded, true, k8sSWInconclusive},
		// a message that merely mentions a table is not an error number
		{"text only", errors.New("Table 'x.t' doesn't exist"), true, k8sSWInconclusive},
	} {
		if got, detail := k8sSWClassifySQLError(c.err, c.verified); got != c.want {
			t.Errorf("%s: class %q (%s), want %q", c.name, got, detail, c.want)
		}
	}
}

func TestK8SSWPodTrouble(t *testing.T) {
	unsched := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: apiv1.PodStatus{Conditions: []apiv1.PodCondition{{Type: apiv1.PodScheduled, Status: apiv1.ConditionFalse, Reason: apiv1.PodReasonUnschedulable, Message: "0/2 nodes: Insufficient cpu"}}}}
	pull := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: apiv1.PodStatus{ContainerStatuses: []apiv1.ContainerStatus{{Name: "c", State: apiv1.ContainerState{Waiting: &apiv1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}
	oom := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: apiv1.PodStatus{ContainerStatuses: []apiv1.ContainerStatus{{Name: "c", LastTerminationState: apiv1.ContainerState{Terminated: &apiv1.ContainerStateTerminated{Reason: "OOMKilled"}}}}}}
	evicted := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: apiv1.PodStatus{Reason: "Evicted"}}
	fine := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: apiv1.PodStatus{Phase: apiv1.PodRunning}}
	for name, c := range map[string]struct {
		pod  *apiv1.Pod
		want string
	}{"unschedulable": {unsched, "unschedulable"}, "image pull": {pull, "image"}, "oom": {oom, "OOMKilled"}, "evicted": {evicted, "evicted"}, "fine": {fine, ""}} {
		got := k8sSWPodTrouble(c.pod)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want a mention of %q", name, got, c.want)
		}
	}
}

// --- the Deployment: patches, the interlock and the restoration --------------------------------------

func strategyOf(t appsv1.DeploymentStrategyType) appsv1.DeploymentStrategy {
	s := appsv1.DeploymentStrategy{Type: t}
	if t == appsv1.RollingUpdateDeploymentStrategyType {
		s.RollingUpdate = &appsv1.RollingUpdateDeployment{}
	}
	return s
}

func swDeployment(server string, strategy appsv1.DeploymentStrategy, lc *apiv1.Lifecycle) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: server, Namespace: swNS, UID: "dep-uid"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: strategy,
			Template: apiv1.PodTemplateSpec{Spec: apiv1.PodSpec{Containers: []apiv1.Container{
				{Name: server, Image: "db", Lifecycle: lc},
				{Name: server + "-dbjobs", Image: "jobs"},
			}}},
		},
	}
}

func postStartOnly() *apiv1.Lifecycle {
	return &apiv1.Lifecycle{PostStart: &apiv1.LifecycleHandler{HTTPGet: &apiv1.HTTPGetAction{Path: "/up"}}}
}

func lifecycleOf(t *testing.T, cs *fake.Clientset, server string) *apiv1.Lifecycle {
	t.Helper()
	dep, err := cs.AppsV1().Deployments(swNS).Get(context.TODO(), server, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return dep.Spec.Template.Spec.Containers[k8sSWDBContainerIndex(dep, server)].Lifecycle
}

func applyLifecyclePatch(t *testing.T, cs *fake.Clientset, dep *appsv1.Deployment, desired *apiv1.Lifecycle) {
	t.Helper()
	patch, needed, err := k8sSWLifecycleJSONPatch(dep, dep.Name, desired)
	if err != nil || !needed {
		t.Fatalf("patch needed %t, error %v", needed, err)
	}
	if _, err := cs.AppsV1().Deployments(swNS).Patch(context.TODO(), dep.Name, types.JSONPatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("applying %s: %v", patch, err)
	}
}

// The lifecycle patch makes the database container's lifecycle exactly the wanted one: the preStop is
// added to what exists, and removing it brings back the original and nothing of the temporary handler.
func TestK8SSWLifecyclePatchIsExact(t *testing.T) {
	for name, orig := range map[string]*apiv1.Lifecycle{"no lifecycle": nil, "an original postStart": postStartOnly()} {
		dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), orig.DeepCopy())
		cs := fake.NewSimpleClientset(dep)
		if _, needed, _ := k8sSWLifecycleJSONPatch(dep, "db1", orig); needed {
			t.Fatalf("%s: the original is the original", name)
		}
		with := k8sSWDesiredWithPreStop(orig, k8sSWPreStopSeconds)
		if orig != nil && with.PostStart == nil {
			t.Fatalf("%s: the original postStart must be kept", name)
		}
		applyLifecyclePatch(t, cs, dep, with)
		now, _ := cs.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
		if got := lifecycleOf(t, cs, "db1"); got == nil || got.PreStop == nil || !strings.Contains(strings.Join(got.PreStop.Exec.Command, " "), "sleep 15") {
			t.Fatalf("%s: preStop not set: %+v", name, got)
		}
		applyLifecyclePatch(t, cs, now, orig)
		if got := lifecycleOf(t, cs, "db1"); !equality.Semantic.DeepEqual(got, orig) {
			t.Fatalf("%s: the original was not restored exactly: %+v, want %+v", name, got, orig)
		}
		// the other container is never touched
		after, _ := cs.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
		if after.Spec.Template.Spec.Containers[1].Lifecycle != nil {
			t.Fatalf("%s: the dbjobs container was changed", name)
		}
	}
	// a preStop of another kind than the temporary one is replaced, not merged with it
	httpPre := &apiv1.Lifecycle{PreStop: &apiv1.LifecycleHandler{HTTPGet: &apiv1.HTTPGetAction{Path: "/x"}}}
	dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), httpPre)
	cs := fake.NewSimpleClientset(dep)
	applyLifecyclePatch(t, cs, dep, k8sSWDesiredWithPreStop(nil, 15))
	if got := lifecycleOf(t, cs, "db1"); got.PreStop.HTTPGet != nil || got.PreStop.Exec == nil {
		t.Fatalf("handler fields were merged: %+v", got.PreStop)
	}
	// no container of that name: an error, no patch
	if _, _, err := k8sSWLifecycleJSONPatch(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil), "other", nil); err == nil {
		t.Fatal("a missing container must be an error")
	}
}

func TestK8SSWTemplatePatchInterlock(t *testing.T) {
	if r := k8sSWTemplatePatchRefusal(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)); r != "" {
		t.Fatalf("Recreate must allow a template patch: %s", r)
	}
	if r := k8sSWTemplatePatchRefusal(swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), nil)); !strings.Contains(r, "second database Pod") {
		t.Fatalf("RollingUpdate must refuse a template patch, got %q", r)
	}
	// Recreate with a rollingUpdate block is not the safe state either
	odd := swDeployment("db1", appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{}}, nil)
	if k8sSWTemplatePatchRefusal(odd) == "" {
		t.Fatal("a rollingUpdate block must refuse")
	}
	// the legacy strategy patch touches no template
	if p := string(k8sSWLegacyStrategyPatch()); !strings.Contains(p, "RollingUpdate") || strings.Contains(p, "template") {
		t.Fatalf("legacy patch: %s", p)
	}
}

type swEnv struct {
	cs       *fake.Clientset
	waited   []map[types.UID]bool
	waitErr  error
	podsFunc func() ([]apiv1.Pod, error)
}

func (e *swEnv) env() k8sSWCleanupEnv {
	return k8sSWCleanupEnv{
		Client: e.cs, NS: swNS, Server: "db1",
		Pods:     e.podsFunc,
		Replaced: func(old map[types.UID]bool) error { e.waited = append(e.waited, old); return e.waitErr },
	}
}

func readyPod(uid string) apiv1.Pod {
	return apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db1-" + uid, UID: types.UID(uid)}, Status: apiv1.PodStatus{ContainerStatuses: []apiv1.ContainerStatus{{Name: "db1", Ready: true}}}}
}

func noPods() ([]apiv1.Pod, error)  { return nil, nil }
func onePod() ([]apiv1.Pod, error)  { return []apiv1.Pod{readyPod("p1")}, nil }
func twoPods() ([]apiv1.Pod, error) { return []apiv1.Pod{readyPod("p1"), readyPod("p2")}, nil }

func patchActions(cs *fake.Clientset) (strategic, jsonPatches int) {
	for _, a := range cs.Actions() {
		if p, ok := a.(ktesting.PatchAction); ok {
			switch p.GetPatchType() {
			case types.StrategicMergePatchType:
				strategic++
			case types.JSONPatchType:
				jsonPatches++
			}
		}
	}
	return
}

func swStopped(d *appsv1.Deployment) *appsv1.Deployment {
	zero := int32(0)
	d.Spec.Replicas = &zero
	return d
}

func swWithPreStop() *apiv1.Lifecycle { return k8sSWDesiredWithPreStop(nil, 15) }

// Restoration from the state the Deployment is really in: both changes undone, nothing started, no wait
// when no Pod can be affected.
func TestK8SSWRestoreBothChanges(t *testing.T) {
	dep := swStopped(swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), swWithPreStop()))
	e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: noPods}
	done, left := k8sSWRestore(e.env(), k8sSWSnap{Strategy: strategyOf(appsv1.RecreateDeploymentStrategyType)}, true, true)
	if len(left) != 0 {
		t.Fatalf("left: %v (done %v)", left, done)
	}
	after, _ := e.cs.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
	if !k8sSWIsRecreate(after) || after.Spec.Template.Spec.Containers[0].Lifecycle != nil {
		t.Fatalf("not restored: strategy %+v lifecycle %+v", after.Spec.Strategy, after.Spec.Template.Spec.Containers[0].Lifecycle)
	}
	if after.Spec.Replicas == nil || *after.Spec.Replicas != 0 {
		t.Fatal("cleanup must not change the replicas: it never starts a database")
	}
	if len(e.waited) != 0 {
		t.Fatal("at zero replicas no rollout exists to wait for")
	}
}

// A lifecycle that already is the original is left alone: no patch, no wait.
func TestK8SSWRestoreNoChangeNoWait(t *testing.T) {
	for name, orig := range map[string]*apiv1.Lifecycle{"nil": nil, "postStart": postStartOnly()} {
		dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), orig.DeepCopy())
		e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: onePod}
		done, left := k8sSWRestore(e.env(), k8sSWSnap{Lifecycle: orig}, true, true)
		if len(left) != 0 || len(done) == 0 {
			t.Fatalf("%s: done %v left %v", name, done, left)
		}
		if s, j := patchActions(e.cs); s+j != 0 {
			t.Fatalf("%s: %d patch(es) sent although nothing differs", name, s+j)
		}
		if len(e.waited) != 0 {
			t.Fatalf("%s: waited for a rollout that no change produces", name)
		}
	}
}

// Nothing attempted: nothing inspected, nothing patched, whatever the Deployment holds.
func TestK8SSWRestoreOnlyWhatWasAttempted(t *testing.T) {
	dep := swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), swWithPreStop())
	e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: onePod}
	if done, left := k8sSWRestore(e.env(), k8sSWSnap{}, false, false); len(done)+len(left) != 0 {
		t.Fatalf("done %v left %v", done, left)
	}
	if len(e.cs.Actions()) != 0 {
		t.Fatalf("no call expected, got %v", e.cs.Actions())
	}
}

// When the strategy cannot be restored no template patch is sent: under RollingUpdate it would start a
// second database Pod.
func TestK8SSWRestoreStrategyFailureSendsNoLifecyclePatch(t *testing.T) {
	dep := swStopped(swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), swWithPreStop()))
	cs := fake.NewSimpleClientset(dep)
	cs.PrependReactor("patch", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.(ktesting.PatchAction).GetPatchType() == types.StrategicMergePatchType {
			return true, nil, errors.New("api unavailable")
		}
		return false, nil, nil
	})
	e := &swEnv{cs: cs, podsFunc: noPods}
	_, left := k8sSWRestore(e.env(), k8sSWSnap{}, true, true)
	if len(left) != 1 || !strings.Contains(left[0], "not Recreate") {
		t.Fatalf("left: %v", left)
	}
	if _, j := patchActions(cs); j != 0 {
		t.Fatalf("%d JSON patch(es) sent with the strategy not restored", j)
	}
}

// Even when the test did not attempt the strategy, a Deployment found under RollingUpdate receives no
// template patch.
func TestK8SSWRestoreInterlockWithoutStrategyAttempt(t *testing.T) {
	dep := swStopped(swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), swWithPreStop()))
	e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: noPods}
	_, left := k8sSWRestore(e.env(), k8sSWSnap{}, false, true)
	if len(left) != 1 || !strings.Contains(left[0], "second database Pod") {
		t.Fatalf("left %v", left)
	}
	if s, j := patchActions(e.cs); s+j != 0 {
		t.Fatalf("%d patch(es) sent under RollingUpdate", s+j)
	}
}

// The state of a database Pod that is not certain never allows a rollout.
func TestK8SSWRestoreUncertainPodStateSendsNoPatch(t *testing.T) {
	terminating := readyPod("p1")
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	notReady := readyPod("p1")
	notReady.Status.ContainerStatuses[0].Ready = false
	for name, pods := range map[string]func() ([]apiv1.Pod, error){
		"two Pods":     twoPods,
		"no Pod":       noPods,
		"terminating":  func() ([]apiv1.Pod, error) { return []apiv1.Pod{terminating}, nil },
		"not ready":    func() ([]apiv1.Pod, error) { return []apiv1.Pod{notReady}, nil },
		"list failure": func() ([]apiv1.Pod, error) { return nil, errors.New("timeout") },
	} {
		dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), swWithPreStop())
		e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: pods}
		_, left := k8sSWRestore(e.env(), k8sSWSnap{}, false, true)
		if len(left) != 1 || !strings.Contains(left[0], "uncertain") {
			t.Fatalf("%s: left %v", name, left)
		}
		if s, j := patchActions(e.cs); s+j != 0 {
			t.Fatalf("%s: a patch was sent with an uncertain Pod state", name)
		}
	}
}

// With one ready Pod the lifecycle is restored by a rollout, and the rollout is verified.
func TestK8SSWRestoreWithRollout(t *testing.T) {
	dep := swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), swWithPreStop())
	e := &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: onePod}
	done, left := k8sSWRestore(e.env(), k8sSWSnap{}, false, true)
	if len(left) != 0 || len(e.waited) != 1 || !e.waited[0]["p1"] {
		t.Fatalf("done %v left %v waited %v", done, left, e.waited)
	}
	if lifecycleOf(t, e.cs, "db1") != nil {
		t.Fatal("the lifecycle was not restored")
	}
	// the rollout does not complete: reported as left
	dep = swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), swWithPreStop())
	e = &swEnv{cs: fake.NewSimpleClientset(dep), podsFunc: onePod, waitErr: errors.New("no ready replacement")}
	if _, left = k8sSWRestore(e.env(), k8sSWSnap{}, false, true); len(left) != 1 || !strings.Contains(left[0], "did not complete") {
		t.Fatalf("left %v", left)
	}
}

// A patch the API applied but answered with an error: the flag says it was attempted, the Deployment says
// what is true, and cleanup acts on the Deployment.
func TestK8SSWRestoreAmbiguousPatchResponse(t *testing.T) {
	applyThenFail := func(cs *fake.Clientset, only types.PatchType) {
		cs.PrependReactor("patch", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
			if a.(ktesting.PatchAction).GetPatchType() != only {
				return false, nil, nil
			}
			if _, _, err := ktesting.ObjectReaction(cs.Tracker())(a); err != nil {
				return true, nil, err
			}
			return true, nil, errors.New("request timeout")
		})
	}
	// 1. the test's own preStop patch is applied although the client gets an error: the Deployment then
	// holds the delay, the flag is true, and cleanup removes it
	dep := swStopped(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil))
	cs := fake.NewSimpleClientset(dep)
	applyThenFail(cs, types.JSONPatchType)
	patch, _, _ := k8sSWLifecycleJSONPatch(dep, "db1", swWithPreStop())
	if _, err := cs.AppsV1().Deployments(swNS).Patch(context.TODO(), "db1", types.JSONPatchType, patch, metav1.PatchOptions{}); err == nil {
		t.Fatal("the ambiguous response was expected")
	}
	if lifecycleOf(t, cs, "db1") == nil {
		t.Fatal("the patch must have been applied")
	}
	e := &swEnv{cs: cs, podsFunc: noPods}
	// the restoration also gets an ambiguous answer, and is recognised from the Deployment
	done, left := k8sSWRestore(e.env(), k8sSWSnap{}, false, true)
	if len(left) != 0 || lifecycleOf(t, cs, "db1") != nil {
		t.Fatalf("the applied delay must be removed, left %v", left)
	}
	if !strings.Contains(strings.Join(done, ";"), "request timeout") {
		t.Fatalf("the ambiguous response should be reported: %v", done)
	}
	// 2. the strategy restoration is applied but answers an error: verified by reading, not left
	cs = fake.NewSimpleClientset(swStopped(swDeployment("db1", strategyOf(appsv1.RollingUpdateDeploymentStrategyType), nil)))
	applyThenFail(cs, types.StrategicMergePatchType)
	e = &swEnv{cs: cs, podsFunc: noPods}
	if _, left = k8sSWRestore(e.env(), k8sSWSnap{}, true, false); len(left) != 0 {
		t.Fatalf("an applied strategy restoration must be recognised from the Deployment: left %v", left)
	}
	// 3. the flag is true but the Deployment already is the original: nothing is patched or waited for
	cs = fake.NewSimpleClientset(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil))
	e = &swEnv{cs: cs, podsFunc: onePod}
	k8sSWRestore(e.env(), k8sSWSnap{}, true, true)
	if s, j := patchActions(cs); s+j != 0 || len(e.waited) != 0 {
		t.Fatal("a flag must not cause a patch or a wait when the Deployment is already restored")
	}
	// 4. the Deployment cannot be read: left, with what was attempted
	e = &swEnv{cs: fake.NewSimpleClientset(), podsFunc: noPods}
	if _, left = k8sSWRestore(e.env(), k8sSWSnap{}, true, true); len(left) != 1 || !strings.Contains(left[0], "cannot be read") {
		t.Fatalf("left %v", left)
	}
}

// --- the gates, the holder and the fixture -------------------------------------------------------------

// S5, S6 and every step after S2c refuse to run before both safety gates passed, without touching the
// Pods, the watch or the cluster.
func TestK8SSWGatesBeforeTerminatingScenarios(t *testing.T) {
	newRun := func() *k8sSWRun {
		return &k8sSWRun{cl: &cluster.Cluster{Conf: new(config.Config)}, test: &cluster.Test{}}
	}
	for name, set := range map[string]func(r *k8sSWRun){
		"no gate":    func(r *k8sSWRun) {},
		"only S2":    func(r *k8sSWRun) { r.gateS2 = true },
		"only S2c":   func(r *k8sSWRun) { r.gateS2c = true },
		"both gates": func(r *k8sSWRun) { r.gateS2, r.gateS2c = true, true },
	} {
		r := newRun()
		set(r)
		got := r.requireGates("S5")
		if want := name == "both gates"; got != want {
			t.Errorf("%s: requireGates %t, want %t", name, got, want)
		}
		if !got && (r.failedStep != "S5" || r.failedCls != k8sSWHarness) {
			t.Errorf("%s: the refusal is a FAIL with a class: step %q class %q", name, r.failedStep, r.failedCls)
		}
	}
	// the steps that follow the gates refuse before reading anything (the watch and the client are nil)
	r := newRun()
	r.gateS2 = true
	for name, step := range map[string]func() bool{
		"S3": r.stepLegacyMigration, "S5a": r.stepPreStopOn, "S5": r.stepImmediateStart,
		"S6": r.stepConcurrentStarts, "S8": r.stepRolloutOps,
	} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("%s touched the cluster before the gates passed: %v", name, p)
				}
			}()
			if step() {
				t.Errorf("%s ran without both gates", name)
			}
		}()
	}
}

func TestK8SSWHolderPod(t *testing.T) {
	pod := k8sSWHolderPod(swNS, "regtest-holder-1", "run1", "ns-db1-claim", "node-a", 0)
	if pod.Labels["tag"] != "" || pod.Labels["app"] != "" {
		t.Errorf("the holder must not carry a label a Deployment or Service selects: %v", pod.Labels)
	}
	if pod.Labels[k8sSWOwnerLabel] != k8sSWTestName || pod.Labels[k8sSWRunLabel] != "run1" {
		t.Errorf("the holder is labelled as test-owned: %v", pod.Labels)
	}
	vol := pod.Spec.Volumes[0].PersistentVolumeClaim
	if vol == nil || vol.ClaimName != "ns-db1-claim" || !vol.ReadOnly || !pod.Spec.Containers[0].VolumeMounts[0].ReadOnly {
		t.Errorf("the claim must be mounted read-only: %+v", vol)
	}
	if pod.Spec.Containers[0].Lifecycle != nil {
		t.Error("the S2 holder has no preStop delay")
	}
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || terms[0].MatchFields[0].Values[0] != "node-a" || pod.Spec.NodeName != "" {
		t.Errorf("the holder is placed on the database's node through the scheduler: %+v", terms)
	}
	// S2c holder: a bounded Terminating window
	slow := k8sSWHolderPod(swNS, "regtest-holder-2", "run1", "ns-db1-claim", "node-a", k8sSWHolderPreStopSeconds)
	lc := slow.Spec.Containers[0].Lifecycle
	if lc == nil || lc.PreStop == nil || !strings.Contains(strings.Join(lc.PreStop.Exec.Command, " "), fmt.Sprintf("sleep %d", k8sSWHolderPreStopSeconds)) {
		t.Errorf("the S2c holder needs a bounded preStop: %+v", lc)
	}
	if k8sSWHolderPreStopSeconds >= 30 {
		t.Error("the delay must stay within the default termination grace period (30 s)")
	}
	running := pod.DeepCopy()
	running.Status = apiv1.PodStatus{Phase: apiv1.PodRunning, ContainerStatuses: []apiv1.ContainerStatus{{Name: "holder", Ready: true}}}
	running.Spec.NodeName = "node-a"
	if p := k8sSWHolderProblem(running, "ns-db1-claim", "node-a"); p != "" {
		t.Errorf("a good holder: %s", p)
	}
	for name, mutate := range map[string]func(p *apiv1.Pod){
		"pending":    func(p *apiv1.Pod) { p.Status.Phase = apiv1.PodPending },
		"other node": func(p *apiv1.Pod) { p.Spec.NodeName = "node-b" },
		"rw mount":   func(p *apiv1.Pod) { p.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly = false },
		"other pvc":  func(p *apiv1.Pod) { p.Spec.Volumes[0].PersistentVolumeClaim.ClaimName = "x" },
		"not ready":  func(p *apiv1.Pod) { p.Status.ContainerStatuses[0].Ready = false },
		"no status":  func(p *apiv1.Pod) { p.Status.ContainerStatuses = nil },
	} {
		bad := running.DeepCopy()
		mutate(bad)
		if k8sSWHolderProblem(bad, "ns-db1-claim", "node-a") == "" {
			t.Errorf("%s: the holder problem was not detected", name)
		}
	}
}

func TestK8SSWFixtureInserts(t *testing.T) {
	stmts := k8sSWFixtureInserts("s", k8sSWFixtureRows)
	rows := 0
	for _, s := range stmts {
		rows += strings.Count(s, "),(") + 1
	}
	if rows != k8sSWFixtureRows {
		t.Fatalf("%d rows in %d statements, want %d", rows, len(stmts), k8sSWFixtureRows)
	}
	if !slices.Equal(stmts, k8sSWFixtureInserts("s", k8sSWFixtureRows)) {
		t.Fatal("the fixture must be deterministic: the digest is compared across restarts")
	}
	if !k8sSWSameSet(map[types.UID]bool{"a": true}, map[types.UID]bool{"a": true}) || k8sSWSameSet(map[types.UID]bool{"a": true}, map[types.UID]bool{"b": true}) {
		t.Fatal("k8sSWSameSet")
	}
}

// --- PVC retention in the reused reprovision test ---------------------------------------------------------

func TestK8SPVCRetention(t *testing.T) {
	pvc := &apiv1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "ns-db1-claim", UID: "pvc-1"}}
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "ns-db1-claim")
	// an existing Deployment with its claim: retention is asserted
	if uid, applicable, problem := k8sPVCRetentionBefore(true, []string{"ns-db1-claim"}, pvc, nil); uid != "pvc-1" || !applicable || problem != "" {
		t.Fatalf("existing claim: %q %t %q", uid, applicable, problem)
	}
	// an existing Deployment whose claim is missing: a clear precondition error, never a silent skip
	if _, applicable, problem := k8sPVCRetentionBefore(true, []string{"ns-db1-claim"}, nil, notFound); applicable || !strings.Contains(problem, "precondition") || !strings.Contains(problem, "does not exist") {
		t.Fatalf("missing claim of an existing Deployment: %t %q", applicable, problem)
	}
	if _, _, problem := k8sPVCRetentionBefore(true, nil, nil, nil); !strings.Contains(problem, "precondition") {
		t.Fatalf("a Deployment without a claim: %q", problem)
	}
	if _, _, problem := k8sPVCRetentionBefore(true, []string{"c"}, nil, errors.New("timeout")); problem == "" {
		t.Fatal("an unreadable claim is a problem")
	}
	if _, _, problem := k8sPVCRetentionBefore(true, []string{"c"}, &apiv1.PersistentVolumeClaim{}, nil); problem == "" {
		t.Fatal("a claim without a UID cannot be followed")
	}
	// a first provisioning has nothing to retain: explicit, and not a problem
	if uid, applicable, problem := k8sPVCRetentionBefore(false, nil, nil, notFound); uid != "" || applicable || problem != "" {
		t.Fatalf("fresh provisioning: %q %t %q", uid, applicable, problem)
	}
	// after: a changed UID is a failure only where retention applies
	if k8sPVCRetentionAfter(true, "pvc-1", "pvc-2", "c") == "" {
		t.Fatal("a replaced claim must fail")
	}
	if k8sPVCRetentionAfter(true, "pvc-1", "pvc-1", "c") != "" || k8sPVCRetentionAfter(false, "", "pvc-9", "c") != "" {
		t.Fatal("the retained claim, and a fresh provisioning, are not failures")
	}
}

// --- registration and safety of the source ------------------------------------------------------------------

// The files of the feature: the runner and the evidence helpers.
var k8sSWSources = []string{"test_k8s_single_writer_lifecycle.go", "test_k8s_single_writer_evidence.go"}

func swCode(t *testing.T) string {
	t.Helper()
	var code strings.Builder
	for _, f := range k8sSWSources {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			code.WriteString(line + "\n")
		}
	}
	return code.String()
}

// The test is run by name only: not in ALL, no targets entry (TestTargetsDeclared ties targets to ALL),
// dispatched by the server and described in the list's comment.
func TestK8SSWRegistration(t *testing.T) {
	if slices.Contains(tests, k8sSWTestName) {
		t.Errorf("%s must not be in the ALL list: it stops, restarts and reprovisions its database", k8sSWTestName)
	}
	if _, has := testTargets[k8sSWTestName]; has {
		t.Errorf("%s declares targets, which TestTargetsDeclared allows only for tests of the ALL list", k8sSWTestName)
	}
	server, err := os.ReadFile("../server/regtest.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `test.Name == "`+k8sSWTestName+`"`) {
		t.Errorf("server/regtest.go does not dispatch %s", k8sSWTestName)
	}
	list, err := os.ReadFile("regtest.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), k8sSWTestName+" is not in this list either") {
		t.Errorf("regtest.go does not describe %s as run by name", k8sSWTestName)
	}
}

// Nothing the test does may be a forced deletion: ordinary deletion of its own holder Pods only, through
// one call site, in every file of the feature.
func TestK8SSWNeverForceDeletes(t *testing.T) {
	code := swCode(t)
	for _, forbidden := range []string{"GracePeriodSeconds", "NewDeleteOptions", "ForceDelete", "--force", "--grace-period", "Finalizers", "DeletePropagation", "DeleteCollection"} {
		if strings.Contains(code, forbidden) {
			t.Errorf("the test must not force-delete: found %q", forbidden)
		}
	}
	if n := strings.Count(code, "Pods(namespace).Delete("); n != 1 {
		t.Errorf("the test deletes Pods in %d place(s), want only the one deletePod of its holders", n)
	}
	if n := strings.Count(code, ".Delete("); n != 1 {
		t.Errorf("the test deletes objects in %d place(s), want one", n)
	}
}

// The test never starts a database in cleanup, and the restoration checks the strategy before a template
// patch.
func TestK8SSWCleanupNeverStarts(t *testing.T) {
	src, err := os.ReadFile("test_k8s_single_writer_lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	i := strings.Index(text, "func (r *k8sSWRun) cleanup()")
	if i < 0 {
		t.Fatal("cleanup not found")
	}
	if body := text[i:]; strings.Contains(body, "StartDatabaseService") || strings.Contains(body, "startDB") || strings.Contains(body, "startOnce") {
		t.Error("cleanup must never start a database")
	}
	evidence, _ := os.ReadFile("test_k8s_single_writer_evidence.go")
	if !strings.Contains(string(evidence), "k8sSWTemplatePatchRefusal(dep)") {
		t.Error("the restoration must check the strategy before a template patch")
	}
}

// --- the result of the run, the zero-replica interlock and the replacement claim ---------------------------

func swRunWithClient(cs *fake.Clientset) *k8sSWRun {
	return &k8sSWRun{
		cl:     &cluster.Cluster{Conf: new(config.Config)},
		test:   &cluster.Test{},
		client: cs, ns: swNS, server: &cluster.ServerMonitor{Name: "db1"},
		depUID: "dep-new", claim: "ns-db1-claim",
	}
}

// A scenario that passed cannot be reported as PASS when its cleanup left a holder Pod or a temporary
// change of the Deployment; a scenario that failed keeps its own class and step.
func TestK8SSWCleanupDecidesTheResult(t *testing.T) {
	r := swRunWithClient(nil)
	r.passed = []string{"S0", "S2"}
	if !r.conclude(true, nil) {
		t.Fatal("a clean pass stays a pass")
	}
	r = swRunWithClient(nil)
	if r.conclude(true, []string{"the holder Pod regtest-holder-1"}) {
		t.Fatal("a pass with an unresolved holder must be a FAIL")
	}
	if r.failedStep != "CLEANUP" || r.failedCls != k8sSWHarness {
		t.Fatalf("the failure is attributed to the cleanup: %q %q", r.failedStep, r.failedCls)
	}
	r = swRunWithClient(nil)
	if r.conclude(true, []string{"the lifecycle of db1 was not restored"}) {
		t.Fatal("a pass with an un-restored Deployment change must be a FAIL")
	}
	r = swRunWithClient(nil)
	r.failedStep, r.failedCls = "S5", k8sSWProduct
	if r.conclude(false, []string{"x"}) || r.failedStep != "S5" || r.failedCls != k8sSWProduct {
		t.Fatalf("a failed test keeps its own failure: %q %q", r.failedStep, r.failedCls)
	}
	r = swRunWithClient(nil)
	r.skipped = "not eligible"
	if !r.conclude(true, nil) {
		t.Fatal("a SKIP stays a SKIP")
	}
}

// An ineligible run changed nothing: its cleanup has nothing to report.
func TestK8SSWCleanupWithoutChangeLeavesNothing(t *testing.T) {
	r := swRunWithClient(nil)
	if left := r.cleanup(); len(left) != 0 {
		t.Fatalf("nothing was changed: %v", left)
	}
}

// The state S3 requires is read from the API before each mutation, and any uncertainty stops the step.
func TestK8SSWRequireStopped(t *testing.T) {
	stopped := swStopped(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil))
	r := swRunWithClient(fake.NewSimpleClientset(stopped))
	if !r.requireStopped("S3", appsv1.RecreateDeploymentStrategyType, false, "now") {
		t.Fatalf("a stopped Deployment: %s", r.failedStep)
	}
	// a database Pod remains
	r = swRunWithClient(fake.NewSimpleClientset(stopped.DeepCopy(), swDBPod("db1-x", "x")))
	if r.requireStopped("S3", appsv1.RecreateDeploymentStrategyType, false, "now") || r.failedCls != k8sSWInconclusive {
		t.Fatalf("a Pod remains: class %q", r.failedCls)
	}
	// replicas are not zero
	r = swRunWithClient(fake.NewSimpleClientset(swDeployment("db1", strategyOf(appsv1.RecreateDeploymentStrategyType), nil)))
	if r.requireStopped("S3", appsv1.RecreateDeploymentStrategyType, false, "now") {
		t.Fatal("one replica must be refused")
	}
	// the strategy is not the expected one
	r = swRunWithClient(fake.NewSimpleClientset(stopped.DeepCopy()))
	if r.requireStopped("S3", appsv1.RollingUpdateDeploymentStrategyType, true, "now") {
		t.Fatal("the wrong strategy must be refused")
	}
	// a read that fails is uncertainty: refused
	cs := fake.NewSimpleClientset(stopped.DeepCopy())
	cs.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("timeout")
	})
	r = swRunWithClient(cs)
	if r.requireStopped("S3", appsv1.RecreateDeploymentStrategyType, false, "now") || r.failedCls != k8sSWInconclusive {
		t.Fatalf("a failed read: class %q", r.failedCls)
	}
}

// A replacement Pod that does not mount the data claim recorded at S0 is never accepted.
func TestK8SSWReplacementMustMountTheRecordedClaim(t *testing.T) {
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs-new", Namespace: swNS, UID: "rs-new-uid", OwnerReferences: controlled("Deployment", "db1", "dep-new")}}
	pod := func(claim string) *apiv1.Pod {
		p := withDataClaim(*swDBPod("db1-new", "new"), claim)
		p.Namespace = swNS
		p.OwnerReferences = controlled("ReplicaSet", "rs-new", "rs-new-uid")
		p.Status = apiv1.PodStatus{Phase: apiv1.PodRunning, ContainerStatuses: []apiv1.ContainerStatus{{Name: "db1", Ready: true}}}
		return &p
	}
	r := swRunWithClient(fake.NewSimpleClientset(rs, pod("ns-db1-claim")))
	if got, err := r.waitReplacement(map[types.UID]bool{}, 5*time.Second); err != nil || got == nil || got.UID != "new" {
		t.Fatalf("the Pod that mounts the recorded claim: %v %v", got, err)
	}
	r = swRunWithClient(fake.NewSimpleClientset(rs, pod("ns-other-claim")))
	if got, err := r.waitReplacement(map[types.UID]bool{}, 5*time.Second); got != nil || err == nil || !strings.Contains(err.Error(), "recorded data claim") {
		t.Fatalf("another claim must be refused at once: %v %v", got, err)
	}
	// no data mount at all is ambiguous, refused as well
	noMount := pod("ns-db1-claim")
	noMount.Spec.Containers[0].VolumeMounts = nil
	r = swRunWithClient(fake.NewSimpleClientset(rs, noMount))
	if got, err := r.waitReplacement(map[types.UID]bool{}, 5*time.Second); got != nil || err == nil {
		t.Fatalf("an unidentifiable claim must be refused: %v %v", got, err)
	}
}

// The reprovision test records SKIP, never a PASS, when there is no server to reprovision.
func TestK8SProvisionSchedulerVolumeBindingSkipsWithoutServer(t *testing.T) {
	cl := &cluster.Cluster{Conf: new(config.Config)}
	test := &cluster.Test{}
	new(RegTest).TestK8SProvisionSchedulerVolumeBinding(cl, "", test)
	if test.SkipReason == "" {
		t.Fatal("no server: the runner must record SKIP with a reason")
	}
}

// --- V5: fixture residue, the holder deletion precondition, the replacement's claim ---------------------

func TestK8SSWFixtureResidue(t *testing.T) {
	if r := k8sSWFixtureResidue(func() error { return nil }, "s"); r != "" {
		t.Fatalf("a dropped fixture leaves nothing: %q", r)
	}
	if r := k8sSWFixtureResidue(func() error { return errors.New("connection refused") }, "s"); !strings.Contains(r, "connection refused") {
		t.Fatalf("a failed drop is reported: %q", r)
	}
	if r := k8sSWFixtureResidue(nil, "s"); !strings.Contains(r, "not reachable") {
		t.Fatalf("an unreachable database is reported, and nothing is tried: %q", r)
	}
	// a test that passed with a fixture left is a FAIL; a test that failed keeps its own class
	r := swRunWithClient(nil)
	if r.conclude(true, []string{k8sSWFixtureResidue(nil, "s")}) || r.failedStep != "CLEANUP" {
		t.Fatal("a pass with the fixture left must be a FAIL attributed to the cleanup")
	}
	r = swRunWithClient(nil)
	r.failedStep, r.failedCls = "S2c", k8sSWProduct
	if r.conclude(false, []string{k8sSWFixtureResidue(nil, "s")}) || r.failedStep != "S2c" || r.failedCls != k8sSWProduct {
		t.Fatal("a failed test keeps its own class while the cleanup problem is reported apart")
	}
}

// The holder is deleted with its UID as a precondition, once. A name that another Pod now uses is not
// deleted, and an absent holder is told from a replaced one.
func TestK8SSWHolderDeletionUsesTheUIDPrecondition(t *testing.T) {
	holder := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "holder", Namespace: swNS, UID: "mine"}}
	stranger := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "holder", Namespace: swNS, UID: "other"}}
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "holder", errors.New("Precondition failed: UID"))
	deletes := func(cs *fake.Clientset) (n int, uids []types.UID) {
		for _, a := range cs.Actions() {
			if d, ok := a.(ktesting.DeleteAction); ok {
				n++
				if pre := d.GetDeleteOptions().Preconditions; pre != nil && pre.UID != nil {
					uids = append(uids, *pre.UID)
				}
			}
		}
		return
	}
	// the holder is there: one deletion, carrying the UID
	cs := fake.NewSimpleClientset(holder.DeepCopy())
	if out, err := k8sSWDeleteHolder(cs, swNS, "holder", "mine"); out != k8sSWDeleteRequested || err != nil {
		t.Fatalf("%q %v", out, err)
	}
	if n, uids := deletes(cs); n != 1 || len(uids) != 1 || uids[0] != "mine" {
		t.Fatalf("one deletion with the UID precondition expected: %d %v", n, uids)
	}
	// another Pod uses the name: the API server refuses (the fake does not enforce preconditions, so the
	// refusal is simulated); the Pod is not deleted again by name and is reported as replaced
	cs = fake.NewSimpleClientset(stranger.DeepCopy())
	cs.PrependReactor("delete", "pods", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, conflict })
	if out, err := k8sSWDeleteHolder(cs, swNS, "holder", "mine"); out != k8sSWDeleteReplaced || err != nil {
		t.Fatalf("a different Pod on the name: %q %v", out, err)
	}
	if n, _ := deletes(cs); n != 1 {
		t.Fatalf("the deletion was retried by name: %d deletions", n)
	}
	if _, err := cs.CoreV1().Pods(swNS).Get(context.TODO(), "holder", metav1.GetOptions{}); err != nil {
		t.Fatal("the other Pod must be left alone")
	}
	// no Pod of that name: the holder is absent
	if out, err := k8sSWDeleteHolder(fake.NewSimpleClientset(), swNS, "holder", "mine"); out != k8sSWDeleteAbsent || err != nil {
		t.Fatalf("an absent holder: %q %v", out, err)
	}
	// a refusal on the same UID is a real error, never retried by name
	cs = fake.NewSimpleClientset(holder.DeepCopy())
	cs.PrependReactor("delete", "pods", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, conflict })
	if out, err := k8sSWDeleteHolder(cs, swNS, "holder", "mine"); out != "" || err == nil {
		t.Fatalf("a refusal on our own UID: %q %v", out, err)
	}
	if n, _ := deletes(cs); n != 1 {
		t.Fatalf("retried: %d", n)
	}
}

// --- V6: holder creation identity, fixture schema ownership, the probe row --------------------------------

func swHolderRun(cs *fake.Clientset) *k8sSWRun {
	r := swRunWithClient(cs)
	r.id, r.node = "run1", "node-a"
	return r
}

const swHolderName = "regtest-holder-run1-1"

// the Pod the test would create, as the API server would store it (with a UID)
func swStoredHolder(uid string) *apiv1.Pod {
	p := k8sSWHolderPod(swNS, swHolderName, "run1", "ns-db1-claim", "node-a", 0)
	p.UID = types.UID(uid)
	return p
}

func countActions(cs *fake.Clientset, verb string) (n int) {
	for _, a := range cs.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == "pods" {
			n++
		}
	}
	return
}

// A Create that was applied but whose response failed: the holder is recognised by its identity (name,
// run ID, ownership labels, read-only claim, UID), recorded for the UID-preconditioned cleanup, and Create
// is not repeated.
func TestK8SSWHolderCreateAppliedButResponseFailed(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		pod := a.(ktesting.CreateAction).GetObject().(*apiv1.Pod)
		pod.UID = "created-uid"
		if _, _, err := ktesting.ObjectReaction(cs.Tracker())(a); err != nil {
			return true, nil, err
		}
		return true, nil, errors.New("request timeout")
	})
	r := swHolderRun(cs)
	pod, err := r.createHolderPod(0)
	if err != nil || pod == nil || pod.UID != "created-uid" {
		t.Fatalf("the applied holder must be recognised: %v %v", pod, err)
	}
	if r.holder != swHolderName || r.holderUID != "created-uid" || r.holderPending != "" {
		t.Fatalf("recorded %q %q pending %q", r.holder, r.holderUID, r.holderPending)
	}
	if n := countActions(cs, "create"); n != 1 {
		t.Fatalf("Create was repeated: %d", n)
	}
	// the normal cleanup deletes it with the verified UID
	if err := r.removeHolder(); err != nil {
		t.Fatal(err)
	}
	var uid *types.UID
	for _, a := range cs.Actions() {
		if d, ok := a.(ktesting.DeleteAction); ok {
			uid = d.GetDeleteOptions().Preconditions.UID
		}
	}
	if uid == nil || *uid != "created-uid" {
		t.Fatalf("the cleanup must delete with the verified UID, got %v", uid)
	}
}

// A Pod that is not shown to be this run's holder is never recorded nor deleted by name, the name stays
// pending, the cleanup reports it, and the test cannot pass.
func TestK8SSWHolderCreateUnrelatedPodOrUnresolved(t *testing.T) {
	// an unrelated Pod already uses the intended name (the API answers AlreadyExists)
	stranger := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: swHolderName, Namespace: swNS, UID: "stranger", Labels: map[string]string{"app": "x"}}}
	cs := fake.NewSimpleClientset(stranger)
	r := swHolderRun(cs)
	if pod, err := r.createHolderPod(0); pod != nil || err == nil || !strings.Contains(err.Error(), "not shown to be this run's holder") {
		t.Fatalf("an unrelated Pod on the name: %v %v", pod, err)
	}
	if r.holder != "" || r.holderUID != "" || r.holderPending != swHolderName {
		t.Fatalf("nothing recorded, the name pending: %q %q %q", r.holder, r.holderUID, r.holderPending)
	}
	r.mutating = true
	left := r.cleanup()
	if len(left) != 1 || !strings.Contains(left[0], "not shown to be this run's holder") {
		t.Fatalf("the cleanup must report it: %v", left)
	}
	if countActions(cs, "delete") != 0 {
		t.Fatal("an unrelated Pod must never be deleted by name")
	}
	if r.conclude(true, left) {
		t.Fatal("an unresolved holder must not pass")
	}
	// same labels and run ID but another UID/claim attachment: not shown to be ours either
	impostor := swStoredHolder("x")
	impostor.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly = false
	if problem := k8sSWHolderOwned(impostor, swHolderName, "run1", "ns-db1-claim"); !strings.Contains(problem, "read-only") {
		t.Fatalf("the read-only attachment is part of the identity: %q", problem)
	}
	other := swStoredHolder("x")
	other.Labels[k8sSWRunLabel] = "run2"
	if k8sSWHolderOwned(other, swHolderName, "run1", "ns-db1-claim") == "" {
		t.Fatal("another run's holder is not ours")
	}
	if k8sSWHolderOwned(swStoredHolder(""), swHolderName, "run1", "ns-db1-claim") == "" {
		t.Fatal("a Pod without a UID cannot be deleted with a precondition")
	}

	// the outcome cannot be established: the read fails too
	cs = fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("timeout") })
	cs.PrependReactor("get", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})
	r = swHolderRun(cs)
	if pod, err := r.createHolderPod(0); pod != nil || err == nil || !strings.Contains(err.Error(), "cannot be established") {
		t.Fatalf("unresolved: %v %v", pod, err)
	}
	if msg := r.resolveHolderPending(); !strings.Contains(msg, "cannot be established") || r.holderPending == "" {
		t.Fatalf("still unresolved at the cleanup: %q", msg)
	}
	if countActions(cs, "create") != 1 || countActions(cs, "delete") != 0 {
		t.Fatal("no retry, no deletion")
	}

	// no Pod yet: reported as a possible late application; the cleanup looks again and settles it
	cs = fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("timeout") })
	r = swHolderRun(cs)
	if _, err := r.createHolderPod(0); err == nil || !strings.Contains(err.Error(), "late application") {
		t.Fatalf("absent after an ambiguous Create: %v", err)
	}
	if msg := r.resolveHolderPending(); msg != "" || r.holderPending != "" {
		t.Fatalf("still absent at the cleanup: resolved, got %q pending %q", msg, r.holderPending)
	}
	// the creation lands late: the cleanup recognises the holder and takes the UID-safe path
	cs = fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("timeout") })
	r = swHolderRun(cs)
	r.createHolderPod(0)
	if err := cs.Tracker().Add(swStoredHolder("late-uid")); err != nil {
		t.Fatal(err)
	}
	if msg := r.resolveHolderPending(); msg != "" || r.holder != swHolderName || r.holderUID != "late-uid" {
		t.Fatalf("a late holder must be recorded with its UID: %q %q %q", msg, r.holder, r.holderUID)
	}
	// a definitive refusal leaves nothing pending
	cs = fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, swHolderName, errors.New("no"))
	})
	r = swHolderRun(cs)
	if _, err := r.createHolderPod(0); err == nil || r.holderPending != "" || countActions(cs, "get") != 0 {
		t.Fatalf("a refusal is definitive: %v pending %q", err, r.holderPending)
	}
}

// Only a confirmed CREATE DATABASE makes the schema droppable; a refusal and an unknown outcome do not.
func TestK8SSWFixtureSchemaOwnership(t *testing.T) {
	dropped := 0
	drop := func() error { dropped++; return nil }
	exists := &mysql.MySQLError{Number: 1007, Message: "Can't create database; database exists"}
	denied := &mysql.MySQLError{Number: 1044, Message: "Access denied"}
	// failed CREATE (answered by the server): not owned, no residue, nothing dropped, even if the schema exists
	for _, err := range []error{exists, denied, fmt.Errorf("statement failed: %w", exists)} {
		owned, ambiguous := k8sSWClassifyCreate(err)
		if owned || ambiguous {
			t.Fatalf("%v: owned %t ambiguous %t", err, owned, ambiguous)
		}
		if msg := k8sSWFixtureCleanup(owned, ambiguous, drop, "s"); msg != "" || dropped != 0 {
			t.Fatalf("a schema this run did not create is not dropped: %q (drops %d)", msg, dropped)
		}
	}
	// confirmed CREATE, then the setup failed: the schema is this run's and is dropped by the normal cleanup
	owned, ambiguous := k8sSWClassifyCreate(nil)
	if !owned || ambiguous {
		t.Fatalf("a confirmed CREATE: owned %t ambiguous %t", owned, ambiguous)
	}
	if msg := k8sSWFixtureCleanup(owned, ambiguous, drop, "s"); msg != "" || dropped != 1 {
		t.Fatalf("the owned schema is dropped once: %q (drops %d)", msg, dropped)
	}
	if msg := k8sSWFixtureCleanup(true, false, func() error { return errors.New("refused") }, "s"); !strings.Contains(msg, "refused") {
		t.Fatalf("a failed drop of the owned schema is reported: %q", msg)
	}
	// ambiguous CREATE: never dropped, reported as possible residue, and a passing run becomes a FAIL
	dropped = 0
	for _, err := range []error{&net.OpError{Op: "write", Err: errors.New("broken pipe")}, driver.ErrBadConn, mysql.ErrInvalidConn, context.DeadlineExceeded} {
		owned, ambiguous := k8sSWClassifyCreate(err)
		if owned || !ambiguous {
			t.Fatalf("%v: owned %t ambiguous %t", err, owned, ambiguous)
		}
		msg := k8sSWFixtureCleanup(owned, ambiguous, drop, "s")
		if !strings.Contains(msg, "possible SQL fixture residue") || dropped != 0 {
			t.Fatalf("an ambiguous schema is reported, not dropped: %q (drops %d)", msg, dropped)
		}
		if swRunWithClient(nil).conclude(true, []string{msg}) {
			t.Fatal("possible residue must not pass")
		}
	}
	// no database reachable, nothing owned: nothing is tried
	if msg := k8sSWFixtureCleanup(false, false, nil, "s"); msg != "" {
		t.Fatalf("%q", msg)
	}
}

// The failure to remove the probe row is classified, never discarded.
func TestK8SSWRemoveProbe(t *testing.T) {
	var query string
	var args []interface{}
	record := func(err error) func(string, ...interface{}) error {
		return func(q string, a ...interface{}) error { query, args = q, a; return err }
	}
	if class, detail := k8sSWRemoveProbe(record(nil), "s", "tok", true); class != "" || detail != "" {
		t.Fatalf("a removed row: %q %q", class, detail)
	}
	if !strings.Contains(query, "DELETE FROM s.probe WHERE v = ?") || len(args) != 1 || args[0] != "tok" {
		t.Fatalf("the delete stays parameterized: %q %v", query, args)
	}
	for name, c := range map[string]struct {
		err  error
		want k8sSWClass
	}{
		"table gone after the fixture was verified": {&mysql.MySQLError{Number: 1146}, k8sSWProduct},
		"access denied":   {&mysql.MySQLError{Number: 1142}, k8sSWHarness},
		"connection lost": {driver.ErrBadConn, k8sSWInconclusive},
		"unknown":         {errors.New("boom"), k8sSWInconclusive},
	} {
		if class, _ := k8sSWRemoveProbe(record(c.err), "s", "tok", true); class != c.want {
			t.Errorf("%s: class %q, want %q", name, class, c.want)
		}
	}
}

// With a retained claim the replacement must mount it; a different claim fails. A first provisioning
// keeps the explicit "not applicable".
func TestK8SPVCAttachment(t *testing.T) {
	pod := withDataClaim(readyPod("new"), "ns-db1-claim")
	if p := k8sPVCAttachment(true, "ns-db1-claim", &pod, "db1"); p != "" {
		t.Fatalf("the retained claim: %s", p)
	}
	other := withDataClaim(readyPod("new"), "ns-db1-claim-2")
	if p := k8sPVCAttachment(true, "ns-db1-claim", &other, "db1"); !strings.Contains(p, "not the retained claim") {
		t.Fatalf("a replacement on another claim must fail: %q", p)
	}
	noMount := readyPod("new")
	if p := k8sPVCAttachment(true, "ns-db1-claim", &noMount, "db1"); p == "" {
		t.Fatal("an unidentifiable mount must fail")
	}
	if p := k8sPVCAttachment(false, "", &other, "db1"); p != "" {
		t.Fatalf("not applicable: %q", p)
	}
}
