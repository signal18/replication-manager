package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

// k8sDockerParityUlimitShellPrefix caps RLIMIT_NOFILE to plain "docker
// run"'s own default (soft 1024 / hard 1048576), which OpenSVC's containers
// get for free. Some runtimes (observed on kind/containerd) instead hand
// pods an inflated default (~1 billion), which hangs HAProxy's
// external-check fork path until timeout.
func k8sDockerParityUlimitShellPrefix() string {
	return "ulimit -Sn 1024; ulimit -Hn 1048576; "
}

func k8sProxyDeploymentName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-deployment"
}

// k8sLegacyProxyDeploymentName is the pre-per-proxy-naming shared Deployment;
// never auto-deleted since no single proxy can prove it owns it.
func k8sLegacyProxyDeploymentName(clusterName string) string {
	return clusterName + "-deployment"
}

// Must equal prx.GetName(): proxy constructors build the prov-net-cni DNS
// host as "<proxy-name>.<namespace>.svc.<cluster-domain>".
func k8sProxyServiceName(prx DatabaseProxy) string {
	return prx.GetName()
}

// k8sProxyServiceNameErrs validates prx's name against Kubernetes' actual
// Service name constraint (RFC 1035 label: lowercase alphanumeric or '-',
// starting with a letter, no dots) -- stricter than the RFC 1123 subdomain
// Deployment/PVC names accept, which does allow dots. Before this PR, proxy
// provisioning only ever built a Deployment (no Service) and the DB side's
// equivalent (k8sHeadlessServiceName, prov_k8s_db.go) is a constant, not
// derived from a per-server host string, so this constraint never applied
// to a proxy's *-servers entry before. k8sProxyServiceName must equal
// prx.GetName() unchanged for prov-net-cni's DNS host derivation
// (NewProxySQLProxy/NewHaproxyProxy) to resolve, so an invalid name can
// only be reported here, not silently sanitized to a different value that
// would then disagree with that DNS host. Checked up front in
// k8sProvisionProxyServiceWithClient, before any Create() call, so a
// literal IP address or dotted hostname in haproxy-servers/proxysql-servers
// (a normal, pre-existing config on non-Kubernetes orchestrators) fails
// clearly instead of leaving a Deployment/PVC provisioned with no Service
// after an opaque Kubernetes API validation error.
func k8sProxyServiceNameErrs(prx DatabaseProxy) []string {
	return validation.IsDNS1035Label(prx.GetName())
}

// k8sProxyTypeLabel records which proxy type owns a Deployment/Service/PVC,
// since their names (k8sProxyDeploymentName/k8sProxyServiceName/
// k8sProxyPVCName) are keyed on proxy name alone -- AddProxy (cluster.go)
// deliberately does not block two different-type proxies from sharing a
// name (a normal setup outside Kubernetes, e.g. same host / different write
// ports), so k8sProxyNameOwnedByDifferentType below needs this label to
// detect the case here, where it actually does collide.
const k8sProxyTypeLabel = "proxy-type"

// k8sProxyNameOwnedByDifferentType reports whether prx's own Deployment,
// Service, and/or PVC names already belong to a different proxy type in
// this cluster/namespace. Without this check, k8sProvisionProxyServiceWithClient
// would hit AlreadyExists on the Create() calls below and -- correctly, in
// the ordinary same-type reprovision case -- treat that as an idempotent
// success, so a second, different-type proxy sharing a name would silently
// never get a running Pod. The PVC check specifically covers a sequence
// Deployment/Service alone can't: provision type A, unprovision it (PVC
// deliberately retained, k8sUnprovisionProxyServiceWithClient), then
// provision type B under the same name -- Deployment/Service are gone, so
// only the PVC's own label still proves the name was already used.
//
// Known limitation: detection is label-only (k8sProxyTypeLabel), which this
// function can only see on objects created by this same code. Per-proxy
// object naming (k8sProxyDeploymentName/k8sProxyServiceName) has no earlier
// history that shipped without the label, so there is no real upgrade path
// with pre-existing unlabeled objects today -- but an operator-created
// Deployment/Service with a colliding name and no label would still pass
// through undetected. Deliberately not inferring ownership from anything
// else (e.g. container image) instead: an image is expected to legitimately
// change across a same-type reprovision (a version bump), so comparing it
// would produce false-positive collisions on an ordinary upgrade -- worse
// than the gap it would close. If this ever needs closing for real, the
// label is the fix (backfill it on read, or require operators to label
// pre-existing objects manually), not inference.
//
// A Get() failure other than NotFound (RBAC, a transient API error, ...) is
// returned as a real error rather than silently treated as "no collision":
// letting provisioning proceed past an unreadable object reopens the same
// AlreadyExists-treated-as-idempotent-success gap this function exists to
// close.
//
// A nil *k8sProxyNameCollision means no collision. ObjectKind distinguishes
// which object it found the collision on, since the right recovery advice
// differs: a Deployment/Service collision means the other proxy is still
// live (unprovision it, or use a distinct name), while a PVC-only collision
// means it's just retained leftovers from an unprovisioned proxy (delete
// the PVC to reuse the name for a different type).
func (cluster *Cluster) k8sProxyNameOwnedByDifferentType(client kubernetes.Interface, prx DatabaseProxy) (*k8sProxyNameCollision, error) {
	depName := k8sProxyDeploymentName(cluster.Name, prx.GetName())
	dep, depErr := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), depName, metav1.GetOptions{})
	switch {
	case depErr == nil:
		if t := dep.Labels[k8sProxyTypeLabel]; t != "" && t != prx.GetType() {
			return &k8sProxyNameCollision{OwnerType: t, ObjectKind: "Deployment", ObjectName: depName}, nil
		}
	case !apierrors.IsNotFound(depErr):
		return nil, fmt.Errorf("cannot check Deployment %q for a proxy-type collision: %s", depName, depErr)
	}

	svcName := k8sProxyServiceName(prx)
	svc, svcErr := client.CoreV1().Services(cluster.Name).Get(context.TODO(), svcName, metav1.GetOptions{})
	switch {
	case svcErr == nil:
		if t := svc.Labels[k8sProxyTypeLabel]; t != "" && t != prx.GetType() {
			return &k8sProxyNameCollision{OwnerType: t, ObjectKind: "Service", ObjectName: svcName}, nil
		}
	case !apierrors.IsNotFound(svcErr):
		return nil, fmt.Errorf("cannot check Service %q for a proxy-type collision: %s", svcName, svcErr)
	}

	pvcName := k8sProxyPVCName(cluster.Name, prx.GetName())
	pvc, pvcErr := client.CoreV1().PersistentVolumeClaims(cluster.Name).Get(context.TODO(), pvcName, metav1.GetOptions{})
	switch {
	case pvcErr == nil:
		if t := pvc.Labels[k8sProxyTypeLabel]; t != "" && t != prx.GetType() {
			return &k8sProxyNameCollision{OwnerType: t, ObjectKind: "PVC", ObjectName: pvcName}, nil
		}
	case !apierrors.IsNotFound(pvcErr):
		return nil, fmt.Errorf("cannot check PVC %q for a proxy-type collision: %s", pvcName, pvcErr)
	}

	return nil, nil
}

// k8sProxyNameCollision is k8sProxyNameOwnedByDifferentType's result: which
// object the colliding name was found on, and which proxy type owns it.
type k8sProxyNameCollision struct {
	OwnerType  string
	ObjectKind string // "Deployment", "Service", or "PVC"
	ObjectName string
}

// RecoveryHint explains, in terms specific to ObjectKind, what an operator
// needs to do before this name can be reused for a different proxy type.
func (c *k8sProxyNameCollision) RecoveryHint(namespace string) string {
	if c.ObjectKind == "PVC" {
		return fmt.Sprintf("this looks like a retained PVC left behind by an unprovisioned %s proxy -- if you intend to reuse this name for a different proxy type, delete it first: kubectl delete pvc %s -n %s",
			c.OwnerType, c.ObjectName, namespace)
	}
	return fmt.Sprintf("unprovision the existing %s proxy first, or use a distinct name for this one", c.OwnerType)
}

var k8sSupportedProxyTypes = []string{config.ConstProxySqlproxy, config.ConstProxyHaproxy, config.ConstProxyMaxscale}

func k8sUnsupportedProxyTypeErr(proxyType string) error {
	return fmt.Errorf("Kubernetes proxy provisioning does not support proxy type %q yet (only %q are implemented)", proxyType, k8sSupportedProxyTypes)
}

func (cluster *Cluster) k8sProxyImage(prx DatabaseProxy) (string, error) {
	switch prx.GetType() {
	case config.ConstProxySqlproxy:
		return cluster.Conf.ProvProxProxysqlImg, nil
	case config.ConstProxyHaproxy:
		return cluster.Conf.ProvProxHaproxyImg, nil
	case config.ConstProxyMaxscale:
		return cluster.Conf.ProvProxMaxscaleImg, nil
	default:
		return "", k8sUnsupportedProxyTypeErr(prx.GetType())
	}
}

// k8sProxyPortSpec is orchestrator-agnostic (name/protocol/port number);
// k8sProxyContainerPorts and k8sProxyServicePorts each map it onto their own
// k8s API type so the per-type port list (admin/sql/write/read/stat) is
// defined exactly once instead of hand-duplicated in two switches that could
// silently drift apart.
type k8sProxyPortSpec struct {
	Name     string
	Protocol apiv1.Protocol
	Port     int32
}

// HaproxyStatPort isn't carried on the DatabaseProxy interface, so it's read
// via prx.GetCluster().Conf instead.
func k8sProxyPortSpecs(prx DatabaseProxy) ([]k8sProxyPortSpec, error) {
	switch prx.GetType() {
	case config.ConstProxySqlproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid ProxySQL admin port %q: %s", prx.GetPort(), err)
		}
		return []k8sProxyPortSpec{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "sql", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
		}, nil
	case config.ConstProxyHaproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid HAProxy runtime API port %q: %s", prx.GetPort(), err)
		}
		return []k8sProxyPortSpec{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
			{Name: "read", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetReadPort())},
			{Name: "stat", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetCluster().Conf.HaproxyStatPort)},
		}, nil
	case config.ConstProxyMaxscale:
		// maxscale-mode (legacy/pinloki config syntax) and maxscale-rest-api
		// (REST vs MaxAdmin client protocol) are independent settings -- but
		// a pinloki config never generates a MaxAdmin ("CLI") listener (see
		// MaxscaleUsesPinloki), so pinloki + maxscale-rest-api=false would
		// otherwise produce a Service exposing an admin port nothing is
		// listening on. Checked here, before any Kubernetes object is
		// created, since this is the single source both
		// k8sProxyContainerPorts and k8sProxyServicePorts derive from.
		cluster := prx.GetCluster()
		usesPinloki := cluster.MaxscaleUsesPinloki()
		var adminPort int
		if cluster.Conf.MxsRestApi {
			adminPort = cluster.Conf.MxsRestPort
		} else {
			if usesPinloki {
				return nil, fmt.Errorf("MaxScale is configured for pinloki-mode config (maxscale-mode=%q, image %q) but maxscale-rest-api is disabled: pinloki configs provide no MaxAdmin listener, so enable maxscale-rest-api or switch maxscale-mode to legacy", cluster.Conf.MxsMode, cluster.Conf.ProvProxMaxscaleImg)
			}
			var err error
			adminPort, err = strconv.Atoi(prx.GetPort())
			if err != nil {
				return nil, fmt.Errorf("invalid MaxScale MaxAdmin port %q: %s", prx.GetPort(), err)
			}
		}
		specs := []k8sProxyPortSpec{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
			{Name: "rw-split", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetReadWritePort())},
			{Name: "binlog", Protocol: apiv1.ProtocolTCP, Port: int32(cluster.Conf.MxsBinlogPort)},
		}
		// Side-effect-free equivalent of MaxscaleProxy.MaxscaleUsesMaxinfo:
		// that method also raises WARN0211, which this pure port-spec
		// builder must not do. Pinloki dropped the maxinfo HTTP plugin
		// entirely (MaxscaleUsesPinloki), so the port is simply omitted --
		// not a provisioning failure -- leaving admin/write/rw-split/binlog
		// intact and REST monitoring unaffected.
		if cluster.Conf.MxsGetInfoMethod == "maxinfo" && !usesPinloki {
			specs = append(specs, k8sProxyPortSpec{Name: "maxinfo", Protocol: apiv1.ProtocolTCP, Port: int32(cluster.Conf.MxsMaxinfoPort)})
		}
		return specs, nil
	default:
		return nil, k8sUnsupportedProxyTypeErr(prx.GetType())
	}
}

func k8sProxyContainerPorts(prx DatabaseProxy) ([]apiv1.ContainerPort, error) {
	specs, err := k8sProxyPortSpecs(prx)
	if err != nil {
		return nil, err
	}
	ports := make([]apiv1.ContainerPort, len(specs))
	for i, s := range specs {
		ports[i] = apiv1.ContainerPort{Name: s.Name, Protocol: s.Protocol, ContainerPort: s.Port}
	}
	return ports, nil
}

func k8sProxyServicePorts(prx DatabaseProxy) ([]apiv1.ServicePort, error) {
	specs, err := k8sProxyPortSpecs(prx)
	if err != nil {
		return nil, err
	}
	ports := make([]apiv1.ServicePort, len(specs))
	for i, s := range specs {
		ports[i] = apiv1.ServicePort{Name: s.Name, Protocol: s.Protocol, Port: s.Port}
	}
	return ports, nil
}

// k8sProxyTypeHasPersistentStorage reports whether prx's type gets a PVC, a
// bootstrap init container, and a custom startup command.
func k8sProxyTypeHasPersistentStorage(proxyType string) bool {
	return proxyType == config.ConstProxySqlproxy || proxyType == config.ConstProxyHaproxy || proxyType == config.ConstProxyMaxscale
}

func k8sProxyPVCName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-claim"
}

// k8sProxyVolumeName mirrors k8sDatabaseVolumeName (prov_k8s_db.go) for the
// proxy side. Just the Pod-internal identifier linking this one Volume to
// its VolumeMounts -- not a globally-unique object name like the PVC's own
// (k8sProxyPVCName), so it doesn't need the cluster name too.
func k8sProxyVolumeName(proxyName string) string {
	return proxyName + "-data"
}

// Sized from prov-proxy-disk-size, falling back to 20G if unparseable.
func (cluster *Cluster) k8sProxyPVC(prx DatabaseProxy) *apiv1.PersistentVolumeClaim {
	size, err := resource.ParseQuantity(cluster.Conf.ProvProxDisk)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Cannot parse prov-proxy-disk-size %q, falling back to the default 20G: %s ", cluster.Conf.ProvProxDisk, err)
		size = resource.MustParse("20G")
	}
	pvc := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   k8sProxyPVCName(cluster.Name, prx.GetName()),
			Labels: map[string]string{k8sProxyTypeLabel: prx.GetType()},
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
	if cluster.Conf.ProvKubeProxyStorageClass != "" {
		storageClass := cluster.Conf.ProvKubeProxyStorageClass
		pvc.Spec.StorageClassName = &storageClass
	}
	return pvc
}

// k8sProxyConfPersistSubPath is a subPath mount for /etc/proxysql, alongside
// the PVC's full mount at /var/lib/proxysql, so a failed config fetch still
// has the last successful boot's config to fall back to.
const k8sProxyConfPersistSubPath = ".system/etc-proxysql"

// k8sProxySSLPersistSubPath mounts /etc/ssl separately from
// k8sProxyConfPersistSubPath: proxysql.cnf's ssl_p2s_* directives resolve
// CONFDIR to "/etc" on Kubernetes, so the certs must land at /etc/ssl/*.pem,
// not the /etc/proxysql/ssl/*.pem path GenerateProxyConfig stages them at.
const k8sProxySSLPersistSubPath = ".system/etc-ssl-proxysql"

// k8sHaproxyConfPersistSubPath mounts /usr/local/etc/haproxy, where the
// haproxytech/haproxy-alpine image's default entrypoint reads haproxy.cfg
// from. HAProxy's SSL certs are staged under etc/haproxy/ssl/ in the
// tarball, inside this same directory, so no second SSL mount is needed.
const k8sHaproxyConfPersistSubPath = ".system/etc-haproxy"

// k8sMaxscaleConfPersistSubPath persists a whole directory (mounted at
// k8sMaxscaleConfPersistDir), not /etc/maxscale.cnf directly: a subPath
// mount straight onto a single, not-yet-existing file fails on kubelet
// ("not a directory"), the same limitation HAProxy's checkmaster/checkslave
// hit. The main container's Command copies the persisted file over
// /etc/maxscale.cnf before exec'ing the real entrypoint.
const k8sMaxscaleConfPersistSubPath = ".system/etc-maxscale-cnf"

// k8sMaxscaleConfPersistDir is where k8sMaxscaleConfPersistSubPath is
// mounted; the persisted config lands at k8sMaxscaleConfPersistDir +
// "/maxscale.cnf".
const k8sMaxscaleConfPersistDir = "/etc/maxscale-persist"

// k8sMaxscaleCachePersistSubPath persists /var/cache/maxscale: the
// binlogrouter service is always generated regardless of MxsBinlogOn, so
// this is always mounted rather than gated on it.
const k8sMaxscaleCachePersistSubPath = ".system/var-cache-maxscale"

// k8sProxyFetchConfigCmds builds the need-fetch/remote-fetch wget commands
// shared by every proxy family's bootstrap init container; only what each
// family does with the fetched tarball differs.
func k8sProxyFetchConfigCmds(cluster *Cluster, prx DatabaseProxy) (needFetchCmd string, remoteFetchCmd string, initEnv []apiv1.EnvVar) {
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

	// GetProxyFromURL resolves by host+port, not name, so the fetch target
	// must use those too.
	remoteFetchCmd = "wget" + noCheckCert + " -T 8 -qO /tmp/config.tar.gz" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + prx.GetHost() + "/" + prx.GetPort() + "/config"
	needFetchCmd = "wget" + noCheckCert + " -T 8 -qO /dev/null" + authHeader + " " + scheme + "://" + authority + "/api/clusters/" + cluster.Name + "/servers/" + prx.GetHost() + "/" + prx.GetPort() + "/need-config-fetch"

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
	return
}

// k8sProxyBootstrapCommand builds the init container's fetch-and-apply
// command for ProxySQL: only on a successful fetch and extract does the
// persisted config/data get replaced, so a failure leaves the last
// successful boot's config untouched.
func k8sProxyBootstrapCommand(cluster *Cluster, prx DatabaseProxy) ([]string, []apiv1.EnvVar) {
	needFetchCmd, remoteFetchCmd, initEnv := k8sProxyFetchConfigCmds(cluster, prx)

	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"cp /tmp/cfg/etc/proxysql/proxysql.cnf /etc/proxysql/proxysql.cnf 2>/dev/null; " +
		"cp /tmp/cfg/data/*.pem /var/lib/proxysql/ 2>/dev/null; " +
		// proxysql.cnf's ssl_p2s_* directives expect /etc/ssl/*.pem, not the
		// tarball's etc/proxysql/ssl/ staging path.
		"cp /tmp/cfg/etc/proxysql/ssl/*.pem /etc/ssl/ 2>/dev/null; " +
		"fi; fi; fi"

	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /etc/proxysql /var/lib/proxysql /etc/ssl ; MKDIR_STATUS=$? ; " +
			applyConfig +
			" ; exit \"$MKDIR_STATUS\"",
	}
	return cmd, initEnv
}

// k8sHaproxyBootstrapCommand copies the fetched tarball's etc/haproxy/ tree
// and init/checkmaster, init/checkslave into the persistent mount, chmod'd
// executable. checkmaster/checkslave land under /usr/local/etc/haproxy here,
// not at /usr/bin -- for haproxy-mode=externalcheck, k8sProxyDeployment's
// container.Command copies them into /usr/bin from there before exec'ing
// haproxy, since Kubernetes has no safe subPath mount to a single file that
// doesn't already exist in the image. The scripts are still staged here
// unconditionally regardless of mode, matching GetProxyConfig()'s tarball,
// which doesn't vary by haproxy-mode either.
func k8sHaproxyBootstrapCommand(cluster *Cluster, prx DatabaseProxy) ([]string, []apiv1.EnvVar) {
	needFetchCmd, remoteFetchCmd, initEnv := k8sProxyFetchConfigCmds(cluster, prx)

	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"cp -r /tmp/cfg/etc/haproxy/. /usr/local/etc/haproxy/ 2>/dev/null; " +
		"cp /tmp/cfg/init/checkmaster /usr/local/etc/haproxy/checkmaster 2>/dev/null; " +
		"cp /tmp/cfg/init/checkslave /usr/local/etc/haproxy/checkslave 2>/dev/null; " +
		"chmod +x /usr/local/etc/haproxy/checkmaster /usr/local/etc/haproxy/checkslave 2>/dev/null; " +
		"fi; fi; fi"

	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg /usr/local/etc/haproxy ; MKDIR_STATUS=$? ; " +
			applyConfig +
			" ; exit \"$MKDIR_STATUS\"",
	}
	return cmd, initEnv
}

// k8sMaxscaleBootstrapCommand fetches and stages MaxScale's config, selecting
// the legacy or pinloki source file per cluster.MaxscaleUsesPinloki (see
// prov_opensvc_maxscale.go's OpenSVCGetMaxscaleContainerSection, the same
// selection on the OpenSVC side). The final cp+mv step is atomic: cp writes
// to a .tmp file in the same directory as the persisted config, and only a
// successful cp is followed by "mv -f" onto the real file -- so a failed
// need-config-fetch, remote fetch, tar extraction, missing source file, or
// failed copy all leave the previous known-good maxscale.cnf completely
// untouched, and a partially-written .tmp file is never mistaken for the
// active config (mv only ever promotes a fully-written one). No SSL copy
// step: none of the generated maxscale.cnf/maxscale-pinloki.cnf variants
// reference the ssl/*.pem files GenerateProxyConfig stages for every proxy
// family, so mounting/copying them here would be dead weight.
func k8sMaxscaleBootstrapCommand(cluster *Cluster, prx DatabaseProxy) ([]string, []apiv1.EnvVar) {
	needFetchCmd, remoteFetchCmd, initEnv := k8sProxyFetchConfigCmds(cluster, prx)

	sourceFile := "maxscale.cnf"
	if cluster.MaxscaleUsesPinloki() {
		sourceFile = "maxscale-pinloki.cnf"
	}
	persistedFile := k8sMaxscaleConfPersistDir + "/maxscale.cnf"
	tmpFile := persistedFile + ".tmp"

	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"if [ -f /tmp/cfg/etc/maxscale/" + sourceFile + " ]; then " +
		"if cp /tmp/cfg/etc/maxscale/" + sourceFile + " " + tmpFile + " 2>/dev/null; then " +
		"mv -f " + tmpFile + " " + persistedFile + " 2>/dev/null; " +
		"fi; fi; fi; fi; fi"

	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg " + k8sMaxscaleConfPersistDir + " /var/cache/maxscale ; MKDIR_STATUS=$? ; " +
			applyConfig +
			" ; exit \"$MKDIR_STATUS\"",
	}
	return cmd, initEnv
}

// k8sMaxscaleContainerSpec fills in MaxScale's persistent-storage mounts and
// mode-specific startup command on container, and returns the bootstrap init
// container's command/env. See k8sProxyDeployment.
//
// Legacy and pinloki need different startup behavior because the images
// behave differently, not because of the config syntax itself: pinloki-era
// images (e.g. mariadb/maxscale:23.08) have a live-confirmed entrypoint bug
// where a bare "rsyslogd" call never daemonizes and hangs startup before
// maxscale-start ever runs -- harmless to skip since MaxScale itself logs via
// maxlog, not syslog. Legacy images don't carry that bug, so their normal
// docker-entrypoint.sh is used unchanged. Pinloki's shell must stay alive as
// PID 1 (the trap + "wait" pattern) so a Kubernetes TERM to the container
// reaches the trap and shuts MaxScale down gracefully via monit instead of
// killing it out from under a supervised process; an exec here would replace
// the shell and drop that trap.
func k8sMaxscaleContainerSpec(cluster *Cluster, prx DatabaseProxy, container *apiv1.Container, volumeName string) (mounts []apiv1.VolumeMount, initCmd []string, initEnv []apiv1.EnvVar) {
	mounts = []apiv1.VolumeMount{
		{
			Name:      volumeName,
			MountPath: k8sMaxscaleConfPersistDir,
			SubPath:   k8sMaxscaleConfPersistSubPath,
		},
		{
			Name:      volumeName,
			MountPath: "/var/cache/maxscale",
			SubPath:   k8sMaxscaleCachePersistSubPath,
		},
	}

	copyPersistedConfig := "cp " + k8sMaxscaleConfPersistDir + "/maxscale.cnf /etc/maxscale.cnf 2>/dev/null; "
	if cluster.MaxscaleUsesPinloki() {
		container.Command = []string{
			"sh", "-c",
			copyPersistedConfig +
				"rm -f /var/run/*.pid; " +
				"chown -R maxscale:maxscale /var/lib/maxscale; " +
				"trap '/usr/bin/monit unmonitor all; /usr/bin/maxscale-stop; /usr/bin/monit quit' TERM; " +
				"maxscale-start && monit -I & " +
				"wait",
		}
	} else {
		container.Command = []string{
			"sh", "-c",
			copyPersistedConfig +
				"exec /usr/bin/docker-entrypoint.sh /bin/sh -c \"maxscale-start && monit -I\"",
		}
	}

	initCmd, initEnv = k8sMaxscaleBootstrapCommand(cluster, prx)
	return
}

// k8sProxysqlContainerSpec fills in ProxySQL's persistent-storage mounts and
// startup command on container, and returns the bootstrap init container's
// command/env. See k8sProxyDeployment.
func k8sProxysqlContainerSpec(cluster *Cluster, prx DatabaseProxy, container *apiv1.Container, volumeName string) (mounts []apiv1.VolumeMount, initCmd []string, initEnv []apiv1.EnvVar) {
	mounts = []apiv1.VolumeMount{
		{
			Name:      volumeName,
			MountPath: "/var/lib/proxysql",
		},
		{
			Name:      volumeName,
			MountPath: "/etc/proxysql",
			SubPath:   k8sProxyConfPersistSubPath,
		},
		{
			Name:      volumeName,
			MountPath: "/etc/ssl",
			SubPath:   k8sProxySSLPersistSubPath,
		},
	}
	// --initial re-derives ProxySQL's SQLite admin database from
	// proxysql.cnf on every start, so a stale proxysql.db from a
	// previous boot never takes precedence over the fetched config.
	container.Command = []string{
		"sh", "-c",
		k8sDockerParityUlimitShellPrefix() +
			"exec proxysql --initial -f -c /etc/proxysql/proxysql.cnf",
	}
	initCmd, initEnv = k8sProxyBootstrapCommand(cluster, prx)
	return
}

// k8sHaproxyContainerSpec fills in HAProxy's persistent-storage mounts and
// startup command on container, and returns the bootstrap init container's
// command/env. See k8sProxyDeployment.
func k8sHaproxyContainerSpec(cluster *Cluster, prx DatabaseProxy, container *apiv1.Container, volumeName string) (mounts []apiv1.VolumeMount, initCmd []string, initEnv []apiv1.EnvVar) {
	mounts = []apiv1.VolumeMount{
		{
			Name:      volumeName,
			MountPath: "/usr/local/etc/haproxy",
			SubPath:   k8sHaproxyConfPersistSubPath,
		},
	}
	// externalcheck needs an explicit Command (checkmaster/checkslave
	// copied to /usr/bin, "-db" passed explicitly) and RunAsUser=0 (the
	// Debian haproxy:<tag> image drops to uid 99, unlike
	// haproxytech/haproxy-alpine's root default) -- see
	// doc/implementation/cluster/KUBERNETES_PROVISIONING.md. standby
	// and runtimeapi both fall through to the image's default
	// entrypoint against the fetched haproxy.cfg.
	if cluster.Conf.HaproxyMode == "externalcheck" {
		runAsRoot := int64(0)
		container.SecurityContext = &apiv1.SecurityContext{RunAsUser: &runAsRoot}
		container.Command = []string{
			"sh", "-c",
			"cp /usr/local/etc/haproxy/checkmaster /usr/bin/checkmaster 2>/dev/null; " +
				"cp /usr/local/etc/haproxy/checkslave /usr/bin/checkslave 2>/dev/null; " +
				"chmod +x /usr/bin/checkmaster /usr/bin/checkslave 2>/dev/null; " +
				k8sDockerParityUlimitShellPrefix() +
				"exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg",
		}
	}
	initCmd, initEnv = k8sHaproxyBootstrapCommand(cluster, prx)
	return
}

// k8sProxyDeployment is a pure builder, returning before any Kubernetes
// object is touched when the proxy type isn't supported.
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

	if k8sProxyTypeHasPersistentStorage(prx.GetType()) {
		volumeName := k8sProxyVolumeName(prx.GetName())
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

		var mounts []apiv1.VolumeMount
		var initCmd []string
		var initEnv []apiv1.EnvVar

		switch prx.GetType() {
		case config.ConstProxySqlproxy:
			mounts, initCmd, initEnv = k8sProxysqlContainerSpec(cluster, prx, &container, volumeName)
		case config.ConstProxyHaproxy:
			mounts, initCmd, initEnv = k8sHaproxyContainerSpec(cluster, prx, &container, volumeName)
		case config.ConstProxyMaxscale:
			mounts, initCmd, initEnv = k8sMaxscaleContainerSpec(cluster, prx, &container, volumeName)
		}

		container.VolumeMounts = mounts
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
			Name:   k8sProxyDeploymentName(cluster.Name, prx.GetName()),
			Labels: map[string]string{k8sProxyTypeLabel: prx.GetType()},
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

func (cluster *Cluster) k8sProxyService(prx DatabaseProxy) (*apiv1.Service, error) {
	ports, err := k8sProxyServicePorts(prx)
	if err != nil {
		return nil, err
	}
	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   k8sProxyServiceName(prx),
			Labels: map[string]string{k8sProxyTypeLabel: prx.GetType()},
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

// k8sWarnIfLegacyProxyDeploymentExists never deletes, only warns: its
// selector also label-matches new per-proxy Deployments' pods, so no
// single-proxy operation can prove it's safe to remove.
func (cluster *Cluster) k8sWarnIfLegacyProxyDeploymentExists(client kubernetes.Interface) {
	name := k8sLegacyProxyDeploymentName(cluster.Name)
	if _, err := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), name, metav1.GetOptions{}); err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Legacy Kubernetes proxy deployment %s still exists alongside per-proxy deployments — see doc/implementation/cluster/KUBERNETES_PROVISIONING.md for manual migration steps", name)
	}
}

// k8sProvisionProxyServiceWithClient creates the per-proxy Deployment and
// Service, and a PVC plus auth-header Secret for types with persistent
// storage. Ensures the Namespace itself (k8sEnsureNamespace, prov_k8s_db.go)
// rather than assuming DB provisioning already created it -- a proxy can be
// provisioned directly (handlerMuxProxyProvision, server/api_proxy.go)
// without a DB ever having been provisioned in this cluster/namespace, and
// OpenSVCProvisionProxyService (prov_opensvc_prx.go) is the parity
// reference: it never assumes DB provisioning ran either, it creates its own
// service and maps (OpenSVCCreateMaps) unconditionally.
//
// Also guards against a name already owned by a different proxy type
// (k8sProxyNameOwnedByDifferentType) before touching anything -- AddProxy
// (cluster.go) intentionally allows two different-type proxies to share a
// name (valid outside Kubernetes), so this is the point that actually knows
// the Deployment/Service names collide and must fail loudly instead of
// treating the second proxy's Create() calls as an idempotent AlreadyExists.
//
// Ordering: name validation, then the read-only collision checks and legacy
// warning, then the pure k8sProxyDeployment builder -- deliberately before
// k8sEnsureNamespace and any Create() call. k8sProxyDeployment is where a
// proxy-type-specific configuration error surfaces (e.g. MaxScale's
// pinloki-without-REST combination, k8sProxyPortSpecs), so failing there
// first means an invalid configuration creates no Namespace, PVC,
// Deployment, or Service, rather than leaving a half-provisioned proxy
// behind.
func (cluster *Cluster) k8sProvisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	if errs := k8sProxyServiceNameErrs(prx); len(errs) > 0 {
		err := fmt.Errorf(clusterError["ERR00110"], prx.GetName(), strings.Join(errs, "; "))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		if cluster.StateMachine != nil {
			cluster.StateMachine.AddState("ERR00110", state.State{ErrType: "ERROR", ErrDesc: err.Error(), ErrFrom: "PROXY", ServerUrl: prx.GetName()})
		}
		return err
	}

	collision, err := cluster.k8sProxyNameOwnedByDifferentType(client, prx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot check for a Kubernetes proxy name collision: %s ", err)
		return err
	}
	if collision != nil {
		err := fmt.Errorf(clusterError["ERR00109"], prx.GetType(), prx.GetName(), collision.ObjectKind, collision.ObjectName, collision.OwnerType, collision.RecoveryHint(cluster.Name))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		if cluster.StateMachine != nil {
			cluster.StateMachine.AddState("ERR00109", state.State{ErrType: "ERROR", ErrDesc: err.Error(), ErrFrom: "PROXY", ServerUrl: prx.GetName()})
		}
		return err
	}

	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)

	deployment, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot build Kubernetes proxy deployment: %s ", err)
		return err
	}

	cluster.k8sEnsureNamespace(client, cluster.Name)

	if k8sProxyTypeHasPersistentStorage(prx.GetType()) {
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
