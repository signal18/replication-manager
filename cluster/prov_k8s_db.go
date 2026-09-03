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
	return &appsv1.Deployment{
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
							Name:  s.Name + "-dbjobs",
							Image: cluster.Conf.ProvDbImg,
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
	deployment := cluster.k8sDatabaseDeployment(s, port, nodeHostnameLabel)

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating Kubernetes deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
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

// k8sRestartDatabaseServiceWithClient triggers a rolling pod replacement
// like `kubectl rollout restart`, patching only the restartedAt annotation
// -- unlike k8sForceRepullDatabaseServiceWithClient, never ImagePullPolicy:
// a plain restart (used by RollingRestart, often on a schedule) must never
// silently re-pull a different image.
func (cluster *Cluster) k8sRestartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"` + time.Now().Format(time.RFC3339) + `"}}}}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
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
func (cluster *Cluster) k8sForceRepullDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"template":{` +
		`"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"` + time.Now().Format(time.RFC3339) + `"}},` +
		`"spec":{"containers":[{"name":"` + name + `","imagePullPolicy":"` + string(k8sImagePullPolicy(cluster)) + `"}]}` +
		`}}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
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
