package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
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

// Never fatal: a least-privilege RBAC setup may grant no verb at all on the
// cluster-scoped "namespaces" resource, so Create() can be Forbidden even when
// the namespace already exists and provisioning would otherwise succeed. A
// genuinely missing namespace still surfaces below, at the PVC/Deployment/
// Service creates that actually need it.
func (cluster *Cluster) k8sEnsureNamespace(client kubernetes.Interface, name string) {
	namespace := &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := client.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Cannot create namespace %s ", err)
	}
}

// k8sHeadlessServiceName is the shared headless Service every DB pod is a
// member of, for per-pod DNS (see Subdomain in k8sDatabaseDeployment). Not
// cluster.Name-prefixed: Service names only need to be unique per namespace,
// and every cluster already gets its own namespace.
const k8sHeadlessServiceName = "db"

// k8sRoleLabel distinguishes DB pods from proxy pods (prov_k8s_prx.go),
// which share the "app" label but not this one -- needed so the headless
// Service's selector doesn't also match proxies.
const k8sRoleLabel = "role"
const k8sRoleDB = "db"

// k8sClusterDomain is the Kubernetes cluster's DNS domain (kubelet
// --cluster-domain), read from prov-orchestrator-cluster and falling back to
// the Kubernetes default "cluster.local" when unset. "local" (the flag's own
// CLI default, server/server.go) is treated the same as unset -- it's an
// OpenSVC-oriented default (their own env-naming convention), never a real
// Kubernetes --cluster-domain, so any cluster that doesn't explicitly
// override this shared flag would otherwise silently build ".svc.local", not
// resolvable by CoreDNS (confirmed live: clusterin got Failed on every
// server until this fallback existed).
func k8sClusterDomain(cluster *Cluster) string {
	if cluster.Conf.ProvOrchestratorCluster != "" && cluster.Conf.ProvOrchestratorCluster != "local" {
		return cluster.Conf.ProvOrchestratorCluster
	}
	return "cluster.local"
}

// k8sImagePullPolicy mirrors opensvc-image-force-pull's exact semantic
// (prov_opensvc_db.go) for Kubernetes: PullAlways when the flag is set,
// otherwise an explicit PullIfNotPresent rather than leaving the field
// unset -- the K8s implicit default already differs by tag (Always for
// ":latest", IfNotPresent otherwise), which is surprising/undocumented
// behavior; being explicit here means redeploying with the same non-latest
// tag never silently skips a genuinely updated image without this flag.
func k8sImagePullPolicy(cluster *Cluster) apiv1.PullPolicy {
	if cluster.Conf.ProvKubeImageForcePull {
		return apiv1.PullAlways
	}
	return apiv1.PullIfNotPresent
}

// k8sSecretKeyRootPassword is the key MYSQL_ROOT_PASSWORD is stored under in
// each server's own Secret (k8sSecretName) -- one Secret per server, not
// shared, since each server can have its own password.
const k8sSecretKeyRootPassword = "MYSQL_ROOT_PASSWORD"

func k8sSecretName(s *ServerMonitor) string {
	return s.Name + "-secret"
}

// k8sEnsureDatabaseSecret creates or updates the Secret holding
// MYSQL_ROOT_PASSWORD, referenced by the container via SecretKeyRef rather
// than a raw Env value -- a raw value would put the password in cleartext
// in the Deployment spec (kubectl get deploy -o yaml, RBAC permitting), the
// same class of exposure the config-bootstrap Basic Auth header was fixed
// to avoid. Update (not just create-if-missing): password rotation must
// take effect on the next pod restart, not silently keep the old one -- a
// merge Patch, not Update with a freshly-constructed object: Update()
// requires the current resourceVersion for optimistic concurrency, which a
// fresh object never has, so it would be rejected by a real API server
// (the fake clientset used in tests doesn't enforce this, so that gap
// wasn't test-visible). A merge patch needs no resourceVersion at all.
func (cluster *Cluster) k8sEnsureDatabaseSecret(client kubernetes.Interface, s *ServerMonitor) error {
	secretsClient := client.CoreV1().Secrets(cluster.Name)
	secret := &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sSecretName(s),
		},
		Type: apiv1.SecretTypeOpaque,
		StringData: map[string]string{
			k8sSecretKeyRootPassword: s.Pass,
		},
	}
	_, err := secretsClient.Create(context.TODO(), secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// json.Marshal, not manual string concatenation: a password can
		// contain arbitrary characters, and Go's own quoting syntax
		// (strconv.Quote) isn't guaranteed identical to JSON's.
		patch, marshalErr := json.Marshal(struct {
			StringData map[string]string `json:"stringData"`
		}{StringData: map[string]string{k8sSecretKeyRootPassword: s.Pass}})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = secretsClient.Patch(context.TODO(), k8sSecretName(s), ktypes.MergePatchType, patch, metav1.PatchOptions{})
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot provision Kubernetes secret %s ", err)
	}
	return err
}

// k8sDatabasePVC is a pure builder for the database PVC, following
// k8sDatabaseDeployment's own "no API calls" convention so it can be
// asserted directly in tests. StorageClassName is a *string in the K8s API
// specifically to distinguish "use the cluster's default StorageClass" (nil)
// from "use no StorageClass, static pre-bound PV matching only" (a pointer
// to ""), so prov-kube-storage-class empty must stay nil, not "". Size comes
// from prov-db-disk-size (e.g. "20G"), already used by every other
// orchestrator for the same purpose, not the 1Gi that was hardcoded here
// before -- a size that never matched what the operator actually asked for.
func (cluster *Cluster) k8sDatabasePVC(s *ServerMonitor) *apiv1.PersistentVolumeClaim {
	size, err := resource.ParseQuantity(cluster.Conf.ProvDisk)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot parse prov-db-disk-size %q, falling back to the default 20G: %s ", cluster.Conf.ProvDisk, err)
		size = resource.MustParse("20G")
	}
	pvc := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Name + "-" + s.Name + "-claim",
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

// k8sStorageClassesFromClient lists the cluster's available StorageClass
// names, for the provisioning GUI's dropdown (see prov-kube-storage-class) --
// same "*FromClient testable + public live-connecting wrapper" split as
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


// k8sDatabaseDeployment is a pure builder — no API calls, no ServerMonitor
// methods invoked — so the Deployment's placement/selector/env can be
// asserted directly in tests. In particular NodeSelector, not
// Spec.NodeName: NodeName bypasses the scheduler entirely, which means a
// WaitForFirstConsumer StorageClass (the default for most dynamic
// provisioners) never binds the PVC, since that only happens during
// scheduling. NodeSelector pins the pod to the same node while still going
// through the scheduler.
func (cluster *Cluster) k8sDatabaseDeployment(s *ServerMonitor, port int, nodeHostnameLabel string) *appsv1.Deployment {
	// api-credentials-secure-config requires a Bearer JWT or HTTP Basic Auth;
	// sent as a base64 wget --header rather than raw user:pass@host userinfo,
	// since the whole cmd runs through the init container's shell and
	// base64's alphabet can't contain shell metacharacters. Uses "admin",
	// same convention as every other bootstrap credential injection in this
	// codebase (GetExecEnv, OpenSVC's secrets injection, onpremise env
	// exports) -- not "whichever api-credentials entry is configured
	// first", which may lack the grant /config actually requires. Falls
	// back to the default password "repman" if admin hasn't been
	// reconfigured. Only sent when actually required: embedding a real
	// credential in every Deployment spec regardless of whether the
	// endpoint enforces auth would be needless exposure.
	authority := cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	authHeader := ""
	if cluster.Conf.APISecureConfig {
		adminPass := "repman"
		if u, ok := cluster.APIUsers["admin"]; ok {
			adminPass = u.Password
		}
		authHeader = " --header=\"Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("admin:"+adminPass)) + "\""
	}
	// MariaDB's !includedir is non-recursive, so only the generated
	// etc/mysql/conf.d/*.cnf fragments are copied in -- those point
	// log/binlog/tmp/innodb/aria paths at ./.system/... under the datadir,
	// which mariadbd needs pre-created (OpenSVC's moduleset does this as
	// explicit directory resources; nothing analogous exists for K8s).
	// .system/jobs (JOBS_DATADIR) is where the dbjobs sidecar's launcher
	// script (dbjobs_launcher_with_sigterm) cleans up stale *.run
	// directories on every cycle -- confirmed live: missing this one made
	// cleanup_run_dirs fail immediately on startup. Hardcoded, matching the
	// other paths here, rather than calling s.GetJobDatadir(): that method
	// dereferences s.ClusterGroup.Configurator with no nil check, which
	// would violate this function's own "pure builder, no ServerMonitor
	// methods" contract and panic on a bare *ServerMonitor (confirmed via a
	// test). Matches GetJobDatadir()'s own Kubernetes-path result unless
	// the "nosplitpath" db-tag is set, which would use
	// /var/lib/replication-manager-jobs instead -- not handled here.
	systemDirs := "/var/lib/mysql/.system/tmp /var/lib/mysql/.system/logs " +
		"/var/lib/mysql/.system/repl /var/lib/mysql/.system/innodb/undo " +
		"/var/lib/mysql/.system/innodb/redo /var/lib/mysql/.system/aria " +
		"/var/lib/mysql/.system/jobs"
	// GetServerFromURL (cluster_get.go), which handlerMuxServersPortConfig
	// uses to resolve the {serverName} path segment, matches only
	// server.Host -- the domain-qualified name when prov-net-cni is on, not
	// the bare s.Name. Using s.Name here alone would 500 ("No server") on
	// every fetch once the flag is enabled, since it would never match.
	serverPath := s.Name + cluster.GetDomain()
	// The fetched archive's root is repman's own Datadir/init (confirmed via
	// configurator.TarGz's caller, configurator.go) -- so its "init/" entry
	// (dbjobs_new, dbjobs_launcher_with_sigterm, already fully resolved
	// server-side: GenerateDatabaseConfig substitutes every %%ENV:...%%
	// placeholder, including JOBS_DATADIR, before the archive is built, so
	// nothing needs to be templated again here) is copied into a shared
	// volume the dbjobs sidecar mounts at /docker-entrypoint-initdb.d,
	// matching OpenSVC's own mount path for the same purpose
	// (OpenSVCGetJobsContainerSection, prov_opensvc_db.go). No auth on the
	// replication-manager-cli fetch: /static/ is a plain, unauthenticated
	// file server (server/http.go), the same as OpenSVC's own bootstrap
	// script fetching it.
	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /docker-entrypoint-initdb.d " + systemDirs +
			" && wget -qO-" + authHeader + " http://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + serverPath + "/" + s.Port + "/config | tar xzf - -C /tmp/cfg" +
			" && cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/ 2>/dev/null" +
			" && cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/ 2>/dev/null" +
			" && wget -qO /docker-entrypoint-initdb.d/replication-manager-cli http://" + authority + "/static/configurator/bin/replication-manager-cli" +
			" && chmod +x /docker-entrypoint-initdb.d/replication-manager-cli /docker-entrypoint-initdb.d/dbjobs_new /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm 2>/dev/null",
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
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      s.Name + "-conf",
									MountPath: "/etc/mysql/conf.d",
								},
								{
									Name:      s.Name + "-persistent-storage",
									MountPath: "/var/lib/mysql",
								},
								{
									Name:      s.Name + "-init",
									MountPath: "/docker-entrypoint-initdb.d",
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
							Env: []apiv1.EnvVar{
								{
									Name: "MYSQL_ROOT_PASSWORD",
									ValueFrom: &apiv1.EnvVarSource{
										SecretKeyRef: &apiv1.SecretKeySelector{
											LocalObjectReference: apiv1.LocalObjectReference{Name: k8sSecretName(s)},
											Key:                  k8sSecretKeyRootPassword,
										},
									},
								},
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      s.Name + "-persistent-storage",
									MountPath: "/var/lib/mysql",
								},
								{
									Name:      s.Name + "-conf",
									MountPath: "/etc/mysql/conf.d",
								},
							},
						},
						// dbjobs sidecar: runs backups/optimize/config-refresh via
						// share/scripts/dbjobs_new.sh, fetched pre-resolved (every
						// %%ENV:...%% placeholder, including JOBS_DATADIR, already
						// substituted server-side by GenerateDatabaseConfig) as part
						// of the same config archive the init container already
						// fetches -- see the init container's Command above.
						// Same pod as the DB container, so no netns/socket sharing
						// is needed the way OpenSVC's own jobs container
						// (OpenSVCGetJobsContainerSection, prov_opensvc_db.go) needs:
						// Kubernetes pods share network by default, and the script
						// connects over TCP (DB_CONN_PARAMETERS uses -h$MYSQL_SERVER,
						// server.Host), not a local socket.
						{
							Name:    s.Name + "-dbjobs",
							Image:   cluster.Conf.ProvDbImg,
							Command: []string{"/bin/bash", "/docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm"},
							Env: []apiv1.EnvVar{
								{
									Name: "MYSQL_ROOT_PASSWORD",
									ValueFrom: &apiv1.EnvVarSource{
										SecretKeyRef: &apiv1.SecretKeySelector{
											LocalObjectReference: apiv1.LocalObjectReference{Name: k8sSecretName(s)},
											Key:                  k8sSecretKeyRootPassword,
										},
									},
								},
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      s.Name + "-persistent-storage",
									MountPath: "/var/lib/mysql",
								},
								{
									Name:      s.Name + "-init",
									MountPath: "/docker-entrypoint-initdb.d",
								},
							},
						},
					},
					Volumes: []apiv1.Volume{
						{
							Name: s.Name + "-persistent-storage",
							VolumeSource: apiv1.VolumeSource{
								PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
									ClaimName: cluster.Name + "-" + s.Name + "-claim",
								},
							},
						},
						{
							// dbjobs_new/dbjobs_launcher_with_sigterm/replication-manager-cli,
							// populated by the init container, consumed by the dbjobs
							// sidecar -- not the data PVC, since like -conf it's
							// regenerated by the init container on every pod start.
							Name: s.Name + "-init",
							VolumeSource: apiv1.VolumeSource{
								EmptyDir: &apiv1.EmptyDirVolumeSource{},
							},
						},
						{
							// Not the data PVC: config is regenerated by the init
							// container on every pod start, so it doesn't need to
							// survive a restart -- an emptyDir keeps it off the
							// persistent volume entirely.
							Name: s.Name + "-conf",
							VolumeSource: apiv1.VolumeSource{
								EmptyDir: &apiv1.EmptyDirVolumeSource{},
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
	if err := cluster.k8sEnsureDatabaseSecret(client, s); err != nil {
		cluster.errorChan <- err
		return
	}

	/*
			apiVersion: v1
			kind: PersistentVolume
			metadata:
				name: mysql-pv-volume
				labels:
					type: local
			spec:
				storageClassName: manual
				capacity:
					storage: 20Gi
				accessModes:
					- ReadWriteOnce
				hostPath:
					path: "/mnt/data"
			---
			apiVersion: v1
			kind: PersistentVolumeClaim
			metadata:
				name: mysql-pv-claim
			spec:
				storageClassName: manual
				accessModes:
					- ReadWriteOnce
				resources:
					requests:
						storage: 20Gi

		persistentVolumes := client.CoreV1().PersistentVolumes(cluster.Name)
		pv := &apiv1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: cluster.Name + "-" + s.Name + "-volume",
			},
			Spec: apiv1.PersistentVolumeSpec{
				StorageClassName: "manual",
				AccessModes:      {apiv1.ReadWriteOnce},
				Resources: apiv1.ResourceRequirements{
					Requests: apiv1.ResourceList{
						api.ResourceName(api.ResourceStorage): resource.MustParse("1Gi"),
					},
				},
			},
		}
		pvresult, pverr := persistentVolumes.Create(pv)
		if pverr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "Cannot deploy Kubernetes pv %s ", pverr)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "Created Kubernetes physical volume %q.\n", pvresult.GetObjectMeta().GetName())
	*/
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

	// Not mounted or consumed by the Deployment below (bootstrap is the HTTP
	// init-container fetch further down) — best-effort only.
	s.GetDatabaseConfig()
	data, err := os.ReadFile(s.Datadir + "/config.tar.gz")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Provision can not found file %s ", s.Datadir+"/config.tar.gz")
	} else {
		configMapName := s.Name + "-config-map"
		configMap := apiv1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: cluster.Name,
			},
			BinaryData: map[string][]byte{
				"config.tar.gz": data,
			},
		}

		_, cmerr := client.CoreV1().ConfigMaps(cluster.Name).Create(context.TODO(), &configMap, metav1.CreateOptions{})
		if cmerr != nil && !apierrors.IsAlreadyExists(cmerr) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision config map  %s ", cmerr)
		}
	}
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

// No scale-to-zero/drain semantic exists for the Deployment; an explicit
// error here (not silent nil) makes RestartDatabaseService fail fast instead
// of timing out in WaitDatabaseFailed for a stop that never happened.
func (cluster *Cluster) K8SStopDatabaseService(s *ServerMonitor) error {
	return errors.New("stop is not supported for the kubernetes orchestrator")
}

// No real start/scale-up exists either; this only confirms the Deployment is
// present with a non-zero desired replica count before reporting success.
func (cluster *Cluster) k8sStartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: deployment not found: %s ", name, err)
		return err
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
		return err
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas == 0 {
		err := errors.New("database deployment " + name + " is scaled to zero and kubernetes start does not scale it back up")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
		return err
	}
	return nil
}

func (cluster *Cluster) K8SStartDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStartDatabaseServiceWithClient(client, s.Name)
}

// k8sForceRepullDatabaseServiceWithClient triggers a rolling pod
// replacement the same way `kubectl rollout restart` does -- patching the
// pod template's own restartedAt annotation, which the Deployment
// controller treats as a spec change and rolls out even though nothing
// else differs. The container's ImagePullPolicy is patched in the same
// call, to the *current* prov-kube-image-force-pull value: the Deployment
// object itself only ever gets ImagePullPolicy at creation time
// (k8sDatabaseDeployment), so toggling the setting later would otherwise
// have no effect on an already-provisioned server -- IfNotPresent, in
// particular, would then keep skipping the re-pull this action exists to
// force. name (both the Deployment and its DB container's name, s.Name) is
// a Kubernetes object name -- restricted to [a-z0-9-], so, like the pull
// policy (one of two fixed constants), it can't contain characters that
// need JSON escaping, unlike the password in k8sEnsureDatabaseSecret.
// StrategicMergePatchType's containers[].name merge key targets that one
// container without needing its other fields. Not Update(): same
// resourceVersion problem as k8sEnsureDatabaseSecret had.
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

// ConfigMap, PVC and Namespace are intentionally retained — PVC deletion is
// destructive and retention semantics are an open question.
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
