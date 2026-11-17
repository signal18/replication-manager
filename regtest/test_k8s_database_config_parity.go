// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/k8sexec"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// TestK8SDatabaseConfigParity is the real-cluster regression test of #2395: a Kubernetes database whose
// data claim has the service-dirs-v1 layout gets the /etc/mysql tree that replication-manager generates (the
// archive it serves to the init container, produced by the same configurator that serves OpenSVC), the
// server runs on it, and a guarded Stop and Start keeps it. The test compares with that generated archive:
// it does not run OpenSVC and does not compare with an OpenSVC instance. The configuration is asserted on a
// curated set of settings (see C3) and on the hash of every generated file, not on every setting the server
// loads. Run by name only:
// POST /api/clusters/<cluster>/tests/actions/run/testK8SDatabaseConfigParity
//
// It is not in the ALL list and declares no targets, because it stops and restarts its database. It is SKIP,
// before anything is changed, unless the orchestrator is Kubernetes, the cluster has exactly one server, the
// namespace carries regtest.replication-manager/disposable=true, the data claim carries the annotation
// replication-manager.signal18.io/pvc-layout=service-dirs-v1 (a legacy claim is not what it tests) and, on
// MySQL and Percona, the cluster runs with the mysqlgtid tag. An ineligible cluster is never a PASS, and a
// required check that did not run is not a PASS either (k8sCPRequired).
//
// Where each observation comes from:
//   - Kubernetes API reads: the claim's annotation and UID, the Deployment strategy and the mounts of the
//     init, database and dbjobs containers, the running Pod's mounts and claim (C1, C2, C8);
//   - SQL, through dbhelper: for a curated set of options (k8sCPSpecs), the value the generated
//     configuration sets and the origin the server reports for it (an option file under /etc/mysql, not the
//     command line or a default), the GTID and binary-log state, the identity of the server, a data digest
//     (C3, C4, C7);
//   - pods/exec (utils/k8sexec), only for what is not visible any other way: what the RUNNING database and
//     dbjobs containers see at /etc/mysql: the mount points, and the hash of every generated file (C5, C6). The content of a file is never read or logged, only its name,
//     size and hash.
//
// The expected values are not written in the test: they are read from the archive replication-manager serves
// to the init container (the same GetDatabaseConfig the /config endpoint runs), parsed like the server
// parses it. The layout contract (data, etc/mysql, init) is written out in the test, as an oracle independent
// of the product's constants.
//
//	C0  eligibility, permissions (k8sCPPermissions and create on pods/exec), one database Pod
//	C1  the claim's annotation, Deployment strategy Recreate, mounts of the three containers (init and dbjobs:
//	    data, etc/mysql, init; database: data, etc/mysql, like OpenSVC), the running Pod
//	S1  unique SQL fixture, identities, the baseline (server id, UUID, GTID position, stable settings)
//	C2  the guarded Stop and Start; the replacement Pod mounts the same claim
//	C3  a curated set of generated options (server_id, max_connections, the log_bin prefix, the InnoDB data,
//	    log and undo directories, binlog_format and, on MySQL/Percona, gtid_mode and
//	    enforce_gtid_consistency) is the server's value; all of them but the derived log_bin are reported by
//	    the server as coming from an option file under /etc/mysql
//	C4  binary logging and GTID: MySQL/Percona gtid_mode and enforce_gtid_consistency ON; MariaDB a binlog GTID
//	    position written by this server id
//	C5  the running database container: data and etc/mysql are mount points, conf.d is not a mount of its
//	    own, a non-empty my.cnf and conf.d, every generated etc/mysql file present with its hash. Which
//	    directory of the claim each mount comes from is the subPath contract of C1/C8, not read from the
//	    storage driver's mount roots
//	C6  the same in the dbjobs container (which also mounts init)
//	C7  the fixture, the claim UID, the server id and UUID, the GTID position and every followed setting
//	    (value, origin, option file) survived
//	C8  the annotation, the claim, the Deployment and the mounts are what they were
//
// Nothing is deleted or patched. The only change of state is the Stop and Start through the lifecycle
// functions and the test's own SQL fixture, dropped by the cleanup; the cleanup never starts a database.
// The RBAC the test needs beyond the lifecycle test is create on pods/exec; it is not part of the product's
// Role, and without it the test says so (HARNESS) instead of passing.
func (regtest *RegTest) TestK8SDatabaseConfigParity(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := &k8sCPRun{k8sSWRun: k8sSWRun{cl: cl, test: test, id: k8sSWRandomID()}}
	return r.finish(r.run())
}

type k8sCPRun struct {
	k8sSWRun
	cfg *rest.Config
	env k8sCPEnv

	gen      *k8sCPTree
	genPre   *k8sCPTree // generated before the lifecycle: what differs from gen is not stable between the two
	volatile map[string]bool

	// the baseline, before the lifecycle
	b0ID, b0UUID, b0GTID string
	b0All                map[string]string
	live                 map[string]dbhelper.VariableOrigin
	rows                 []k8sCPRow
	stopped              bool // the test stopped the database and has not seen it running again
}

// k8sCPStableVars are the variables whose value and origin a restart must not change.
var k8sCPStableVars = []string{"server_id", "log_bin", "log_bin_basename", "innodb_data_home_dir", "innodb_log_group_home_dir"}

func (r *k8sCPRun) finish(ok bool) bool { return r.conclude(ok, r.cleanup()) }

// cleanup is the lifecycle test's cleanup (the test's own SQL fixture; the Deployment is not touched, so
// there is nothing to restore there) plus the one temporary change this test makes: a database it stopped
// and did not see running again is reported, not silently left. The cleanup never starts a database.
func (r *k8sCPRun) cleanup() []string {
	left := r.k8sSWRun.cleanup()
	if r.stopped {
		msg := "the database was stopped by the test and was not seen running again; the cleanup never starts a database: start it through the lifecycle (Start)"
		if dep, err := r.deployment(); err == nil && dep.Spec.Replicas != nil {
			msg += fmt.Sprintf(" (the Deployment has %d replica(s))", *dep.Spec.Replicas)
		}
		r.logf("CLEANUP LEFT: %s", msg)
		left = append(left, msg)
	}
	return left
}

func (r *k8sCPRun) conclude(ok bool, left []string) bool {
	if ok && r.skipped == "" {
		if missing := k8sCPMissingChecks(r.passed); len(missing) > 0 {
			ok = false
			r.failedStep, r.failedCls = "REQUIRED", k8sSWHarness
			r.logf("CHECK REQUIRED FAIL[%s]: the required check(s) %s did not pass or did not run: not a PASS", k8sSWHarness, strings.Join(missing, ","))
		}
	}
	if ok && len(left) > 0 {
		ok = false
		r.failedStep, r.failedCls = "CLEANUP", k8sSWHarness
		r.logf("CHECK CLEANUP FAIL[%s]: the test passed but its cleanup left %d object(s) unresolved: %s", k8sSWHarness, len(left), strings.Join(left, "; "))
	} else if len(left) > 0 {
		r.logf("CLEANUP UNRESOLVED (reported apart, the failure above keeps its class): %s", strings.Join(left, "; "))
	}
	switch {
	case r.skipped != "":
		r.logf("SUMMARY %s result=SKIP reason=%q (nothing was changed)", k8sCPTestName, r.skipped)
	case ok:
		r.logf("SUMMARY %s result=PASS engine=%s checks=%s", k8sCPTestName, r.engine(), strings.Join(r.passed, ","))
	default:
		r.logf("SUMMARY %s result=FAIL class=%s step=%s engine=%s passed=%s", k8sCPTestName, r.failedCls, r.failedStep, r.engine(), strings.Join(r.passed, ","))
	}
	return ok
}

func (r *k8sCPRun) engine() string {
	if r.server == nil || r.server.DBVersion == nil {
		return "unknown"
	}
	v := r.server.DBVersion
	return fmt.Sprintf("%s-%d.%d", v.Flavor, v.Major, v.Minor)
}

// k8sCPPermissions are what this test reads and patches itself, beyond what the lifecycle functions need
// (the product's own Role): it creates and deletes no Pod and watches nothing. Create on pods/exec is
// checked apart (k8sCPExecAllowed).
var k8sCPPermissions = []k8sSWPermission{
	{Verb: "get", Resource: "namespaces", Cluster: true},
	{Verb: "get", Resource: "pods"},
	{Verb: "list", Resource: "pods"},
	{Verb: "get", Group: "apps", Resource: "replicasets"},
	{Verb: "list", Group: "apps", Resource: "replicasets"},
	{Verb: "get", Group: "apps", Resource: "deployments"},
	{Verb: "get", Resource: "persistentvolumeclaims"},
}

// k8sCPMissingPermissions asks the API server what the identity may do (SelfSubjectAccessReview) for the
// permissions of this test. A review that cannot be made is an error (HARNESS), never an empty answer.
func k8sCPMissingPermissions(ctx context.Context, client kubernetes.Interface, namespace string) ([]string, error) {
	var missing []string
	for _, p := range k8sCPPermissions {
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

// k8sCPExecAllowed asks whether the identity may create pods/exec in the namespace.
func k8sCPExecAllowed(ctx context.Context, client kubernetes.Interface, namespace string) (bool, error) {
	review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{Namespace: namespace, Verb: "create", Resource: "pods", Subresource: "exec"}},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("cannot check the permission create pods/exec: %w", err)
	}
	return review.Status.Allowed, nil
}

func (r *k8sCPRun) run() bool {
	servers := r.cl.GetServers()
	if reason := k8sSWTopologyEligibility(r.cl.GetOrchestrator(), len(servers)); reason != "" {
		return r.skip(reason)
	}
	r.server = servers[0]
	r.ns = r.cl.Name
	client, err := r.cl.K8SConnectAPI()
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot connect to the Kubernetes API: %s", err)
	}
	r.client = client
	r.cfg, err = clientcmd.BuildConfigFromFlags("", r.cl.Conf.KubeConfig)
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot load the Kubernetes REST configuration: %s", err)
	}
	ctx := context.Background()

	missing, err := k8sCPMissingPermissions(ctx, client, r.ns)
	if err != nil {
		return r.fail("C0", k8sSWHarness, "%s", err)
	}
	if allowed, err := k8sCPExecAllowed(ctx, client, r.ns); err != nil {
		return r.fail("C0", k8sSWHarness, "%s", err)
	} else if !allowed {
		missing = append(missing, "create pods/exec")
	}
	if len(missing) > 0 {
		return r.fail("C0", k8sSWHarness, "the identity of replication-manager lacks permissions the test needs in namespace %s: %s (create on pods/exec is needed by this test only and is not part of the product's Role)", r.ns, strings.Join(missing, ", "))
	}
	nsObj, err := client.CoreV1().Namespaces().Get(ctx, r.ns, metav1.GetOptions{})
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot read namespace %s: %s", r.ns, err)
	}
	if reason := k8sSWNamespaceEligibility(r.ns, nsObj.Labels); reason != "" {
		return r.skip(reason)
	}
	dep, err := client.AppsV1().Deployments(r.ns).Get(ctx, r.server.Name, metav1.GetOptions{})
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot read the Deployment %s (is the server provisioned?): %s", r.server.Name, err)
	}
	claim, problem := k8sSWDataClaim(dep, r.server.Name)
	if problem != "" {
		return r.fail("C0", k8sSWHarness, "precondition: %s", problem)
	}
	r.claim = claim
	r.server.Refresh()
	if r.server.IsDown() || r.server.DBVersion == nil {
		return r.fail("C0", k8sSWHarness, "the database %s is not healthy before the test", r.server.URL)
	}
	r.env = k8sCPEnv{Maria: r.server.DBVersion.IsMariaDB(), GTIDTag: r.cl.Configurator.HaveDBTag("mysqlgtid")}
	if r.server.DBVersion.IsPostgreSQL() {
		return r.skip("the test is for MariaDB, MySQL and Percona")
	}
	if !r.env.Maria && !r.env.GTIDTag {
		return r.skip("MySQL and Percona are tested with the mysqlgtid tag: the cluster does not declare it")
	}
	dbPods, err := r.dbPods()
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot list the database Pods: %s", err)
	}
	pod, problem := k8sSWBaselinePod(dbPods, r.server.Name, r.claim, dep, func(name string) (*appsv1.ReplicaSet, error) {
		return client.AppsV1().ReplicaSets(r.ns).Get(ctx, name, metav1.GetOptions{})
	})
	if problem != "" {
		return r.fail("C0", k8sSWHarness, "precondition: the starting state is not a single database Pod owned by the Deployment: %s", problem)
	}
	all, err := client.CoreV1().Pods(r.ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return r.fail("C0", k8sSWHarness, "cannot list the Pods of namespace %s: %s", r.ns, err)
	}
	if foreign := k8sSWForeignConsumers(all.Items, r.claim, pod.UID); len(foreign) > 0 {
		return r.skip(fmt.Sprintf("Pod(s) %s, other than the database Pod, reference the data PVC %s: not a clean disposable setup", strings.Join(foreign, ","), r.claim))
	}
	r.node = pod.Spec.NodeName
	r.pass("C0", fmt.Sprintf("engine %s, one server %s, namespace %s labelled disposable, permissions present (pods/exec included), claim %s, one database Pod %s", r.engine(), r.server.Name, r.ns, r.claim, pod.Name))

	// C1 reads only; an unannotated claim is a SKIP, before anything is changed
	if skip, ok := r.stepLayout("C1"); skip != "" {
		return r.skip(skip)
	} else if !ok {
		return false
	}

	// from here on the test changes things
	r.mutating = true
	if !r.stepFixture(dep) || !r.stepBaseline() {
		return false
	}
	return r.stepLifecycle() &&
		r.stepEffective() &&
		r.stepGTID() &&
		r.stepContainer("C5", r.server.Name, "database") &&
		r.stepContainer("C6", r.server.Name+"-dbjobs", "dbjobs") &&
		r.stepPersistence() &&
		r.stepLayoutAgain()
}

// ---------------------------------------------------------------------------------------------
// C1, C8: the layout, from the Kubernetes API
// ---------------------------------------------------------------------------------------------

// stepLayout checks the layout contract on the claim, the Deployment and the running Pod. skip is a reason
// to skip (an unannotated claim); ok is false after a failure.
func (r *k8sCPRun) stepLayout(step string) (skip string, ok bool) {
	ctx := context.Background()
	pvc, err := r.client.CoreV1().PersistentVolumeClaims(r.ns).Get(ctx, r.claim, metav1.GetOptions{})
	if err != nil {
		return "", r.fail(step, r.waitErrClass(err), "cannot read the claim %s: %s", r.claim, err)
	}
	skip, bad := k8sCPLayoutVerdict(pvc.Annotations)
	if skip != "" {
		return skip, false
	}
	if bad != "" {
		return "", r.fail(step, k8sSWProduct, "%s", bad)
	}
	if r.pvcUID != "" && pvc.UID != r.pvcUID {
		return "", r.fail(step, k8sSWProduct, "the claim %s was replaced (uid %s, was %s)", r.claim, pvc.UID, r.pvcUID)
	}
	dep, err := r.deployment()
	if err != nil {
		return "", r.fail(step, r.waitErrClass(err), "cannot read the Deployment: %s", err)
	}
	if r.depUID != "" && dep.UID != r.depUID {
		return "", r.fail(step, k8sSWProduct, "the Deployment was replaced (uid %s, was %s)", dep.UID, r.depUID)
	}
	if !k8sSWIsRecreate(dep) {
		return "", r.fail(step, k8sSWProduct, "the Deployment strategy is %s, expected Recreate", dep.Spec.Strategy.Type)
	}
	if problems := k8sCPMountProblems(dep.Spec.Template.Spec, r.server.Name, r.claim); len(problems) > 0 {
		return "", r.fail(step, k8sSWProduct, "the Deployment's Pod template does not follow the layout: %s", strings.Join(problems, "; "))
	}
	pod, err := r.currentDBPod()
	if err != nil {
		return "", r.fail(step, r.waitErrClass(err), "cannot read the database Pod: %s", err)
	}
	if pod == nil {
		return "", r.fail(step, k8sSWInconclusive, "there is no running database Pod to check")
	}
	if problems := k8sCPMountProblems(pod.Spec, r.server.Name, r.claim); len(problems) > 0 {
		return "", r.fail(step, k8sSWProduct, "the running Pod %s does not follow the layout: %s", pod.Name, strings.Join(problems, "; "))
	}
	if claim, problem := k8sSWPodDataClaim(pod, r.server.Name); problem != "" || claim != r.claim {
		return "", r.fail(step, k8sSWProduct, "the running Pod %s uses the claim %q (%s), expected %s", pod.Name, claim, problem, r.claim)
	}
	r.pass(step, fmt.Sprintf("claim %s annotated %s, Deployment Recreate, init and dbjobs mount data, etc/mysql and init of the claim, the database data and etc/mysql, no .system or conf.d mount, running Pod %s uses the claim", r.claim, k8sCPLayoutValue, pod.Name))
	return "", true
}

func (r *k8sCPRun) stepLayoutAgain() bool {
	if skip, ok := r.stepLayout("C8"); skip != "" {
		return r.fail("C8", k8sSWProduct, "the claim lost its layout annotation: %s", skip)
	} else if !ok {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------------------------
// S1 baseline, C2 lifecycle
// ---------------------------------------------------------------------------------------------

func (r *k8sCPRun) variable(name string) (string, error) {
	v, _, err := dbhelper.GetVariableByName(r.server.Conn, name, r.server.DBVersion)
	return v, err
}

func (r *k8sCPRun) gtidVariable() string {
	if r.env.Maria {
		return "gtid_binlog_pos"
	}
	return "gtid_executed"
}

// snapshotAll is the value, origin and option file of every variable the test follows.
func (r *k8sCPRun) snapshotAll() (map[string]string, error) {
	got, _, err := dbhelper.GetVariableOrigins(r.server.Conn, r.server.DBVersion, k8sCPVariableNames(r.env))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, v := range got {
		out[strings.ToLower(name)] = v.Value + "|" + v.Origin + "|" + v.Path
	}
	return out, nil
}

func (r *k8sCPRun) stepBaseline() bool {
	var err error
	if r.b0ID, err = r.variable("server_id"); err != nil {
		return r.fail("S1", k8sSWInconclusive, "cannot read server_id: %s", err)
	}
	if !r.env.Maria {
		if r.b0UUID, err = r.variable("server_uuid"); err != nil {
			return r.fail("S1", k8sSWInconclusive, "cannot read server_uuid: %s", err)
		}
	}
	if r.b0GTID, err = r.variable(r.gtidVariable()); err != nil {
		return r.fail("S1", k8sSWInconclusive, "cannot read %s: %s", r.gtidVariable(), err)
	}
	if r.b0All, err = r.snapshotAll(); err != nil {
		return r.fail("S1", k8sSWInconclusive, "cannot read the origin of the followed settings: %s", err)
	}
	if r.genPre, err = r.generate(); err != nil {
		return r.fail("S1", k8sSWHarness, "%s", err)
	}
	r.logf("OBSERVE baseline: server_id=%s server_uuid=%q gtid position recorded (%d bytes), %d followed variable(s) with their origin", r.b0ID, r.b0UUID, len(r.b0GTID), len(r.b0All))
	return true
}

func (r *k8sCPRun) stepLifecycle() bool {
	step := "C2"
	before, err := r.dbPods()
	if err != nil {
		return r.fail(step, r.waitErrClass(err), "cannot list the database Pods: %s", err)
	}
	old := map[types.UID]bool{}
	for i := range before {
		old[before[i].UID] = true
	}
	r.stopped = true // until the database is seen running again
	if err := r.cl.StopDatabaseService(r.server); err != nil {
		return r.fail(step, k8sSWInconclusive, "StopDatabaseService failed: %s", err)
	}
	if err := r.waitNoDBPods(k8sSWWait); err != nil {
		return r.failWait(step, err)
	}
	if _, err := r.startDB(); err != nil {
		replicas := int32(-1)
		if dep, derr := r.deployment(); derr == nil && dep.Spec.Replicas != nil {
			replicas = *dep.Spec.Replicas
		}
		v := k8sSWClassifyStartError(err, replicas, 0, false)
		return r.failVerdict(step, v)
	}
	pod, err := r.waitReplacement(old, k8sSWWait)
	if err != nil {
		return r.failWait(step, err)
	}
	if err := r.waitSQL(k8sSWWait); err != nil {
		return r.failWait(step, err)
	}
	r.stopped = false
	r.pass(step, fmt.Sprintf("guarded Stop then Start: the replacement Pod %s mounts the same claim %s", pod.Name, r.claim))
	return true
}

// ---------------------------------------------------------------------------------------------
// C3, C4: the running server
// ---------------------------------------------------------------------------------------------

// generate runs the generation the /config endpoint runs and reads the archive it serves.
func (r *k8sCPRun) generate() (*k8sCPTree, error) {
	if err := r.server.GetDatabaseConfig(); err != nil {
		return nil, fmt.Errorf("cannot generate the database configuration: %w", err)
	}
	data, err := readServedArchive(r.server.Datadir)
	if err != nil {
		return nil, err
	}
	return k8sCPReadArchive(data)
}

// loadGenerated generates the tree after the Start, twice, and compares with the one generated before the
// lifecycle: a file that differs between any two of them (apart from the "# Generated by" stamp line) is
// not stable, and so is checked for presence only, as is everything under custom.d.
func (r *k8sCPRun) loadGenerated() error {
	first, err := r.generate()
	if err != nil {
		return err
	}
	second, err := r.generate()
	if err != nil {
		return err
	}
	r.gen, r.volatile = second, k8sCPVolatile(first, second)
	if r.genPre != nil {
		for n := range k8sCPVolatile(r.genPre, second) {
			r.volatile[n] = true
		}
	}
	return nil
}

func (r *k8sCPRun) stepEffective() bool {
	step := "C3"
	if err := r.loadGenerated(); err != nil {
		return r.fail(step, k8sSWHarness, "%s", err)
	}
	v := r.server.DBVersion
	eff, err := k8sCPEffective(r.gen, r.env.Maria, v.Major, v.Minor)
	if err != nil {
		return r.fail(step, k8sSWHarness, "cannot read the generated option files: %s", err)
	}
	live, _, err := dbhelper.GetVariableOrigins(r.server.Conn, v, k8sCPVariableNames(r.env))
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot read the origin of the variables: %s", err)
	}
	r.live = live
	rows, findings := k8sCPEvaluate(r.env, eff, live)
	r.rows = rows
	for _, row := range rows {
		r.logf("OBSERVE option %s: generated=%q server=%q origin=%s path=%s", row.Option, row.Expected, row.Live, row.Origin, row.Path)
	}
	if len(findings) > 0 {
		class, details := k8sSWInconclusive, make([]string, 0, len(findings))
		for _, f := range findings {
			if f.Class == k8sSWProduct {
				class = k8sSWProduct
			}
			details = append(details, f.Detail)
		}
		return r.fail(step, class, "%s", strings.Join(details, "; "))
	}
	fromFile := 0
	for _, row := range rows {
		if row.Origin == "CONFIG" && strings.HasPrefix(row.Path, k8sCPEtcMysqlPrefix) {
			fromFile++
		}
	}
	r.pass(step, fmt.Sprintf("%d followed option(s) of the generated configuration are the server's values; %d of them are reported by the server as coming from an option file under /etc/mysql (log_bin is derived and has no such origin: its non-default prefix is checked, with log_bin ON); %d file(s) are volatile between generations", len(rows), fromFile, len(r.volatile)))
	return true
}

func (r *k8sCPRun) stepGTID() bool {
	step := "C4"
	get := func(name string) (string, bool) {
		v, err := r.variable(name)
		if err != nil {
			r.fail(step, k8sSWInconclusive, "cannot read %s: %s", name, err)
			return "", false
		}
		return v, true
	}
	logbin, ok := get("log_bin")
	if !ok {
		return false
	}
	if k8sCPNormValue(k8sCPBool, logbin) != "ON" {
		return r.fail(step, k8sSWProduct, "binary logging is %s", logbin)
	}
	id, ok := get("server_id")
	if !ok {
		return false
	}
	if r.env.Maria {
		pos, ok := get("gtid_binlog_pos")
		if !ok {
			return false
		}
		if pos == "" || !k8sCPGTIDHasServer(pos, id) {
			return r.fail(step, k8sSWProduct, "the binlog GTID position %q holds no transaction written by server_id %s", pos, id)
		}
		r.pass(step, fmt.Sprintf("MariaDB: log_bin ON and the binlog GTID position holds this server's id %s", id))
		return true
	}
	mode, ok := get("gtid_mode")
	if !ok {
		return false
	}
	enforce, ok := get("enforce_gtid_consistency")
	if !ok {
		return false
	}
	uuid, ok := get("server_uuid")
	if !ok {
		return false
	}
	executed, ok := get("gtid_executed")
	if !ok {
		return false
	}
	switch {
	case k8sCPNormValue(k8sCPStr, mode) != "ON":
		return r.fail(step, k8sSWProduct, "gtid_mode is %s with the mysqlgtid tag", mode)
	case k8sCPNormValue(k8sCPBool, enforce) != "ON":
		return r.fail(step, k8sSWProduct, "enforce_gtid_consistency is %s with the mysqlgtid tag", enforce)
	case !strings.Contains(strings.ToLower(executed), strings.ToLower(uuid)):
		return r.fail(step, k8sSWProduct, "gtid_executed holds no transaction of this server's UUID %s: the fixture was written with binary logging on", uuid)
	}
	r.pass(step, fmt.Sprintf("%s: log_bin ON, gtid_mode ON, enforce_gtid_consistency ON, gtid_executed holds this server's UUID", r.engine()))
	return true
}

// ---------------------------------------------------------------------------------------------
// C5, C6: what a running container sees, through pods/exec
// ---------------------------------------------------------------------------------------------

// execFail classifies a failed observation: a missing permission, an unavailable container and the
// infrastructure are not product failures.
func (r *k8sCPRun) execFail(step, container string, res k8sexec.Result, err error) bool {
	switch k8sexec.KindOf(err) {
	case k8sexec.KindDenied:
		return r.fail(step, k8sSWHarness, "pods/exec was refused (grant create on pods/exec in namespace %s to the identity of replication-manager for this test): %s", r.ns, err)
	case k8sexec.KindNotFound, k8sexec.KindUnavailable:
		return r.fail(step, k8sSWInconclusive, "the container %s is not available for exec: %s", container, err)
	case k8sexec.KindTimeout, k8sexec.KindCanceled:
		return r.fail(step, k8sSWInconclusive, "the observation in %s did not finish: %s", container, err)
	case k8sexec.KindExit:
		return r.fail(step, k8sSWHarness, "the observation script exited %d in %s (a tool the script needs may be missing in the image): %s", res.ExitCode, container, trimLog(res.Stderr))
	case k8sexec.KindInvalid:
		return r.fail(step, k8sSWHarness, "%s", err)
	}
	return r.fail(step, k8sSWResource, "exec in %s failed: %s", container, err)
}

func trimLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// observe runs the observation script in a container of the current database Pod.
func (r *k8sCPRun) observe(step, container string) (*k8sCPObserved, bool) {
	pod, err := r.currentDBPod()
	if err != nil {
		return nil, r.fail(step, r.waitErrClass(err), "cannot read the database Pod: %s", err)
	}
	if pod == nil {
		return nil, r.fail(step, k8sSWInconclusive, "there is no running database Pod to observe")
	}
	running := false
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == container && cs.State.Running != nil {
			running = true
		}
	}
	if !running {
		return nil, r.fail(step, k8sSWInconclusive, "the container %s of Pod %s is not running", container, pod.Name)
	}
	res, err := k8sexec.Run(context.Background(), r.cfg, k8sexec.Target{Namespace: r.ns, Pod: pod.Name, Container: container},
		[]string{"sh", "-c", k8sCPObserveScript}, k8sexec.Options{Timeout: 60 * time.Second})
	if err != nil {
		return nil, r.execFail(step, container, res, err)
	}
	if res.StdoutTruncated {
		return nil, r.fail(step, k8sSWHarness, "the observation of %s is longer than the bound: the tree is larger than expected", container)
	}
	obs, err := k8sCPParseObserved(res.Stdout)
	if err != nil {
		return nil, r.fail(step, k8sSWHarness, "cannot read the observation of %s: %s", container, err)
	}
	return obs, true
}

func (r *k8sCPRun) stepContainer(step, container, role string) bool {
	obs, ok := r.observe(step, container)
	if !ok {
		return false
	}
	withInit := role == "dbjobs" // the database container mounts data and etc/mysql, not init
	problems := k8sCPObservedMountProblems(obs, withInit)
	problems = append(problems, k8sCPTreeProblems(obs, r.gen, r.volatile, withInit)...)
	if len(problems) > 0 {
		return r.fail(step, k8sSWProduct, "the running %s container %s: %s", role, container, strings.Join(problems, "; "))
	}
	r.logf("OBSERVE %s container mounts as the driver reports them (not asserted): etc/mysql from %q, data from %q, init from %q", role, obs.Mounts["etc"], obs.Mounts["data"], obs.Mounts["init"])
	r.pass(step, fmt.Sprintf("%s container %s: the paths %s are mount points, /etc/mysql/conf.d is not a mount of its own, my.cnf non-empty, conf.d has %d entries, the generated files it sees are present with their hash", role, container, map[bool]string{true: "data, etc/mysql, init", false: "data, etc/mysql"}[withInit], obs.ConfdCount))
	return true
}

// ---------------------------------------------------------------------------------------------
// C7: what survived
// ---------------------------------------------------------------------------------------------

func (r *k8sCPRun) stepPersistence() bool {
	step := "C7"
	if !r.integrity(step) { // SQL answers, the claim UID, the fixture digest, a probe write
		return false
	}
	id, err := r.variable("server_id")
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot read server_id: %s", err)
	}
	if id != r.b0ID {
		return r.fail(step, k8sSWProduct, "server_id changed across the restart: %s, was %s", id, r.b0ID)
	}
	if !r.env.Maria {
		uuid, err := r.variable("server_uuid")
		if err != nil {
			return r.fail(step, k8sSWInconclusive, "cannot read server_uuid: %s", err)
		}
		if uuid != r.b0UUID {
			return r.fail(step, k8sSWProduct, "server_uuid changed across the restart: %s, was %s: the database was initialised again", uuid, r.b0UUID)
		}
	}
	now, err := r.variable(r.gtidVariable())
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot read %s: %s", r.gtidVariable(), err)
	}
	if r.env.Maria {
		okPos, why, perr := k8sCPMariaGTIDNotBehind(r.b0GTID, now)
		if perr != nil {
			return r.fail(step, k8sSWInconclusive, "cannot read the GTID position: %s", perr)
		}
		if !okPos {
			return r.fail(step, k8sSWProduct, "the GTID position went back across the restart: %s", why)
		}
	} else {
		contained, gerr := dbhelper.GTIDSubset(r.server.Conn, r.server.DBVersion, r.b0GTID)
		if gerr != nil {
			return r.fail(step, k8sSWInconclusive, "cannot compare the GTID positions: %s", gerr)
		}
		if !contained {
			return r.fail(step, k8sSWProduct, "the GTID position before the restart is not contained in gtid_executed now: transactions were lost")
		}
	}
	snap, err := r.snapshotAll()
	if err != nil {
		return r.fail(step, k8sSWInconclusive, "cannot read the origin of the followed settings: %s", err)
	}
	var stable, other []string
	for _, name := range k8sCPSnapshotDiff(r.b0All, snap) {
		if slices.Contains(k8sCPStableVars, strings.Fields(name)[0]) {
			stable = append(stable, name)
		} else {
			other = append(other, name)
		}
	}
	if len(stable) > 0 {
		return r.fail(step, k8sSWProduct, "the value or origin of %s changed across the restart", strings.Join(stable, ", "))
	}
	if len(other) > 0 {
		return r.fail(step, k8sSWInconclusive, "the value or origin of %s changed across the restart: the generated configuration may have been changed (a setting applied, a tag set) between the two starts", strings.Join(other, ", "))
	}
	r.pass(step, "the fixture digest, the claim UID and a write are intact; server id and UUID unchanged; the GTID position did not go back; every followed setting has the same value, origin and option file")
	return true
}

// readServedArchive reads the archive of a server's configuration.
func readServedArchive(datadir string) ([]byte, error) {
	return readFileBounded(datadir+"/config.tar.gz", k8sCPMaxTotalBytes)
}

// readFileBounded reads a file of at most max bytes.
func readFileBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open the served archive: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the served archive: %w", err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("the served archive is larger than %d bytes", max)
	}
	return data, nil
}
