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

var k8sSupportedProxyTypes = []string{config.ConstProxySqlproxy, config.ConstProxyHaproxy, config.ConstProxyMaxscale}

func k8sUnsupportedProxyTypeErr(proxyType string) error {
	return fmt.Errorf("Kubernetes proxy provisioning does not support proxy type %q yet (only %q are implemented)", proxyType, k8sSupportedProxyTypes)
}

// k8sMaxscaleAdminPort is whichever port repman's own client actually
// connects on: MxsRestPort under maxscale-rest-api (default), MxsPort (the
// MaxAdmin "cli" listener) otherwise.
func k8sMaxscaleAdminPort(prx DatabaseProxy) (int, error) {
	conf := prx.GetCluster().Conf
	if conf.MxsRestApi {
		return conf.MxsRestPort, nil
	}
	adminPort, err := strconv.Atoi(prx.GetPort())
	if err != nil {
		return 0, fmt.Errorf("invalid MaxScale admin port %q: %s", prx.GetPort(), err)
	}
	return adminPort, nil
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

// HaproxyStatPort isn't carried on the DatabaseProxy interface, so it's read
// via prx.GetCluster().Conf instead.
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
	case config.ConstProxyHaproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid HAProxy runtime API port %q: %s", prx.GetPort(), err)
		}
		return []apiv1.ContainerPort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetWritePort())},
			{Name: "read", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetReadPort())},
			{Name: "stat", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetCluster().Conf.HaproxyStatPort)},
		}, nil
	case config.ConstProxyMaxscale:
		// "admin" is whichever port repman's own client actually connects
		// on -- MxsRestPort (REST API) when maxscale-rest-api is on
		// (default), MxsPort (the MaxAdmin "cli" listener) when off. No
		// "read": no listener by default. "binlog" is unconditional: the
		// binlogrouter service is always generated regardless of
		// MxsBinlogOn. "maxinfo" is exposed only when repman's own client
		// is actually configured to use it (maxscale-get-info-method).
		adminPort, err := k8sMaxscaleAdminPort(prx)
		if err != nil {
			return nil, err
		}
		ports := []apiv1.ContainerPort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetWritePort())},
			{Name: "rw-split", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetReadWritePort())},
			{Name: "binlog", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetCluster().Conf.MxsBinlogPort)},
		}
		if prx.GetCluster().Conf.MxsGetInfoMethod == "maxinfo" {
			ports = append(ports, apiv1.ContainerPort{Name: "maxinfo", Protocol: apiv1.ProtocolTCP, ContainerPort: int32(prx.GetCluster().Conf.MxsMaxinfoPort)})
		}
		return ports, nil
	default:
		return nil, k8sUnsupportedProxyTypeErr(prx.GetType())
	}
}

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
	case config.ConstProxyHaproxy:
		adminPort, err := strconv.Atoi(prx.GetPort())
		if err != nil {
			return nil, fmt.Errorf("invalid HAProxy runtime API port %q: %s", prx.GetPort(), err)
		}
		return []apiv1.ServicePort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
			{Name: "read", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetReadPort())},
			{Name: "stat", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetCluster().Conf.HaproxyStatPort)},
		}, nil
	case config.ConstProxyMaxscale:
		// See k8sProxyContainerPorts.
		adminPort, err := k8sMaxscaleAdminPort(prx)
		if err != nil {
			return nil, err
		}
		ports := []apiv1.ServicePort{
			{Name: "admin", Protocol: apiv1.ProtocolTCP, Port: int32(adminPort)},
			{Name: "write", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetWritePort())},
			{Name: "rw-split", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetReadWritePort())},
			{Name: "binlog", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetCluster().Conf.MxsBinlogPort)},
		}
		if prx.GetCluster().Conf.MxsGetInfoMethod == "maxinfo" {
			ports = append(ports, apiv1.ServicePort{Name: "maxinfo", Protocol: apiv1.ProtocolTCP, Port: int32(prx.GetCluster().Conf.MxsMaxinfoPort)})
		}
		return ports, nil
	default:
		return nil, k8sUnsupportedProxyTypeErr(prx.GetType())
	}
}

// k8sProxyTypeHasPersistentStorage reports whether prx's type gets a PVC, a
// bootstrap init container, and a custom startup command.
func k8sProxyTypeHasPersistentStorage(proxyType string) bool {
	return proxyType == config.ConstProxySqlproxy || proxyType == config.ConstProxyHaproxy || proxyType == config.ConstProxyMaxscale
}

func k8sProxyPVCName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-claim"
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
// mounted; maxscale.cnf lands at k8sMaxscaleConfPersistDir + "/maxscale.cnf".
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
// not at /usr/bin -- for haproxy-mode=standby, k8sProxyDeployment's
// container.Command copies them into /usr/bin from there before exec'ing
// haproxy, since Kubernetes has no safe subPath mount to a single file that
// doesn't already exist in the image.
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

// k8sMaxscaleBootstrapCommand fetches maxscale.cnf into
// k8sMaxscaleConfPersistDir. No SSL copy step: the generated config never
// references the ssl/*.pem files GenerateProxyConfig stages for every proxy
// family, so mounting them here would be dead weight.
func k8sMaxscaleBootstrapCommand(cluster *Cluster, prx DatabaseProxy) ([]string, []apiv1.EnvVar) {
	needFetchCmd, remoteFetchCmd, initEnv := k8sProxyFetchConfigCmds(cluster, prx)

	sourceFile := "maxscale.cnf"
	if cluster.MaxscaleUsesPinloki() {
		sourceFile = "maxscale-pinloki.cnf"
	}

	applyConfig := "if " + needFetchCmd + " 2>/dev/null; then " +
		"if " + remoteFetchCmd + " 2>/dev/null; then " +
		"if tar xzf /tmp/config.tar.gz -C /tmp/cfg 2>/dev/null; then " +
		"cp /tmp/cfg/etc/maxscale/" + sourceFile + " " + k8sMaxscaleConfPersistDir + "/maxscale.cnf 2>/dev/null; " +
		"fi; fi; fi"

	cmd := []string{
		"sh", "-c",
		"mkdir -p /tmp/cfg " + k8sMaxscaleConfPersistDir + " /var/cache/maxscale ; MKDIR_STATUS=$? ; " +
			applyConfig +
			" ; exit \"$MKDIR_STATUS\"",
	}
	return cmd, initEnv
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

		var mounts []apiv1.VolumeMount
		var initCmd []string
		var initEnv []apiv1.EnvVar

		switch prx.GetType() {
		case config.ConstProxySqlproxy:
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
		case config.ConstProxyHaproxy:
			mounts = []apiv1.VolumeMount{
				{
					Name:      volumeName,
					MountPath: "/usr/local/etc/haproxy",
					SubPath:   k8sHaproxyConfPersistSubPath,
				},
			}
			// standby needs an explicit Command (checkmaster/checkslave copied
			// to /usr/bin, "-db" passed explicitly) and RunAsUser=0 (the
			// Debian haproxy:<tag> image drops to uid 99, unlike
			// haproxytech/haproxy-alpine's root default) -- see
			// doc/implementation/cluster/KUBERNETES_PROVISIONING.md.
			if cluster.Conf.HaproxyMode == "standby" {
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
		case config.ConstProxyMaxscale:
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
			// The rsyslogd workaround is pinloki-only: a live-confirmed bug
			// in mariadb/maxscale:23.08's entrypoint (bare "rsyslogd" never
			// daemonizes, hanging startup before maxscale-start ever runs;
			// not needed since MaxScale logs via maxlog, not syslog) that an
			// older image may not share.
			if cluster.MaxscaleUsesPinloki() {
				container.Command = []string{
					"sh", "-c",
					"cp " + k8sMaxscaleConfPersistDir + "/maxscale.cnf /etc/maxscale.cnf 2>/dev/null; " +
						"rm -f /var/run/*.pid; " +
						"chown -R maxscale:maxscale /var/lib/maxscale; " +
						"trap '/usr/bin/monit unmonitor all; /usr/bin/maxscale-stop; /usr/bin/monit quit' TERM; " +
						"maxscale-start && monit -I & " +
						"wait",
				}
			} else {
				container.Command = []string{
					"sh", "-c",
					"cp " + k8sMaxscaleConfPersistDir + "/maxscale.cnf /etc/maxscale.cnf 2>/dev/null; " +
						"exec /usr/bin/docker-entrypoint.sh /bin/sh -c \"maxscale-start && monit -I\"",
				}
			}
			initCmd, initEnv = k8sMaxscaleBootstrapCommand(cluster, prx)
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
// storage. No Namespace ensure: relies on one already existing from DB
// provisioning.
func (cluster *Cluster) k8sProvisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)

	deployment, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot build Kubernetes proxy deployment: %s ", err)
		return err
	}

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
