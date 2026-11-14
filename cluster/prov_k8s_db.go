package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/state"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Best-effort: a least-privilege RBAC setup may lack "namespaces" verbs
// entirely, so Create() can be Forbidden even when the namespace already
// exists. A genuinely missing namespace still surfaces at the
// PVC/Deployment/Service creates below.
func (cluster *Cluster) k8sEnsureNamespace(client kubernetes.Interface, name string) {
	namespace := &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := client.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Cannot create namespace %s ", err)
	}
}

// k8sHeadlessServiceName is the shared headless Service every DB pod
// belongs to, for per-pod DNS. Not cluster.Name-prefixed: Service names
// only need to be unique per namespace, and each cluster has its own.
const k8sHeadlessServiceName = "db"

// k8sRoleLabel distinguishes DB pods from proxy pods (prov_k8s_prx.go) so
// the headless Service's selector doesn't also match proxies.
const k8sRoleLabel = "role"
const k8sRoleDB = "db"

// k8sConfPersistSubPath and k8sInitPersistSubPath are subPath mounts of the
// PVC backing /var/lib/mysql, for /etc/mysql/conf.d and
// /docker-entrypoint-initdb.d -- persisted, not emptyDir, so a failed
// config fetch has something to fall back to. Under ".system/", the same
// repman-reserved subtree systemDirs uses, never touched by MariaDB
// itself.
const k8sConfPersistSubPath = ".system/conf.d"
const k8sInitPersistSubPath = ".system/init"

// k8sClusterDomain resolves the Kubernetes cluster's DNS domain from
// prov-orchestrator-cluster, falling back to "cluster.local" when unset or
// left at "local" (that flag's own CLI default is OpenSVC-oriented, not a
// real --cluster-domain, and would otherwise build an unresolvable
// ".svc.local").
func k8sClusterDomain(cluster *Cluster) string {
	if cluster.Conf.ProvOrchestratorCluster != "" && cluster.Conf.ProvOrchestratorCluster != "local" {
		return cluster.Conf.ProvOrchestratorCluster
	}
	return "cluster.local"
}

// k8sImagePullPolicy mirrors opensvc-image-force-pull: PullAlways when set,
// otherwise an explicit PullIfNotPresent -- Kubernetes' own implicit
// default varies by tag, which is surprising to rely on implicitly.
func k8sImagePullPolicy(cluster *Cluster) apiv1.PullPolicy {
	if cluster.Conf.ProvKubeImageForcePull {
		return apiv1.PullAlways
	}
	return apiv1.PullIfNotPresent
}

// k8sDBAllocatorEnv maps the shared allocator tuning (GetDBAllocatorEnv,
// #1749) onto the DB container env; nil when the feature is disabled.
func k8sDBAllocatorEnv(cluster *Cluster) []apiv1.EnvVar {
	var env []apiv1.EnvVar
	if cluster.DBImageAutoUpgradeEnv() {
		env = append(env, apiv1.EnvVar{Name: "MARIADB_AUTO_UPGRADE", Value: "1"})
	}
	preload, arenaMax := cluster.GetDBAllocatorEnv()
	if preload == "" {
		return env
	}
	return append(env,
		apiv1.EnvVar{Name: "LD_PRELOAD", Value: preload},
		apiv1.EnvVar{Name: "MALLOC_ARENA_MAX", Value: arenaMax},
	)
}

// k8sSecretKeyRootPassword is the key MYSQL_ROOT_PASSWORD is stored under
// on the cluster's shared Secret. One value for the whole cluster, not
// per-server: every server in a replication topology shares the same root
// credential (RotatePasswords, cluster/cluster_sec.go, generates and
// applies exactly one), so a per-server Secret would only ever hold
// duplicate copies of the same value.
const k8sSecretKeyRootPassword = "MYSQL_ROOT_PASSWORD"

// k8sSecretKeyAPIAuthHeader is the key the init container's bootstrap Basic
// Auth value is stored under, on the same shared Secret as
// k8sSecretKeyRootPassword -- also the env var name the init container
// reads it from (k8sDatabaseDeployment), matching OpenSVC's own
// REPLICATION_MANAGER_PASSWORD secret (CreateSecretKeyValueV2,
// prov_opensvc.go) instead of baking the value into the Deployment's own
// command array, recoverable via a plain `kubectl get deploy -o yaml`.
const k8sSecretKeyAPIAuthHeader = "REPMAN_AUTH_HEADER"

// k8sClusterSecretName is shared by every server's Deployment in the
// cluster -- all of them already live in the same namespace (cluster.Name),
// so a single Secret works fine and matches OpenSVC's own single
// cluster-wide secret store instead of duplicating the same value once per
// server.
func k8sClusterSecretName(clusterName string) string {
	return clusterName + "-secret"
}

// k8sPatchSecretValues creates or updates the cluster's shared Secret with
// the given key/value pairs. Update path is a merge Patch, not Update():
// Update() requires the current resourceVersion, which a freshly-built
// object never has; a merge Patch also leaves any other key already on the
// Secret (e.g. the other credential) untouched.
func (cluster *Cluster) k8sPatchSecretValues(client kubernetes.Interface, values map[string]string) error {
	if err := cluster.k8sPatchSecretValuesRaw(context.TODO(), client, values); err != nil {
		return err
	}
	// the values set go to the credential stores, never read back (#2227 rule 7)
	for k, v := range values {
		cluster.reportOrchestratorSecret("env", k, v)
	}
	return nil
}

// k8sPatchSecretValuesRaw creates or merge-patches the cluster's shared Secret with the given
// key/value pairs, WITHOUT reporting them to the credential stores. The StoreWriter copy path
// (k8sStore, #2356) uses it: the version queue is already the store copy, so a report would
// loop the orchestrator's value back into the stores. Update path is a merge Patch, not
// Update() (which requires the current resourceVersion a freshly-built object never has); a
// merge Patch also leaves any other key already on the Secret untouched.
func (cluster *Cluster) k8sPatchSecretValuesRaw(ctx context.Context, client kubernetes.Interface, values map[string]string) error {
	return cluster.k8sPatchNamedSecretRaw(ctx, client, cluster.Name, k8sClusterSecretName(cluster.Name), values)
}

// k8sPatchNamedSecretRaw is k8sPatchSecretValuesRaw on any Secret: namespace/name (the server
// level's repman-system goes through it too).
func (cluster *Cluster) k8sPatchNamedSecretRaw(ctx context.Context, client kubernetes.Interface, namespace, name string, values map[string]string) error {
	secretsClient := client.CoreV1().Secrets(namespace)
	secret := &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type:       apiv1.SecretTypeOpaque,
		StringData: values,
	}
	_, err := secretsClient.Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// json.Marshal, not manual string concatenation: a credential can
		// contain arbitrary characters, and Go's own quoting syntax
		// (strconv.Quote) isn't guaranteed identical to JSON's.
		patch, marshalErr := json.Marshal(struct {
			StringData map[string]string `json:"stringData"`
		}{StringData: values})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = secretsClient.Patch(ctx, name, ktypes.MergePatchType, patch, metav1.PatchOptions{})
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot provision Kubernetes secret %s ", err)
		return err
	}
	return nil
}

// k8sEnsureDatabaseSecret is k8sPatchSecretValues for just
// MYSQL_ROOT_PASSWORD. Takes password explicitly rather than reading
// s.Pass, so ProvisionRotatePasswords (prov.go) can call it directly with
// the freshly rotated value.
func (cluster *Cluster) k8sEnsureDatabaseSecret(client kubernetes.Interface, password string) error {
	// through the store writer, which keeps the superseded value in <KEY>_OLD (#2356), then the
	// stores get the value (rule 7), as k8sPatchSecretValues does
	if _, err := cluster.k8sStore().writeWith(context.TODO(), client, []credstore.StoreItem{{
		Entry: orchestratorStoreEntry, Key: k8sSecretKeyRootPassword, Version: cluster.selfVersion(password),
	}}, cluster.storeTimeout()); err != nil {
		return err
	}
	cluster.reportOrchestratorSecret("env", k8sSecretKeyRootPassword, password)
	return nil
}

// k8sAPIAuthHeaderValue computes the base64 "admin:<password>" Basic Auth
// value the init container's bootstrap wget calls send. Uses "admin",
// falling back to the default password "repman" -- same convention as
// every other bootstrap credential injection in this codebase. Returns ""
// when api-credentials-secure-config is off, since the endpoint doesn't
// enforce auth in that case and embedding a real credential regardless
// would be needless exposure.
func k8sAPIAuthHeaderValue(cluster *Cluster) string {
	if !cluster.Conf.APISecureConfig {
		return ""
	}
	adminPass := "repman"
	if u, ok := cluster.GetAPIUsers()["admin"]; ok {
		adminPass = u.Password
	}
	return base64.StdEncoding.EncodeToString([]byte("admin:" + adminPass))
}

// k8sDatabasePVCName mirrors k8sProxyPVCName (prov_k8s_prx.go) for the
// database side.
func k8sDatabasePVCName(clusterName, serverName string) string {
	return clusterName + "-" + serverName + "-claim"
}

// k8sDatabaseVolumeName mirrors k8sProxyVolumeName (prov_k8s_prx.go) for the
// database side. Just the Pod-internal identifier linking this one Volume to
// its VolumeMounts -- not a globally-unique object name like the PVC's own
// (k8sDatabasePVCName), so it doesn't need the cluster name too.
func k8sDatabaseVolumeName(serverName string) string {
	return serverName + "-data"
}

// k8sDatabaseRunVolumeName is the database container's /run/mysqld (PID file,
// socket). It is an emptyDir, which Kubernetes creates world-writable, so any
// prov-db-run-as-uid can write there: some images ship that directory as
// 0775 owned by their own mysql account (Percona Server: 1001), and mysqld
// then aborts with "can't create PID file" under another UID. OpenSVC gets the
// same from its {name}/run/mysqld volume mount. Like the image directory it
// replaces, it lives as long as the Pod. Only mounted when prov-db-run-as-uid is
// set.
func k8sDatabaseRunVolumeName(serverName string) string {
	return serverName + "-run"
}

// k8sByteQuantity is a Kubernetes quantity with an explicit byte unit: the shapes
// resource.ParseQuantity reads as a volume size ("512Mi", "500M", "1.5G"), never a bare number,
// an exponent or milli-bytes.
var k8sByteQuantity = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)$`)

// k8sDiskQuantity reads a disk size setting (prov-db-disk-size, prov-proxy-disk-size) as the
// volume size it declares. The setting is the repman measurement "G,bytes,required" -- the one
// the DBU alignment writes as a bare number ("20"), the binlog budget and the resize code read,
// and provDiskSizeForOpenSVC writes as "20g": gigabytes by default, 1024-based. Kubernetes
// reads a bare quantity as bytes and "20G" as 10^9, so that number is converted and handed
// over in Gi. A Kubernetes quantity with an explicit byte unit that the measurement does not
// accept ("20Gi", "512Mi", "500M", "1.5G") is kept as written, as it was before: it can reach
// here when the measurement is off or through the runtime API, and it is not portable to
// OpenSVC. A size that is not strictly positive is an error, never a zero-byte request.
func k8sDiskQuantity(value string) (resource.Quantity, error) {
	if gb, err := config.ParseUnitMeasurementToInt("G,bytes,required", value, true); err == nil {
		if gb < 1 {
			return resource.Quantity{}, fmt.Errorf("size %q resolves to zero gigabytes", value)
		}
		return resource.ParseQuantity(strconv.Itoa(gb) + "Gi")
	}
	trimmed := strings.TrimSpace(value)
	if k8sByteQuantity.MatchString(trimmed) {
		if q, err := resource.ParseQuantity(trimmed); err == nil && q.Sign() > 0 {
			return q, nil
		}
	}
	return resource.Quantity{}, fmt.Errorf("size %q is neither a size in gigabytes or larger units (a bare number of gigabytes, or a number with G, T, P, E, Z or Y, optionally followed by B) nor a Kubernetes quantity with a byte unit (Ki, Mi, Gi, Ti, Pi, Ei, k, M, G, T, P, E)", value)
}

// k8sDiskQuantityOrDefault is k8sDiskQuantity for a PVC builder: an unusable size is logged
// and the default of the setting (config.DefaultProvDiskSize, read like any other value) requested,
// so no PVC is ever created with an invalid size.
func (cluster *Cluster) k8sDiskQuantityOrDefault(setting, value string) resource.Quantity {
	size, err := k8sDiskQuantity(value)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot use %s %q (%s), falling back to the default %s ", setting, value, err, config.DefaultProvDiskSize)
		// The default is a constant of this module that k8sDiskQuantity accepts (tested).
		size, _ = k8sDiskQuantity(config.DefaultProvDiskSize)
	}
	return size
}

// k8sDatabasePVC is a pure builder, directly testable. StorageClassName is
// a *string specifically to distinguish "cluster default" (nil) from "no
// StorageClass" (pointer to ""), so prov-kube-storage-class empty must stay
// nil. Size comes from prov-db-disk-size, like every other orchestrator.
func (cluster *Cluster) k8sDatabasePVC(s *ServerMonitor) *apiv1.PersistentVolumeClaim {
	size := cluster.k8sDiskQuantityOrDefault("prov-db-disk-size", cluster.Conf.ProvDisk)
	pvc := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sDatabasePVCName(cluster.Name, s.Name),
		},
		Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteOnce,
			},
			Resources: apiv1.VolumeResourceRequirements{
				Requests: apiv1.ResourceList{
					apiv1.ResourceName(apiv1.ResourceStorage): size,
				},
			},
		},
	}
	if cluster.Conf.ProvKubeStorageClass != "" {
		storageClass := cluster.Conf.ProvKubeStorageClass
		pvc.Spec.StorageClassName = &storageClass
	}
	return pvc
}

// k8sStorageClassesFromClient lists available StorageClass names, for the
// provisioning GUI's dropdown -- same testable/live-wrapper split as
// k8sNodesFromClient/K8SGetNodes (prov_k8s.go).
func (cluster *Cluster) k8sStorageClassesFromClient(client kubernetes.Interface) ([]string, error) {
	scs, err := client.StorageV1().StorageClasses().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot list Kubernetes storage classes %s ", err)
		return nil, err
	}
	names := make([]string, 0, len(scs.Items))
	for _, sc := range scs.Items {
		names = append(names, sc.Name)
	}
	return names, nil
}

func (cluster *Cluster) K8SGetStorageClasses() ([]string, error) {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return nil, err
	}
	return cluster.k8sStorageClassesFromClient(client)
}

// k8sEnsureHeadlessService creates the shared headless Service if it doesn't
// already exist, selecting every DB pod (app+role, not the per-server "tag")
// so each one's Hostname+Subdomain DNS record gets published.
func (cluster *Cluster) k8sEnsureHeadlessService(client kubernetes.Interface, port int) {
	svc := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sHeadlessServiceName,
		},
		Spec: apiv1.ServiceSpec{
			ClusterIP: apiv1.ClusterIPNone,
			Ports: []apiv1.ServicePort{
				{
					Name:     "mysql",
					Protocol: apiv1.ProtocolTCP,
					Port:     int32(port),
				},
			},
			Selector: map[string]string{
				"app":        "repication-manager",
				k8sRoleLabel: k8sRoleDB,
			},
		},
	}
	_, err := client.CoreV1().Services(cluster.Name).Create(context.TODO(), svc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot create Kubernetes headless service %s ", err)
	}
}

// k8sDatabaseDeployment is a pure builder -- no API calls, no
// ServerMonitor methods -- so it's directly testable. NodeSelector, not
// Spec.NodeName: NodeName bypasses the scheduler, which breaks
// WaitForFirstConsumer StorageClass binding (that only happens during
// scheduling).
func (cluster *Cluster) k8sDatabaseDeployment(s *ServerMonitor, port int, nodeHostnameLabel string) *appsv1.Deployment {
	// api-credentials-secure-config requires Basic Auth, sent as a wget
	// --header referencing an env var sourced from this server's own Secret
	// (k8sSecretKeyAPIAuthHeader) rather than baking the base64 value
	// directly into this command string: the Deployment's own command array
	// is plain-text visible via `kubectl get deploy -o yaml`, while a Secret
	// is a separate, often more tightly RBAC-gated resource. Not raw
	// user:pass@host userinfo either, which would let shell metacharacters
	// in the password reach the init container's shell.
	//
	// HTTPS on api-port (10005): api.go's apiserver() always terminates TLS
	// with a self-signed cert (hence --no-check-certificate), matching
	// every other orchestrator's own bootstrap fetch. Falls back to plain
	// HTTP on http-port when api-server=false, since nothing listens on
	// api-port in that case -- otherwise the wget would hang forever with
	// no error.
	scheme := "https"
	noCheckCert := " --no-check-certificate"
	authority := cluster.Conf.MonitorAddress + ":" + cluster.Conf.APIPort
	if !cluster.Conf.ApiServ {
		scheme = "http"
		noCheckCert = ""
		authority = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	}
	// Active/standby (#1942): with the pair's URLs known, the init container
	// first picks the first of the main URL and the pair's URLs that answers
	// the public /api/version, then uses it ($B) for every call below. Without
	// them the command is unchanged (no pod restart for clusters without DR).
	base := scheme + "://" + authority
	baseRef := base
	pickBase := ""
	if cluster.Conf.ApiServ {
		candidates := []string{base}
		for _, u := range strings.Fields(cluster.bootstrapDRURLs()) {
			if !slices.Contains(candidates, u) {
				candidates = append(candidates, u)
			}
		}
		if len(candidates) > 1 {
			baseRef = "$B"
			pickBase = "B=" + base + " ; for u in " + strings.Join(candidates, " ") + " ; do if wget" + noCheckCert + " -T 8 -qO /dev/null $u/api/version 2>/dev/null ; then B=$u ; break ; fi ; done ; "
		}
	}
	authHeaderValue := k8sAPIAuthHeaderValue(cluster)
	authHeader := ""
	if authHeaderValue != "" {
		authHeader = " --header=\"Authorization: Basic $" + k8sSecretKeyAPIAuthHeader + "\""
	}
	// MariaDB's !includedir is non-recursive, so only conf.d fragments are
	// copied in; those reference ./.system/... paths under the datadir that
	// mariadbd needs pre-created (OpenSVC's moduleset does this via
	// directory resources; nothing analogous exists for K8s). .system/jobs
	// is where the dbjobs launcher cleans up stale *.run dirs each cycle.
	// Hardcoded rather than s.GetJobDatadir(), which needs a non-nil
	// ClusterGroup and would panic on this function's bare-ServerMonitor
	// contract.
	systemDirs := "/var/lib/mysql/.system/tmp /var/lib/mysql/.system/logs " +
		"/var/lib/mysql/.system/repl /var/lib/mysql/.system/innodb/undo " +
		"/var/lib/mysql/.system/innodb/redo /var/lib/mysql/.system/aria " +
		"/var/lib/mysql/.system/jobs"
	// GetServerFromURL (cluster_get.go) matches only server.Host -- the
	// domain-qualified name when prov-net-cni is on -- not the bare s.Name.
	serverPath := s.Name + cluster.GetDomain()
	// Bounded so an unreachable repman can't hang the init container
	// forever. "-T", not the GNU "--timeout"/"--tries" long forms: those
	// aren't listed in busybox's own `wget --help`, and depending on
	// undocumented behavior of a floating base image tag is fragile. "-T
	// SEC" is documented and bounds both the connect and read phases.
	remoteFetchCmd := "wget" + noCheckCert + " -T 8 -qO /tmp/config.tar.gz" + authHeader + " " + baseRef + "/api/clusters/" + cluster.Name + "/servers/" + serverPath + "/" + s.Port + "/config"

	// need-config-fetch mirrors OpenSVC's own bootstrap gate exactly
	// (share/dashboard/static/configurator/opensvc/bootstrap,
	// handlerMuxServerNeedConfigFetch/CheckNeedConfigFetch in
	// server/api_database.go and cluster/srv_chk.go): prov-db-start-fetch-config
	// is read live, server-side, on every bootstrap attempt, not baked into
	// this command at Deployment-build time -- toggling it takes effect on
	// the pod's next restart, no reprovision required. wget treats the
	// endpoint's HTTP 500 ("no fetch needed") as a failure, so an
	// unreachable repman and "fetch not needed" both skip the fetch the
	// same way.
	needFetchCmd := "wget" + noCheckCert + " -T 8 -qO /dev/null" + authHeader + " " + baseRef + "/api/clusters/" + cluster.Name + "/servers/" + serverPath + "/" + s.Port + "/need-config-fetch"

	// Mirrors OpenSVC's own bootstrap script: fetch into a scratch dir, and
	// only on a successful fetch *and* extract, clear the persisted
	// /etc/mysql/conf.d and /docker-entrypoint-initdb.d (subPath mounts of
	// the same PVC as /var/lib/mysql, not emptyDir) and replace them -- so
	// a variable removed server-side actually disappears, and any failure
	// leaves the last successful boot's config untouched. The wipe
	// excludes replication-manager-cli: it's fetched separately below and
	// shouldn't be destroyed by a config refresh whose own CLI re-fetch
	// happens to fail.
	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"rm -f /etc/mysql/conf.d/*.cnf 2>/dev/null; " +
		"find /docker-entrypoint-initdb.d -mindepth 1 ! -name replication-manager-cli -delete 2>/dev/null; " +
		"cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/ 2>/dev/null; " +
		"cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/ 2>/dev/null; " +
		"fi; fi; fi"

	// initEnv is empty (nil) unless a Basic Auth header is actually needed,
	// so a cluster with api-credentials-secure-config off gets a
	// byte-identical init container to before this credential moved into a
	// Secret.
	var initEnv []apiv1.EnvVar
	if authHeaderValue != "" {
		initEnv = []apiv1.EnvVar{
			{
				Name: k8sSecretKeyAPIAuthHeader,
				ValueFrom: &apiv1.EnvVarSource{
					SecretKeyRef: &apiv1.SecretKeySelector{
						LocalObjectReference: apiv1.LocalObjectReference{Name: k8sClusterSecretName(cluster.Name)},
						Key:                  k8sSecretKeyAPIAuthHeader,
					},
				},
			},
		}
	}

	dbSecurityContext, runtimeChown, jobsSecurityContext := cluster.k8sDBIdentity()

	dbVolumeMounts := []apiv1.VolumeMount{
		{
			Name:      k8sDatabaseVolumeName(s.Name),
			MountPath: "/var/lib/mysql",
		},
		{
			Name:      k8sDatabaseVolumeName(s.Name),
			MountPath: "/etc/mysql/conf.d",
			SubPath:   k8sConfPersistSubPath,
		},
	}
	podVolumes := []apiv1.Volume{
		{
			Name: k8sDatabaseVolumeName(s.Name),
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: k8sDatabasePVCName(cluster.Name, s.Name),
				},
			},
		},
	}
	if dbSecurityContext != nil {
		// Explicit run-as user: see k8sDatabaseRunVolumeName.
		dbVolumeMounts = append(dbVolumeMounts, apiv1.VolumeMount{Name: k8sDatabaseRunVolumeName(s.Name), MountPath: "/run/mysqld"})
		podVolumes = append(podVolumes, apiv1.Volume{
			Name:         k8sDatabaseRunVolumeName(s.Name),
			VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}},
		})
	}

	// MKDIR_STATUS is the only thing that determines this container's exit
	// code. Kubernetes init containers have no "optional" resource flag
	// like OpenSVC's (a nonzero exit always blocks the pod), so everything
	// after mkdir -- config fetch/apply, CLI fetch, chmod -- is
	// unconditional and best-effort by construction instead.
	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /docker-entrypoint-initdb.d " + systemDirs +
			" ; MKDIR_STATUS=$? ; " +
			pickBase +
			applyConfig +
			// replication-manager-cli persists across restarts like
			// conf.d/init above -- a failed fetch just means it isn't
			// refreshed, not missing. Fetched to a temp file first, copied
			// into place only on success: busybox wget's "-qO" has no
			// atomic rename, so a connection dropped mid-transfer would
			// otherwise corrupt a previously-good cached binary in place.
			" ; wget" + noCheckCert + " -T 8 -qO /tmp/replication-manager-cli.new " + baseRef + "/static/configurator/bin/replication-manager-cli 2>/dev/null && cp /tmp/replication-manager-cli.new /docker-entrypoint-initdb.d/replication-manager-cli 2>/dev/null" +
			" ; chmod +x /docker-entrypoint-initdb.d/replication-manager-cli /docker-entrypoint-initdb.d/dbjobs_new /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm 2>/dev/null" +
			runtimeChown +
			" ; exit \"$MKDIR_STATUS\"",
	}
	// Subdomain/role label are gated on prov-net-cni so a cluster that
	// hasn't opted in gets byte-identical Deployments to before; the role
	// label must match k8sEnsureHeadlessService's selector.
	var subdomain string
	podLabels := map[string]string{
		"app": "repication-manager",
		"tag": s.Name,
	}
	if cluster.Conf.ProvNetCNI {
		subdomain = k8sHeadlessServiceName
		podLabels[k8sRoleLabel] = k8sRoleDB
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			// Recreate, not the inherited RollingUpdate: with one replica the default creates the
			// new Pod BEFORE the old one is stopped, so two database processes could share the
			// datadir (ReadWriteOnce limits attachment per node, not per Pod, and the Pod is pinned
			// to one node). Under Recreate the Deployment controller waits until the old Pods have
			// TERMINATED (their containers stopped, the Pod in a terminal phase) before it creates the
			// new one; the old Pod's API object may still exist for a moment when the new Pod is
			// created, which is not a second running process. Observed with a non-database sentinel
			// Deployment on Kubernetes v1.37 (issue #2389), not yet with a real database. Normal
			// template rollouts only: see k8sWaitPVCConsumersGone for the repman-controlled scale-up
			// and doc/implementation/cluster/KUBERNETES_PROVISIONING.md for the guarantee boundary.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "repication-manager",
					"tag": s.Name,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: apiv1.PodSpec{
					Hostname: s.Name,
					// Hostname+Subdomain, paired with the headless Service, is
					// what makes CoreDNS publish this pod's current real IP.
					Subdomain: subdomain,
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": nodeHostnameLabel,
					},
					InitContainers: []apiv1.Container{
						{
							Name:    s.Name + "-init",
							Image:   "alpine",
							Command: cmd,
							Env:     initEnv,
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/var/lib/mysql",
								},
								{
									// SubPath, not emptyDir: matches OpenSVC's own
									// {name}/etc/mysql (see applyConfig above).
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/etc/mysql/conf.d",
									SubPath:   k8sConfPersistSubPath,
								},
								{
									// SubPath, matches OpenSVC's {name}/init.
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/docker-entrypoint-initdb.d",
									SubPath:   k8sInitPersistSubPath,
								},
							},
						},
					},
					Containers: []apiv1.Container{
						{
							Name:            s.Name,
							Image:           cluster.deployImage(),
							ImagePullPolicy: k8sImagePullPolicy(cluster),
							Resources:       cluster.k8sDatabaseContainerResources(),
							SecurityContext: dbSecurityContext,
							Ports: []apiv1.ContainerPort{
								{
									Name:          "mysql",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: int32(port),
								},
							},
							Env: append([]apiv1.EnvVar{
								{
									Name: "MYSQL_ROOT_PASSWORD",
									ValueFrom: &apiv1.EnvVarSource{
										SecretKeyRef: &apiv1.SecretKeySelector{
											LocalObjectReference: apiv1.LocalObjectReference{Name: k8sClusterSecretName(cluster.Name)},
											Key:                  k8sSecretKeyRootPassword,
										},
									},
								},
							}, k8sDBAllocatorEnv(cluster)...),
							VolumeMounts: dbVolumeMounts,
						},
						// dbjobs sidecar: runs share/scripts/dbjobs_new.sh (backups,
						// optimize, config refresh), fetched pre-resolved as part
						// of the same archive the init container already applies.
						// Same pod as the DB container, so no netns sharing needed
						// (unlike OpenSVC's own jobs container) -- Kubernetes pods
						// share network by default, and the script connects over
						// TCP.
						{
							Name:      s.Name + "-dbjobs",
							Image:     cluster.deployImage(),
							Resources: cluster.k8sDBJobsContainerResources(),
							// Root whatever the image's own USER is (Percona
							// Server images default to mysql, 1001): dbjobs must
							// read and chown a datadir owned by prov-db-volume-uid
							// (db_owner in dbjobs_new.sh). Only with a managed
							// identity; otherwise the image's own user, as before.
							SecurityContext: jobsSecurityContext,
							// Guarded, not a direct exec: on a server with nothing
							// ever persisted (a first boot with repman
							// unreachable), /docker-entrypoint-initdb.d is empty --
							// mariadbd degrades gracefully to the image's own
							// defaults, but a bare exec here would crash-loop this
							// container on a missing file. Idles instead until the
							// next pod restart re-runs the init container.
							Command: []string{"/bin/sh", "-c",
								"if [ -f /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm ]; then " +
									"exec /bin/bash /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm; " +
									"else " +
									"echo 'dbjobs_launcher_with_sigterm not found -- no config has ever been successfully persisted for this server; idling until the next pod restart' >&2; " +
									"exec sleep infinity; " +
									"fi"},
							Env: []apiv1.EnvVar{
								{
									Name: "MYSQL_ROOT_PASSWORD",
									ValueFrom: &apiv1.EnvVarSource{
										SecretKeyRef: &apiv1.SecretKeySelector{
											LocalObjectReference: apiv1.LocalObjectReference{Name: k8sClusterSecretName(cluster.Name)},
											Key:                  k8sSecretKeyRootPassword,
										},
									},
								},
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/var/lib/mysql",
								},
								{
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/docker-entrypoint-initdb.d",
									SubPath:   k8sInitPersistSubPath,
								},
							},
						},
					},
					Volumes: podVolumes,
				},
			},
		},
	}
	cluster.k8sAddXtrabackupBundle(dep, s)
	return dep
}

// k8sDBIdentity maps the two independent identity settings to the pod: the
// database container's securityContext (prov-db-run-as-uid, nil when empty), an
// ownership fix appended to the init container's command (prov-db-volume-uid, or 1001
// for a Percona Server image; empty when not managed), which runs as root like
// OpenSVC's bootstrap chown, and the dbjobs sidecar's securityContext (root
// whenever any of this is managed; it takes file ownership from the datadir:
// db_owner in dbjobs_new.sh). With nothing managed all three are empty: the pod
// is what it was before these settings.
func (cluster *Cluster) k8sDBIdentity() (db *apiv1.SecurityContext, chown string, jobs *apiv1.SecurityContext) {
	if runAsUID, runAsGID, set := cluster.dbRunAs(); set {
		runAsUser, runAsGroup := int64(runAsUID), int64(runAsGID)
		db = &apiv1.SecurityContext{RunAsUser: &runAsUser, RunAsGroup: &runAsGroup}
	}
	if uid, gid, managed := cluster.dbVolumeOwner(); managed {
		u, g := strconv.Itoa(uid), strconv.Itoa(gid)
		// Only entries that differ are touched, so a restart of a pod whose
		// volume already matches does not rewrite the whole datadir.
		// Best effort like the rest of that command (the init container's exit code is
		// only MKDIR_STATUS, and a volume the process can still use must not block the
		// pod), but a failure is written to the init container log, where
		// `kubectl logs -c <name>-init` shows why mysqld then cannot use the datadir.
		chown = " ; find /var/lib/mysql \\( ! -user " + u + " -o ! -group " + g + " \\) -exec chown -h " + u + ":" + g + " {} +" +
			" || echo \"WARNING: could not set the owner of /var/lib/mysql to " + u + ":" + g + "\" >&2"
	}
	if cluster.dbIdentityManaged() {
		root := int64(0)
		jobs = &apiv1.SecurityContext{RunAsUser: &root, RunAsGroup: &root}
	}
	return db, chown, jobs
}

// k8sDatabaseMemoryMiB is the memory limit, in MiB, of the database container. It is the value
// OpenSVC renders as `--memory=<n>m --memory-swap=<n>m` on container#db when
// prov-db-docker-run-args-limit is on (OpenSVCGetDBContainerSection): GetDBContainerMemoryCapMB,
// the DBU-tier cap that never goes below prov-db-memory. Kubernetes reads it from that same helper
// everywhere it renders the limit -- the provisioning builder, the restart/repull template patch
// and the native resizer -- and computes nothing of its own: no carve-out for dbjobs, no floor.
// ok is false when the helper yields no value (an unparseable prov-db-memory with no DBU tier);
// docker's `--memory=0m` then means no limit, and so no memory limit is set here either.
func (cluster *Cluster) k8sDatabaseMemoryMiB() (mb int, ok bool) {
	mb = cluster.GetDBContainerMemoryCapMB()
	return mb, mb > 0
}

// k8sDatabaseContainerResources returns the DB container's Requests/Limits -- gated by the same
// prov-db-docker-run-args-limit opt-in OpenSVC's own container run_args --memory/--cpus use, so a
// cluster that has chosen not to cap container resources on OpenSVC gets the identical choice
// honored on Kubernetes.
//
// Memory: the limit is k8sDatabaseMemoryMiB. Request == limit is the existing Kubernetes mechanism
// (the resize target is then unambiguous, as docker's --memory == --memory-swap); OpenSVC itself
// reserves nothing at the orchestrator level, so the value of the request, which the scheduler
// does honor, is tracked as an open parity point (see K8S_DBJOBS_MEMORY.md).
//
// CPU rides the same Requests == Limits pair, from prov-db-cpu-cores, unchanged: CPU/SMT parity
// (GetDBContainerCPUCapCores) is a separate tracked gap.
func (cluster *Cluster) k8sDatabaseContainerResources() apiv1.ResourceRequirements {
	if !cluster.Conf.ProvDBDockerRunArgsLimit {
		return apiv1.ResourceRequirements{}
	}
	rl := apiv1.ResourceList{}
	if mb, ok := cluster.k8sDatabaseMemoryMiB(); ok {
		rl[apiv1.ResourceMemory] = resource.MustParse(strconv.Itoa(mb) + "Mi")
	}
	if cpuMilli, ok := cluster.k8sDatabaseCPUTargetMilli(); ok {
		rl[apiv1.ResourceCPU] = *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI)
	}
	if len(rl) == 0 {
		return apiv1.ResourceRequirements{}
	}
	return apiv1.ResourceRequirements{
		Requests: rl.DeepCopy(),
		Limits:   rl,
	}
}

// k8sDatabaseCPUTargetMilli is prov-db-cpu-cores in millicores (1 core per DBU),
// false when unset/unparseable (the container then carries no CPU pair, as
// before -- memory-only cap).
func (cluster *Cluster) k8sDatabaseCPUTargetMilli() (int64, bool) {
	cores, err := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	if err != nil || cores <= 0 {
		return 0, false
	}
	return int64(cores*1000 + 0.5), true
}

// k8sDBJobsContainerResources is the dbjobs sidecar's resources: none. OpenSVC's jobs container
// (OpenSVCGetJobsContainerSection) carries only prov-db-jobs-docker-run-args, whose default is
// `--ulimit nofile=262144:262144`: no memory or CPU limit and no reservation. Kubernetes sets
// none either. The dbjobs memory limit that used to be set here was OOMKilled during the
// configuration print at 128Mi (#2369) and has no OpenSVC counterpart. Translating an operator's
// own resource arguments in prov-db-jobs-docker-run-args to the sidecar is a separate tracked gap.
func (cluster *Cluster) k8sDBJobsContainerResources() apiv1.ResourceRequirements {
	return apiv1.ResourceRequirements{}
}

// k8sAPICallTimeout bounds every Kubernetes API call reachable from the
// monitor loop (F2: the perpetual-monitoring invariant outranks everything
// else -- a stalled API server/network connection must never stall
// ServerMonitor.Refresh() indefinitely). Generous for a normally-responsive
// in-cluster API server, far below any realistic monitoring-ticker interval.
const k8sAPICallTimeout = 5 * time.Second

// k8sCurrentReplicaSet returns the Deployment's CURRENT ReplicaSet -- the one
// with the highest "deployment.kubernetes.io/revision" annotation among the
// ReplicaSets it owns. That revision counter is assigned by the Deployment
// controller itself each time the Pod template changes, so it is the
// authoritative "this is the desired generation" marker (the same one
// `kubectl rollout history` reads) -- unlike Pod phase alone, it survives a
// RollingUpdate window where an OLD-generation Pod is still Running while the
// NEW-generation Pod is still Pending (e.g. an RWO PVC blocking the new Pod
// from scheduling until the old one releases it -- exactly the scenario a
// label+phase-only lookup gets wrong). Returns (nil, nil), not an error, when
// the Deployment does not yet own any ReplicaSet (not rolled out yet).
func (cluster *Cluster) k8sCurrentReplicaSet(ctx context.Context, client kubernetes.Interface, dep *appsv1.Deployment) (*appsv1.ReplicaSet, error) {
	sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return nil, err
	}
	rsList, err := client.AppsV1().ReplicaSets(cluster.Name).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, err
	}
	var current *appsv1.ReplicaSet
	currentRev := int64(-1)
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		owned := false
		for _, ref := range rs.OwnerReferences {
			if ref.UID == dep.UID {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		rev, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		if rev > currentRev {
			currentRev = rev
			current = rs
		}
	}
	return current, nil
}

// k8sContainerReady reports whether the named container is Ready in the
// Pod's own status -- Running Phase alone does not mean the DB container
// itself has passed its readiness probe.
func k8sContainerReady(pod *apiv1.Pod, containerName string) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == containerName {
			return cs.Ready
		}
	}
	return false
}

// k8sFindDatabasePod finds the live Pod backing a server's Deployment,
// controller-aware: Deployment -> its CURRENT ReplicaSet (k8sCurrentReplicaSet)
// -> exactly one non-terminating, Running, DB-container-Ready Pod OWNED by
// that ReplicaSet. A label+phase-only lookup (the previous implementation)
// can select an old-generation Pod that is still Running while its
// replacement (new ReplicaSet) is still Pending -- e.g. an RWO PVC blocking
// the new Pod from scheduling until the old one releases it -- and resizing
// or confirming against that old Pod would target a Pod already on its way
// out while the replacement serves traffic at the OLD memory limit.
//
// Zero or multiple eligible candidates both return (nil, nil), not an error:
// callers that need "cannot confirm" vs. "confirmed absent" to differ check
// for a nil Pod explicitly, and every caller of this function already treats
// a nil Pod as "wait / cannot confirm" rather than a hard failure. A missing
// Deployment or ReplicaSet (not provisioned/rolled out yet) is the same
// "cannot confirm" case, not an error.
func (cluster *Cluster) k8sFindDatabasePod(ctx context.Context, client kubernetes.Interface, s *ServerMonitor) (*apiv1.Pod, error) {
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, s.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	rs, err := cluster.k8sCurrentReplicaSet(ctx, client, dep)
	if err != nil {
		return nil, err
	}
	if rs == nil {
		return nil, nil
	}
	pods, err := client.CoreV1().Pods(cluster.Name).List(ctx, metav1.ListOptions{
		LabelSelector: "app=repication-manager,tag=" + s.Name,
	})
	if err != nil {
		return nil, err
	}
	var candidate *apiv1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil || p.Status.Phase != apiv1.PodRunning {
			continue
		}
		owned := false
		for _, ref := range p.OwnerReferences {
			if ref.UID == rs.UID {
				owned = true
				break
			}
		}
		if !owned {
			continue // belongs to an old/other ReplicaSet, not the Deployment's current one
		}
		if !k8sContainerReady(p, s.Name) {
			continue
		}
		if candidate != nil {
			return nil, nil // ambiguous: more than one eligible Pod, cannot confirm a single target
		}
		candidate = p
	}
	return candidate, nil
}

// k8sResourceSensorCheckEveryNHeartbeats throttles the Deployment/Pod reads behind
// WARN0212/WARN0215 to a slow cadence; PreserveState keeps the state alive on the
// ticks in between so it does not flap (pstates contract).
const k8sResourceSensorCheckEveryNHeartbeats = 30

// k8sResourceSensorFreshnessWindow is how stale the last successful DBU reading
// (server.DBUConsumed.WindowEnd) may be before the sensor is considered not
// actually delivering data. Generous relative to the dbjobs launcher's own ~60s
// push cadence so a couple of missed/slow cycles don't flap WARN0215.
const k8sResourceSensorFreshnessWindow = resourceSensorFreshnessWindow // one notion of "stale" for every orchestrator

// CheckK8SResourceSensor observes, from the Kubernetes API, whether the DBU
// resource sensor can actually run -- both the provisioning-time precondition
// and the ongoing runtime reality, not just the Deployment template boolean:
//
//   - WARN0212: the database Deployment must carry shareProcessNamespace (so
//     the sidecar reads the DB cgroup via /proc/<pid>/root). Missing means the
//     namespace policy (PodSecurity) forbade it at provision, so provisioning
//     fell back without it. Cluster-scoped: the namespace policy applies to
//     every server alike, so one missing Deployment is enough to raise it.
//   - WARN0215: per-server runtime prerequisites -- the DB Pod is Running, the
//     dbjobs sidecar (the container that actually runs collect_dbu) is Ready,
//     and a reading has arrived within k8sResourceSensorFreshnessWindow. A
//     Deployment can carry the shareProcessNamespace policy correctly and
//     still not be delivering real data (Pod not scheduled yet, dbjobs
//     crash-looping, sensor silently failing every push).
//
// Both are derived from live state (not a stored flag), so they clear by
// themselves once the underlying condition is fixed and this re-observes it.
func (cluster *Cluster) CheckK8SResourceSensor() {
	if !cluster.Conf.MonitoringSystemResources || cluster.GetOrchestrator() != config.ConstOrchestratorKubernetes {
		return
	}
	// Slow cadence: only hit the API every N heartbeats, preserve in between.
	if cluster.StateMachine.GetHeartbeats()%k8sResourceSensorCheckEveryNHeartbeats != 0 {
		cluster.GetStateMachine().PreserveState("WARN0212")
		cluster.GetStateMachine().PreserveState("WARN0215")
		return
	}
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.GetStateMachine().PreserveState("WARN0212") // API unreachable: keep the last verdict
		cluster.GetStateMachine().PreserveState("WARN0215")
		return
	}
	cluster.checkK8SResourceSensorWithClient(client)
}

// checkK8SResourceSensorWithClient is the *WithClient testable half of
// CheckK8SResourceSensor (same split as every other prov_k8s_*.go entry
// point) -- everything past the cadence throttle and the K8SConnectAPI call.
func (cluster *Cluster) checkK8SResourceSensorWithClient(client kubernetes.Interface) {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()

	// The Deployment-policy scan (WARN0212, cluster-scoped) and the per-server
	// runtime scan (WARN0215) are tracked independently across the WHOLE loop:
	// a confirmed runtime problem on one server must never cut the scan short
	// and leave a LATER server's missing shareProcessNamespace undiscovered,
	// so both warnings stay cluster-wide-accurate for this pass regardless of
	// which server triggers which condition.
	spnMissing := false
	deploymentPolicyUnconfirmed := false
	runtimeBroken := false
	runtimeUnconfirmed := false
	var runtimeBrokenURL, runtimeBrokenReason, firstUnconfirmedURL string

	for _, s := range cluster.Servers {
		if s == nil || !s.HasProvisionCookie() {
			continue
		}
		dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, s.Name, metav1.GetOptions{})
		if err != nil {
			// A transient Get failure verifies NEITHER prerequisite for this
			// server: it must not be allowed to silently clear a real,
			// previously-known WARN0212 (the policy could not be re-checked)
			// nor look confirmed-good on WARN0215 just because this pass
			// could not check either.
			deploymentPolicyUnconfirmed = true
			runtimeUnconfirmed = true
			if firstUnconfirmedURL == "" {
				firstUnconfirmedURL = s.URL
			}
			continue
		}
		if spn := dep.Spec.Template.Spec.ShareProcessNamespace; spn == nil || !*spn {
			spnMissing = true
			continue // still worth checking other servers' runtime prerequisites below
		}
		if runtimeBroken {
			// Already have a confirmed runtime problem to report this pass --
			// keep walking the loop for the Deployment-policy scan above,
			// just skip the (redundant) runtime check itself.
			continue
		}
		verdict, reason := cluster.k8sResourceSensorRuntimeIssue(ctx, client, s)
		switch verdict {
		case k8sSensorBroken:
			runtimeBroken = true
			runtimeBrokenURL, runtimeBrokenReason = s.URL, reason
		case k8sSensorUnconfirmed:
			runtimeUnconfirmed = true
			if firstUnconfirmedURL == "" {
				firstUnconfirmedURL = s.URL
			}
		}
	}

	if spnMissing {
		// A positively-confirmed missing capability always wins over an
		// unrelated inconclusive check on some OTHER server this same pass --
		// an unconfirmed condition elsewhere must never suppress a confirmed
		// one (finding: a real db1 failure was being swallowed by a
		// transient db2 API/RBAC error in the same scan).
		cluster.SetState("WARN0212", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0212"], cluster.Name), ErrFrom: "CONF"})
	} else if deploymentPolicyUnconfirmed {
		cluster.GetStateMachine().PreserveState("WARN0212")
	}

	switch {
	case runtimeBroken:
		cluster.SetState("WARN0215", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0215"], runtimeBrokenURL, runtimeBrokenReason), ErrFrom: "CONF"})
	case runtimeUnconfirmed:
		// Explicitly SET (not merely preserved): a service whose sensor has
		// never been positively verified -- newly provisioned, or every check
		// so far has been inconclusive (missing/ambiguous Pod, API error) --
		// must get a tracked non-ready state from its very FIRST observation,
		// not only once a pre-existing WARN0215 already happens to exist
		// (PreserveState is a no-op when there is nothing to preserve).
		cluster.SetState("WARN0215", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0215"], firstUnconfirmedURL, "sensor state not yet verified"), ErrFrom: "CONF"})
	}
}

// k8sSensorRuntimeVerdict distinguishes a positively-confirmed sensor problem
// from "could not check this pass" -- the two must never be conflated, or a
// transient API hiccup would silently clear a real WARN0215.
type k8sSensorRuntimeVerdict int

const (
	k8sSensorHealthy k8sSensorRuntimeVerdict = iota
	k8sSensorUnconfirmed
	k8sSensorBroken
)

// k8sResourceSensorRuntimeIssue checks the runtime prerequisites the DBU sensor
// needs beyond the Deployment's shareProcessNamespace policy: a Running DB Pod,
// a Ready dbjobs sidecar, and a reading fresh enough to prove the sensor is
// actually delivering data, not just theoretically able to. An API error, an
// ambiguous Pod set, or a not-yet-scheduled Pod is k8sSensorUnconfirmed --
// distinct from k8sSensorHealthy -- so the caller preserves rather than clears
// the existing state.
func (cluster *Cluster) k8sResourceSensorRuntimeIssue(ctx context.Context, client kubernetes.Interface, s *ServerMonitor) (k8sSensorRuntimeVerdict, string) {
	pod, err := cluster.k8sFindDatabasePod(ctx, client, s)
	if err != nil {
		return k8sSensorUnconfirmed, ""
	}
	if pod == nil {
		return k8sSensorUnconfirmed, ""
	}
	jobsName := s.Name + "-dbjobs"
	jobsFound := false
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != jobsName {
			continue
		}
		jobsFound = true
		if !cs.Ready {
			return k8sSensorBroken, fmt.Sprintf("dbjobs sidecar %s is not Ready on Pod %s", jobsName, pod.Name)
		}
	}
	if !jobsFound {
		return k8sSensorBroken, fmt.Sprintf("dbjobs sidecar container %s not found on Pod %s", jobsName, pod.Name)
	}
	if s.DBUConsumed == nil {
		return k8sSensorBroken, "no DBU reading has ever been received"
	}
	// resourceReadingAge, not time.Since(s.DBUConsumed.WindowEnd): prefers ReceivedAt
	// (repman's own clock, stamped on ingest) so a skewed sensor clock cannot make a
	// stale reading look fresh to this check either -- same clock CheckResourceConsumed's
	// ResourceReadingStale() already uses for the orchestrator-agnostic WARN0218 gate.
	if age, _ := s.resourceReadingAge(); age > k8sResourceSensorFreshnessWindow {
		return k8sSensorBroken, fmt.Sprintf("last DBU reading is %s old (window %s)", age.Round(time.Second), k8sResourceSensorFreshnessWindow)
	}
	return k8sSensorHealthy, ""
}

// k8sWaitBeforeCreatingDeployment is the scale-up guard for a Deployment that is about to be CREATED
// (it starts at one replica, so it never goes through k8sStartDatabaseServiceWithWait). When the
// Deployment does not exist yet, it waits (bounded) until no Pod references the PVC claims of its Pod
// template: a foreground delete followed by NotFound is control-plane evidence that the Deployment and
// its dependent API objects are gone, not a proof for a Pod an earlier, external or failed operation
// left behind. An existing Deployment (running, stopped or being deleted) is not created again, so
// nothing is waited for: the Create then reports AlreadyExists and k8sExistingDeploymentUsable decides.
// A Deployment that cannot be read is an error, never a go-ahead.
func (cluster *Cluster) k8sWaitBeforeCreatingDeployment(client kubernetes.Interface, deployment *appsv1.Deployment, timeout, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	_, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, deployment.Name, metav1.GetOptions{})
	cancel()
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("cannot read Deployment %s before creating it: %w", deployment.Name, err)
	}
	return cluster.k8sWaitPVCConsumersGone(client, k8sDeploymentPVCClaims(deployment, k8sDatabasePVCName(cluster.Name, deployment.Name)), timeout, poll)
}

// k8sExistingDeploymentUsable says whether the Deployment a Create reported as already existing can
// stand for a successful provisioning: it must be readable and not being deleted. A Deployment with a
// deletion timestamp (a foreground delete still waiting for its Pods) vanishes when that completes, so
// accepting it would report success and leave no database.
func (cluster *Cluster) k8sExistingDeploymentUsable(client kubernetes.Interface, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Deployment %s already exists but cannot be read: %w", name, err)
	}
	if dep.DeletionTimestamp != nil {
		return fmt.Errorf("Deployment %s is still being deleted (since %s): wait for its unprovision to complete, then provision again", name, dep.DeletionTimestamp.Format(time.RFC3339))
	}
	return nil
}

func (cluster *Cluster) K8SProvisionDatabaseService(s *ServerMonitor) {
	cluster.ResolveDatabaseImage(false) // the deployment carries a release, not a pointer (#1862)
	cluster.warnDBRunAsVolumeMismatch()

	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	if cluster.Conf.APISecureConfig {
		if u, ok := cluster.GetAPIUsers()["admin"]; !ok || u.Grants == nil || !u.Grants[config.GrantDBConfigFlag] {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
				"api-credentials-secure-config is enabled but the admin user lacks the db-config-flag grant: the Kubernetes DB init-container config fetch will get HTTP 403, so %s will not bootstrap", s.Name)
		}
	}
	cluster.k8sEnsureNamespace(client, cluster.Name)
	if err := cluster.k8sEnsureDatabaseSecret(client, s.Pass); err != nil {
		cluster.errorChan <- err
		return
	}
	// Only written here (provision/reprovision) -- an api-credentials
	// change afterward goes stale until the next reprovision, same as
	// OpenSVC's own REPLICATION_MANAGER_PASSWORD secret (OpenSVCCreateMaps,
	// prov_opensvc.go), which has the identical characteristic. Consistent
	// with that existing behavior, not a gap introduced here.
	if authHeaderValue := k8sAPIAuthHeaderValue(cluster); authHeaderValue != "" {
		if err := cluster.k8sPatchSecretValues(client, map[string]string{k8sSecretKeyAPIAuthHeader: authHeaderValue}); err != nil {
			cluster.errorChan <- err
			return
		}
	}

	persistentVolumeClaims := client.CoreV1().PersistentVolumeClaims(cluster.Name)
	pvc := cluster.k8sDatabasePVC(s)
	pvcresult, pvcerr := persistentVolumeClaims.Create(context.TODO(), pvc, metav1.CreateOptions{})
	if pvcerr != nil && !apierrors.IsAlreadyExists(pvcerr) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes pvc %s ", pvcerr)
		cluster.errorChan <- pvcerr
		return
	}
	if pvcerr == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes physical volume claim %q.\n", pvcresult.GetObjectMeta().GetName())
	}

	// Nothing to prime: config now persists on the PVC itself, written by
	// the init container's own fetch at pod-start time. The live endpoint
	// the init container's wget hits already regenerates config.tar.gz
	// fresh on every request.
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)

	port, err := strconv.Atoi(s.Port)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Invalid database port %s: %s ", s.Port, err)
		cluster.errorChan <- err
		return
	}
	if cluster.Conf.ProvNetCNI {
		cluster.k8sEnsureHeadlessService(client, port)
	}
	agent, err := cluster.GetDatabaseAgent(s)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision database  %s ", err)
		cluster.errorChan <- err
		return
	}
	nodeHostnameLabel := cluster.k8sHostnameLabel(agent.HostName)
	// k8sDatabaseContainerResources/k8sDBJobsContainerResources (called by the
	// builder below) read cluster.Conf.ProvMem fresh -- SetDBMemorySize
	// (cluster/cluster_set.go) already updates it BEFORE a dynamic resize is
	// dispatched, so a genuine recreate here automatically picks up the last
	// confirmed target with no separate persistence mechanism needed (the
	// same guarantee every other dynamic prov-db-* setting already has: it
	// survives for the life of this process, and durably across a restart
	// only once config-merge writes it back to TOML -- not a gap unique to
	// this feature).
	deployment := cluster.k8sDatabaseDeployment(s, port, nodeHostnameLabel)

	// Enable the DBU resource sensor's pod requirement: a shared PID namespace so
	// the "-dbjobs" sidecar reads the database container's own cgroup via
	// /proc/<pid>/root (no hostPath, no node access). monitoring-system-resources
	// is a capability that changes the service definition -- the same way
	// enabling OpenSVC's own resource-sensor capability changes the service
	// definition there, and OpenSVC does not retry with that capability silently
	// dropped. So if the cluster's admission (PodSecurity) forbids
	// shareProcessNamespace, provisioning does NOT fall back to a sensor-less
	// Deployment: that would let an operator believe the DBU sensor is active
	// when it can never run under this namespace policy. It fails the
	// provisioning call instead (ERR00113) so the operator sees the capability
	// gap immediately, not as a silently-degraded WARN0212 discovered later. A
	// Cloud18-managed cluster (we set the policy) admits it and never hits this
	// path. See doc/implementation/cluster/DBU_RESOURCE_SENSOR.md.
	sensorEnabled := cluster.Conf.MonitoringSystemResources
	if sensorEnabled {
		share := true
		deployment.Spec.Template.Spec.ShareProcessNamespace = &share
	}

	// A new Deployment starts at one replica: the scale-up rule applies, no Pod may still use the data
	// PVC (retained across an unprovision). Nothing to wait for on a first provisioning, and an
	// existing Deployment is not created again, so it is not checked.
	if guardErr := cluster.k8sWaitBeforeCreatingDeployment(client, deployment, k8sLifecycleWaitTimeout, k8sRolloutPollInterval); guardErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot provision %s: %s ", deployment.Name, guardErr)
		cluster.errorChan <- guardErr
		return
	}

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating Kubernetes deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if sensorEnabled && apierrors.IsForbidden(err) {
		cluster.SetState("WARN0212", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0212"], cluster.Name), ErrFrom: "CONF"})
		capErr := fmt.Errorf(clusterError["ERR00113"], s.Name, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", capErr)
		cluster.errorChan <- capErr
		return
	}
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes deployment %s ", err)
		cluster.errorChan <- err
		return
	}
	if apierrors.IsAlreadyExists(err) {
		// idempotent only for a Deployment that stays: one still being deleted is not a provisioned
		// service, it disappears when its deletion completes
		if existErr := cluster.k8sExistingDeploymentUsable(client, deployment.Name); existErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot provision %s: %s ", deployment.Name, existErr)
			cluster.errorChan <- existErr
			return
		}
	}
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes deployment %q.\n", result.GetObjectMeta().GetName())
	}
	servicesClient := client.CoreV1().Services(cluster.Name)

	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Name:     "mysql",
					Protocol: apiv1.ProtocolTCP,
					Port:     int32(port),
				},
			},
			Selector: map[string]string{
				"app": "repication-manager",
				"tag": s.Name,
			},
		},
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating service...")
	result2, err2 := servicesClient.Create(context.TODO(), service, metav1.CreateOptions{})
	if err2 != nil && !apierrors.IsAlreadyExists(err2) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes service %s ", err2)
		cluster.errorChan <- err2
		return
	}
	if err2 == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes service %s.\n", result2.GetObjectMeta().GetName())
	}
	if cluster.Conf.ProvNetCNI {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Server %s reachable via CoreDNS at %s (in-cluster clients, or an external repman with its resolver pointed at CoreDNS)", s.Name, s.Name+cluster.GetDomain())
	}
	cluster.errorChan <- nil
}

// k8sStopDatabaseServiceWithClient scales the Deployment to 0 replicas --
// a genuine stop, not a no-op. Not a Delete of the Deployment itself, so
// K8SStartDatabaseService can bring it back with a plain scale-up.
func (cluster *Cluster) k8sStopDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":0}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot stop database %s: %s ", name, err)
	}
	return err
}

func (cluster *Cluster) K8SStopDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStopDatabaseServiceWithClient(client, s.Name)
}

// k8sLifecycleWaitTimeout is the one deadline of the waits an explicit database lifecycle operation
// makes on objects the previous generation still owns. It is a failure boundary, not a permission, and
// what a timeout leaves depends on the wait:
//   - the PVC-consumer wait (a Start from zero replicas, the creation of a Deployment that does not
//     exist yet): after a failed guard nothing is scaled up and no database Deployment is created; the
//     wait only reads. A provisioning may already have created its namespace, credentials Secret, PVC
//     and headless Service.
//   - the Deployment-gone wait (unprovision): the foreground deletion of the Deployment and the
//     deletion of its Service were requested before the wait began and may go on after the timeout;
//     the wait itself force-deletes nothing and requests no further deletion.
//
// Each API request inside a wait is bounded on its own (k8sAPICallTimeout). The waits belong to
// explicit lifecycle operations (API actions, rolling operations, bootstrap, deletion), which block
// their caller until they finish. The monitoring and failover paths traced for MariaDB, MySQL and
// Percona do not reach them. The generic PostgreSQL planned-switchover chain (postgresSwitchover ->
// postgresStartServer -> StartDatabaseService) can structurally reach a Kubernetes Start, but
// PostgreSQL provisioning on Kubernetes is not implemented or validated: that is an unconfirmed,
// support-limited risk, with no monitoring degradation demonstrated (see "Single-writer safety" in
// doc/implementation/cluster/KUBERNETES_PROVISIONING.md).
const k8sLifecycleWaitTimeout = 90 * time.Second

// k8sDeploymentPVCClaims lists the PersistentVolumeClaims the Deployment's Pod template mounts: the
// database's actual data claim, not a name rebuilt from a convention. fallback is used only when the
// template names none.
func k8sDeploymentPVCClaims(dep *appsv1.Deployment, fallback string) []string {
	var claims []string
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName != "" {
			claims = append(claims, v.PersistentVolumeClaim.ClaimName)
		}
	}
	if len(claims) == 0 && fallback != "" {
		claims = append(claims, fallback)
	}
	return claims
}

// k8sPVCConsumers lists the Pods of the cluster namespace whose volumes reference one of claims: every
// such Pod, whatever its labels, owner (old ReplicaSets included), readiness, phase or deletion state.
// A Terminating Pod still runs its containers until the kubelet has stopped them, so it counts.
// The whole namespace is listed on purpose: a field selector cannot filter on volumes, and a label
// selector would miss a Pod that does not carry ours; the cost is one list per poll.
func (cluster *Cluster) k8sPVCConsumers(client kubernetes.Interface, claims []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	pods, err := client.CoreV1().Pods(cluster.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var consumers []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, v := range pod.Spec.Volumes {
			if v.PersistentVolumeClaim == nil || !slices.Contains(claims, v.PersistentVolumeClaim.ClaimName) {
				continue
			}
			state := string(pod.Status.Phase)
			if pod.DeletionTimestamp != nil {
				state += ", terminating"
			}
			consumers = append(consumers, fmt.Sprintf("%s (%s)", pod.Name, state))
			break
		}
	}
	return consumers, nil
}

// k8sWaitPVCConsumersGone returns nil only once no Pod references any of claims. A list that fails is
// retried until the deadline (a scale-up never follows a failed read); at the deadline the error names
// the Pods still holding the claim or the last API error. Nothing is deleted or scaled here, a stale
// Pod object (for instance an evicted one) is for the operator to remove.
func (cluster *Cluster) k8sWaitPVCConsumersGone(client kubernetes.Interface, claims []string, timeout, poll time.Duration) error {
	_, err := cluster.k8sWaitPVCConsumersGoneUnless(client, claims, timeout, poll, nil)
	return err
}

// k8sWaitPVCConsumersGoneUnless is k8sWaitPVCConsumersGone with one more way out, for a caller whose
// goal another operation may reach while it waits: before every poll after the first, superseded (nil
// for none) is asked whether that is so, and when it says true the wait ends at once, with true and no
// error, whether or not the Pods are gone. The wait itself never scales or deletes anything, so ending
// it early cannot start a process. A superseded that fails never counts as "no": that poll is not
// completed (no Pod list, nothing proceeds), the error is retried until the deadline, and a permanent
// refusal (Forbidden, Unauthorized) ends the wait at once.
func (cluster *Cluster) k8sWaitPVCConsumersGoneUnless(client kubernetes.Interface, claims []string, timeout, poll time.Duration, superseded func() (bool, error)) (bool, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	var holders []string
	for attempt := 0; ; attempt++ {
		holders, lastErr = nil, nil
		if superseded != nil && attempt > 0 {
			done, err := superseded()
			if err == nil && done {
				return true, nil
			}
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				return false, fmt.Errorf("not allowed to read Deployment in namespace %s (replication-manager's Kubernetes identity needs `get` on `deployments` there), not scaling up: %w", cluster.Name, err)
			}
			lastErr = err
		}
		if lastErr == nil {
			holders, lastErr = cluster.k8sPVCConsumers(client, claims)
			if lastErr == nil && len(holders) == 0 {
				return false, nil
			}
		}
		if apierrors.IsForbidden(lastErr) || apierrors.IsUnauthorized(lastErr) {
			// permanent, not worth the deadline: the Pods cannot be checked, so nothing proceeds
			return false, fmt.Errorf("not allowed to list Pods in namespace %s (replication-manager's Kubernetes identity needs `list` on `pods` there), not scaling up or creating the Deployment: %w", cluster.Name, lastErr)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		time.Sleep(min(poll, remaining))
	}
	if lastErr != nil {
		return false, fmt.Errorf("cannot confirm that no Pod uses PVC %s within %s, not scaling up or creating the Deployment: %w", strings.Join(claims, ","), timeout, lastErr)
	}
	return false, fmt.Errorf("Pod(s) still use PVC %s after %s, not scaling up or creating the Deployment and nothing deleted: %s", strings.Join(claims, ","), timeout, strings.Join(holders, ", "))
}

// k8sStartDatabaseServiceWithClient scales the Deployment to 1 replica -- idempotent (a Deployment
// that already runs is patched to 1 as before, with no wait, since Start is called unconditionally
// regardless of whether Stop actually ran). Creates a brand new pod when scaling up from 0,
// re-running the init container. A genuine 0 -> 1 first waits (k8sWaitPVCConsumersGone) until no
// Pod, terminating ones included, still references the data PVC: the scale-down returns when the
// patch is accepted, not when the old database process has exited, and the ReplicaSet controller does
// not count a Terminating Pod when it creates the replacement (observed on Kubernetes v1.37 with a
// sentinel workload: a raw scale-up right after the scale-down ran both containers together).
// A genuine 0 -> 1 issues its scale-up PATCH only after that wait has succeeded; while it waits it only
// reads. If the Deployment is seen to have left zero replicas meanwhile (another operation scaled it
// up), the wait ends and, at one replica, this call issues no PATCH of its own.
// On timeout or API error nothing is scaled.
func (cluster *Cluster) k8sStartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	return cluster.k8sStartDatabaseServiceWithWait(client, name, k8sLifecycleWaitTimeout, k8sRolloutPollInterval)
}

func (cluster *Cluster) k8sStartDatabaseServiceWithWait(client kubernetes.Interface, name string, timeout, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
	cancel()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: cannot read its Deployment: %s ", name, err)
		return err
	}
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
		claims := k8sDeploymentPVCClaims(dep, k8sDatabasePVCName(cluster.Name, name))
		// While this Start waits, another Start may scale the Deployment up and its Pod then holds the
		// claim; waiting for that Pod to go would only time out. So the Deployment is read again before
		// each later poll: no longer at zero replicas means this call has no scale-up of its own to issue,
		// and it issues no scale-up PATCH. That is the decision a Start already makes on a Deployment that
		// runs (no wait, no Pod list). It does not know who changed the replicas, nor whether that change
		// was safe: the PVC-consumer guard is not a distributed lock and cannot protect against another
		// Start that did not check, an operator or another controller.
		var observed *int32
		superseded := func() (bool, error) {
			ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
			defer cancel()
			current, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if current.Spec.Replicas != nil && *current.Spec.Replicas == 0 {
				return false, nil
			}
			observed = current.Spec.Replicas
			return true, nil
		}
		scaledUp, err := cluster.k8sWaitPVCConsumersGoneUnless(client, claims, timeout, poll, superseded)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
			return err
		}
		if scaledUp && (observed == nil || *observed == 1) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database %s was scaled up by another operation while this start waited: no second patch ", name)
			return nil
		}
		// scaled to more than one by someone else: falls through to the patch, which only brings it back to one
	}
	patch := []byte(`{"spec":{"replicas":1}}`)
	pctx, pcancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer pcancel()
	_, err = client.AppsV1().Deployments(cluster.Name).Patch(pctx, name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
	}
	return err
}

func (cluster *Cluster) K8SStartDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStartDatabaseServiceWithClient(client, s.Name)
}

// k8sMemoryResourcesFragment is the strategic-merge "resources" sub-map for
// one container's memory Requests==Limits.
func k8sMemoryResourcesFragment(mb int) map[string]interface{} {
	memStr := strconv.Itoa(mb) + "Mi"
	return map[string]interface{}{
		"requests": map[string]string{"memory": memStr},
		"limits":   map[string]string{"memory": memStr},
	}
}

// k8sNoMemoryResourcesFragment is the strategic-merge "resources" sub-map that deletes a
// container's memory request AND limit (a null removes the key): what makes an existing
// Deployment, whose dbjobs template still carries the old request == limit, match the
// no-resources dbjobs container of k8sDBJobsContainerResources.
func k8sNoMemoryResourcesFragment() map[string]interface{} {
	return map[string]interface{}{
		"requests": map[string]interface{}{"memory": nil},
		"limits":   map[string]interface{}{"memory": nil},
	}
}

// k8sDBJobsContainerName is the dbjobs sidecar's container name convention
// (k8sDatabaseDeployment).
func k8sDBJobsContainerName(dbContainerName string) string {
	return dbContainerName + "-dbjobs"
}

// k8sDBJobsHasLegacyMemory reports whether dep's dbjobs container still carries a memory request or
// limit: what earlier versions gave it (a fixed 128Mi, #2369). OpenSVC's jobs container has none, in
// either prov-db-docker-run-args-limit mode.
func k8sDBJobsHasLegacyMemory(dep *appsv1.Deployment, name string) bool {
	if dep == nil {
		return false
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != k8sDBJobsContainerName(name) {
			continue
		}
		_, req := c.Resources.Requests[apiv1.ResourceMemory]
		_, lim := c.Resources.Limits[apiv1.ResourceMemory]
		return req || lim
	}
	return false
}

// k8sDeploymentReadTimeout bounds the read of the Deployment a Pod-replacing path inspects before it
// patches. A variable only so a test can make the deadline short; it is k8sAPICallTimeout.
var k8sDeploymentReadTimeout = k8sAPICallTimeout

// k8sGetDeploymentForResources reads the Deployment a Pod-replacing path is about to patch, to know
// whether its template still carries the old dbjobs resources (and, with the limit on, which
// containers exist). The read has its OWN bounded context (k8sDeploymentReadTimeout): the caller
// patches under a context of its own, created after this returns, so a read that ran into its
// deadline cannot leave the patch with an expired one.
//
// With prov-db-docker-run-args-limit on the read is required, as it always was, and a failure is an
// error (fail closed). With it off the patch does not depend on the read -- a clean Deployment is
// patched exactly as before -- so a failed or timed-out read only skips the dbjobs clean-up of a
// legacy Deployment (nil, nil) and the restart goes on.
func (cluster *Cluster) k8sGetDeploymentForResources(client kubernetes.Interface, name, action string) (*appsv1.Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), k8sDeploymentReadTimeout)
	defer cancel()
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return dep, nil
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot %s %s: %s ", action, name, err)
		return nil, err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Cannot read %s to clean any legacy dbjobs resources, %s goes on without: %s ", name, action, err)
	return nil, nil
}

// k8sRecreateStrategyPatch is the strategic-merge "strategy" value that moves an existing database
// Deployment to Recreate: the type, and a null that removes the old rollingUpdate block (the API
// rejects Recreate with a rollingUpdate left in place). It is sent inside the Deployment patches the
// restart, force re-pull and update-config operations already make, so a Deployment created before
// the strategy was explicit migrates on its next lifecycle operation. A strategy change alone does not
// touch the Pod template, so it never replaces a Pod by itself: no separate migration restarts a
// healthy database. nil when dep is known and already Recreate; dep may be nil (unknown), the patch
// is then sent (it changes nothing on a Deployment that already is Recreate).
func k8sRecreateStrategyPatch(dep *appsv1.Deployment) map[string]interface{} {
	if dep != nil && dep.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType && dep.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return map[string]interface{}{"type": string(appsv1.RecreateDeploymentStrategyType), "rollingUpdate": nil}
}

// k8sContainerMemoryResourcesPatch is the SINGLE authoritative source for the
// strategic-merge "containers" entries every Pod-replacing restart path
// (k8sRestartDatabaseServiceWithClient, k8sForceRepullDatabaseServiceWithClient)
// patches onto the Deployment template, so none of them can ever drift onto
// different numbers (the exact drift that let a fallback restart recreate
// the OLD template limit even after Repman had already confirmed/applied a
// new DB memory target). Two independent parts:
//   - with prov-db-docker-run-args-limit on, the DB container to the limit the
//     provisioning builder renders (k8sDatabaseMemoryMiB) -- the Kubernetes
//     counterpart of OpenSVC re-rendering the full deployment on a restart
//     (UpgradeDatabaseDeploymentOnStart). With the flag off the database memory is
//     left exactly as it was;
//   - in either mode, when dep's template still carries a dbjobs memory request or
//     limit from an earlier version, a null that removes both, so an existing
//     Deployment migrates to the no-resources dbjobs container (OpenSVC's jobs
//     container carries none either way). Only included when dep already has the
//     container AND the resources: patching a name that doesn't exist would create
//     a NEW, incomplete container (missing image, command, volume mounts), the same
//     landmine k8sUpdateDatabaseServiceConfigWithClient's own doc comment describes.
//
// nil when there is nothing to patch: with the flag off, a clean Deployment (no legacy dbjobs
// resources) gets no entry at all, so its patch body is as before this change. Callers only send
// it when it is not empty (a null list would delete the containers). dep may be nil (unknown):
// only the database part.
func (cluster *Cluster) k8sContainerMemoryResourcesPatch(dep *appsv1.Deployment, name string) []map[string]interface{} {
	var patch []map[string]interface{}
	if cluster.Conf.ProvDBDockerRunArgsLimit {
		if mb, ok := cluster.k8sDatabaseMemoryMiB(); ok {
			patch = append(patch, map[string]interface{}{"name": name, "resources": k8sMemoryResourcesFragment(mb)})
		}
	}
	if k8sDBJobsHasLegacyMemory(dep, name) {
		patch = append(patch, map[string]interface{}{"name": k8sDBJobsContainerName(name), "resources": k8sNoMemoryResourcesFragment()})
	}
	return patch
}

// k8sRestartDatabaseServiceWithClient triggers a rolling pod replacement
// like `kubectl rollout restart`, patching only the restartedAt annotation
// -- unlike k8sForceRepullDatabaseServiceWithClient, never ImagePullPolicy:
// a plain restart (used by RollingRestart, often on a schedule) must never
// silently re-pull a different image.
//
// The SAME patch also carries what k8sContainerMemoryResourcesPatch computes: with
// prov-db-docker-run-args-limit set, the CURRENT database memory limit (the helper
// the provisioning builder uses, GetDBContainerMemoryCapMB); in either mode, the
// removal of the memory resources an older dbjobs template still carries: an explicit
// restart already replaces the Pod by design, so this is the one safe moment
// to reconcile the Deployment's own template (which the resize subresource
// never touched) with the contract, instead of the replacement Pod reverting
// to whatever the template held at last (re)provision.
//
// With the flag off the database memory is never part of the patch. A clean Deployment (no old
// dbjobs memory resources) gets the same patch body as before this change, only the restartedAt
// annotation; the one difference is an extra read of the Deployment, whose failure or timeout is
// tolerated. A legacy Deployment (an old dbjobs request or limit) additionally gets the dbjobs
// clean-up entry -- the one flag-off change -- and, if that read failed, goes without it.
//
// Needs a bounded Get first (k8sGetDeploymentForResources, a context of its own; the patch
// below has another) to know whether dep still carries the old dbjobs resources -- still not
// Update(): the Get is read-only and the Patch call below never uses its resourceVersion, so
// this does not reintroduce the resourceVersion problem k8sEnsureDatabaseSecret has with Update().
func (cluster *Cluster) k8sRestartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	templatePatch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339)},
		},
	}
	dep, err := cluster.k8sGetDeploymentForResources(client, name, "restart")
	if err != nil {
		return err
	}
	if containers := cluster.k8sContainerMemoryResourcesPatch(dep, name); len(containers) > 0 {
		templatePatch["spec"] = map[string]interface{}{"containers": containers}
	}
	specPatch := map[string]interface{}{"template": templatePatch}
	if strategy := k8sRecreateStrategyPatch(dep); strategy != nil {
		specPatch["strategy"] = strategy
	}
	patch, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout) // its own, after the read
	defer cancel()
	_, err = client.AppsV1().Deployments(cluster.Name).Patch(ctx, name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot restart %s: %s ", name, err)
	}
	return err
}

func (cluster *Cluster) K8SRestartDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sRestartDatabaseServiceWithClient(client, s.Name)
}

const k8sRolloutCompleteTimeout = 90 * time.Second
const k8sRolloutPollInterval = 2 * time.Second

// k8sWaitRolloutCompleteWithClient polls the Deployment until the rolling
// replacement genuinely completes -- the same condition `kubectl rollout
// status` checks -- or returns an error on timeout. Needed because
// WaitRejoin's own signal (K8SRestartDatabaseServiceWaitRejoin,
// cluster/cluster_tst.go) only fires on a PrevState==stateFailed
// transition, which a clean rollout may never trigger: without this check
// a stalled rollout (image pull failure, scheduling problem, PVC attach
// issue) is indistinguishable from a fast successful one.
func (cluster *Cluster) k8sWaitRolloutCompleteWithClient(client kubernetes.Interface, name string, timeout, pollInterval time.Duration) error {
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	deadline := time.Now().Add(timeout)
	for {
		dep, err := deploymentsClient.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			// NotFound fails fast -- the Deployment genuinely doesn't
			// exist, retrying won't change that. Any other error (a
			// transient API server hiccup, rate limiting) is retried like
			// "not yet rolled out" instead of aborting the whole wait.
			if apierrors.IsNotFound(err) || time.Now().After(deadline) {
				return err
			}
			time.Sleep(pollInterval)
			continue
		}
		wantReplicas := int32(1)
		if dep.Spec.Replicas != nil {
			wantReplicas = *dep.Spec.Replicas
		}
		if dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas >= wantReplicas &&
			dep.Status.ReadyReplicas >= wantReplicas &&
			dep.Status.Replicas == wantReplicas {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for Kubernetes rollout of " + name + " to complete")
		}
		time.Sleep(pollInterval)
	}
}

func (cluster *Cluster) K8SWaitRolloutComplete(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sWaitRolloutCompleteWithClient(client, s.Name, k8sRolloutCompleteTimeout, k8sRolloutPollInterval)
}

// k8sForceRepullDatabaseServiceWithClient triggers a rolling pod
// replacement like `kubectl rollout restart`, by patching the pod
// template's restartedAt annotation -- the Deployment controller treats
// that as a spec change and rolls out even though nothing else differs.
// ImagePullPolicy is patched in the same call to the *current* setting,
// since k8sDatabaseDeployment only sets it at creation time. Not Update():
// same resourceVersion problem as k8sEnsureDatabaseSecret.
//
// This is the fallback path a native Kubernetes resize dispatch reaches
// on timeout/Deferred/Infeasible (server.SetRestartCookie, cluster_resize_k8s.go)
// AND the path CheckRestartContainerCookies (reachable every monitor tick,
// see cluster.go) drives automatically for any RestartRidJobsContainer
// cookie -- so it MUST carry the current database memory limit and the dbjobs
// clean-up too (k8sContainerMemoryResourcesPatch, the same single source
// k8sRestartDatabaseServiceWithClient uses): without it, a replacement Pod
// created by this path would revert to whatever the Deployment template held
// at last (re)provision even though Repman may have already applied a new
// DB-side memory configuration expecting the larger cgroup. Bounded by
// k8sAPICallTimeout, not context.TODO(): this call is reachable from the
// perpetual monitor loop (F2), which must never be allowed to stall on a
// hung API request. Needs a bounded Get first (k8sGetDeploymentForResources,
// a context of its own; the patch has another) to know whether dep still
// carries the old dbjobs resources -- still not Update(): the Get is read-only
// and the Patch call below never uses its resourceVersion.
//
// With the flag off the database memory is never part of the patch; a clean
// Deployment is patched as before (an extra, tolerated read aside) and only a
// legacy Deployment gets the dbjobs clean-up entry (see
// k8sRestartDatabaseServiceWithClient).
func (cluster *Cluster) k8sForceRepullDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	containers := []map[string]interface{}{
		{"name": name, "imagePullPolicy": string(k8sImagePullPolicy(cluster))},
	}
	dep, err := cluster.k8sGetDeploymentForResources(client, name, "force image re-pull")
	if err != nil {
		return err
	}
	for _, c := range cluster.k8sContainerMemoryResourcesPatch(dep, name) {
		if c["name"] == name {
			containers[0]["resources"] = c["resources"]
		} else {
			containers = append(containers, c)
		}
	}
	specPatch := map[string]interface{}{
		"template": map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339)},
			},
			"spec": map[string]interface{}{
				"containers": containers,
			},
		},
	}
	if strategy := k8sRecreateStrategyPatch(dep); strategy != nil {
		specPatch["strategy"] = strategy
	}
	patch, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout) // its own, after the read
	defer cancel()
	_, err = client.AppsV1().Deployments(cluster.Name).Patch(ctx, name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot force image re-pull for %s: %s ", name, err)
	}
	return err
}

func (cluster *Cluster) K8SForceRepullDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sForceRepullDatabaseServiceWithClient(client, s.Name)
}

// k8sUpdateDatabaseServiceConfigWithClient patches the Deployment's pod
// template so the main DB container (named exactly like the Deployment) and,
// if present, its dbjobs sidecar (named "<deployment>-dbjobs",
// k8sDatabaseDeployment) both track cluster.Conf.ProvDbImg -- the Kubernetes
// counterpart of OpenSVCUpdateDatabaseServiceConfig, used by
// RollingUpgrade (cluster/cluster_roll.go) to actually change the running
// image instead of only restarting the existing spec.
//
// forcePull=true patches PullAlways regardless of prov-kube-image-force-pull,
// so the upgrade's pull phase re-fetches the tag even when it was already
// cached locally under a different digest; forcePull=false restores the
// steady-state k8sImagePullPolicy.
//
// The Deployment is fetched first and only container names already present
// are included in the patch: a strategic merge patch treats "containers" as
// a merge-by-name list, so patching a name that doesn't exist would create a
// new, incomplete container (missing command, volume mounts, env) rather
// than erroring. The main DB container is required; an older Deployment
// without the dbjobs sidecar is patched on just the main container.
//
// Refuses to patch unless the Deployment is already scaled to 0 replicas
// (enforced below, not just documented on the exported wrapper) -- patching
// a live pod's image would race the Deployment controller's own rollout
// against the caller's explicit stop/start.
func (cluster *Cluster) k8sUpdateDatabaseServiceConfigWithClient(client kubernetes.Interface, name string, forcePull bool) error {
	return cluster.k8sUpdateDatabaseServiceConfigWithClientImage(client, name, forcePull, false)
}

// k8sUpdateDatabaseServiceConfigKeepImage is the rolling-restart entry: the Deployment is
// re-rendered with the image its main container runs today, not prov-db-image (#1861).
func (cluster *Cluster) k8sUpdateDatabaseServiceConfigKeepImage(s *ServerMonitor, keepImage bool) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sUpdateDatabaseServiceConfigWithClientImage(client, s.Name, false, keepImage)
}

func (cluster *Cluster) k8sUpdateDatabaseServiceConfigWithClientImage(client kubernetes.Interface, name string, forcePull bool, keepImage bool) error {
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	dep, err := deploymentsClient.Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot fetch Kubernetes deployment %s: %s ", name, err)
		return err
	}

	// Enforced, not just documented: a nil Replicas is apps/v1's "default to
	// 1" case, so both nil and any non-zero value mean pods may still be
	// live. Patching the image while live would race the Deployment
	// controller's own rollout against the caller's explicit stop/start
	// (RollingUpgrade, cluster/cluster_roll.go) -- refusing here turns that
	// precondition into a guarantee instead of relying on every future
	// caller to remember it.
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		replicas := "nil"
		if dep.Spec.Replicas != nil {
			replicas = strconv.Itoa(int(*dep.Spec.Replicas))
		}
		err := fmt.Errorf("deployment %s is not scaled to 0 replicas (replicas=%s): refusing to patch database image while pods may be live", name, replicas)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}

	jobsName := name + "-dbjobs"
	hasMain := false
	hasJobs := false
	currentImage := ""
	for _, c := range dep.Spec.Template.Spec.Containers {
		switch c.Name {
		case name:
			hasMain = true
			currentImage = c.Image
		case jobsName:
			hasJobs = true
		}
	}
	if !hasMain {
		err := fmt.Errorf("deployment %s has no container named %s: cannot update database image", name, name)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}

	pullPolicy := k8sImagePullPolicy(cluster)
	if forcePull {
		pullPolicy = apiv1.PullAlways
	}
	image := cluster.deployImage()
	if keepImage && currentImage != "" {
		image = currentImage // a restart keeps the running image (#1861)
	}

	container := func(cname string) map[string]interface{} {
		return map[string]interface{}{
			"name":            cname,
			"image":           image,
			"imagePullPolicy": pullPolicy,
		}
	}
	containers := []map[string]interface{}{container(name)}
	if hasJobs {
		containers = append(containers, container(jobsName))
	}
	// The rolling upgrade (scale 0, patch, scale 1) is a Pod-replacing path like a restart: the
	// Deployment is reconciled with the resources contract here too, from the same helper the restart
	// and the force re-pull use (k8sContainerMemoryResourcesPatch) -- the Kubernetes counterpart of
	// OpenSVC re-rendering the full deployment before the start (UpgradeDatabaseDeploymentOnStart).
	// With prov-db-docker-run-args-limit on the database container takes the memory limit and
	// request of GetDBContainerMemoryCapMB; with it off the database memory is left as it is. In
	// either mode an old dbjobs memory request and limit are removed. Image and pull policy are
	// unchanged by it.
	for _, c := range cluster.k8sContainerMemoryResourcesPatch(dep, name) {
		for _, entry := range containers {
			if entry["name"] == c["name"] {
				entry["resources"] = c["resources"]
			}
		}
	}

	// Plain nested maps, not a named Go struct: StrategicMergePatchType's
	// merge-by-name semantics on the "containers" list only need name/image/
	// imagePullPolicy present -- a struct would either omit unrelated
	// Container fields as their JSON zero values (fine for MergePatchType,
	// but silently wrong for a *strategic* merge, which instead relies on
	// the caller sending exactly the fields meant to change) or require
	// pointer fields to make the omission explicit. Maps sidestep the
	// question entirely.
	specPatch := map[string]interface{}{
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": containers,
			},
		},
	}
	if strategy := k8sRecreateStrategyPatch(dep); strategy != nil {
		specPatch["strategy"] = strategy
	}
	patch, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return err
	}

	_, err = deploymentsClient.Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot update database image for %s: %s ", name, err)
	}
	return err
}

// K8SUpdateDatabaseServiceConfig is the Kubernetes implementation of
// UpdateDatabaseServiceConfig (cluster/prov.go). Errors, including "not
// scaled to 0", if called while the Deployment still has live pods (see the
// Kubernetes ordering in RollingUpgrade, cluster/cluster_roll.go): patching
// the pod template at that point would race the Deployment controller's own
// rollout against the caller's explicit stop/start, unlike OpenSVC where a
// service-config update is inert until the next container start.
func (cluster *Cluster) K8SUpdateDatabaseServiceConfig(s *ServerMonitor, forcePull bool) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sUpdateDatabaseServiceConfigWithClient(client, s.Name, forcePull)
}

// PVC and Namespace are intentionally retained — PVC deletion is
// destructive (and now also destroys the persisted conf.d/init subPaths
// mounted from it, k8sConfPersistSubPath/k8sInitPersistSubPath, not just
// the database's own data) and retention semantics are an open question.
func (cluster *Cluster) k8sUnprovisionDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	return cluster.k8sUnprovisionDatabaseServiceWithWait(client, name, k8sLifecycleWaitTimeout, k8sRolloutPollInterval)
}

// k8sWaitDeploymentGone returns nil only once the Deployment API reports NotFound. The delete is
// foreground, so the Deployment stays (with a deletion timestamp) until the garbage collector has
// removed its dependents, its ReplicaSets and their Pods. NotFound is therefore control-plane
// evidence that the Deployment and the relevant dependent API objects have been removed. It is not
// proof that a database process has exited (a Pod force-deleted while its kubelet is unreachable, or
// a Deployment deleted without foreground propagation, can leave one running) nor of storage
// fencing: k8sWaitPVCConsumersGone covers the Pods that still reference the claim. A Deployment that
// is still there at the deadline is an error; it is never force-deleted.
func (cluster *Cluster) k8sWaitDeploymentGone(client kubernetes.Interface, name string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
		_, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
		cancel()
		if apierrors.IsNotFound(err) {
			return nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			return fmt.Errorf("not allowed to read Deployment %s in namespace %s (replication-manager's Kubernetes identity needs `get` on `deployments` there), cannot confirm it is gone: %w", name, cluster.Name, err)
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		time.Sleep(min(poll, remaining))
	}
	if lastErr != nil {
		return fmt.Errorf("cannot confirm that Deployment %s is gone within %s: %w", name, timeout, lastErr)
	}
	return fmt.Errorf("Deployment %s still exists %s after its deletion was requested (foreground deletion waits for its Pods to stop); nothing was force-deleted, retry the operation once it is gone", name, timeout)
}

// k8sUnprovisionDatabaseServiceWithWait deletes the Deployment and the Service, then waits (bounded,
// k8sLifecycleWaitTimeout) for the Deployment to be gone: the foreground delete returns as soon as the
// API accepts it, and a reprovision that followed would find the old Deployment still there. The PVC
// and the namespace are retained as before. Exactly one error is returned (the callers send one value
// on errorChan).
func (cluster *Cluster) k8sUnprovisionDatabaseServiceWithWait(client kubernetes.Interface, name string, timeout, poll time.Duration) error {
	deletePolicy := metav1.DeletePropagationForeground
	var firstErr error

	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	deploymentDeleteFailed := false
	delCtx, delCancel := context.WithTimeout(context.Background(), k8sAPICallTimeout) // every request of a wait-holding operation is bounded
	delErr := deploymentsClient.Delete(delCtx, name, metav1.DeleteOptions{PropagationPolicy: &deletePolicy})
	delCancel()
	if err := delErr; err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes deployment %s %s ", name, err)
		firstErr = err
		deploymentDeleteFailed = true
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes deployment %s.", name)
	}

	servicesClient := client.CoreV1().Services(cluster.Name)
	svcCtx, svcCancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	svcErr := servicesClient.Delete(svcCtx, name, metav1.DeleteOptions{PropagationPolicy: &deletePolicy})
	svcCancel()
	if err := svcErr; err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes service %s %s ", name, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes service %s.", name)
	}

	if !deploymentDeleteFailed {
		if err := cluster.k8sWaitDeploymentGone(client, name, timeout, poll); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Unprovision of %s: %s ", name, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Exactly one value is ever sent on cluster.errorChan.
func (cluster *Cluster) K8SUnprovisionDatabaseService(s *ServerMonitor) {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sUnprovisionDatabaseServiceWithClient(client, s.Name)
}
