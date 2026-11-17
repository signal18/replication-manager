// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

// The decisions of testK8SSingleWriterLifecycle that do not need the cluster: eligibility, permissions,
// the Pod watch and its synchronisation with a List, the ordering analysis, the observer of a held PVC,
// the classification of failures, the patches and the restoration of what the test changed. They take a
// kubernetes.Interface or plain values, so the unit tests exercise them with a fake clientset.

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	k8sSWTestName        = "testK8SSingleWriterLifecycle"
	k8sSWDisposableLabel = "regtest.replication-manager/disposable"
	k8sSWOwnerLabel      = "regtest.replication-manager/owner"
	k8sSWRunLabel        = "regtest.replication-manager/run"

	// k8sSWGuardBound mirrors the fixed deadline of the PVC-consumer wait of the product (90 s): the
	// guard probe expects the refusal to come after that wait, within a tolerance.
	k8sSWGuardBound = 90 * time.Second
	k8sSWGuardMin   = 60 * time.Second
	k8sSWGuardMax   = 150 * time.Second

	k8sSWPreStopSeconds       = 15 // the temporary delay on the database container (S5, S6)
	k8sSWHolderPreStopSeconds = 20 // the delay of the Terminating holder (S2c)
	k8sSWFixtureRows          = 2000
	k8sSWWait                 = 180 * time.Second
	k8sSWSyncWait             = 30 * time.Second
)

// k8sSWClass is the class of a failure, kept in the log (the runner has no such status). Every class
// but the empty one is a FAIL of the regtest.
type k8sSWClass string

const (
	k8sSWHarness      k8sSWClass = "HARNESS"
	k8sSWResource     k8sSWClass = "RESOURCE"
	k8sSWProduct      k8sSWClass = "PRODUCT"
	k8sSWInconclusive k8sSWClass = "INCONCLUSIVE"
)

// k8sSWEvidenceNote says what the ordering evidence is and is not.
const k8sSWEvidenceNote = "the evidence is the state the kubelet reported to the API server (second resolution): no overlap was observed, a process-level overlap is not excluded"

// k8sSWVerdict is what an analysis found. An empty Class means nothing was found against the guarantee
// and every required observation was made.
type k8sSWVerdict struct {
	Class   k8sSWClass
	Detail  string
	NewPods int
	NewUIDs []types.UID
}

func (v k8sSWVerdict) ok() bool { return v.Class == "" }

func k8sSWVerdictOf(class k8sSWClass, format string, args ...interface{}) k8sSWVerdict {
	return k8sSWVerdict{Class: class, Detail: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------------------------
// Eligibility: decided before any change
// ---------------------------------------------------------------------------------------------

// k8sSWTopologyEligibility returns why the cluster is not eligible, empty when it is.
func k8sSWTopologyEligibility(orchestrator string, servers int) string {
	if orchestrator != config.ConstOrchestratorKubernetes {
		return fmt.Sprintf("the test needs the Kubernetes orchestrator, the cluster uses %q", orchestrator)
	}
	if servers != 1 {
		return fmt.Sprintf("the test needs exactly one database server (no replication is involved), the cluster has %d", servers)
	}
	return ""
}

// k8sSWNamespaceEligibility returns why the namespace may not be used, empty when it carries the
// explicit disposable label. An absent label is never eligible.
func k8sSWNamespaceEligibility(namespace string, labels map[string]string) string {
	if labels[k8sSWDisposableLabel] != "true" {
		return fmt.Sprintf("namespace %s is not marked disposable: the test stops, restarts, patches and reprovisions its database; label the namespace %s=true to allow it", namespace, k8sSWDisposableLabel)
	}
	return ""
}

type k8sSWPermission struct {
	Verb, Group, Resource string
	Cluster               bool // checked without a namespace (the namespace object itself)
}

var k8sSWPermissions = []k8sSWPermission{
	{Verb: "get", Resource: "namespaces", Cluster: true},
	{Verb: "get", Resource: "pods"},
	{Verb: "list", Resource: "pods"},
	{Verb: "watch", Resource: "pods"},
	{Verb: "create", Resource: "pods"},
	{Verb: "delete", Resource: "pods"},
	{Verb: "get", Group: "apps", Resource: "replicasets"},
	{Verb: "list", Group: "apps", Resource: "replicasets"},
	{Verb: "get", Group: "apps", Resource: "deployments"},
	{Verb: "patch", Group: "apps", Resource: "deployments"},
	{Verb: "get", Resource: "persistentvolumeclaims"},
}

// k8sSWMissingPermissions asks the API server what the identity may do (SelfSubjectAccessReview).
// A review that cannot be made is an error (HARNESS), never an empty answer.
func k8sSWMissingPermissions(ctx context.Context, client kubernetes.Interface, namespace string) ([]string, error) {
	var missing []string
	for _, p := range k8sSWPermissions {
		attrs := &authv1.ResourceAttributes{Verb: p.Verb, Group: p.Group, Resource: p.Resource}
		if p.Cluster {
			attrs.Name = namespace
		} else {
			attrs.Namespace = namespace
		}
		review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
			Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attrs},
		}, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("cannot check the permission %s %s: %w", p.Verb, p.Resource, err)
		}
		if !review.Status.Allowed {
			missing = append(missing, p.Verb+" "+strings.Trim(p.Group+"/"+p.Resource, "/"))
		}
	}
	return missing, nil
}

// k8sSWDataMountPath is where the database container mounts its data volume.
const k8sSWDataMountPath = "/var/lib/mysql"

// k8sSWDataClaim resolves the data PVC from the database container's mount of the data directory and the
// Pod volume it names, never from the position of a claim in the template. The topology of this regtest
// has one data claim: no mount, several, or a volume that is not a PVC is a precondition problem.
func k8sSWDataClaim(dep *appsv1.Deployment, server string) (claim, problem string) {
	return k8sSWDataClaimOf(dep.Spec.Template.Spec, server)
}

// k8sSWPodDataClaim is the same resolution on a Pod: the claim its database container really mounts.
func k8sSWPodDataClaim(pod *apiv1.Pod, server string) (claim, problem string) {
	return k8sSWDataClaimOf(pod.Spec, server)
}

func k8sSWDataClaimOf(spec apiv1.PodSpec, server string) (claim, problem string) {
	var container *apiv1.Container
	for i := range spec.Containers {
		if spec.Containers[i].Name == server {
			container = &spec.Containers[i]
		}
	}
	if container == nil {
		return "", fmt.Sprintf("there is no container named %s", server)
	}
	volume := ""
	for _, m := range container.VolumeMounts {
		if m.MountPath != k8sSWDataMountPath {
			continue
		}
		if volume != "" {
			return "", fmt.Sprintf("the container %s mounts %s more than once: the data claim is ambiguous", server, k8sSWDataMountPath)
		}
		volume = m.Name
	}
	if volume == "" {
		return "", fmt.Sprintf("the container %s does not mount %s: the data claim cannot be identified", server, k8sSWDataMountPath)
	}
	for _, v := range spec.Volumes {
		if v.Name != volume {
			continue
		}
		if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName == "" {
			return "", fmt.Sprintf("the volume %s mounted at %s is not a PVC", volume, k8sSWDataMountPath)
		}
		return v.PersistentVolumeClaim.ClaimName, ""
	}
	return "", fmt.Sprintf("the volume %s mounted at %s is not defined in the Pod spec", volume, k8sSWDataMountPath)
}

// k8sSWOwnership checks the owner chain of a Pod: its controller reference names a ReplicaSet, the fetched
// ReplicaSet has that name and UID, and the controller of the ReplicaSet is the Deployment, by kind, name
// and UID. A reference that is not a controller reference does not count. problem says what does not match;
// err is a failed read of the ReplicaSet (a NotFound included), which the caller decides on. Both the
// baseline Pod and every replacement Pod go through it (k8sReplacementPod).
func k8sSWOwnership(pod *apiv1.Pod, depName string, depUID types.UID, getRS func(name string) (*appsv1.ReplicaSet, error)) (problem string, err error) {
	ref := metav1.GetControllerOf(pod)
	if ref == nil || ref.Kind != "ReplicaSet" || ref.Name == "" {
		return fmt.Sprintf("the Pod %s is not controlled by a ReplicaSet", pod.Name), nil
	}
	rs, err := getRS(ref.Name)
	if err != nil {
		return "", err
	}
	if rs.UID == "" || rs.UID != ref.UID || rs.Name != ref.Name {
		return fmt.Sprintf("the ReplicaSet %s (uid %s) is not the one the Pod %s names (%s, uid %s)", rs.Name, rs.UID, pod.Name, ref.Name, ref.UID), nil
	}
	owner := metav1.GetControllerOf(rs)
	if owner == nil || owner.Kind != "Deployment" || owner.Name != depName || owner.UID != depUID || depUID == "" {
		return fmt.Sprintf("the ReplicaSet %s is not controlled by the Deployment %s (uid %s)", rs.Name, depName, depUID), nil
	}
	return "", nil
}

// k8sSWBaselinePod requires, before anything is changed, exactly one database Pod (selected by the tag
// label, whatever its state), running, ready, not terminating, owned by the expected Deployment through its
// ReplicaSet (k8sSWOwnership) and mounting, in its database container, the data claim the Deployment
// template names. An ambiguous start is rejected, no Pod is picked among several.
func k8sSWBaselinePod(pods []apiv1.Pod, server, claim string, dep *appsv1.Deployment, getRS func(name string) (*appsv1.ReplicaSet, error)) (*apiv1.Pod, string) {
	if len(pods) != 1 {
		return nil, fmt.Sprintf("%d Pod(s) carry the label tag=%s, exactly one is required", len(pods), server)
	}
	pod := &pods[0]
	switch {
	case pod.DeletionTimestamp != nil:
		return nil, fmt.Sprintf("the Pod %s is terminating", pod.Name)
	case pod.Status.Phase != apiv1.PodRunning || !k8sSWDBReady(pod, server):
		return nil, fmt.Sprintf("the Pod %s is not running with a ready database container", pod.Name)
	}
	problem, err := k8sSWOwnership(pod, dep.Name, dep.UID, getRS)
	if err != nil {
		return nil, fmt.Sprintf("cannot read the ReplicaSet of the Pod %s: %s", pod.Name, err)
	}
	if problem != "" {
		return nil, problem
	}
	if mounted, problem := k8sSWPodDataClaim(pod, server); problem != "" || mounted != claim {
		return nil, fmt.Sprintf("the Pod %s does not mount the data claim %s of the Deployment (%q: %s)", pod.Name, claim, mounted, problem)
	}
	return pod, ""
}

// k8sSWStoppedProblem says why the Deployment is not, at this moment, in the state the next mutation of
// S3 requires, empty when it is: replicas explicitly zero, no database Pod at all (terminating ones
// included), and the strategy the step expects. It is a point-in-time read: it does not stop another actor
// from changing the cluster afterwards, the disposable namespace and the exclusive use of it by the test
// are operational preconditions.
func k8sSWStoppedProblem(dep *appsv1.Deployment, pods []apiv1.Pod, wantType appsv1.DeploymentStrategyType, wantRollingUpdate bool) string {
	switch {
	case dep.Spec.Replicas == nil:
		return "spec.replicas is not set (the API default is one)"
	case *dep.Spec.Replicas != 0:
		return fmt.Sprintf("spec.replicas is %d, not 0", *dep.Spec.Replicas)
	case len(pods) > 0:
		return fmt.Sprintf("%d database Pod(s) still exist (%s)", len(pods), pods[0].Name)
	case dep.Spec.Strategy.Type != wantType || (dep.Spec.Strategy.RollingUpdate != nil) != wantRollingUpdate:
		return fmt.Sprintf("the strategy is %s (rollingUpdate set: %t), not %s (rollingUpdate set: %t)", dep.Spec.Strategy.Type, dep.Spec.Strategy.RollingUpdate != nil, wantType, wantRollingUpdate)
	}
	return ""
}

// k8sSWForeignConsumers returns the Pods that reference the data claim and are not the baseline database
// Pod, whatever their labels or owners: any of them matters to the single-writer guard.
func k8sSWForeignConsumers(pods []apiv1.Pod, claim string, baseline types.UID) []string {
	var foreign []string
	for i := range pods {
		p := &pods[i]
		if p.UID == baseline {
			continue
		}
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim {
				foreign = append(foreign, p.Name)
				break
			}
		}
	}
	return foreign
}

// k8sSWDBContainerIndex is the index of the database container (named after the server) in the Pod
// template, -1 when the Deployment has none.
func k8sSWDBContainerIndex(dep *appsv1.Deployment, server string) int {
	for i, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == server {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------------------------
// Pod events, the snapshot of the old Pods and the ordering analysis
// ---------------------------------------------------------------------------------------------

const k8sSWSync watch.EventType = "SYNC"

// k8sSWPodEvent is what the watch (or a List) saw of one Pod at one moment.
type k8sSWPodEvent struct {
	Type         watch.EventType
	UID          types.UID
	Name         string
	Node         string
	Deleting     bool
	DBRunning    bool
	DBTerminated bool
	DBStarted    time.Time
	DBFinished   time.Time
}

// k8sSWEventOf reads the database container (named after the server) of a Pod.
func k8sSWEventOf(t watch.EventType, pod *apiv1.Pod, server string) k8sSWPodEvent {
	e := k8sSWPodEvent{Type: t, UID: pod.UID, Name: pod.Name, Node: pod.Spec.NodeName, Deleting: pod.DeletionTimestamp != nil}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != server {
			continue
		}
		if cs.State.Running != nil {
			e.DBRunning = true
			e.DBStarted = cs.State.Running.StartedAt.Time
		}
		if cs.State.Terminated != nil {
			e.DBTerminated = true
			e.DBStarted = cs.State.Terminated.StartedAt.Time
			e.DBFinished = cs.State.Terminated.FinishedAt.Time
		}
	}
	return e
}

// k8sSWOldPod is an identity that existed before an operation, with the state of its database
// container read from the API (not from the watch): a Pod that ran must be seen to end.
type k8sSWOldPod struct {
	UID       types.UID
	Name      string
	Node      string
	DBRunning bool
}

func k8sSWSnapshotOf(pods []apiv1.Pod, server string) []k8sSWOldPod {
	out := make([]k8sSWOldPod, 0, len(pods))
	for i := range pods {
		e := k8sSWEventOf(k8sSWSync, &pods[i], server)
		out = append(out, k8sSWOldPod{UID: e.UID, Name: e.Name, Node: e.Node, DBRunning: e.DBRunning})
	}
	return out
}

type k8sSWTrack struct {
	first, end int // index in the window of the first event, of the first event that ended the database process (-1: none)
	added      bool
	deleting   bool
	node       string
	started    time.Time
	ended      time.Time
}

// k8sSWAnalyzeWindow judges the events of one operation (from index mark on) against the identities
// that existed before it (old, read from the API) and the number of replacement Pods the operation
// must produce: expectNew is 0 (nothing may be replaced) or 1.
//
// Every observation the verdict rests on is required, and a missing one is INCONCLUSIVE, never a pass:
//   - expectNew 1: exactly one new Pod was seen ADDED, the database container of every old Pod that was
//     running was seen to end (terminated or deleted) before that Pod appeared, the end carries a
//     finish timestamp and the new database container a start timestamp;
//   - expectNew 0: no new Pod, and no old running Pod terminating or deleted.
//
// The timestamps are the kubelet's, with a one second resolution. They are compared only when both Pods
// ran on the same node: across nodes a contradiction is INCONCLUSIVE (the clocks may differ). The
// verdict is no evidence of an overlap in the states reported to the API (k8sSWEvidenceNote), not a
// proof that two database processes never ran together.
func k8sSWAnalyzeWindow(events []k8sSWPodEvent, mark int, old []k8sSWOldPod, expectNew int) k8sSWVerdict {
	if mark < 0 || mark > len(events) {
		return k8sSWVerdictOf(k8sSWInconclusive, "the start of the observation window (%d) is outside the %d recorded events", mark, len(events))
	}
	window := events[mark:]
	oldByUID := map[types.UID]k8sSWOldPod{}
	for _, o := range old {
		oldByUID[o.UID] = o
	}
	// the Pods reported running by the events of the window: an old Pod the snapshot showed running but
	// no event ended is not counted here, its end is a required observation (INCONCLUSIVE below)
	running := map[types.UID]bool{}
	maxRunning := 0
	tracks := map[types.UID]*k8sSWTrack{}
	var order []types.UID
	for i, e := range window {
		t := tracks[e.UID]
		if t == nil {
			t = &k8sSWTrack{first: i, end: -1, added: e.Type == watch.Added}
			tracks[e.UID] = t
			order = append(order, e.UID)
		}
		if e.Node != "" {
			t.node = e.Node
		}
		if e.Deleting {
			t.deleting = true
		}
		if !e.DBStarted.IsZero() {
			t.started = e.DBStarted
		}
		if !e.DBFinished.IsZero() {
			t.ended = e.DBFinished
		}
		if (e.DBTerminated || e.Type == watch.Deleted) && t.end < 0 {
			t.end = i
		}
		if e.Type == watch.Deleted || e.DBTerminated || !e.DBRunning {
			delete(running, e.UID)
		} else {
			running[e.UID] = true
		}
		if len(running) > maxRunning {
			maxRunning = len(running)
		}
	}
	var newUIDs []types.UID
	for _, uid := range order {
		if _, isOld := oldByUID[uid]; isOld {
			continue
		}
		if !tracks[uid].added {
			return k8sSWVerdictOf(k8sSWInconclusive, "the watch reported the Pod %s, which was not in the snapshot taken before the operation, without having seen it created", uid)
		}
		newUIDs = append(newUIDs, uid)
	}
	v := k8sSWVerdict{NewPods: len(newUIDs), NewUIDs: newUIDs}
	if maxRunning > 1 {
		v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("%d database containers were reported running at the same time", maxRunning)
		return v
	}
	oldRunning := 0
	for _, o := range old {
		if o.DBRunning {
			oldRunning++
		}
	}
	switch expectNew {
	case 0:
		if len(newUIDs) > 0 {
			v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("%d new Pod(s) appeared where none was expected", len(newUIDs))
			return v
		}
		for _, o := range old {
			if t := tracks[o.UID]; o.DBRunning && t != nil && (t.end >= 0 || t.deleting) {
				v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("the running Pod %s was terminated or deleted where nothing was expected to change", o.Name)
				return v
			}
		}
		v.Detail = fmt.Sprintf("no replacement: %d running Pod(s) unchanged and no new Pod", oldRunning)
		return v
	case 1:
	default:
		return k8sSWVerdictOf(k8sSWInconclusive, "an expectation of %d replacement Pods is not supported", expectNew)
	}
	if len(newUIDs) == 0 {
		return k8sSWVerdictOf(k8sSWInconclusive, "no replacement Pod was seen created by the watch: the replacement cannot be tied to this operation")
	}
	if len(newUIDs) > 1 {
		v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("%d new Pods appeared where one replacement was expected", len(newUIDs))
		return v
	}
	n := tracks[newUIDs[0]]
	if n.started.IsZero() {
		v.Class, v.Detail = k8sSWInconclusive, "the start time of the database container of the new Pod was not reported"
		return v
	}
	for _, o := range old {
		if !o.DBRunning {
			continue
		}
		t := tracks[o.UID]
		switch {
		case t == nil || t.end < 0:
			v.Class, v.Detail = k8sSWInconclusive, fmt.Sprintf("the end of the database container of the earlier Pod %s was not observed, so the order against the new Pod cannot be shown", o.Name)
			return v
		case t.ended.IsZero():
			v.Class, v.Detail = k8sSWInconclusive, fmt.Sprintf("the finish time of the database container of the earlier Pod %s was not reported (a likely false negative to watch for at runtime)", o.Name)
			return v
		case t.end > n.first:
			v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("the new Pod appeared before the database container of the earlier Pod %s was seen terminated or the Pod deleted", o.Name)
			return v
		case n.started.Before(t.ended):
			if t.node != "" && t.node == n.node {
				v.Class, v.Detail = k8sSWProduct, fmt.Sprintf("on node %s the database container of the new Pod started at %s, before the earlier Pod %s finished at %s", t.node, n.started.UTC().Format(time.RFC3339), o.Name, t.ended.UTC().Format(time.RFC3339))
			} else {
				v.Class, v.Detail = k8sSWInconclusive, fmt.Sprintf("the start time of the new Pod precedes the finish time of the earlier Pod %s, but they ran on different nodes (%q, %q): the clocks may differ", o.Name, n.node, t.node)
			}
			return v
		}
	}
	v.Detail = fmt.Sprintf("one new Pod created after the database container of %d earlier Pod(s) was seen to end; %s", oldRunning, k8sSWEvidenceNote)
	return v
}

// sameFacts says whether two observations of a Pod agree on everything the analysis reads.
func (e k8sSWPodEvent) sameFacts(o k8sSWPodEvent) bool {
	return e.Node == o.Node && e.Deleting == o.Deleting && e.DBRunning == o.DBRunning && e.DBTerminated == o.DBTerminated &&
		e.DBStarted.Equal(o.DBStarted) && e.DBFinished.Equal(o.DBFinished)
}

// k8sSWSynced says why the events of a watch have not caught up with a List taken after them, empty when
// they have: every listed Pod is, in the last event the watch delivered for it, in the state the List
// shows (the facts the analysis reads: node, terminating, database container running or terminated and its
// timestamps), and every Pod the watch still holds alive is in the List, the others having been seen
// DELETED. The resourceVersions are not compared: they are opaque, and any unrelated update of a Pod
// changes them.
func k8sSWSynced(events []k8sSWPodEvent, listed []apiv1.Pod, server string) string {
	last := map[types.UID]k8sSWPodEvent{}
	for _, e := range events {
		last[e.UID] = e
	}
	inList := map[types.UID]bool{}
	for i := range listed {
		p := &listed[i]
		inList[p.UID] = true
		e, ok := last[p.UID]
		switch {
		case !ok:
			return fmt.Sprintf("the Pod %s is in the List but the watch has not reported it", p.Name)
		case e.Type == watch.Deleted:
			return fmt.Sprintf("the Pod %s is in the List but the watch reported it deleted", p.Name)
		case !e.sameFacts(k8sSWEventOf(k8sSWSync, p, server)):
			return fmt.Sprintf("the Pod %s is in a state in the List that the watch has not delivered yet", p.Name)
		}
	}
	for uid, e := range last {
		if e.Type != watch.Deleted && !inList[uid] {
			return fmt.Sprintf("the Pod %s is gone from the List and its deletion was not yet delivered by the watch", e.Name)
		}
	}
	return ""
}

// k8sSWWatcher records the Pods of the database from before an operation to its end.
type k8sSWWatcher struct {
	mu     sync.Mutex
	events []k8sSWPodEvent
	err    error
	stop   context.CancelFunc
	wg     sync.WaitGroup
	server string
}

func startK8sSWWatcher(client kubernetes.Interface, namespace, server string) (*k8sSWWatcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	selector := "tag=" + server
	lctx, lcancel := context.WithTimeout(ctx, 20*time.Second)
	list, err := client.CoreV1().Pods(namespace).List(lctx, metav1.ListOptions{LabelSelector: selector})
	lcancel()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("list pods to start the watch: %w", err)
	}
	w := &k8sSWWatcher{stop: cancel, server: server}
	for i := range list.Items {
		w.add(k8sSWSync, &list.Items[i])
	}
	wi, err := client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{LabelSelector: selector, ResourceVersion: list.ResourceVersion, AllowWatchBookmarks: true})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("watch pods: %w", err)
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer wi.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-wi.ResultChan():
				if !ok {
					if ctx.Err() == nil {
						w.fail(errors.New("the Pod watch stream ended"))
					}
					return
				}
				switch ev.Type {
				case watch.Bookmark:
					continue
				case watch.Error:
					w.fail(fmt.Errorf("the Pod watch reported an error: %v", apierrors.FromObject(ev.Object)))
					return
				}
				if pod, isPod := ev.Object.(*apiv1.Pod); isPod {
					w.add(ev.Type, pod)
				}
			}
		}
	}()
	return w, nil
}

func (w *k8sSWWatcher) add(t watch.EventType, pod *apiv1.Pod) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, k8sSWEventOf(t, pod, w.server))
}

func (w *k8sSWWatcher) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = err
	}
}

// mark is the index to give to k8sSWAnalyzeWindow: the operation starts here.
func (w *k8sSWWatcher) mark() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.events)
}

func (w *k8sSWWatcher) snapshot() ([]k8sSWPodEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]k8sSWPodEvent(nil), w.events...), w.err
}

// waitSynced waits, within timeout, until the watch has delivered what a List shows (k8sSWSynced). It
// replaces a fixed delay: the events analysed afterwards are known to include the state the API holds.
func (w *k8sSWWatcher) waitSynced(list func() ([]apiv1.Pod, error), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	reason := "not checked"
	for {
		events, werr := w.snapshot()
		if werr != nil {
			return fmt.Errorf("the Pod watch was lost: %w", werr)
		}
		pods, err := list()
		if err != nil {
			reason = "the List failed: " + err.Error()
		} else if reason = k8sSWSynced(events, pods, w.server); reason == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the watch did not catch up with the List within %s: %s", timeout, reason)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (w *k8sSWWatcher) Stop() {
	w.stop()
	w.wg.Wait()
}

// ---------------------------------------------------------------------------------------------
// The held PVC: one observer for S2, S2c, S5 and S6
// ---------------------------------------------------------------------------------------------

// k8sSWWindow is what the polls made while a Start waited found. A poll reads the Deployment and the
// database Pods first and the held Pod last: a Pod that is still there at the later read was there at the
// earlier ones (a deleted Pod does not come back), so a scale-up or a second Pod seen before it is a
// violation of the guard. The polls are periodic samples, not a continuous observation: a failed poll is
// recorded, and the longest stretch without a successful poll (from the call of Start to its return) must
// stay short, or the evidence is insufficient (coverage). The watch of the run complements them.
type k8sSWWindow struct {
	mu          sync.Mutex
	now         func() time.Time // the clock, replaceable in tests
	polls       int              // polls that completed
	present     int              // after Start was invoked, held Pod present, zero replicas, no other database Pod
	terminating int              // the same, with the held Pod Terminating
	fails       int              // polls that failed to read
	lastErr     string
	invokedAt   time.Time
	lastOK      time.Time
	maxGap      time.Duration
	violation   string
}

// k8sSWMaxPollGap is the longest stretch without a successful poll that still counts as observed.
const k8sSWMaxPollGap = 10 * time.Second

func (w *k8sSWWindow) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// markInvoked records the moment Start is called.
func (w *k8sSWWindow) markInvoked() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invokedAt = w.clock()
	w.lastOK = w.invokedAt
}

func (w *k8sSWWindow) failed(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fails++
	w.lastErr = err.Error()
}

func (w *k8sSWWindow) judge(invoked bool, replicas int32, others int, held *apiv1.Pod, heldUID types.UID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.polls++
	if invoked && !w.invokedAt.IsZero() {
		now := w.clock()
		if gap := now.Sub(w.lastOK); gap > w.maxGap {
			w.maxGap = gap
		}
		w.lastOK = now
	}
	if held == nil || held.UID != heldUID {
		return
	}
	if replicas > 0 || others > 0 {
		if w.violation == "" {
			w.violation = fmt.Sprintf("the Deployment was at %d replica(s) with %d other database Pod(s) while the Pod %s still referenced the PVC", replicas, others, held.Name)
		}
		return
	}
	if invoked {
		w.present++
		if held.DeletionTimestamp != nil {
			w.terminating++
		}
	}
}

func (w *k8sSWWindow) result() (polls, present, terminating int, violation string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.polls, w.present, w.terminating, w.violation
}

// coverage says why the polls are not enough evidence for the interval that ended at end, empty when they
// are: Start was marked, and no stretch from the call to the return without a successful poll exceeds
// k8sSWMaxPollGap.
func (w *k8sSWWindow) coverage(end time.Time) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.invokedAt.IsZero() {
		return "the call of Start was not recorded"
	}
	gap := w.maxGap
	if tail := end.Sub(w.lastOK); tail > gap {
		gap = tail
	}
	if gap > k8sSWMaxPollGap {
		return fmt.Sprintf("the polls left a stretch of %s without a successful observation (limit %s; %d poll(s) failed, last error: %q)", gap.Round(time.Millisecond), k8sSWMaxPollGap, w.fails, w.lastErr)
	}
	return ""
}

// k8sSWPollOnce makes one poll of the window. A failed read is recorded in the window and returned, and
// counts as no poll.
func k8sSWPollOnce(client kubernetes.Interface, namespace, server, held string, heldUID types.UID, invoked bool, w *k8sSWWindow) (err error) {
	defer func() {
		if err != nil {
			w.failed(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, server, metav1.GetOptions{})
	if err != nil {
		return err
	}
	list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "tag=" + server})
	if err != nil {
		return err
	}
	others := 0
	for i := range list.Items {
		if list.Items[i].UID != heldUID {
			others++
		}
	}
	heldPod, err := client.CoreV1().Pods(namespace).Get(ctx, held, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		heldPod, err = nil, nil
	} else if err != nil {
		return err
	}
	replicas := int32(1) // nil is the API default of one
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	w.judge(invoked, replicas, others, heldPod, heldUID)
	return nil
}

// k8sSWAwaitInitial requires, before Start is called, one successful observation of the starting state:
// the held Pod present, zero replicas, no other database Pod. It retries within timeout; without it Start
// is not called.
func k8sSWAwaitInitial(client kubernetes.Interface, namespace, server, held string, heldUID types.UID, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		w := &k8sSWWindow{}
		err := k8sSWPollOnce(client, namespace, server, held, heldUID, true, w)
		_, present, _, violation := w.result()
		switch {
		case err != nil:
			last = err
		case violation != "":
			return fmt.Errorf("the starting state already shows a violation: %s", violation)
		case present < 1:
			last = errors.New("the held Pod is not present with zero replicas and no database Pod")
		default:
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no successful initial observation within %s: %w", timeout, last)
		}
		time.Sleep(interval)
	}
}

// ---------------------------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------------------------

// k8sSWClassifyGuardProbe judges the Start made while a test-owned Pod holds the PVC. The guard
// fails closed: Start returns an error naming the holder after the bounded wait, nothing is scaled up.
func k8sSWClassifyGuardProbe(startErr error, elapsed time.Duration, replicas int32, dbPods int, holder string) k8sSWVerdict {
	if startErr == nil {
		return k8sSWVerdictOf(k8sSWProduct, "Start returned success after %s while the Pod %s still referenced the PVC: the PVC-consumer guard is absent", elapsed.Round(time.Second), holder)
	}
	if replicas != 0 || dbPods > 0 {
		return k8sSWVerdictOf(k8sSWProduct, "Start returned an error but the Deployment has %d replica(s) and %d database Pod(s): the scale-up was sent despite the refusal (%s)", replicas, dbPods, startErr)
	}
	msg := startErr.Error()
	if apierrors.IsForbidden(startErr) || strings.Contains(msg, "not allowed to list Pods") || strings.Contains(msg, "forbidden") {
		return k8sSWVerdictOf(k8sSWHarness, "the identity of replication-manager lacks a permission the guard needs: %s", msg)
	}
	if !strings.Contains(msg, "still use PVC") {
		return k8sSWVerdictOf(k8sSWInconclusive, "Start was refused for a reason other than the PVC-consumer guard: %s", msg)
	}
	if !strings.Contains(msg, holder) {
		return k8sSWVerdictOf(k8sSWInconclusive, "Start was refused by the guard but the error does not name the holder Pod %s: %s", holder, msg)
	}
	if elapsed < k8sSWGuardMin {
		return k8sSWVerdictOf(k8sSWInconclusive, "Start was refused after %s, not through the bounded wait of about %s", elapsed.Round(time.Second), k8sSWGuardBound)
	}
	if elapsed > k8sSWGuardMax {
		return k8sSWVerdictOf(k8sSWProduct, "the guard wait lasted %s, beyond its bound of %s", elapsed.Round(time.Second), k8sSWGuardBound)
	}
	return k8sSWVerdict{Detail: fmt.Sprintf("Start failed closed after %s naming the holder, replicas 0, no database Pod", elapsed.Round(time.Second))}
}

// k8sSWClassifyStartError classifies a Start that returned an error where a success was expected. It is a
// product failure only when the test established a violation: a scale-up although the Start refused. A
// refusal of the guard while an earlier Pod still holds the PVC is the guard working (the Pod may be stuck
// Terminating): INCONCLUSIVE, with the reason.
func k8sSWClassifyStartError(err error, replicas int32, otherDBPods int, holderStillThere bool) k8sSWVerdict {
	if replicas != 0 || otherDBPods > 0 {
		return k8sSWVerdictOf(k8sSWProduct, "Start returned an error but the Deployment has %d replica(s) and %d other database Pod(s): the scale-up was sent despite the refusal (%s)", replicas, otherDBPods, err)
	}
	msg := err.Error()
	switch {
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || strings.Contains(msg, "not allowed to list Pods") || strings.Contains(msg, "not allowed to read"):
		return k8sSWVerdictOf(k8sSWHarness, "the identity of replication-manager lacks a permission: %s", msg)
	case strings.Contains(msg, "still use PVC") && holderStillThere:
		return k8sSWVerdictOf(k8sSWInconclusive, "the guard refused while the earlier Pod still held the PVC (stuck Terminating or a slow shutdown): the guard worked, the scenario did not complete: %s", msg)
	case strings.Contains(msg, "still use PVC"):
		return k8sSWVerdictOf(k8sSWInconclusive, "the guard refused although the earlier Pod is gone now (timing, or another Pod uses the PVC): %s", msg)
	}
	return k8sSWVerdictOf(k8sSWInconclusive, "Start failed and the cause is not established: %s", msg)
}

// k8sSWClassifySQLError classifies an error of the database by its structure: the MySQL error number or
// the kind of error, never its text. A table or schema that is gone after the fixture was verified is a
// data-integrity failure; a refused permission is the harness; a connection or deadline error is
// inconclusive.
func k8sSWClassifySQLError(err error, fixtureVerified bool) (k8sSWClass, string) {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1146, 1049: // ER_NO_SUCH_TABLE, ER_BAD_DB_ERROR
			if fixtureVerified {
				return k8sSWProduct, fmt.Sprintf("the fixture is gone after it was verified (MySQL error %d: %s)", me.Number, me.Message)
			}
			return k8sSWHarness, fmt.Sprintf("the fixture object does not exist (MySQL error %d: %s)", me.Number, me.Message)
		case 1044, 1045, 1142, 1143, 1227: // access denied
			return k8sSWHarness, fmt.Sprintf("the database refused the test user (MySQL error %d: %s)", me.Number, me.Message)
		}
		return k8sSWInconclusive, fmt.Sprintf("the database returned MySQL error %d: %s", me.Number, me.Message)
	}
	var ne net.Error
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) {
		return k8sSWInconclusive, "the connection to the database failed: " + err.Error()
	}
	return k8sSWInconclusive, "the database call failed and the cause is not established: " + err.Error()
}

// k8sSWJudgeHeldStart judges a Start that was called while a Pod still referenced the PVC (S2c, S5, S6):
// from the violation the polls saw and whether their coverage of the interval was sufficient, the verdict of the Start itself (nil when it succeeded), whether the
// held Pod still existed when Start returned, and how many polls made after the call found it
// Terminating with zero replicas. A Terminating window that was not exercised is INCONCLUSIVE.
func k8sSWJudgeHeldStart(startVerdict *k8sSWVerdict, violation, coverage string, heldStillThere bool, terminatingPolls int, held string) k8sSWVerdict {
	switch {
	case violation != "":
		return k8sSWVerdictOf(k8sSWProduct, "%s", violation)
	case startVerdict == nil && heldStillThere:
		return k8sSWVerdictOf(k8sSWProduct, "Start returned success while the Pod %s still referenced the PVC", held)
	case startVerdict != nil:
		return *startVerdict
	case coverage != "":
		return k8sSWVerdictOf(k8sSWInconclusive, "the Start made while %s held the PVC was not observed closely enough: %s", held, coverage)
	case terminatingPolls < 1:
		return k8sSWVerdictOf(k8sSWInconclusive, "no poll made after Start was invoked found %s Terminating with zero replicas: the Terminating window was not exercised", held)
	}
	return k8sSWVerdict{Detail: fmt.Sprintf("Start waited for %s: %d poll(s) saw it Terminating with zero replicas and no other database Pod, and it was gone when Start returned", held, terminatingPolls)}
}

// k8sSWPodTrouble recognizes a Pod the cluster could not run (a RESOURCE failure, not a product one).
func k8sSWPodTrouble(pod *apiv1.Pod) string {
	if pod.Status.Reason == "Evicted" {
		return "Pod " + pod.Name + " was evicted"
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == apiv1.PodScheduled && c.Status == apiv1.ConditionFalse && c.Reason == apiv1.PodReasonUnschedulable {
			return "Pod " + pod.Name + " is unschedulable: " + c.Message
		}
	}
	all := append(append([]apiv1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, cs := range all {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull":
				return fmt.Sprintf("Pod %s container %s cannot get its image (%s)", pod.Name, cs.Name, w.Reason)
			}
		}
		for _, st := range []*apiv1.ContainerStateTerminated{cs.State.Terminated, cs.LastTerminationState.Terminated} {
			if st != nil && st.Reason == "OOMKilled" {
				return fmt.Sprintf("Pod %s container %s was OOMKilled", pod.Name, cs.Name)
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------------------------
// Patches and the restoration of what the test changed
// ---------------------------------------------------------------------------------------------

// k8sSWLegacyStrategyPatch sets the strategy a Deployment created by an earlier release has. It
// touches nothing of the Pod template, so it starts no rollout.
func k8sSWLegacyStrategyPatch() []byte {
	return []byte(`{"spec":{"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":"25%","maxUnavailable":"25%"}}}}`)
}

// k8sSWRecreateStrategyPatch is the strategy-only patch that restores the safe strategy.
func k8sSWRecreateStrategyPatch() []byte {
	return []byte(`{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}`)
}

func k8sSWIsRecreate(dep *appsv1.Deployment) bool {
	return dep.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType && dep.Spec.Strategy.RollingUpdate == nil
}

// k8sSWTemplatePatchRefusal says why a patch of the Pod template must not be sent: under the legacy
// RollingUpdate a template change surges a second database Pod next to the old one. Empty when safe.
func k8sSWTemplatePatchRefusal(dep *appsv1.Deployment) string {
	if !k8sSWIsRecreate(dep) {
		return fmt.Sprintf("the Deployment %s uses strategy %q (rollingUpdate set: %t): a change of its Pod template would start a second database Pod before the first stops", dep.Name, dep.Spec.Strategy.Type, dep.Spec.Strategy.RollingUpdate != nil)
	}
	return ""
}

// k8sSWDesiredWithPreStop is the original lifecycle of the database container with the temporary delay
// as its preStop hook.
func k8sSWDesiredWithPreStop(orig *apiv1.Lifecycle, seconds int) *apiv1.Lifecycle {
	lc := &apiv1.Lifecycle{}
	if orig != nil {
		lc = orig.DeepCopy()
	}
	lc.PreStop = &apiv1.LifecycleHandler{Exec: &apiv1.ExecAction{Command: []string{"sh", "-c", fmt.Sprintf("sleep %d", seconds)}}}
	return lc
}

type k8sSWJSONOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// k8sSWLifecycleJSONPatch builds the JSON Patch that makes the lifecycle of the database container of
// dep equal to desired, exactly (a merge patch would keep fields of the previous handler). The first
// operation tests the container name, so a patch never lands on another container. needed is false,
// and there is no patch, when the lifecycle already is desired.
func k8sSWLifecycleJSONPatch(dep *appsv1.Deployment, server string, desired *apiv1.Lifecycle) (patch []byte, needed bool, err error) {
	i := k8sSWDBContainerIndex(dep, server)
	if i < 0 {
		return nil, false, fmt.Errorf("the Deployment %s has no container named %s", dep.Name, server)
	}
	current := dep.Spec.Template.Spec.Containers[i].Lifecycle
	if equality.Semantic.DeepEqual(current, desired) {
		return nil, false, nil
	}
	base := fmt.Sprintf("/spec/template/spec/containers/%d", i)
	ops := []k8sSWJSONOp{{Op: "test", Path: base + "/name", Value: server}}
	switch {
	case desired == nil:
		ops = append(ops, k8sSWJSONOp{Op: "remove", Path: base + "/lifecycle"})
	case current == nil:
		ops = append(ops, k8sSWJSONOp{Op: "add", Path: base + "/lifecycle", Value: desired})
	default:
		ops = append(ops, k8sSWJSONOp{Op: "replace", Path: base + "/lifecycle", Value: desired})
	}
	patch, err = json.Marshal(ops)
	return patch, true, err
}

// k8sSWSnap is the state the test restores: the strategy and the lifecycle of the database container
// as they were before the test changed anything.
type k8sSWSnap struct {
	Strategy  appsv1.DeploymentStrategy
	Lifecycle *apiv1.Lifecycle
}

// k8sSWCleanupEnv is what the restoration needs from the run.
type k8sSWCleanupEnv struct {
	Client   kubernetes.Interface
	NS       string
	Server   string
	Pods     func() ([]apiv1.Pod, error)
	Replaced func(old map[types.UID]bool) error // waits for a replacement of the Pods in old
}

// k8sSWRestore inspects the Deployment as it is and restores what the test changed. The flags say what
// the test attempted (set before the patch was sent: a patch may be applied although the client got an
// error); they never say what the Deployment holds, which is read. The strategy comes first: a template
// change under RollingUpdate would start a second database Pod, so with the strategy not restored no
// template patch is sent at all. A lifecycle that is already the original is left alone and nothing is
// waited for. A rollout is requested only when the template differs and, with replicas, only when the
// Pod state is certain (one ready, not terminating Pod). Nothing here starts a database: replicas stay.
// The strategy is restored to Recreate, the safe one, whatever it was.
func k8sSWRestore(env k8sSWCleanupEnv, orig k8sSWSnap, strategyAttempted, lifecycleAttempted bool) (done, left []string) {
	get := func() (*appsv1.Deployment, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return env.Client.AppsV1().Deployments(env.NS).Get(ctx, env.Server, metav1.GetOptions{})
	}
	patch := func(pt types.PatchType, data []byte) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := env.Client.AppsV1().Deployments(env.NS).Patch(ctx, env.Server, pt, data, metav1.PatchOptions{})
		return err
	}
	if !strategyAttempted && !lifecycleAttempted {
		return nil, nil
	}
	dep, err := get()
	if err != nil {
		return nil, []string{fmt.Sprintf("the Deployment %s cannot be read, so what the test changed (strategy attempted: %t, lifecycle attempted: %t) was not inspected: %s", env.Server, strategyAttempted, lifecycleAttempted, err)}
	}
	if strategyAttempted {
		if k8sSWIsRecreate(dep) {
			done = append(done, "the strategy is Recreate")
		} else {
			perr := patch(types.StrategicMergePatchType, k8sSWRecreateStrategyPatch())
			after, gerr := get()
			switch {
			case gerr == nil && k8sSWIsRecreate(after):
				dep = after
				done = append(done, fmt.Sprintf("the strategy was restored to Recreate (patch error: %v)", perr))
			default:
				return done, append(left, fmt.Sprintf("the strategy of %s is not Recreate and could not be restored (%v, %v): no template patch was sent; restore it with the patch %s", env.Server, perr, gerr, k8sSWRecreateStrategyPatch()))
			}
		}
	}
	if !lifecycleAttempted {
		return done, left
	}
	if reason := k8sSWTemplatePatchRefusal(dep); reason != "" {
		return done, append(left, reason+"; the lifecycle was not restored")
	}
	lcPatch, needed, err := k8sSWLifecycleJSONPatch(dep, env.Server, orig.Lifecycle)
	if err != nil {
		return done, append(left, "the lifecycle cannot be compared: "+err.Error())
	}
	if !needed {
		return append(done, "the lifecycle of the database container is the original: nothing to restore, no rollout"), left
	}
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	old := map[types.UID]bool{}
	if replicas != 0 {
		pods, perr := env.Pods()
		if perr != nil || len(pods) != 1 || pods[0].DeletionTimestamp != nil || !k8sSWDBReady(&pods[0], env.Server) {
			return done, append(left, fmt.Sprintf("the lifecycle of %s was not restored: the state of the database Pod is uncertain, and a rollout needs one ready Pod (restore it with the JSON patch %s)", env.Server, lcPatch))
		}
		old[pods[0].UID] = true
	}
	perr := patch(types.JSONPatchType, lcPatch)
	after, gerr := get()
	if gerr != nil || k8sSWDBContainerIndex(after, env.Server) < 0 || !equality.Semantic.DeepEqual(after.Spec.Template.Spec.Containers[k8sSWDBContainerIndex(after, env.Server)].Lifecycle, orig.Lifecycle) {
		return done, append(left, fmt.Sprintf("the lifecycle of %s was not restored (%v, %v); restore it with the JSON patch %s", env.Server, perr, gerr, lcPatch))
	}
	done = append(done, fmt.Sprintf("the lifecycle was restored exactly (patch error: %v)", perr))
	if replicas != 0 {
		if rerr := env.Replaced(old); rerr != nil {
			return done, append(left, "the rollout of the restored lifecycle did not complete: "+rerr.Error())
		}
		done = append(done, "the rollout of the restored lifecycle completed")
	}
	return done, left
}

// ---------------------------------------------------------------------------------------------
// The holder Pod and the fixture
// ---------------------------------------------------------------------------------------------

// k8sSWClassifyCreate says what a CREATE DATABASE tells about the ownership of the schema. Only a confirmed
// creation (no error) makes the schema this run's to drop. An error the server answered with (a MySQL error
// number, including "database exists") means nothing was created by the statement: not owned, and no
// residue. Any other error (connection, deadline, cancellation) leaves the outcome unknown: ambiguous, the
// schema is possible residue and is never dropped automatically.
func k8sSWClassifyCreate(err error) (owned, ambiguous bool) {
	if err == nil {
		return true, false
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return false, false
	}
	return false, true
}

// k8sSWFixtureCleanup says what the SQL fixture leaves after the cleanup, empty when nothing is left. A
// schema this run confirmed it created is dropped (k8sSWFixtureResidue); a schema whose creation was
// ambiguous is not touched and is reported as possible residue; a schema never created is not this run's.
func k8sSWFixtureCleanup(owned, ambiguous bool, drop func() error, schema string) string {
	switch {
	case owned:
		return k8sSWFixtureResidue(drop, schema)
	case ambiguous:
		return fmt.Sprintf("possible SQL fixture residue: the creation of the schema %s returned an unknown outcome, it is not dropped automatically", schema)
	}
	return ""
}

// k8sSWRemoveProbe removes the probe row the integrity check wrote. A failure is classified like any other
// database error, never discarded: the row stays in the fixture schema, which the cleanup drops.
func k8sSWRemoveProbe(exec func(query string, args ...interface{}) error, schema, token string, fixtureVerified bool) (k8sSWClass, string) {
	if err := exec("DELETE FROM "+schema+".probe WHERE v = ?", token); err != nil {
		return k8sSWClassifySQLError(err, fixtureVerified)
	}
	return "", ""
}

// k8sSWHolderOwned says why a Pod is not shown to be the holder this run intended to create, empty when it
// is: the intended name, this run's ownership labels and run ID, a UID, and the intended read-only
// attachment of the data claim. Nothing is deleted by name on a Pod that does not pass.
func k8sSWHolderOwned(pod *apiv1.Pod, name, runID, claim string) string {
	switch {
	case pod.Name != name:
		return fmt.Sprintf("the name is %s, not %s", pod.Name, name)
	case pod.UID == "":
		return "the Pod has no UID"
	case pod.Labels[k8sSWOwnerLabel] != k8sSWTestName || pod.Labels[k8sSWRunLabel] != runID:
		return "the ownership labels or the run ID are not this run's"
	}
	attached := false
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim && v.PersistentVolumeClaim.ReadOnly {
			attached = true
		}
	}
	if !attached {
		return fmt.Sprintf("it does not mount the claim %s read-only", claim)
	}
	return ""
}

// k8sSWDefinitiveCreateError says whether a Create error is an answer of the API server that no Pod was
// created (refused, invalid). Any other error (timeout, connection, an unexpected status, AlreadyExists) may
// hide a creation that was applied.
func k8sSWDefinitiveCreateError(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}

// k8sSWFixtureResidue says what the SQL fixture leaves after the cleanup, empty when it is gone. drop is
// nil when the database cannot be reached (nothing is tried, and no database is started to reach it).
func k8sSWFixtureResidue(drop func() error, schema string) string {
	if drop == nil {
		return fmt.Sprintf("the SQL fixture %s (the database is not reachable)", schema)
	}
	if err := drop(); err != nil {
		return fmt.Sprintf("the SQL fixture %s: %s", schema, err)
	}
	return ""
}

// k8sSWDeleteOutcome is what an ordinary deletion of the test's own holder Pod found.
type k8sSWDeleteOutcome string

const (
	k8sSWDeleteRequested k8sSWDeleteOutcome = "requested" // the deletion was accepted for that Pod
	k8sSWDeleteAbsent    k8sSWDeleteOutcome = "absent"    // no Pod has that name any more: the holder is gone
	k8sSWDeleteReplaced  k8sSWDeleteOutcome = "replaced"  // a different Pod now uses the name: not ours, not deleted
)

// k8sSWDeleteHolder deletes the holder by ordinary deletion with its UID as a precondition: the API server
// refuses (Conflict) when the name now belongs to another Pod. After a refusal the name is read once: a Pod
// with another UID means the original holder is gone and a foreign Pod uses its name (replaced); no Pod
// means absent. A deletion is never repeated by name alone.
func k8sSWDeleteHolder(client kubernetes.Interface, namespace, name string, uid types.UID) (k8sSWDeleteOutcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	switch {
	case err == nil:
		return k8sSWDeleteRequested, nil
	case apierrors.IsNotFound(err):
		return k8sSWDeleteAbsent, nil
	case apierrors.IsConflict(err):
		current, gerr := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(gerr):
			return k8sSWDeleteAbsent, nil
		case gerr != nil:
			return "", fmt.Errorf("the deletion of %s was refused (%v) and the Pod cannot be read: %w", name, err, gerr)
		case current.UID != uid:
			return k8sSWDeleteReplaced, nil
		}
	}
	return "", err
}

// k8sSWHolderPod is the test-owned Pod that holds the PVC: not a database, read-only mount, placed
// on the node the database ran on, labelled so that no Deployment or Service selects it. With
// preStopSeconds > 0 it takes that long to stop once deleted (a bounded, test-only Terminating window).
func k8sSWHolderPod(namespace, name, runID, claim, node string, preStopSeconds int) *apiv1.Pod {
	noToken := false
	c := apiv1.Container{
		Name:            "holder",
		Image:           "alpine",
		ImagePullPolicy: apiv1.PullIfNotPresent,
		// leaves at once on TERM: an ordinary deletion is quick
		Command:      []string{"sh", "-c", "trap 'exit 0' TERM; sleep 3600 & wait"},
		Resources:    apiv1.ResourceRequirements{Requests: apiv1.ResourceList{apiv1.ResourceCPU: resource.MustParse("10m"), apiv1.ResourceMemory: resource.MustParse("16Mi")}},
		VolumeMounts: []apiv1.VolumeMount{{Name: "data", MountPath: "/data", ReadOnly: true}},
	}
	if preStopSeconds > 0 {
		c.Lifecycle = &apiv1.Lifecycle{PreStop: &apiv1.LifecycleHandler{Exec: &apiv1.ExecAction{Command: []string{"sh", "-c", fmt.Sprintf("sleep %d", preStopSeconds)}}}}
	}
	return &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{k8sSWOwnerLabel: k8sSWTestName, k8sSWRunLabel: runID},
		},
		Spec: apiv1.PodSpec{
			RestartPolicy:                apiv1.RestartPolicyNever,
			AutomountServiceAccountToken: &noToken,
			Affinity: &apiv1.Affinity{NodeAffinity: &apiv1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
				NodeSelectorTerms: []apiv1.NodeSelectorTerm{{MatchFields: []apiv1.NodeSelectorRequirement{{Key: "metadata.name", Operator: apiv1.NodeSelectorOpIn, Values: []string{node}}}}},
			}}},
			Containers: []apiv1.Container{c},
			Volumes:    []apiv1.Volume{{Name: "data", VolumeSource: apiv1.VolumeSource{PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: claim, ReadOnly: true}}}},
		},
	}
}

// k8sSWHolderProblem checks that the holder really holds the intended PVC, read-only, where intended.
func k8sSWHolderProblem(pod *apiv1.Pod, claim, node string) string {
	if pod.Status.Phase != apiv1.PodRunning {
		return fmt.Sprintf("holder Pod %s is %s, not Running", pod.Name, pod.Status.Phase)
	}
	if pod.Spec.NodeName != node {
		return fmt.Sprintf("holder Pod %s runs on node %q, the database ran on %q", pod.Name, pod.Spec.NodeName, node)
	}
	mounted := false
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim && v.PersistentVolumeClaim.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		return fmt.Sprintf("holder Pod %s does not mount the claim %s read-only", pod.Name, claim)
	}
	if len(pod.Status.ContainerStatuses) == 0 {
		return fmt.Sprintf("holder Pod %s reports no container status", pod.Name)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return fmt.Sprintf("the container of holder Pod %s is not ready", pod.Name)
		}
	}
	return ""
}

func k8sSWDBReady(pod *apiv1.Pod, server string) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == server && cs.Ready {
			return true
		}
	}
	return false
}

// k8sSWFixtureInserts builds the rows of the fixture: deterministic, so the digest is stable.
func k8sSWFixtureInserts(schema string, rows int) []string {
	var stmts []string
	const batch = 500
	for start := 1; start <= rows; start += batch {
		var vals []string
		for i := start; i < start+batch && i <= rows; i++ {
			vals = append(vals, fmt.Sprintf("(%d,'row-%06d-%08x')", i, i, uint32(i)*2654435761))
		}
		stmts = append(stmts, "INSERT INTO "+schema+".t (id,payload) VALUES "+strings.Join(vals, ","))
	}
	return stmts
}

func k8sSWRandomID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b)
}
