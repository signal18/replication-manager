package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
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
	preload, arenaMax := cluster.GetDBAllocatorEnv()
	if preload == "" {
		return nil
	}
	return []apiv1.EnvVar{
		{Name: "LD_PRELOAD", Value: preload},
		{Name: "MALLOC_ARENA_MAX", Value: arenaMax},
	}
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
	name := k8sClusterSecretName(cluster.Name)
	secretsClient := client.CoreV1().Secrets(cluster.Name)
	secret := &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type:       apiv1.SecretTypeOpaque,
		StringData: values,
	}
	_, err := secretsClient.Create(context.TODO(), secret, metav1.CreateOptions{})
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
		_, err = secretsClient.Patch(context.TODO(), name, ktypes.MergePatchType, patch, metav1.PatchOptions{})
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot provision Kubernetes secret %s ", err)
	}
	return err
}

// k8sEnsureDatabaseSecret is k8sPatchSecretValues for just
// MYSQL_ROOT_PASSWORD. Takes password explicitly rather than reading
// s.Pass, so ProvisionRotatePasswords (prov.go) can call it directly with
// the freshly rotated value.
func (cluster *Cluster) k8sEnsureDatabaseSecret(client kubernetes.Interface, password string) error {
	return cluster.k8sPatchSecretValues(client, map[string]string{k8sSecretKeyRootPassword: password})
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
	if u, ok := cluster.APIUsers["admin"]; ok {
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

// k8sDatabasePVC is a pure builder, directly testable. StorageClassName is
// a *string specifically to distinguish "cluster default" (nil) from "no
// StorageClass" (pointer to ""), so prov-kube-storage-class empty must stay
// nil. Size comes from prov-db-disk-size, like every other orchestrator.
func (cluster *Cluster) k8sDatabasePVC(s *ServerMonitor) *apiv1.PersistentVolumeClaim {
	size, err := resource.ParseQuantity(cluster.Conf.ProvDisk)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot parse prov-db-disk-size %q, falling back to the default 20G: %s ", cluster.Conf.ProvDisk, err)
		size = resource.MustParse("20G")
	}
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
	remoteFetchCmd := "wget" + noCheckCert + " -T 8 -qO /tmp/config.tar.gz" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + serverPath + "/" + s.Port + "/config"

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
	needFetchCmd := "wget" + noCheckCert + " -T 8 -qO /dev/null" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + serverPath + "/" + s.Port + "/need-config-fetch"

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

	// MKDIR_STATUS is the only thing that determines this container's exit
	// code. Kubernetes init containers have no "optional" resource flag
	// like OpenSVC's (a nonzero exit always blocks the pod), so everything
	// after mkdir -- config fetch/apply, CLI fetch, chmod -- is
	// unconditional and best-effort by construction instead.
	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /docker-entrypoint-initdb.d " + systemDirs +
			" ; MKDIR_STATUS=$? ; " +
			applyConfig +
			// replication-manager-cli persists across restarts like
			// conf.d/init above -- a failed fetch just means it isn't
			// refreshed, not missing. Fetched to a temp file first, copied
			// into place only on success: busybox wget's "-qO" has no
			// atomic rename, so a connection dropped mid-transfer would
			// otherwise corrupt a previously-good cached binary in place.
			" ; wget" + noCheckCert + " -T 8 -qO /tmp/replication-manager-cli.new " + scheme + "://" + authority + "/static/configurator/bin/replication-manager-cli 2>/dev/null && cp /tmp/replication-manager-cli.new /docker-entrypoint-initdb.d/replication-manager-cli 2>/dev/null" +
			" ; chmod +x /docker-entrypoint-initdb.d/replication-manager-cli /docker-entrypoint-initdb.d/dbjobs_new /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm 2>/dev/null" +
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
							Image:           cluster.Conf.ProvDbImg,
							ImagePullPolicy: k8sImagePullPolicy(cluster),
							Resources:       cluster.k8sDatabaseContainerResources(),
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
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/var/lib/mysql",
								},
								{
									Name:      k8sDatabaseVolumeName(s.Name),
									MountPath: "/etc/mysql/conf.d",
									SubPath:   k8sConfPersistSubPath,
								},
							},
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
							Image:     cluster.Conf.ProvDbImg,
							Resources: cluster.k8sDBJobsContainerResources(),
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
					Volumes: []apiv1.Volume{
						{
							Name: k8sDatabaseVolumeName(s.Name),
							VolumeSource: apiv1.VolumeSource{
								PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
									ClaimName: k8sDatabasePVCName(cluster.Name, s.Name),
								},
							},
						},
					},
				},
			},
		},
	}
	return dep
}

// k8sDBJobsMemoryCapMB is the dbjobs sidecar's own technical minimum (MiB) --
// NOT an operator-tunable policy (no TOML/Viper key: OpenSVC has no
// equivalent knob for it either, see k8sDatabaseMemoryTargets). Proven in
// Kind (doc/implementation/cluster/KUBERNETES_OPENSVC_RESOURCE_PARITY_IMPLEMENTATION_PLAN.md,
// Phase 1): kubelet sets a Pod-level cgroup memory.max equal to the SUM of
// every container's own memory limit, but only when every container in the
// Pod declares one -- so this is the Kubernetes equivalent of OpenSVC's
// service-level PG cgroup, which wraps both the DB and dbjobs containers.
// OpenSVC's own dbjobs container carries no explicit cap of its own
// (OpenSVCGetJobsContainerSection): it is implicitly bounded only by that
// service-level PG limit. Kubernetes has no such implicit parent enforcement
// without an explicit limit on every container, so dbjobs needs SOME
// concrete number -- this is carved OUT of the same service-level target
// OpenSVC uses (k8sDatabaseMemoryTargets), never added on top of it.
const k8sDBJobsMemoryCapMB = 128

// k8sDatabaseMinMB is the floor the DB container's own share is never let
// drop below, even when prov-db-memory itself is too small to spare
// k8sDBJobsMemoryCapMB for dbjobs (an unrealistic config in practice --
// prov-db-memory defaults to 4G -- but the split must degrade safely rather
// than produce a zero/negative DB share) and the safe fallback when
// prov-db-memory itself fails to parse.
const k8sDatabaseMinMB = 256

// k8sDatabaseMemoryTargets is the SINGLE authority for the Kubernetes
// service-level memory split -- used identically by the provisioning-time
// builder below, the restart/force-repull template reconciliation
// (k8sContainerMemoryResourcesPatch), and the native resizer
// (cluster_resize_k8s.go), so none of the three can ever drift onto
// different numbers.
//
// dbMB + jobsMB == T (cluster.Conf.ProvMem, parsed the SAME way
// openSVCResize parses it for OpenSVC's own DEFAULT.pg_mem_limit) ALWAYS --
// OpenSVC's behavior is the authority for what the effective service cap
// is, not a Kubernetes-specific reinterpretation of it: T IS the effective
// OpenSVC service cap (pg_mem_limit), so the Kubernetes Pod-level cgroup cap
// (dbMB+jobsMB, Phase 1: kubelet sets it to the SUM of every container's own
// limit) must equal that SAME T, never silently add anything above it. This
// does NOT use GetDBContainerMemoryCapMB() (OpenSVC's separate, larger,
// DBU-tier-padded CONTAINER cap, used only for OpenSVC's own container
// run_args -- prov_opensvc_db.go, untouched by this split): mirroring that
// number here would reproduce OpenSVC's OWN two-layer headroom on
// Kubernetes, which is a different, larger effective cap than pg_mem_limit,
// not the one this contract targets. dbjobs is carved OUT of T (see
// k8sDBJobsMemoryCapMB's own doc comment for why it needs an explicit
// number at all), never added on top; if T itself is smaller than that
// technical minimum, dbjobs is the one that shrinks (down to 1Mi) so the DB
// container -- the actually load-bearing process -- keeps as much of T as
// possible.
func (cluster *Cluster) k8sDatabaseMemoryTargets() (dbMB, jobsMB int) {
	totalMB, err := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	if err != nil || totalMB <= 0 {
		// Never surface an invalid/unparseable target as a 0Mi cap: fall back
		// to the same technical minimum the split is itself built around.
		return k8sDatabaseMinMB, k8sDBJobsMemoryCapMB
	}
	jobsMB = k8sDBJobsMemoryCapMB
	if jobsMB >= totalMB {
		jobsMB = 1
	}
	dbMB = totalMB - jobsMB
	return dbMB, jobsMB
}

// k8sDatabaseContainerResources returns the DB container's memory
// Requests/Limits (k8sDatabaseMemoryTargets) -- gated by the same
// prov-db-docker-run-args-limit opt-in OpenSVC's own container run_args
// --memory uses, so a cluster that has chosen not to cap container memory on
// OpenSVC gets the identical choice honored on Kubernetes rather than an
// unrequested new default. Requests == Limits so the resize target is
// unambiguous (matches Docker's own --memory==--memory-swap hard cap) --
// this does NOT by itself make the Pod Kubernetes "Guaranteed" QoS class
// (that also requires a CPU request==limit pair on every container, which
// prov-db-docker-run-args-limit does not set here: CPU/IO cgroup resizing is
// explicitly out of scope for this feature). The native Pod resize
// subresource does not require full-Pod Guaranteed QoS, only that the
// resized resource itself has a request/limit pair on the target container
// -- proven live in Kind with a memory-only, no-CPU-limit test container
// (cluster/smoke_kind_pod_resize_test.go).
func (cluster *Cluster) k8sDatabaseContainerResources() apiv1.ResourceRequirements {
	if !cluster.Conf.ProvDBDockerRunArgsLimit {
		return apiv1.ResourceRequirements{}
	}
	dbMB, _ := cluster.k8sDatabaseMemoryTargets()
	mem := resource.MustParse(strconv.Itoa(dbMB) + "Mi")
	return apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{apiv1.ResourceMemory: mem},
		Limits:   apiv1.ResourceList{apiv1.ResourceMemory: mem},
	}
}

// k8sDBJobsContainerResources mirrors k8sDatabaseContainerResources for the
// dbjobs sidecar -- see k8sDatabaseMemoryTargets.
func (cluster *Cluster) k8sDBJobsContainerResources() apiv1.ResourceRequirements {
	if !cluster.Conf.ProvDBDockerRunArgsLimit {
		return apiv1.ResourceRequirements{}
	}
	_, jobsMB := cluster.k8sDatabaseMemoryTargets()
	mem := resource.MustParse(strconv.Itoa(jobsMB) + "Mi")
	return apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{apiv1.ResourceMemory: mem},
		Limits:   apiv1.ResourceList{apiv1.ResourceMemory: mem},
	}
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
// WARN0212/WARN0213 to a slow cadence; PreserveState keeps the state alive on the
// ticks in between so it does not flap (pstates contract).
const k8sResourceSensorCheckEveryNHeartbeats = 30

// k8sResourceSensorFreshnessWindow is how stale the last successful DBU reading
// (server.DBUConsumed.WindowEnd) may be before the sensor is considered not
// actually delivering data. Generous relative to the dbjobs launcher's own ~60s
// push cadence so a couple of missed/slow cycles don't flap WARN0213.
const k8sResourceSensorFreshnessWindow = 5 * time.Minute

// CheckK8SResourceSensor observes, from the Kubernetes API, whether the DBU
// resource sensor can actually run -- both the provisioning-time precondition
// and the ongoing runtime reality, not just the Deployment template boolean:
//
//   - WARN0212: the database Deployment must carry shareProcessNamespace (so
//     the sidecar reads the DB cgroup via /proc/<pid>/root). Missing means the
//     namespace policy (PodSecurity) forbade it at provision, so provisioning
//     fell back without it. Cluster-scoped: the namespace policy applies to
//     every server alike, so one missing Deployment is enough to raise it.
//   - WARN0213: per-server runtime prerequisites -- the DB Pod is Running, the
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
		cluster.GetStateMachine().PreserveState("WARN0213")
		return
	}
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.GetStateMachine().PreserveState("WARN0212") // API unreachable: keep the last verdict
		cluster.GetStateMachine().PreserveState("WARN0213")
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
	// runtime scan (WARN0213) are tracked independently across the WHOLE loop:
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
			// nor look confirmed-good on WARN0213 just because this pass
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
		cluster.SetState("WARN0213", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0213"], runtimeBrokenURL, runtimeBrokenReason), ErrFrom: "CONF"})
	case runtimeUnconfirmed:
		// Explicitly SET (not merely preserved): a service whose sensor has
		// never been positively verified -- newly provisioned, or every check
		// so far has been inconclusive (missing/ambiguous Pod, API error) --
		// must get a tracked non-ready state from its very FIRST observation,
		// not only once a pre-existing WARN0213 already happens to exist
		// (PreserveState is a no-op when there is nothing to preserve).
		cluster.SetState("WARN0213", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0213"], firstUnconfirmedURL, "sensor state not yet verified"), ErrFrom: "CONF"})
	}
}

// k8sSensorRuntimeVerdict distinguishes a positively-confirmed sensor problem
// from "could not check this pass" -- the two must never be conflated, or a
// transient API hiccup would silently clear a real WARN0213.
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
	if age := time.Since(s.DBUConsumed.WindowEnd); age > k8sResourceSensorFreshnessWindow {
		return k8sSensorBroken, fmt.Sprintf("last DBU reading is %s old (window %s)", age.Round(time.Second), k8sResourceSensorFreshnessWindow)
	}
	return k8sSensorHealthy, ""
}

func (cluster *Cluster) K8SProvisionDatabaseService(s *ServerMonitor) {

	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	if cluster.Conf.APISecureConfig {
		if u, ok := cluster.APIUsers["admin"]; !ok || u.Grants == nil || !u.Grants[config.GrantDBConfigFlag] {
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
	// provisioning call instead (ERR00112) so the operator sees the capability
	// gap immediately, not as a silently-degraded WARN0212 discovered later. A
	// Cloud18-managed cluster (we set the policy) admits it and never hits this
	// path. See doc/implementation/cluster/DBU_RESOURCE_SENSOR.md.
	sensorEnabled := cluster.Conf.MonitoringSystemResources
	if sensorEnabled {
		share := true
		deployment.Spec.Template.Spec.ShareProcessNamespace = &share
	}

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating Kubernetes deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if sensorEnabled && apierrors.IsForbidden(err) {
		cluster.SetState("WARN0212", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0212"], cluster.Name), ErrFrom: "CONF"})
		capErr := fmt.Errorf(clusterError["ERR00112"], s.Name, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", capErr)
		cluster.errorChan <- capErr
		return
	}
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes deployment %s ", err)
		cluster.errorChan <- err
		return
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

// k8sStartDatabaseServiceWithClient scales the Deployment to 1 replica --
// idempotent (a no-op if already at 1, since Start is called
// unconditionally regardless of whether Stop actually ran). Creates a
// brand new pod when scaling up from 0, re-running the init container.
func (cluster *Cluster) k8sStartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":1}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
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

// k8sDBJobsContainerName is the dbjobs sidecar's container name convention
// (k8sDatabaseDeployment).
func k8sDBJobsContainerName(dbContainerName string) string {
	return dbContainerName + "-dbjobs"
}

func k8sDeploymentHasContainer(dep *appsv1.Deployment, name string) bool {
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}

// k8sContainerMemoryResourcesPatch is the SINGLE authoritative source for the
// strategic-merge "containers" entries every Pod-replacing restart path
// (k8sRestartDatabaseServiceWithClient, k8sForceRepullDatabaseServiceWithClient)
// patches onto the Deployment template, so none of them can ever drift onto
// different numbers (the exact drift that let a fallback restart recreate
// the OLD template limit even after Repman had already confirmed/applied a
// new DB memory target). Reconciles BOTH the DB container AND -- if dep's
// live template already has one -- its dbjobs sidecar to the current
// k8sDatabaseMemoryTargets split, so their AGGREGATE always matches the same
// service-level target OpenSVC's own DEFAULT.pg_mem_limit uses, never
// leaving the dbjobs half silently unbounded on an existing Deployment even
// after the DB container's own limit was patched. Only includes the dbjobs
// entry when dep already carries that container: patching a name that
// doesn't exist would create a NEW, incomplete container (missing image,
// command, volume mounts) rather than erroring -- the same landmine
// k8sUpdateDatabaseServiceConfigWithClient's own doc comment describes. nil
// when prov-db-docker-run-args-limit is off (byte-identical patch to before
// this feature in that case).
func (cluster *Cluster) k8sContainerMemoryResourcesPatch(dep *appsv1.Deployment, name string) []map[string]interface{} {
	if !cluster.Conf.ProvDBDockerRunArgsLimit {
		return nil
	}
	dbMB, jobsMB := cluster.k8sDatabaseMemoryTargets()
	patch := []map[string]interface{}{
		{"name": name, "resources": k8sMemoryResourcesFragment(dbMB)},
	}
	jobsName := k8sDBJobsContainerName(name)
	if k8sDeploymentHasContainer(dep, jobsName) {
		patch = append(patch, map[string]interface{}{"name": jobsName, "resources": k8sMemoryResourcesFragment(jobsMB)})
	}
	return patch
}

// k8sRestartDatabaseServiceWithClient triggers a rolling pod replacement
// like `kubectl rollout restart`, patching only the restartedAt annotation
// -- unlike k8sForceRepullDatabaseServiceWithClient, never ImagePullPolicy:
// a plain restart (used by RollingRestart, often on a schedule) must never
// silently re-pull a different image.
//
// When prov-db-docker-run-args-limit is set, the SAME patch also carries the
// CURRENT DB+dbjobs memory targets (k8sContainerMemoryResourcesPatch, sourced
// from cluster.Conf.ProvMem -- already up to date after any confirmed
// dynamic resize, see SetDBMemorySize) onto both containers' Requests/Limits:
// an explicit restart already replaces the Pod by design, so this is the one
// safe moment to reconcile the Deployment's own template (which the resize
// subresource never touched) with the value already confirmed live, instead
// of the replacement Pod reverting to whatever the template held at last
// (re)provision. A cluster with the flag off gets a byte-identical patch to
// before this feature. Needs a bounded Get first (to know whether dep
// already carries a dbjobs container) -- still not Update(): the Get is
// read-only and the Patch call below never uses its resourceVersion, so this
// does not reintroduce the resourceVersion problem k8sEnsureDatabaseSecret
// has with Update().
func (cluster *Cluster) k8sRestartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	templatePatch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339)},
		},
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit {
		dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot restart %s: %s ", name, err)
			return err
		}
		templatePatch["spec"] = map[string]interface{}{
			"containers": cluster.k8sContainerMemoryResourcesPatch(dep, name),
		}
	}
	patch, err := json.Marshal(map[string]interface{}{"spec": map[string]interface{}{"template": templatePatch}})
	if err != nil {
		return err
	}
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
// cookie -- so it MUST carry the current DB+dbjobs memory targets too
// (k8sContainerMemoryResourcesPatch, the same single source
// k8sRestartDatabaseServiceWithClient uses): without it, a replacement Pod
// created by this path would revert to whatever the Deployment template held
// at last (re)provision even though Repman may have already applied a new
// DB-side memory configuration expecting the larger cgroup. Bounded by
// k8sAPICallTimeout, not context.TODO(): this call is reachable from the
// perpetual monitor loop (F2), which must never be allowed to stall on a
// hung API request. Needs a bounded Get first (to know whether dep already
// carries a dbjobs container) -- still not Update(): the Get is read-only
// and the Patch call below never uses its resourceVersion.
func (cluster *Cluster) k8sForceRepullDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	containers := []map[string]interface{}{
		{"name": name, "imagePullPolicy": string(k8sImagePullPolicy(cluster))},
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit {
		dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot force image re-pull for %s: %s ", name, err)
			return err
		}
		for _, c := range cluster.k8sContainerMemoryResourcesPatch(dep, name) {
			if c["name"] == name {
				containers[0]["resources"] = c["resources"]
			} else {
				containers = append(containers, c)
			}
		}
	}
	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339)},
				},
				"spec": map[string]interface{}{
					"containers": containers,
				},
			},
		},
	})
	if err != nil {
		return err
	}
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
	for _, c := range dep.Spec.Template.Spec.Containers {
		switch c.Name {
		case name:
			hasMain = true
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
	image := cluster.Conf.ProvDbImg

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

	// Plain nested maps, not a named Go struct: StrategicMergePatchType's
	// merge-by-name semantics on the "containers" list only need name/image/
	// imagePullPolicy present -- a struct would either omit unrelated
	// Container fields as their JSON zero values (fine for MergePatchType,
	// but silently wrong for a *strategic* merge, which instead relies on
	// the caller sending exactly the fields meant to change) or require
	// pointer fields to make the omission explicit. Maps sidestep the
	// question entirely.
	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": containers,
				},
			},
		},
	})
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
	deletePolicy := metav1.DeletePropagationForeground
	var firstErr error

	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	if err := deploymentsClient.Delete(context.TODO(), name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes deployment %s %s ", name, err)
		firstErr = err
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes deployment %s.", name)
	}

	servicesClient := client.CoreV1().Services(cluster.Name)
	if err := servicesClient.Delete(context.TODO(), name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes service %s %s ", name, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes service %s.", name)
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
