package cluster

import (
	"context"
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Unique per proxy instance: a shared name/selector would collide across
// proxies in the same cluster.
func k8sProxyDeploymentName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-deployment"
}

// Shared cluster-wide across every proxy — never auto-deleted, since no
// single-proxy-scoped operation can prove it belongs to that proxy rather
// than a different, not-yet-migrated one in the same cluster.
func k8sLegacyProxyDeploymentName(clusterName string) string {
	return clusterName + "-deployment"
}

// The per-proxy Service is named after the proxy itself, not
// k8sProxyDeploymentName's cluster-prefixed form: proxy constructors
// (NewProxySQLProxy, prx_proxysql.go) already build the prov-net-cni
// in-cluster DNS host as "<proxy-name>.<namespace>.svc.<cluster-domain>",
// which only resolves if the Service is named exactly prx.GetName().
func k8sProxyServiceName(prx DatabaseProxy) string {
	return prx.GetName()
}

// k8sProxyImage is type-aware: only ProxySQL is implemented today. Every
// other proxy family returns an explicit error instead of silently
// deploying ProvProxProxysqlImg under a different type's name, which would
// look provisioned while running the wrong software entirely.
func (cluster *Cluster) k8sProxyImage(prx DatabaseProxy) (string, error) {
	switch prx.GetType() {
	case config.ConstProxySqlproxy:
		return cluster.Conf.ProvProxProxysqlImg, nil
	default:
		return "", fmt.Errorf("Kubernetes proxy provisioning does not support proxy type %q yet (only %q is implemented)", prx.GetType(), config.ConstProxySqlproxy)
	}
}

// k8sProxyContainerPorts is type-aware, matching k8sProxyImage: ProxySQL
// exposes both its admin interface (prx.GetPort(), used for
// hostgroup/backend configuration) and its SQL traffic interface
// (prx.GetWritePort(), what clients actually connect to) -- GetPort() alone
// (the pre-Phase-3 behavior) only ever exposed the admin port, never the
// port applications need.
func k8sProxyContainerPorts(prx DatabaseProxy) ([]apiv1.ContainerPort, error) {
	switch prx.GetType() {
	case config.ConstProxySqlproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid ProxySQL admin port %q: %s", prx.GetPort(), err)
		}
		return []apiv1.ContainerPort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(adminPort)},
			{Name: "sql", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetWritePort())},
		}, nil
	default:
		return nil, fmt.Errorf("Kubernetes proxy provisioning does not support proxy type %q yet (only %q is implemented)", prx.GetType(), config.ConstProxySqlproxy)
	}
}

// k8sProxyServicePorts mirrors k8sProxyContainerPorts -- the Service must
// expose the same admin/sql ports the container actually listens on.
// TargetPort is left unset: it defaults to Port, which already matches the
// container's own port numbers here.
func k8sProxyServicePorts(prx DatabaseProxy) ([]apiv1.ServicePort, error) {
	switch prx.GetType() {
	case config.ConstProxySqlproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid ProxySQL admin port %q: %s", prx.GetPort(), err)
		}
		return []apiv1.ServicePort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "sql", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
		}, nil
	default:
		return nil, fmt.Errorf("Kubernetes proxy provisioning does not support proxy type %q yet (only %q is implemented)", prx.GetType(), config.ConstProxySqlproxy)
	}
}

// k8sProxyPVCName is the name of the PersistentVolumeClaim backing a
// ProxySQL proxy's config and data directories -- one PVC per proxy,
// mirroring k8sDatabasePVC's one-PVC-per-server model (prov_k8s_db.go).
// Retained on unprovision (k8sUnprovisionProxyServiceWithClient): PVC
// deletion is destructive, same rationale as the database PVC.
func k8sProxyPVCName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-claim"
}

// k8sProxyPVC is a pure builder, directly testable -- mirrors
// k8sDatabasePVC (prov_k8s_db.go) but sized from prov-proxy-disk-size
// (ProvProxDisk) instead of prov-db-disk-size, and its own default
// fallback (also 20G, matching that flag's own CLI default).
func (cluster *Cluster) k8sProxyPVC(prx DatabaseProxy) *apiv1.PersistentVolumeClaim {
	size, err := resource.ParseQuantity(cluster.Conf.ProvProxDisk)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot parse prov-proxy-disk-size %q, falling back to the default 20G: %s ", cluster.Conf.ProvProxDisk, err)
		size = resource.MustParse("20G")
	}
	pvc := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sProxyPVCName(cluster.Name, prx.GetName()),
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

// k8sProxyConfPersistSubPath is a subPath mount of the proxy's own PVC for
// /etc/proxysql, alongside the same PVC's full mount at /var/lib/proxysql --
// same persisted-not-emptyDir rationale as k8sConfPersistSubPath
// (prov_k8s_db.go): a failed config fetch still has the last successful
// boot's config to fall back to, instead of starting from nothing.
const k8sProxyConfPersistSubPath = ".system/etc-proxysql"

// k8sProxySSLPersistSubPath is a third subPath mount, at /etc/ssl. The
// generated proxysql.cnf's ssl_p2s_cert/ssl_p2s_key/ssl_p2s_ca directives
// (mariadb.svc.mrm.proxy.cnf.proxysql.default/.readwritesplit,
// share/opensvc/moduleset_mariadb.svc.mrm.proxy.json) are built from
// "%%ENV:SVC_CONF_ENV_CONFDIR%%/ssl/...", and GetConfigConfigdir()
// (prx_get.go) resolves CONFDIR to the bare "/etc" for Kubernetes (as it
// does for every non-SlapOS orchestrator) -- so the config that's actually
// applied expects those three certs at /etc/ssl/*.pem, not at
// /etc/proxysql/ssl/*.pem, which is where GenerateProxyConfig
// (cluster/configurator/configurator.go) stages them in the fetched
// tarball. A dedicated subPath, not folded into k8sProxyConfPersistSubPath,
// so it can be mounted at /etc/ssl without also relocating proxysql.cnf
// itself out of /etc/proxysql.
const k8sProxySSLPersistSubPath = ".system/etc-ssl-proxysql"

// k8sProxyBootstrapCommand builds the init container's fetch-and-apply
// command and env for ProxySQL. Mirrors k8sDatabaseDeployment's own DB
// bootstrap (prov_k8s_db.go) almost exactly -- same scheme/auth resolution,
// the same bounded wget calls, the same need-config-fetch gate -- but
// targets the proxy's own admin endpoint instead of a database server's,
// and applies the fetched tarball's etc/proxysql/proxysql.cnf and data/*.pem
// (GenerateProxyConfig, cluster/configurator/configurator.go) into the
// ProxySQL-specific persistent paths instead of MariaDB's.
func k8sProxyBootstrapCommand(cluster *Cluster, prx DatabaseProxy) ([]string, []apiv1.EnvVar) {
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

	// prx.GetHost()/prx.GetPort() -- not prx.GetName() -- because
	// GetProxyFromURL (cluster/cluster_get.go), which the server-side
	// /config and /need-config-fetch handlers use to resolve the target
	// (server/api_database.go handlerMuxServersPortConfig,
	// handlerMuxServerNeedConfigFetch), matches on exactly that pair.
	remoteFetchCmd := "wget" + noCheckCert + " -T 8 -qO /tmp/config.tar.gz" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + prx.GetHost() + "/" + prx.GetPort() + "/config"
	needFetchCmd := "wget" + noCheckCert + " -T 8 -qO /dev/null" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + prx.GetHost() + "/" + prx.GetPort() + "/need-config-fetch"

	// Mirrors k8sDatabaseDeployment's own applyConfig: fetch into a scratch
	// dir, and only on a successful fetch *and* extract does the persisted
	// config/data actually get replaced -- any failure leaves the last
	// successful boot's config untouched.
	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"cp /tmp/cfg/etc/proxysql/proxysql.cnf /etc/proxysql/proxysql.cnf 2>/dev/null; " +
		"cp /tmp/cfg/data/*.pem /var/lib/proxysql/ 2>/dev/null; " +
		// GenerateProxyConfig stages the p2s SSL certs under
		// etc/proxysql/ssl/ in the tarball, but the generated cnf's own
		// ssl_p2s_cert/ssl_p2s_key/ssl_p2s_ca directives reference
		// /etc/ssl/*.pem (CONFDIR="/etc" + "/ssl") -- copied here to the
		// path the config actually expects, not the tarball's own staging
		// path. A no-op (empty source glob) when have_ssl is off and the
		// tarball has no etc/proxysql/ssl/ directory at all.
		"cp /tmp/cfg/etc/proxysql/ssl/*.pem /etc/ssl/ 2>/dev/null; " +
		"fi; fi; fi"

	// MKDIR_STATUS is the only thing that determines this container's exit
	// code -- same rationale as k8sDatabaseDeployment: a Kubernetes init
	// container has no "optional" resource flag, so everything after mkdir
	// is unconditional and best-effort by construction instead.
	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /etc/proxysql /var/lib/proxysql /etc/ssl ; MKDIR_STATUS=$? ; " +
			applyConfig +
			" ; exit \"$MKDIR_STATUS\"",
	}

	// initEnv is nil unless a Basic Auth header is actually needed, so a
	// cluster with api-credentials-secure-config off gets a byte-identical
	// init container to before this credential moved into a Secret --
	// matches k8sDatabaseDeployment's own initEnv handling.
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
	return cmd, initEnv
}

// k8sProxyDeployment is a pure builder (no API calls), like
// k8sDatabaseDeployment -- directly testable, and returns before any
// Kubernetes object is touched when the proxy type isn't supported.
//
// Persistent storage, the config/data bootstrap init container, and the
// explicit startup command are ProxySQL-specific (gated on prx.GetType()),
// matching the same type gate k8sProxyImage/k8sProxyContainerPorts already
// enforce: a future proxy family needs its own paths and bootstrap logic,
// not an assumption that ProxySQL's apply here unchanged.
func (cluster *Cluster) k8sProxyDeployment(prx DatabaseProxy) (*appsv1.Deployment, error) {
	image, err := cluster.k8sProxyImage(prx)
	if err != nil {
		return nil, err
	}
	ports, err := k8sProxyContainerPorts(prx)
	if err != nil {
		return nil, err
	}

	container := apiv1.Container{
		Name:            prx.GetName(),
		Image:           image,
		ImagePullPolicy: k8sImagePullPolicy(cluster),
		Ports:           ports,
	}
	var initContainers []apiv1.Container
	var volumes []apiv1.Volume

	if prx.GetType() == config.ConstProxySqlproxy {
		volumeName := prx.GetName() + "-persistent-storage"
		volumes = []apiv1.Volume{
			{
				Name: volumeName,
				VolumeSource: apiv1.VolumeSource{
					PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
						ClaimName: k8sProxyPVCName(cluster.Name, prx.GetName()),
					},
				},
			},
		}
		mounts := []apiv1.VolumeMount{
			{
				Name:      volumeName,
				MountPath: "/var/lib/proxysql",
			},
			{
				// SubPath, not emptyDir: matches k8sDatabaseDeployment's own
				// /etc/mysql/conf.d subPath mount off the same PVC as the
				// data directory.
				Name:      volumeName,
				MountPath: "/etc/proxysql",
				SubPath:   k8sProxyConfPersistSubPath,
			},
			{
				// Where the generated proxysql.cnf's ssl_p2s_* directives
				// actually look (see k8sProxySSLPersistSubPath) -- a
				// separate subPath so mounting it doesn't also relocate
				// proxysql.cnf itself out of /etc/proxysql.
				Name:      volumeName,
				MountPath: "/etc/ssl",
				SubPath:   k8sProxySSLPersistSubPath,
			},
		}
		container.VolumeMounts = mounts
		// --initial: re-derive ProxySQL's on-disk SQLite admin database from
		// proxysql.cnf on every start, matching OpenSVC's own run_command
		// (OpenSVCGetProxysqlContainerSection, prov_opensvc_proxysql.go) --
		// otherwise a stale /var/lib/proxysql/proxysql.db from a previous
		// boot would take precedence over the freshly fetched config.
		container.Command = []string{"proxysql", "--initial", "-f", "-c", "/etc/proxysql/proxysql.cnf"}

		initCmd, initEnv := k8sProxyBootstrapCommand(cluster, prx)
		initContainers = []apiv1.Container{
			{
				Name:         prx.GetName() + "-init",
				Image:        "alpine",
				Command:      initCmd,
				Env:          initEnv,
				VolumeMounts: mounts,
			},
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sProxyDeploymentName(cluster.Name, prx.GetName()),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "repication-manager",
					"tag": prx.GetName(),
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "repication-manager",
						"tag": prx.GetName(),
					},
				},
				Spec: apiv1.PodSpec{
					InitContainers: initContainers,
					Containers:     []apiv1.Container{container},
					Volumes:        volumes,
				},
			},
		},
	}, nil
}

// k8sProxyService is a pure builder mirroring k8sProxyDeployment. Selector
// matches the Deployment's own pod labels exactly, so the Service only ever
// routes to this proxy's own pod, never another proxy's.
func (cluster *Cluster) k8sProxyService(prx DatabaseProxy) (*apiv1.Service, error) {
	ports, err := k8sProxyServicePorts(prx)
	if err != nil {
		return nil, err
	}
	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sProxyServiceName(prx),
		},
		Spec: apiv1.ServiceSpec{
			Ports: ports,
			Selector: map[string]string{
				"app": "repication-manager",
				"tag": prx.GetName(),
			},
		},
	}, nil
}

// Read-only: never deletes, only warns. Its selector ("app: repication-manager"
// only) label-matches new per-proxy Deployments' pods too, and a proxy that
// never migrated off it would otherwise look successfully unprovisioned while
// still running. See doc/implementation/cluster/KUBERNETES_PROVISIONING.md.
func (cluster *Cluster) k8sWarnIfLegacyProxyDeploymentExists(client kubernetes.Interface) {
	name := k8sLegacyProxyDeploymentName(cluster.Name)
	if _, err := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), name, metav1.GetOptions{}); err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Legacy Kubernetes proxy deployment %s still exists alongside per-proxy deployments — see doc/implementation/cluster/KUBERNETES_PROVISIONING.md for manual migration steps", name)
	}
}

// k8sProvisionProxyServiceWithClient creates the per-proxy Deployment and
// its Service, and for ProxySQL, the PVC and (if api-credentials-secure-config
// is enabled) the shared auth-header Secret key its init container's
// bootstrap fetch needs. Type-aware via k8sProxyDeployment/k8sProxyService:
// an unsupported proxy type errors out before any object is touched, so a
// failed provision never leaves a half-created Deployment-without-Service
// (or vice versa) for an unsupported type. No Namespace ensure (relies on
// one already existing from DB provisioning) -- see
// doc/implementation/cluster/KUBERNETES_PROVISIONING.md.
func (cluster *Cluster) k8sProvisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)

	deployment, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot build Kubernetes proxy deployment: %s ", err)
		return err
	}

	// Only reached once the type is known supported (k8sProxyDeployment
	// above already errored out for anything else), so this never creates a
	// PVC for a proxy type that has no Deployment to attach it to.
	if prx.GetType() == config.ConstProxySqlproxy {
		if authHeaderValue := k8sAPIAuthHeaderValue(cluster); authHeaderValue != "" {
			if err := cluster.k8sPatchSecretValues(client, map[string]string{k8sSecretKeyAPIAuthHeader: authHeaderValue}); err != nil {
				return err
			}
		}

		pvc := cluster.k8sProxyPVC(prx)
		pvcResult, pvcErr := client.CoreV1().PersistentVolumeClaims(cluster.Name).Create(context.TODO(), pvc, metav1.CreateOptions{})
		if pvcErr != nil && !apierrors.IsAlreadyExists(pvcErr) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes proxy pvc %s ", pvcErr)
			return pvcErr
		}
		if pvcErr == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes proxy physical volume claim %q.\n", pvcResult.GetObjectMeta().GetName())
		}
	}

	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes deployment %s ", err)
		return err
	}
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created deployment %q.\n", result.GetObjectMeta().GetName())
	}

	// Not re-checked for a type error here: k8sProxyDeployment already
	// validated the type above via the same k8sProxyImage/
	// k8sProxyContainerPorts switch k8sProxyService's own port lookup goes
	// through, so this can only fail on a genuine builder bug.
	service, err := cluster.k8sProxyService(prx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot build Kubernetes proxy service: %s ", err)
		return err
	}

	servicesClient := client.CoreV1().Services(cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating service...")
	result2, err := servicesClient.Create(context.TODO(), service, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes service %s ", err)
		return err
	}
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created service %q.\n", result2.GetObjectMeta().GetName())
	}
	return nil
}

func (cluster *Cluster) K8SProvisionProxyService(prx DatabaseProxy) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sProvisionProxyServiceWithClient(clientset, prx)
}

// k8sUnprovisionProxyServiceWithClient deletes both the per-proxy Deployment
// and its Service, mirroring k8sUnprovisionDatabaseServiceWithClient's
// firstErr pattern: a Service delete failure doesn't stop the Deployment
// delete from being attempted, and either one failing (genuinely, not
// NotFound) is still reported. The PVC (k8sProxyPVC) is intentionally
// retained -- same rationale as the database PVC
// (k8sUnprovisionDatabaseServiceWithClient, prov_k8s_db.go): deletion is
// destructive and retention semantics are an open question.
func (cluster *Cluster) k8sUnprovisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	deletePolicy := metav1.DeletePropagationForeground
	var firstErr error

	deploymentName := k8sProxyDeploymentName(cluster.Name, prx.GetName())
	if err := client.AppsV1().Deployments(cluster.Name).Delete(context.TODO(), deploymentName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes deployment %s %s ", deploymentName, err)
		firstErr = err
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes deployment %s.", deploymentName)
	}

	serviceName := k8sProxyServiceName(prx)
	if err := client.CoreV1().Services(cluster.Name).Delete(context.TODO(), serviceName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes service %s %s ", serviceName, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes service %s.", serviceName)
	}

	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)
	return firstErr
}

func (cluster *Cluster) K8SUnprovisionProxyService(prx DatabaseProxy) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sUnprovisionProxyServiceWithClient(clientset, prx)
}

// k8sStopProxyServiceWithClient scales the per-proxy Deployment to 0
// replicas -- same scale-to-0 pattern as k8sStopDatabaseServiceWithClient
// (prov_k8s_db.go), never the shared legacy Deployment
// (k8sLegacyProxyDeploymentName).
func (cluster *Cluster) k8sStopProxyServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":0}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot stop proxy %s: %s ", name, err)
	}
	return err
}

// K8SStopProxyService is the Kubernetes implementation of StopProxyService
// (cluster/prov.go). Does not auto-provision a missing Deployment, and never
// touches the legacy shared <cluster>-deployment.
func (cluster *Cluster) K8SStopProxyService(server DatabaseProxy) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStopProxyServiceWithClient(client, k8sProxyDeploymentName(cluster.Name, server.GetName()))
}

// k8sStartProxyServiceWithClient scales the per-proxy Deployment back to 1
// replica -- idempotent, same as k8sStartDatabaseServiceWithClient
// (prov_k8s_db.go): a no-op if already at 1, since Start is called
// unconditionally regardless of whether Stop actually ran.
func (cluster *Cluster) k8sStartProxyServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":1}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start proxy %s: %s ", name, err)
	}
	return err
}

// K8SStartProxyService is the Kubernetes implementation of StartProxyService
// (cluster/prov.go). Does not auto-provision a missing Deployment, and never
// touches the legacy shared <cluster>-deployment.
func (cluster *Cluster) K8SStartProxyService(server DatabaseProxy) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStartProxyServiceWithClient(client, k8sProxyDeploymentName(cluster.Name, server.GetName()))
}
