// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// testK8SSingleWriterLifecycle exercises, on a REAL Kubernetes cluster, what issue #2389 and
// PR #2390 guarantee for the database Deployment of one server: a second database process is not
// started on the data directory while the previous one may still run.
//
// It is run BY NAME ONLY, never by ALL (it stops, restarts and patches its database):
//
//	POST /api/clusters/<cluster>/tests/actions/run/testK8SSingleWriterLifecycle
//
// It needs: prov-orchestrator = kube, exactly one database server (no replication is involved), and a
// namespace (the cluster name) that EXPLICITLY carries the label
//
//	regtest.replication-manager/disposable=true
//
// These are checked before anything is changed. An ineligible cluster is SKIP, never PASS; a missing
// permission or a failed live API call is a FAIL (class HARNESS), never disguised as SKIP. On MySQL and
// Percona the global gate of the runner applies first: the cluster must declare the mysqlgtid tag, or
// the run is a SKIP of the runner, which is not a PASS.
//
// The runner knows PASS, FAIL and SKIP only. Every proof that is incomplete is a FAIL, and the log
// carries the detail: each step logs "CHECK <step> PASS" or "CHECK <step> FAIL[<class>]", the class being
//
//	HARNESS       the test could not do what it needs (API, permission, helper object)
//	RESOURCE      the cluster could not run the workload (unschedulable, image pull, OOM, eviction)
//	PRODUCT       a guarantee of the change was seen not to hold
//	INCONCLUSIVE  a required observation is missing or the scenario was not exercised
//
// and the last line is "SUMMARY ...". Facts that are not assertions are logged as "OBSERVE".
//
// Steps (the safety gates S2 and S2c come first, and nothing that stops a database Pod while another
// Start may be issued runs before both passed):
//
//	S0  eligibility, permissions; the database container, its data claim (the PVC mounted at
//	    /var/lib/mysql) and exactly one running database Pod owned by the Deployment through its ReplicaSet;
//	    no other Pod references the data claim; the original strategy and container lifecycle are recorded
//	S1  unique SQL fixture; identities (Deployment, PVC) recorded; the Pod watch starts
//	S2  a test-owned non-database Pod holds the PVC (Running, read-only, verified): after a successful initial
//	    observation, Start must fail closed after the bounded wait, with zero replicas, observed by polls and
//	    by the watch for the whole wait
//	S2c a test-owned holder with a bounded preStop delay is deleted and observed Terminating: Start, called
//	    while it is Terminating, must not scale up until it is gone, and must then succeed
//	S3  migration patch at zero replicas: the database is stopped and its Pod gone, the strategy is set back to
//	    RollingUpdate, the production force re-pull patch must move it to Recreate with no Pod appearing,
//	    then Start through the guarded lifecycle. This checks the patch and a controlled startup, not the
//	    overlap safety of a live migration
//	S5  a temporary preStop delay is added (one rollout), then Stop and an immediate Start while the old
//	    database Pod is observed Terminating; S6 two concurrent Starts in that window; S7 the exact
//	    original lifecycle is restored
//	S8  force re-pull and restart through the lifecycle functions
//
// The reprovision (unprovision, provision, retained claim) is NOT part of this test: it is covered, on its
// own, by testK8SProvisionSchedulerVolumeBinding.
//
// The ordering evidence is a Pod watch established BEFORE each operation, synchronised with a List after
// it (no fixed delay), the identities and container states of the old Pods read before it, the owner
// chain of the replacement Pod, and the kubelet's container timestamps. It shows that no overlap was
// reported to the API; it does not prove that two database processes never ran together. The checks never
// assert a number of PATCH requests: that stays covered by the unit tests.
//
// Nothing is force-deleted. The test deletes only its own holder Pods (ordinary deletion) and its own
// SQL fixture. Cleanup inspects the real Deployment, restores the strategy and the lifecycle only where
// they differ, never starts a database, and says what it left.
func (regtest *RegTest) TestK8SSingleWriterLifecycle(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := &k8sSWRun{cl: cl, test: test, id: k8sSWRandomID()}
	return r.finish(r.run())
}

// ---------------------------------------------------------------------------------------------
// The run
// ---------------------------------------------------------------------------------------------

type k8sSWRun struct {
	cl   *cluster.Cluster
	test *cluster.Test
	id   string

	server *cluster.ServerMonitor
	client kubernetes.Interface
	ns     string
	claim  string // the data claim, resolved from the mount of /var/lib/mysql
	node   string
	watch  *k8sSWWatcher

	schema           string
	fixtureOwned     bool // CREATE DATABASE was confirmed by this run: the schema is its own to drop
	fixtureAmbiguous bool // the outcome of CREATE DATABASE is unknown: possible residue, never dropped
	fixtureVerified  bool
	digest           string
	depUID           types.UID
	pvcUID           types.UID

	// what the test may change, as it was; the flags say that a change was attempted (set before the
	// patch is sent), never what the Deployment holds
	orig             k8sSWSnap
	strategyTouched  bool
	lifecycleTouched bool

	holder        string
	holderUID     types.UID
	holderSeq     int
	holderPending string // the intended holder name while its creation is not accounted for

	gateS2, gateS2c bool

	mutating   bool
	passed     []string
	failedStep string
	failedCls  k8sSWClass
	skipped    string
}

func (r *k8sSWRun) logf(format string, args ...interface{}) {
	r.cl.LogModulePrintf(r.cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
}

func (r *k8sSWRun) pass(step, detail string) {
	r.passed = append(r.passed, step)
	r.logf("CHECK %s PASS: %s", step, detail)
}

func (r *k8sSWRun) fail(step string, class k8sSWClass, format string, args ...interface{}) bool {
	r.failedStep, r.failedCls = step, class
	r.logf("CHECK %s FAIL[%s]: %s", step, class, fmt.Sprintf(format, args...))
	return false
}

// failVerdict fails a step on a verdict that is not ok; a verdict without a class is inconclusive.
func (r *k8sSWRun) failVerdict(step string, v k8sSWVerdict) bool {
	if v.Class == "" {
		v.Class = k8sSWInconclusive
	}
	return r.fail(step, v.Class, "%s", v.Detail)
}

func (r *k8sSWRun) skip(reason string) bool {
	r.skipped = reason
	r.test.SkipReason = reason
	r.logf("CHECK S0 SKIP: %s", reason)
	return true
}

// finish runs the cleanup and reports the result of the test: a scenario that passed is a FAIL when the
// cleanup left a test-owned holder Pod or a temporary change of the Deployment (HARNESS, step CLEANUP).
func (r *k8sSWRun) finish(ok bool) bool {
	return r.conclude(ok, r.cleanup())
}

// conclude logs the summary and returns the final result. left is what the cleanup could not resolve.
func (r *k8sSWRun) conclude(ok bool, left []string) bool {
	if ok && len(left) > 0 {
		ok = false
		r.failedStep, r.failedCls = "CLEANUP", k8sSWHarness
		r.logf("CHECK CLEANUP FAIL[%s]: the test passed but its cleanup left %d object(s) or change(s) unresolved: %s", k8sSWHarness, len(left), strings.Join(left, "; "))
	} else if len(left) > 0 { // a failed test keeps its own class and step; the cleanup problem is reported apart
		r.logf("CLEANUP UNRESOLVED (reported apart, the failure above keeps its class): %s", strings.Join(left, "; "))
	}
	switch {
	case r.skipped != "":
		r.logf("SUMMARY %s result=SKIP reason=%q (nothing was changed)", k8sSWTestName, r.skipped)
	case ok:
		r.logf("SUMMARY %s result=PASS checks=%s", k8sSWTestName, strings.Join(r.passed, ","))
	default:
		r.logf("SUMMARY %s result=FAIL class=%s step=%s passed=%s", k8sSWTestName, r.failedCls, r.failedStep, strings.Join(r.passed, ","))
	}
	return ok
}

func (r *k8sSWRun) requireGates(step string) bool {
	if !r.gateS2 || !r.gateS2c {
		return r.fail(step, k8sSWHarness, "the safety gates S2 (Running holder) and S2c (Terminating holder) have not both passed: %s does not run", step)
	}
	return true
}

func (r *k8sSWRun) run() bool {
	servers := r.cl.GetServers()
	if reason := k8sSWTopologyEligibility(r.cl.GetOrchestrator(), len(servers)); reason != "" {
		return r.skip(reason)
	}
	r.server = servers[0]
	r.ns = r.cl.Name
	client, err := r.cl.K8SConnectAPI()
	if err != nil {
		return r.fail("S0", k8sSWHarness, "cannot connect to the Kubernetes API: %s", err)
	}
	r.client = client
	ctx := context.Background()

	missing, err := k8sSWMissingPermissions(ctx, client, r.ns)
	if err != nil {
		return r.fail("S0", k8sSWHarness, "%s", err)
	}
	if len(missing) > 0 {
		return r.fail("S0", k8sSWHarness, "the identity of replication-manager lacks permissions the test needs in namespace %s: %s", r.ns, strings.Join(missing, ", "))
	}
	nsObj, err := client.CoreV1().Namespaces().Get(ctx, r.ns, metav1.GetOptions{})
	if err != nil {
		return r.fail("S0", k8sSWHarness, "cannot read namespace %s: %s", r.ns, err)
	}
	if reason := k8sSWNamespaceEligibility(r.ns, nsObj.Labels); reason != "" {
		return r.skip(reason)
	}
	dep, err := client.AppsV1().Deployments(r.ns).Get(ctx, r.server.Name, metav1.GetOptions{})
	if err != nil {
		return r.fail("S0", k8sSWHarness, "cannot read the Deployment %s (is the server provisioned?): %s", r.server.Name, err)
	}
	idx := k8sSWDBContainerIndex(dep, r.server.Name)
	if idx < 0 {
		return r.fail("S0", k8sSWHarness, "the Deployment %s has no container named %s: the database container cannot be followed", dep.Name, r.server.Name)
	}
	claim, problem := k8sSWDataClaim(dep, r.server.Name)
	if problem != "" {
		return r.fail("S0", k8sSWHarness, "precondition: %s", problem)
	}
	r.claim = claim
	r.server.Refresh()
	if r.server.IsDown() {
		return r.fail("S0", k8sSWHarness, "the database %s is not healthy before the test", r.server.URL)
	}
	dbPods, err := r.dbPods()
	if err != nil {
		return r.fail("S0", k8sSWHarness, "cannot list the database Pods: %s", err)
	}
	pod, problem := k8sSWBaselinePod(dbPods, r.server.Name, r.claim, dep, func(name string) (*appsv1.ReplicaSet, error) {
		return client.AppsV1().ReplicaSets(r.ns).Get(ctx, name, metav1.GetOptions{})
	})
	if problem != "" {
		return r.fail("S0", k8sSWHarness, "precondition: the starting state is not a single database Pod owned by the Deployment: %s", problem)
	}
	all, err := client.CoreV1().Pods(r.ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return r.fail("S0", k8sSWHarness, "cannot list the Pods of namespace %s: %s", r.ns, err)
	}
	if foreign := k8sSWForeignConsumers(all.Items, r.claim, pod.UID); len(foreign) > 0 {
		return r.skip(fmt.Sprintf("Pod(s) %s, other than the database Pod, reference the data PVC %s: not a clean disposable setup", strings.Join(foreign, ","), r.claim))
	}
	r.node = pod.Spec.NodeName
	r.orig = k8sSWSnap{Strategy: *dep.Spec.Strategy.DeepCopy(), Lifecycle: dep.Spec.Template.Spec.Containers[idx].Lifecycle.DeepCopy()}
	r.pass("S0", fmt.Sprintf("Kubernetes orchestrator, one server %s, namespace %s labelled disposable, permissions present, data claim %s, one database Pod %s owned by the Deployment and mounting that claim, no other consumer of the claim", r.server.Name, r.ns, r.claim, pod.Name))
	r.logf("OBSERVE original state: strategy %s (rollingUpdate set: %t), database container lifecycle set: %t", dep.Spec.Strategy.Type, dep.Spec.Strategy.RollingUpdate != nil, r.orig.Lifecycle != nil)
	r.observeConfig()

	// from here on the test changes things
	r.mutating = true
	if !r.stepFixture(dep) {
		return false
	}
	w, err := startK8sSWWatcher(client, r.ns, r.server.Name)
	if err != nil {
		return r.fail("S1", k8sSWHarness, "cannot start the Pod watch: %s", err)
	}
	r.watch = w

	return r.stepGuardProbe() &&
		r.stepTerminatingHolder() &&
		r.stepLegacyMigration() &&
		r.stepPreStopOn() &&
		r.stepImmediateStart() &&
		r.stepConcurrentStarts() &&
		r.stepPreStopOff() &&
		r.stepRolloutOps()
}

// observeConfig logs the effective configuration. It asserts nothing: a configuration that is not
// loaded (the Percona image does not read /etc/mysql/conf.d) is a separate issue and changes neither
// the lifecycle checks nor their result.
func (r *k8sSWRun) observeConfig() {
	vars := r.server.Variables // the variables the monitor read through dbhelper
	if vars == nil {
		return
	}
	gtid := vars.Get("GTID_MODE") // MariaDB has none
	if gtid == "" {
		gtid = "n/a"
	}
	r.logf("OBSERVE effective configuration (monitor variables, not asserted): version=%s server_id=%s gtid_mode=%s max_connections=%s", vars.Get("VERSION"), vars.Get("SERVER_ID"), gtid, vars.Get("MAX_CONNECTIONS"))
}

// ---------------------------------------------------------------------------------------------
// Kubernetes and SQL helpers
// ---------------------------------------------------------------------------------------------

func (r *k8sSWRun) dbPods() ([]apiv1.Pod, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list, err := r.client.CoreV1().Pods(r.ns).List(ctx, metav1.ListOptions{LabelSelector: "tag=" + r.server.Name})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// currentDBPod is the Pod of the database that is running, ready and not terminating (nil if none).
func (r *k8sSWRun) currentDBPod() (*apiv1.Pod, error) {
	pods, err := r.dbPods()
	if err != nil {
		return nil, err
	}
	for i := range pods {
		p := &pods[i]
		if p.DeletionTimestamp == nil && p.Status.Phase == apiv1.PodRunning && k8sSWDBReady(p, r.server.Name) {
			return p, nil
		}
	}
	return nil, nil
}

func (r *k8sSWRun) deployment() (*appsv1.Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.client.AppsV1().Deployments(r.ns).Get(ctx, r.server.Name, metav1.GetOptions{})
}

func (r *k8sSWRun) patchDeployment(pt types.PatchType, patch []byte) (*appsv1.Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.client.AppsV1().Deployments(r.ns).Patch(ctx, r.server.Name, pt, patch, metav1.PatchOptions{})
}

func (r *k8sSWRun) replicaSetUIDs() (map[types.UID]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list, err := r.client.AppsV1().ReplicaSets(r.ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	uids := map[types.UID]bool{}
	for _, rs := range list.Items {
		for _, ref := range rs.OwnerReferences {
			if ref.UID == r.depUID {
				uids[rs.UID] = true
			}
		}
	}
	return uids, nil
}

func k8sSWSameSet(a, b map[types.UID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// trouble returns what the cluster could not run among the Pods of the database.
func (r *k8sSWRun) trouble() string {
	pods, err := r.dbPods()
	if err != nil {
		return ""
	}
	for i := range pods {
		if t := k8sSWPodTrouble(&pods[i]); t != "" {
			return t
		}
	}
	return ""
}

// waitErrClass names the cause of a wait that ran out: a refusal of the API (HARNESS), a Pod the cluster
// could not run (RESOURCE), or an observation that did not come (INCONCLUSIVE).
func (r *k8sSWRun) waitErrClass(err error) k8sSWClass {
	if k8sRefusedByAPI(err) {
		return k8sSWHarness
	}
	if r.trouble() != "" {
		return k8sSWResource
	}
	return k8sSWInconclusive
}

func (r *k8sSWRun) failWait(step string, err error) bool {
	if t := r.trouble(); t != "" && !k8sRefusedByAPI(err) {
		return r.fail(step, k8sSWResource, "%s (%s)", t, err)
	}
	return r.fail(step, r.waitErrClass(err), "%s", err)
}

// waitNoDBPods waits until no Pod of the database exists, terminating ones included: a Pod leaves the API
// only once the kubelet has stopped its containers.
func (r *k8sSWRun) waitNoDBPods(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pods, err := r.dbPods()
		if err != nil {
			if k8sRefusedByAPI(err) {
				return err
			}
		} else if len(pods) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Pod(s) of %s are still there after %s", r.server.Name, timeout)
		}
		time.Sleep(time.Second)
	}
}

// waitReplacement waits for a ready database Pod owned by the Deployment (through its ReplicaSet, the
// existing k8sReplacementPod), with every Pod of old gone.
func (r *k8sSWRun) waitReplacement(old map[types.UID]bool, timeout time.Duration) (*apiv1.Pod, error) {
	oldNames := map[types.UID]string{}
	for uid := range old {
		oldNames[uid] = ""
	}
	deadline := time.Now().Add(timeout)
	for {
		pods, err := r.dbPods()
		if err == nil {
			oldLeft := false
			for i := range pods {
				if old[pods[i].UID] {
					oldLeft = true
				}
			}
			if !oldLeft {
				pod, rerr := k8sReplacementPod(r.client, r.ns, r.server.Name, r.depUID, oldNames)
				if rerr != nil {
					if k8sRefusedByAPI(rerr) {
						return nil, rerr
					}
				} else if pod != nil {
					// the replacement must mount the data claim recorded at S0: a mismatch is not accepted
					if mounted, problem := k8sSWPodDataClaim(pod, r.server.Name); problem != "" || mounted != r.claim {
						return nil, fmt.Errorf("the replacement Pod %s does not mount the recorded data claim %s (%q: %s)", pod.Name, r.claim, mounted, problem)
					}
					return pod, nil
				}
			}
		} else if k8sRefusedByAPI(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no ready replacement Pod owned by the Deployment %s within %s", r.server.Name, timeout)
		}
		time.Sleep(time.Second)
	}
}

// waitObservedGeneration waits until the Deployment controller has observed generation gen. A
// generation is a counter (not an opaque resourceVersion), so it may be compared.
func (r *k8sSWRun) waitObservedGeneration(gen int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if dep, err := r.deployment(); err == nil && dep.Status.ObservedGeneration >= gen {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Deployment controller did not observe generation %d within %s", gen, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitSQL waits until repman reaches the database again.
func (r *k8sSWRun) waitSQL(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		r.server.Refresh()
		if !r.server.IsDown() && r.server.Conn != nil {
			if last = r.server.Conn.Ping(); last == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the database %s does not answer SQL after %s: %w", r.server.URL, timeout, last)
		}
		time.Sleep(2 * time.Second)
	}
}

// integrity checks the real database and the volume, not the Pod: SQL answers, the claim is the one
// recorded before the test, the fixture is unchanged (row count and content digest) and a write is read
// back. Errors are classified by their structure.
func (r *k8sSWRun) integrity(step string) bool {
	if err := r.waitSQL(k8sSWWait); err != nil {
		if t := r.trouble(); t != "" {
			return r.fail(step, k8sSWResource, "%s (%s)", t, err)
		}
		inner := errors.Unwrap(err)
		if inner == nil {
			inner = err
		}
		class, detail := k8sSWClassifySQLError(inner, r.fixtureVerified)
		return r.fail(step, class, "%s (%s)", detail, err)
	}
	pvc, err := r.client.CoreV1().PersistentVolumeClaims(r.ns).Get(context.TODO(), r.claim, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return r.fail(step, k8sSWProduct, "the data volume claim %s is gone", r.claim)
	case err != nil:
		return r.fail(step, r.waitErrClass(err), "cannot read the claim %s: %s", r.claim, err)
	case pvc.UID != r.pvcUID:
		return r.fail(step, k8sSWProduct, "the data volume claim %s was replaced (uid %s, was %s)", r.claim, pvc.UID, r.pvcUID)
	case pvc.Status.Phase != apiv1.ClaimBound:
		return r.fail(step, k8sSWInconclusive, "the data volume claim %s is %s, not Bound", r.claim, pvc.Status.Phase)
	}
	digest, err := dbhelper.TableContentDigest(r.server.Conn, r.schema+".t", "CONCAT(id,payload)")
	if err != nil {
		class, detail := k8sSWClassifySQLError(err, r.fixtureVerified)
		return r.fail(step, class, "cannot read the fixture digest: %s", detail)
	}
	if digest != r.digest {
		return r.fail(step, k8sSWProduct, "the fixture changed: digest %s, was %s", digest, r.digest)
	}
	// The probe is direct, parameterized SQL in this regtest: no dbhelper function writes and reads back a
	// row by value. Whether T1 reaches regtest/ (its text names cluster/ and server/) is open for review.
	token := fmt.Sprintf("%s-%d", step, time.Now().UnixNano())
	if _, err := r.server.Conn.Exec("INSERT INTO "+r.schema+".probe (v) VALUES (?)", token); err != nil {
		class, detail := k8sSWClassifySQLError(err, r.fixtureVerified)
		return r.fail(step, class, "a write to the database failed: %s", detail)
	}
	var n int
	if err := r.server.Conn.QueryRowx("SELECT COUNT(*) FROM "+r.schema+".probe WHERE v = ?", token).Scan(&n); err != nil {
		class, detail := k8sSWClassifySQLError(err, r.fixtureVerified)
		return r.fail(step, class, "the row just written cannot be read back: %s", detail)
	} else if n != 1 {
		return r.fail(step, k8sSWProduct, "the row just written is not read back (count %d)", n)
	}
	if class, detail := k8sSWRemoveProbe(func(q string, a ...interface{}) error { _, err := r.server.Conn.Exec(q, a...); return err }, r.schema, token, r.fixtureVerified); class != "" {
		return r.fail(step, class, "the probe row could not be removed: %s (it stays in the fixture schema, which the cleanup drops)", detail)
	}
	return true
}

func (r *k8sSWRun) startDB() (time.Duration, error) {
	t := time.Now()
	err := r.cl.StartDatabaseService(r.server)
	return time.Since(t), err
}

// podPresent returns the Pod when it still exists with that UID, nil when it is gone.
func (r *k8sSWRun) podPresent(name string, uid types.UID) (*apiv1.Pod, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := r.client.CoreV1().Pods(r.ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.UID != uid {
		return nil, nil
	}
	return p, nil
}

// waitPodTerminating waits until the Pod is seen Terminating (deletion timestamp set, still there).
func (r *k8sSWRun) waitPodTerminating(name string, uid types.UID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p, err := r.podPresent(name, uid)
		if err == nil {
			if p == nil {
				return false // gone already: the window was missed
			}
			if p.DeletionTimestamp != nil {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// startOnce calls Start. An error is classified from the state the test reads after it: a scale-up
// despite the error is a violation, a refusal while the held Pod remains is the guard working.
func (r *k8sSWRun) startOnce(heldName string, heldUID types.UID) (time.Duration, *k8sSWVerdict) {
	elapsed, err := r.startDB()
	if err == nil {
		return elapsed, nil
	}
	dep, derr := r.deployment()
	pods, perr := r.dbPods()
	if derr != nil || perr != nil {
		v := k8sSWVerdictOf(k8sSWHarness, "Start failed (%s) and the state after it cannot be read (%v, %v)", err, derr, perr)
		return elapsed, &v
	}
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	others := 0
	for i := range pods {
		if pods[i].UID != heldUID {
			others++
		}
	}
	still := false
	if heldName != "" {
		if p, perr := r.podPresent(heldName, heldUID); perr == nil && p != nil {
			still = true
		}
	}
	v := k8sSWClassifyStartError(err, replicas, others, still)
	return elapsed, &v
}

// startObserver polls, until stop, the Deployment, the database Pods and the held Pod (k8sSWWindow).
// invoke marks the moment Start is called: only polls begun after it count as a proof that the window
// was exercised.
func (r *k8sSWRun) startObserver(held string, heldUID types.UID) (invoke func(), stop func() *k8sSWWindow) {
	w := &k8sSWWindow{}
	var invoked atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_ = k8sSWPollOnce(r.client, r.ns, r.server.Name, held, heldUID, invoked.Load(), w)
			select {
			case <-ctx.Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
		}
	}()
	return func() { w.markInvoked(); invoked.Store(true) }, func() *k8sSWWindow {
		cancel()
		wg.Wait()
		return w
	}
}

// stopScaledUp stops, normally, the database a Start scaled up although the guard should have refused.
// No earlier database process existed in the scenarios that call it (the Pod it waited for was not a
// database), and nothing is deleted.
func (r *k8sSWRun) stopScaledUp(step string) {
	r.logf("%s: the PVC-consumer guard did not hold: stopping, normally, the database Start scaled up", step)
	if err := r.cl.StopDatabaseService(r.server); err != nil {
		r.logf("%s: the normal Stop failed: %s", step, err)
		return
	}
	if err := r.waitNoDBPods(k8sSWWait); err != nil {
		r.logf("%s: %s", step, err)
	}
}

// ---------------------------------------------------------------------------------------------
// Windows: what an operation did to the Pods
// ---------------------------------------------------------------------------------------------

type k8sSWWindowState struct {
	mark   int
	old    []k8sSWOldPod
	oldSet map[types.UID]bool
}

// begin takes the mark of the watch and the snapshot of the Pods that exist before the operation.
// gated steps refuse to run before both safety gates passed.
func (r *k8sSWRun) begin(step string, gated bool) (*k8sSWWindowState, bool) {
	if gated && !r.requireGates(step) {
		return nil, false
	}
	mark := r.watch.mark()
	pods, err := r.dbPods()
	if err != nil {
		return nil, r.fail(step, k8sSWHarness, "cannot list the Pods before the operation: %s", err)
	}
	st := &k8sSWWindowState{mark: mark, old: k8sSWSnapshotOf(pods, r.server.Name), oldSet: map[types.UID]bool{}}
	for _, o := range st.old {
		st.oldSet[o.UID] = true
	}
	return st, true
}

// analyze waits until the watch has caught up with the List, then judges the window. The replacement
// found through the API (owner chain verified) must be the Pod the watch saw created.
func (r *k8sSWRun) analyze(st *k8sSWWindowState, expectNew int, replacement *apiv1.Pod) k8sSWVerdict {
	if err := r.watch.waitSynced(r.dbPods, k8sSWSyncWait); err != nil {
		return k8sSWVerdictOf(k8sSWInconclusive, "%s", err)
	}
	events, werr := r.watch.snapshot()
	if werr != nil {
		return k8sSWVerdictOf(k8sSWInconclusive, "the Pod watch was lost: %s", werr)
	}
	v := k8sSWAnalyzeWindow(events, st.mark, st.old, expectNew)
	if v.ok() && expectNew == 1 && replacement != nil && (len(v.NewUIDs) != 1 || v.NewUIDs[0] != replacement.UID) {
		return k8sSWVerdictOf(k8sSWInconclusive, "the replacement Pod found through the API (%s) is not the Pod the watch saw created", replacement.UID)
	}
	return v
}

// complete finishes an operation that must replace the database Pod: the replacement owned by the
// Deployment is Ready and the old ones are gone, the window is judged, and the database and the volume
// are checked.
func (r *k8sSWRun) complete(step string, st *k8sSWWindowState, expectNew int) (k8sSWVerdict, bool) {
	var replacement *apiv1.Pod
	if expectNew == 1 {
		pod, err := r.waitReplacement(st.oldSet, k8sSWWait)
		if err != nil {
			return k8sSWVerdict{}, r.failWait(step, err)
		}
		replacement = pod
	}
	v := r.analyze(st, expectNew, replacement)
	if !v.ok() {
		return v, r.failVerdict(step, v)
	}
	if !r.integrity(step) {
		return v, false
	}
	return v, true
}

// operation runs op between a snapshot and its verification.
func (r *k8sSWRun) operation(step string, expectNew int, op func() (k8sSWClass, error)) (k8sSWVerdict, bool) {
	st, ok := r.begin(step, true)
	if !ok {
		return k8sSWVerdict{}, false
	}
	if class, err := op(); err != nil {
		if class == "" {
			class = k8sSWHarness
		}
		return k8sSWVerdict{}, r.fail(step, class, "%s", err)
	}
	return r.complete(step, st, expectNew)
}

// ---------------------------------------------------------------------------------------------
// The holder Pod: the test's own, non-database, deleted only by an ordinary deletion
// ---------------------------------------------------------------------------------------------

// deletePod is the only place the test deletes a Pod: an ordinary deletion of its own holder, with the
// recorded UID as a precondition (k8sSWDeleteHolder). A holder that is absent, or whose name another Pod
// now uses, is not deleted again by name.
func (r *k8sSWRun) deletePod(name string, uid types.UID) (k8sSWDeleteOutcome, error) {
	outcome, err := k8sSWDeleteHolder(r.client, r.ns, name, uid)
	switch {
	case err != nil:
		return "", err
	case outcome == k8sSWDeleteReplaced:
		r.logf("OBSERVE the holder Pod %s (uid %s) is gone and a different Pod now uses its name: that Pod is not deleted", name, uid)
	case outcome == k8sSWDeleteAbsent:
		r.logf("OBSERVE the holder Pod %s (uid %s) is already gone", name, uid)
	}
	return outcome, nil
}

// removeHolder deletes the holder and waits until it is gone.
func (r *k8sSWRun) removeHolder() error {
	if r.holder == "" {
		return nil
	}
	outcome, err := r.deletePod(r.holder, r.holderUID)
	if err != nil {
		return err
	}
	if outcome != k8sSWDeleteRequested { // the original holder no longer exists
		r.holder, r.holderUID = "", ""
		return nil
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := r.podPresent(r.holder, r.holderUID); err == nil && p == nil {
			r.holder, r.holderUID = "", ""
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the holder Pod %s is still there after 120s", r.holder)
}

// createHolderPod creates the holder once, never retried (a retry could create another). The intended name
// is recorded before Create. A Create error that may hide an applied creation is followed by one bounded
// Get: the Pod is recorded (with its UID, for the UID-preconditioned deletion) only if it passes
// k8sSWHolderOwned. Anything else leaves the name pending, nothing is deleted by name, and the cleanup
// reports it: the step fails, so the test cannot pass.
func (r *k8sSWRun) createHolderPod(preStopSeconds int) (*apiv1.Pod, error) {
	r.holderSeq++
	name := fmt.Sprintf("regtest-holder-%s-%d", r.id, r.holderSeq)
	r.holderPending = name
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := r.client.CoreV1().Pods(r.ns).Create(ctx, k8sSWHolderPod(r.ns, name, r.id, r.claim, r.node, preStopSeconds), metav1.CreateOptions{})
	if err == nil {
		r.holder, r.holderUID, r.holderPending = created.Name, created.UID, ""
		return created, nil
	}
	if k8sSWDefinitiveCreateError(err) {
		r.holderPending = ""
		return nil, err
	}
	gctx, gcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer gcancel()
	p, gerr := r.client.CoreV1().Pods(r.ns).Get(gctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(gerr):
		return nil, fmt.Errorf("the creation of %s returned an error (%v) and no such Pod exists now; a late application cannot be excluded, the cleanup checks again", name, err)
	case gerr != nil:
		return nil, fmt.Errorf("the creation of %s returned an error (%v) and its outcome cannot be established (%v)", name, err, gerr)
	}
	if problem := k8sSWHolderOwned(p, name, r.id, r.claim); problem != "" {
		return nil, fmt.Errorf("the creation of %s returned an error (%v) and a Pod of that name exists that is not shown to be this run's holder (%s): it is not deleted", name, err, problem)
	}
	r.holder, r.holderUID, r.holderPending = p.Name, p.UID, ""
	r.logf("OBSERVE the creation of %s returned an error (%v) but the Pod is this run's holder (uid %s): recorded for the UID-preconditioned cleanup", name, err, p.UID)
	return p, nil
}

// resolveHolderPending settles, at the cleanup, a holder name whose creation was not accounted for: no Pod
// of that name is resolved; a Pod shown to be this run's holder is recorded (and removed by the normal
// UID-safe path); anything else, or a read that fails, is reported and never deleted by name.
func (r *k8sSWRun) resolveHolderPending() string {
	name := r.holderPending
	if name == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := r.client.CoreV1().Pods(r.ns).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		r.holderPending = ""
		return ""
	case err != nil:
		return fmt.Sprintf("it cannot be established whether the holder Pod %s exists: %s", name, err)
	}
	if problem := k8sSWHolderOwned(p, name, r.id, r.claim); problem != "" {
		return fmt.Sprintf("a Pod named %s exists and is not shown to be this run's holder (%s): not deleted", name, problem)
	}
	r.holder, r.holderUID, r.holderPending = p.Name, p.UID, ""
	return ""
}

// createHolder creates the holder and waits until it is Running with the intended PVC mounted read-only
// on the intended node (k8sSWHolderProblem). preStopSeconds > 0 gives it a bounded Terminating window.
func (r *k8sSWRun) createHolder(step string, preStopSeconds int) (*apiv1.Pod, bool) {
	created, err := r.createHolderPod(preStopSeconds)
	if err != nil {
		return nil, r.fail(step, k8sSWHarness, "cannot create the holder Pod: %s", err)
	}
	name := created.Name
	ctx := context.Background()
	var last *apiv1.Pod
	deadline := time.Now().Add(k8sSWWait)
	for time.Now().Before(deadline) {
		p, err := r.client.CoreV1().Pods(r.ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			last = p
			if t := k8sSWPodTrouble(p); t != "" {
				return nil, r.fail(step, k8sSWResource, "%s", t)
			}
			if k8sSWHolderProblem(p, r.claim, r.node) == "" {
				r.logf("OBSERVE holder %s Running on %s, claim %s mounted read-only (preStop %ds)", name, r.node, r.claim, preStopSeconds)
				return p, true
			}
		}
		time.Sleep(time.Second)
	}
	if last == nil {
		return nil, r.fail(step, k8sSWHarness, "the holder Pod cannot be read")
	}
	return nil, r.fail(step, k8sSWResource, "the holder is not usable: %s", k8sSWHolderProblem(last, r.claim, r.node))
}

// ---------------------------------------------------------------------------------------------
// Steps
// ---------------------------------------------------------------------------------------------

func (r *k8sSWRun) stepFixture(dep *appsv1.Deployment) bool {
	r.schema = "regtest_k8ssw_" + r.id
	// CREATE DATABASE alone, without IF NOT EXISTS: only its confirmed success makes the schema this run's
	// to drop. A refusal means nothing was created; an unknown outcome is possible residue, never dropped.
	createErr := dbhelper.ExecStatements(r.server.Conn, "CREATE DATABASE "+r.schema)
	r.fixtureOwned, r.fixtureAmbiguous = k8sSWClassifyCreate(createErr)
	if createErr != nil {
		class, detail := k8sSWClassifySQLError(createErr, false)
		if r.fixtureAmbiguous {
			return r.fail("S1", k8sSWInconclusive, "the creation of the schema %s has an unknown outcome, possible residue is reported by the cleanup: %s", r.schema, detail)
		}
		if class == k8sSWInconclusive {
			class = k8sSWHarness
		}
		return r.fail("S1", class, "cannot create the SQL fixture schema: %s", detail)
	}
	stmts := []string{
		"CREATE TABLE " + r.schema + ".t (id INT PRIMARY KEY, payload VARCHAR(64) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE " + r.schema + ".probe (id INT AUTO_INCREMENT PRIMARY KEY, v VARCHAR(48) NOT NULL) ENGINE=InnoDB",
	}
	stmts = append(stmts, k8sSWFixtureInserts(r.schema, k8sSWFixtureRows)...)
	if err := dbhelper.ExecStatements(r.server.Conn, stmts...); err != nil {
		class, detail := k8sSWClassifySQLError(err, false)
		if class == k8sSWInconclusive {
			class = k8sSWHarness
		}
		return r.fail("S1", class, "cannot create the SQL fixture: %s", detail)
	}
	digest, err := dbhelper.TableContentDigest(r.server.Conn, r.schema+".t", "CONCAT(id,payload)")
	if err != nil {
		return r.fail("S1", k8sSWHarness, "cannot read the fixture digest: %s", err)
	}
	if !strings.HasPrefix(digest, fmt.Sprintf("%d/", k8sSWFixtureRows)) {
		return r.fail("S1", k8sSWHarness, "the fixture has an unexpected digest %s", digest)
	}
	r.digest = digest
	r.fixtureVerified = true
	r.depUID = dep.UID
	pvc, err := r.client.CoreV1().PersistentVolumeClaims(r.ns).Get(context.TODO(), r.claim, metav1.GetOptions{})
	if err != nil {
		return r.fail("S1", k8sSWHarness, "cannot read the PVC %s: %s", r.claim, err)
	}
	if pvc.UID == "" {
		return r.fail("S1", k8sSWHarness, "the PVC %s has no UID: its identity cannot be followed", r.claim)
	}
	r.pvcUID = pvc.UID
	r.logf("OBSERVE identities: deployment %s uid %s strategy %s, PVC %s uid %s, node %s", dep.Name, dep.UID, dep.Spec.Strategy.Type, pvc.Name, pvc.UID, r.node)
	r.pass("S1", fmt.Sprintf("fixture %s created (%s), PVC identity recorded", r.schema, digest))
	return true
}

// stepGuardProbe is the first safety gate. The database is stopped normally and its Pod is gone; only
// then a non-database Pod holds the PVC and Start is called through the lifecycle function. With the
// guard, Start fails closed after its bounded wait, and polls made during the whole wait see the holder
// present with zero replicas and no database Pod. Without it (the baseline), Start scales up at once:
// the test ends there, with no old database process anywhere, and nothing that overlaps has run.
func (r *k8sSWRun) stepGuardProbe() bool {
	if err := r.cl.StopDatabaseService(r.server); err != nil {
		return r.fail("S2", k8sSWHarness, "Stop failed: %s", err)
	}
	if err := r.waitNoDBPods(k8sSWWait); err != nil {
		return r.failWait("S2", err)
	}
	st, ok := r.begin("S2", false)
	if !ok {
		return false
	}
	holderPod, ok := r.createHolder("S2", 0)
	if !ok {
		return false
	}
	// Start is not called without one successful observation of the starting state
	if err := k8sSWAwaitInitial(r.client, r.ns, r.server.Name, holderPod.Name, holderPod.UID, k8sSWWait, time.Second); err != nil {
		return r.fail("S2", k8sSWInconclusive, "%s: Start was not called", err)
	}
	invoke, stop := r.startObserver(holderPod.Name, holderPod.UID)
	invoke()
	elapsed, startErr := r.startDB()
	end := time.Now()
	win := stop()
	dep, derr := r.deployment()
	pods, perr := r.dbPods()
	if derr != nil || perr != nil {
		return r.fail("S2", k8sSWHarness, "cannot read the Deployment or the Pods after Start (%v, %v)", derr, perr)
	}
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	v := k8sSWClassifyGuardProbe(startErr, elapsed, replicas, len(pods), holderPod.Name)
	_, present, _, violation := win.result()
	if v.ok() && violation != "" {
		v = k8sSWVerdictOf(k8sSWProduct, "%s", violation)
	}
	if v.ok() && present < 1 {
		v = k8sSWVerdictOf(k8sSWInconclusive, "no poll during the wait found the holder present with zero replicas: the fail-closed wait was not observed")
	}
	if cov := win.coverage(end); v.ok() && cov != "" {
		v = k8sSWVerdictOf(k8sSWInconclusive, "the fail-closed wait was not observed closely enough: %s", cov)
	}
	if v.ok() { // the watch complements the polls: no database Pod was created and deleted between them
		if wv := r.analyze(st, 0, nil); !wv.ok() {
			v = wv
		}
	}
	if !v.ok() {
		if startErr == nil || replicas != 0 || len(pods) > 0 || violation != "" {
			r.stopScaledUp("S2")
		}
		return r.failVerdict("S2", v)
	}
	r.pass("S2", fmt.Sprintf("%s; %d successful poll(s) saw the holder present with zero replicas, the longest stretch without one under %s, and the watch saw no database Pod", v.Detail, present, k8sSWMaxPollGap))
	if err := r.removeHolder(); err != nil {
		return r.fail("S2", k8sSWHarness, "cannot remove the holder Pod: %s", err)
	}
	r.gateS2 = true
	return true
}

// stepTerminatingHolder is the second safety gate, for the case S5 and S6 rely on: a Pod that still
// references the PVC while Terminating. The database Pod is gone and the Deployment is at zero replicas.
// A holder that is not a database, with a bounded preStop delay, is deleted and observed Terminating.
// Start is then called: until the holder is gone it must neither scale up nor create a database Pod
// (polls from before the call to its return), and once the holder is gone it must succeed.
func (r *k8sSWRun) stepTerminatingHolder() bool {
	pods, err := r.dbPods()
	if err != nil {
		return r.fail("S2c", k8sSWHarness, "cannot list the Pods: %s", err)
	}
	if len(pods) > 0 {
		return r.fail("S2c", k8sSWHarness, "a database Pod still exists: the previous database must be completely gone before S2c")
	}
	dep, err := r.deployment()
	if err != nil {
		return r.fail("S2c", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		return r.fail("S2c", k8sSWHarness, "the Deployment is not at zero replicas before S2c")
	}
	st, ok := r.begin("S2c", false)
	if !ok {
		return false
	}
	holderPod, ok := r.createHolder("S2c", k8sSWHolderPreStopSeconds)
	if !ok {
		return false
	}
	name, uid := holderPod.Name, holderPod.UID
	if outcome, err := r.deletePod(name, uid); err != nil {
		return r.fail("S2c", k8sSWHarness, "cannot delete the holder Pod: %s", err)
	} else if outcome != k8sSWDeleteRequested {
		return r.fail("S2c", k8sSWHarness, "the holder Pod %s was not deleted by the test (%s)", name, outcome)
	}
	if !r.waitPodTerminating(name, uid, 20*time.Second) {
		return r.fail("S2c", k8sSWInconclusive, "the holder Pod %s was not observed Terminating: the Terminating window was not exercised", name)
	}
	if err := k8sSWAwaitInitial(r.client, r.ns, r.server.Name, name, uid, 15*time.Second, 500*time.Millisecond); err != nil {
		return r.fail("S2c", k8sSWInconclusive, "%s: Start was not called", err)
	}
	invoke, stop := r.startObserver(name, uid)
	invoke()
	elapsed, startVerdict := r.startOnce(name, uid)
	end := time.Now()
	win := stop()
	_, _, terminating, violation := win.result()
	still, serr := r.podPresent(name, uid)
	if serr != nil {
		return r.fail("S2c", k8sSWHarness, "cannot read the holder Pod after Start: %s", serr)
	}
	if v := k8sSWJudgeHeldStart(startVerdict, violation, win.coverage(end), still != nil, terminating, "the holder Pod "+name); !v.ok() {
		if v.Class == k8sSWProduct {
			r.stopScaledUp("S2c")
		}
		return r.failVerdict("S2c", v)
	}
	r.holder, r.holderUID = "", ""
	if _, ok := r.complete("S2c", st, 1); !ok {
		return false
	}
	r.pass("S2c", fmt.Sprintf("Start waited %s for the Terminating holder (%d poll(s) saw it Terminating with zero replicas and no database Pod), then succeeded once it was gone; database answers, fixture unchanged", elapsed.Round(time.Second), terminating))
	r.gateS2c = true
	return true
}

// requireStopped re-reads the Deployment and the database Pods and refuses the next mutation unless the
// Deployment is explicitly at zero replicas, no database Pod remains and the strategy is the expected one
// (k8sSWStoppedProblem). Any uncertainty, a failed read included, stops the step before the mutation. The
// read does not prevent another actor from changing the cluster afterwards: the disposable namespace and
// its exclusive use by the test are operational preconditions.
func (r *k8sSWRun) requireStopped(step string, wantType appsv1.DeploymentStrategyType, wantRollingUpdate bool, when string) bool {
	dep, err := r.deployment()
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot re-read the Deployment %s: %s", when, err)
	}
	pods, err := r.dbPods()
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot list the database Pods %s: %s", when, err)
	}
	if problem := k8sSWStoppedProblem(dep, pods, wantType, wantRollingUpdate); problem != "" {
		return r.fail(step, k8sSWInconclusive, "the Deployment is not in the state the next mutation requires %s: %s", when, problem)
	}
	return true
}

// stepLegacyMigration checks the migration patch of an older Deployment and a controlled startup, with no
// database Pod anywhere: the database is stopped normally and its Pod is gone before the legacy strategy
// (RollingUpdate, a strategy-only patch) is injected. The production force re-pull patch must then move the
// Deployment to Recreate, at zero replicas, with no Pod appearing; the database is started through the
// normal guarded lifecycle. This is NOT a test of a live migration: whether a template patch under
// RollingUpdate surges a second Pod is not exercised here, and is not allowed to happen (the database is
// stopped). The attempt is recorded before the patch is sent: cleanup inspects the Deployment and restores
// Recreate if needed.
func (r *k8sSWRun) stepLegacyMigration() bool {
	if !r.requireGates("S3") {
		return false
	}
	if err := r.cl.StopDatabaseService(r.server); err != nil {
		return r.fail("S3", k8sSWHarness, "Stop failed: %s", err)
	}
	if err := r.waitNoDBPods(k8sSWWait); err != nil {
		return r.failWait("S3", err)
	}
	// before the legacy strategy is injected: replicas explicitly zero, no database Pod, the strategy unchanged
	if !r.requireStopped("S3", r.orig.Strategy.Type, r.orig.Strategy.RollingUpdate != nil, "before the legacy strategy is injected") {
		return false
	}
	rsBefore, err := r.replicaSetUIDs()
	if err != nil {
		return r.fail("S3", k8sSWHarness, "cannot list the ReplicaSets: %s", err)
	}
	st, ok := r.begin("S3", true)
	if !ok {
		return false
	}
	r.strategyTouched = true
	patched, err := r.patchDeployment(types.StrategicMergePatchType, k8sSWLegacyStrategyPatch())
	if err != nil {
		return r.fail("S3", k8sSWHarness, "cannot set the legacy strategy (the patch may have been applied; cleanup inspects the Deployment): %s", err)
	}
	if err := r.waitObservedGeneration(patched.Generation, k8sSWSyncWait); err != nil {
		return r.fail("S3", k8sSWInconclusive, "%s", err)
	}
	dep, err := r.deployment()
	if err != nil {
		return r.fail("S3", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		return r.fail("S3", k8sSWHarness, "the legacy strategy patch did not apply (strategy %s)", dep.Spec.Strategy.Type)
	}
	rsMid, err := r.replicaSetUIDs()
	if err != nil {
		return r.fail("S3", k8sSWHarness, "cannot list the ReplicaSets: %s", err)
	}
	if !k8sSWSameSet(rsBefore, rsMid) {
		return r.fail("S3", k8sSWHarness, "the strategy-only patch changed the ReplicaSets (%d -> %d): the legacy state cannot be set up", len(rsBefore), len(rsMid))
	}
	if v := r.analyze(st, 0, nil); !v.ok() {
		if v.Class == k8sSWProduct {
			v.Class, v.Detail = k8sSWHarness, "the legacy state cannot be set up: "+v.Detail
		}
		return r.failVerdict("S3", v)
	}
	r.logf("OBSERVE legacy Deployment at zero replicas: strategy RollingUpdate, generation %d observed, no Pod", patched.Generation)

	// again, immediately before the production patch: still stopped, and under the injected legacy strategy
	if !r.requireStopped("S3", appsv1.RollingUpdateDeploymentStrategyType, true, "before the force re-pull") {
		return false
	}
	st, ok = r.begin("S3", true)
	if !ok {
		return false
	}
	if err := r.cl.K8SForceRepullDatabaseService(r.server); err != nil {
		return r.fail("S3", k8sSWHarness, "the force re-pull failed: %s", err)
	}
	dep, err = r.deployment()
	if err != nil {
		return r.fail("S3", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if err := r.waitObservedGeneration(dep.Generation, k8sSWSyncWait); err != nil {
		return r.fail("S3", k8sSWInconclusive, "%s", err)
	}
	if dep, err = r.deployment(); err != nil {
		return r.fail("S3", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if dep.UID != r.depUID {
		return r.fail("S3", k8sSWProduct, "the Deployment was replaced (uid %s, was %s), the migration must keep it", dep.UID, r.depUID)
	}
	if !k8sSWIsRecreate(dep) {
		return r.fail("S3", k8sSWProduct, "the Deployment was not migrated: strategy %s, rollingUpdate set: %t", dep.Spec.Strategy.Type, dep.Spec.Strategy.RollingUpdate != nil)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		return r.fail("S3", k8sSWProduct, "the migration patch changed the replicas: a Pod may start without the guard")
	}
	if v := r.analyze(st, 0, nil); !v.ok() {
		return r.failVerdict("S3", v)
	}
	if rsAfter, err := r.replicaSetUIDs(); err == nil {
		r.logf("OBSERVE ReplicaSets of the Deployment: %d before the migration patch, %d after (not asserted)", len(rsMid), len(rsAfter))
	}

	// the controlled startup: through the guarded lifecycle, one new Pod owned by the Deployment
	v, ok := r.operation("S3", 1, func() (k8sSWClass, error) {
		if _, sv := r.startOnce("", ""); sv != nil {
			return sv.Class, errors.New(sv.Detail)
		}
		return "", nil
	})
	if !ok {
		return false
	}
	r.pass("S3", "the legacy strategy was migrated to Recreate by the production patch at zero replicas with no Pod appearing, then Start created one new Pod: "+v.Detail)
	return true
}

// setLifecycle sends the JSON patch that makes the lifecycle of the database container desired. The
// strategy is read first: under RollingUpdate a template patch would start a second database Pod. The
// attempt is recorded before the patch is sent.
func (r *k8sSWRun) setLifecycle(desired *apiv1.Lifecycle) (k8sSWClass, error) {
	dep, err := r.deployment()
	if err != nil {
		return k8sSWHarness, fmt.Errorf("cannot read the Deployment: %w", err)
	}
	if reason := k8sSWTemplatePatchRefusal(dep); reason != "" {
		return k8sSWHarness, errors.New(reason)
	}
	patch, needed, err := k8sSWLifecycleJSONPatch(dep, r.server.Name, desired)
	if err != nil {
		return k8sSWHarness, err
	}
	if !needed {
		return k8sSWHarness, errors.New("the Deployment already carries the lifecycle the step must set: nothing would change")
	}
	r.lifecycleTouched = true
	if _, err := r.patchDeployment(types.JSONPatchType, patch); err != nil {
		return k8sSWHarness, fmt.Errorf("the lifecycle patch failed (it may have been applied; cleanup inspects the Deployment): %w", err)
	}
	return "", nil
}

// stepPreStopOn adds the temporary preStop delay: a change of the Pod template, hence exactly one
// rollout (Recreate: the old Pod stops, then the new one starts with the delay).
func (r *k8sSWRun) stepPreStopOn() bool {
	v, ok := r.operation("S5a", 1, func() (k8sSWClass, error) {
		return r.setLifecycle(k8sSWDesiredWithPreStop(r.orig.Lifecycle, k8sSWPreStopSeconds))
	})
	if !ok {
		return false
	}
	r.pass("S5a", fmt.Sprintf("the temporary preStop delay (%ds) was added by one rollout: %s", k8sSWPreStopSeconds, v.Detail))
	return true
}

func (r *k8sSWRun) stepImmediateStart() bool   { return r.terminatingStart("S5", 1) }
func (r *k8sSWRun) stepConcurrentStarts() bool { return r.terminatingStart("S6", 2) }

// terminatingStart stops the database, waits until the old Pod is OBSERVED Terminating (its preStop delay
// keeps its database running), then calls Start (starts times, the second one second later). Until the old
// Pod is gone no scale-up and no second database Pod may be seen; Start must then succeed within the bound
// (no false timeout on the Pod another Start created), and one new Pod must result. The number of PATCH
// requests is not asserted: the unit tests cover it. A window that is not exercised is INCONCLUSIVE.
func (r *k8sSWRun) terminatingStart(step string, starts int) bool {
	for attempt := 1; attempt <= 2; attempt++ {
		st, ok := r.begin(step, true)
		if !ok {
			return false
		}
		var held *k8sSWOldPod
		for i := range st.old {
			if st.old[i].DBRunning {
				held = &st.old[i]
				break
			}
		}
		if held == nil {
			return r.fail(step, k8sSWHarness, "no running database Pod to stop")
		}
		if err := r.cl.StopDatabaseService(r.server); err != nil {
			return r.fail(step, k8sSWHarness, "Stop failed: %s", err)
		}
		if !r.waitPodTerminating(held.Name, held.UID, 20*time.Second) {
			r.logf("%s: the old Pod was not observed Terminating (attempt %d): the window was missed", step, attempt)
			// bring the database back before a second attempt, and only once no Pod remains
			if err := r.waitNoDBPods(k8sSWWait); err != nil {
				return r.failWait(step, err)
			}
			if _, sv := r.startOnce("", ""); sv != nil {
				return r.failVerdict(step, *sv)
			}
			if _, err := r.waitReplacement(st.oldSet, k8sSWWait); err != nil {
				return r.failWait(step, err)
			}
			continue
		}
		if err := k8sSWAwaitInitial(r.client, r.ns, r.server.Name, held.Name, held.UID, 15*time.Second, 500*time.Millisecond); err != nil {
			return r.fail(step, k8sSWInconclusive, "%s: Start was not called", err)
		}
		invoke, stop := r.startObserver(held.Name, held.UID)
		invoke()
		type startResult struct {
			elapsed time.Duration
			verdict *k8sSWVerdict
		}
		results := make([]startResult, starts)
		var wg sync.WaitGroup
		for i := 0; i < starts; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i == 1 {
					time.Sleep(time.Second)
				}
				e, v := r.startOnce(held.Name, held.UID)
				results[i] = startResult{e, v}
			}(i)
		}
		wg.Wait()
		end := time.Now()
		win := stop()
		_, _, terminating, violation := win.result()
		var startVerdict *k8sSWVerdict
		for i, x := range results {
			if x.verdict != nil && startVerdict == nil {
				v := *x.verdict
				v.Detail = fmt.Sprintf("Start %d: %s", i+1, v.Detail)
				startVerdict = &v
			}
			if starts > 1 && x.verdict == nil && x.elapsed >= k8sSWGuardMin {
				return r.fail(step, k8sSWProduct, "Start %d took %s: it waited on the Pod the other Start created (false timeout)", i+1, x.elapsed.Round(time.Second))
			}
		}
		still, serr := r.podPresent(held.Name, held.UID)
		if serr != nil {
			return r.fail(step, k8sSWHarness, "cannot read the old Pod after Start: %s", serr)
		}
		if jv := k8sSWJudgeHeldStart(startVerdict, violation, win.coverage(end), still != nil, terminating, "the old Pod "+held.Name); !jv.ok() {
			if jv.Class == k8sSWProduct {
				r.stopScaledUp(step)
			}
			return r.failVerdict(step, jv)
		}
		v, ok := r.complete(step, st, 1)
		if !ok {
			return false
		}
		dep, err := r.deployment()
		if err != nil || dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
			return r.fail(step, k8sSWProduct, "the Deployment is not at one replica after %d Start(s) (%v)", starts, err)
		}
		r.pass(step, fmt.Sprintf("%d Start(s) while the old Pod was Terminating: %d poll(s) saw it Terminating with zero replicas and no other database Pod; Starts returned in %s; %s", starts, terminating, startElapsed(results[0].elapsed, results[starts-1].elapsed), v.Detail))
		return true
	}
	return r.fail(step, k8sSWInconclusive, "the old Pod was never observed Terminating before Start: the scenario was not exercised")
}

func startElapsed(a, b time.Duration) string {
	return a.Round(time.Second).String() + " and " + b.Round(time.Second).String()
}

// stepPreStopOff restores the exact original lifecycle. If the Deployment already holds it, the delay
// never was on it and S5/S6 did not run with it.
func (r *k8sSWRun) stepPreStopOff() bool {
	dep, err := r.deployment()
	if err != nil {
		return r.fail("S7", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if _, needed, perr := k8sSWLifecycleJSONPatch(dep, r.server.Name, r.orig.Lifecycle); perr != nil {
		return r.fail("S7", k8sSWHarness, "%s", perr)
	} else if !needed {
		return r.fail("S7", k8sSWInconclusive, "the temporary preStop delay is not on the Deployment: S5 and S6 cannot have run with it")
	}
	if _, ok := r.operation("S7", 1, func() (k8sSWClass, error) { return r.setLifecycle(r.orig.Lifecycle) }); !ok {
		return false
	}
	dep, err = r.deployment()
	if err != nil {
		return r.fail("S7", k8sSWHarness, "cannot read the Deployment: %s", err)
	}
	if _, needed, perr := k8sSWLifecycleJSONPatch(dep, r.server.Name, r.orig.Lifecycle); perr != nil || needed {
		return r.fail("S7", k8sSWHarness, "the original lifecycle was not restored exactly (%v)", perr)
	}
	r.pass("S7", "the original lifecycle of the database container was restored exactly (one rollout) and the database is healthy")
	return true
}

func (r *k8sSWRun) stepRolloutOps() bool {
	for _, op := range []struct {
		step, name string
		run        func() error
	}{
		{"S8", "force re-pull", func() error { return r.cl.K8SForceRepullDatabaseService(r.server) }},
		{"S8b", "restart", func() error { return r.cl.K8SRestartDatabaseService(r.server) }},
	} {
		v, ok := r.operation(op.step, 1, func() (k8sSWClass, error) { return k8sSWHarness, op.run() })
		if !ok {
			return false
		}
		r.pass(op.step, op.name+": "+v.Detail)
	}
	return true
}

// ---------------------------------------------------------------------------------------------
// Cleanup: best effort, only what the test owns, ordinary deletion, no promise
// ---------------------------------------------------------------------------------------------

// cleanup returns what it could not resolve: a holder Pod, a temporary change of the Deployment, the SQL
// fixture. It never starts a database, so a fixture in a database that is not reachable is left.
func (r *k8sSWRun) cleanup() (left []string) {
	if r.watch != nil {
		r.watch.Stop()
		r.watch = nil
	}
	if !r.mutating {
		return nil
	}
	if msg := r.resolveHolderPending(); msg != "" {
		r.logf("CLEANUP LEFT: %s", msg)
		left = append(left, msg)
	}
	if r.holder != "" {
		if err := r.removeHolder(); err != nil {
			r.logf("CLEANUP LEFT: the holder Pod %s: %s", r.holder, err)
			left = append(left, fmt.Sprintf("the holder Pod %s", r.holder))
		} else {
			r.logf("CLEANUP the holder Pod was removed")
		}
	}
	env := k8sSWCleanupEnv{
		Client: r.client, NS: r.ns, Server: r.server.Name,
		Pods: r.dbPods,
		Replaced: func(old map[types.UID]bool) error {
			_, err := r.waitReplacement(old, k8sSWWait)
			return err
		},
	}
	done, notRestored := k8sSWRestore(env, r.orig, r.strategyTouched, r.lifecycleTouched)
	for _, d := range done {
		r.logf("CLEANUP %s", d)
	}
	for _, l := range notRestored {
		r.logf("CLEANUP LEFT: %s", l)
	}
	left = append(left, notRestored...)
	if r.fixtureOwned || r.fixtureAmbiguous {
		r.server.Refresh()
		var drop func() error
		if !r.server.IsDown() && r.server.Conn != nil {
			drop = func() error {
				_, err := dbhelper.DropDatabaseIfExists(r.server.Conn, r.server.DBVersion, r.schema)
				return err
			}
		}
		if residue := k8sSWFixtureCleanup(r.fixtureOwned, r.fixtureAmbiguous, drop, r.schema); residue != "" {
			r.logf("CLEANUP LEFT: %s", residue)
			left = append(left, residue)
		} else {
			r.logf("CLEANUP the SQL fixture %s was dropped", r.schema)
		}
	}
	state := "unknown"
	if dep, err := r.deployment(); err == nil && dep.Spec.Replicas != nil {
		if *dep.Spec.Replicas == 0 {
			state = "stopped (not started again by the test after a failure)"
		} else if p, _ := r.currentDBPod(); p != nil {
			state = "running"
		} else {
			state = "starting or not ready"
		}
	}
	r.logf("CLEANUP FINAL database %s is %s", r.server.Name, state)
	return left
}
