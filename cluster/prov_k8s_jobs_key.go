// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// The cluster's jobs key on Kubernetes: what the OpenSVC services already do (doc/implementation/security/JOB_LOGIN.md).
// The key is derived from replication-manager's persistent key (GetJobsKey) and never stored; it is delivered in the
// cluster's Secret under the same name as OpenSVC's env secret, and read by the database init container and the dbjobs
// sidecar only, from a file (not an environment variable: frozen at start, shown by `kubectl describe pod`).
const (
	// k8sSecretKeyJobsKey is the key of the cluster's Secret (k8sClusterSecretName) that holds the jobs key; the
	// monitor repairs it with the other init credentials (initCredentials) after a key rotation or a lost Secret.
	k8sSecretKeyJobsKey = "JOBS_API_KEY"
	// k8sJobsKeyVolumeName is the Pod volume that projects that one key, and nothing else of the Secret.
	k8sJobsKeyVolumeName = "jobs-key"
	// k8sJobsKeyDir is where the CLI looks for the key when $JOBS_API_KEY is empty (jobapi.JobsKeyFromEnv).
	k8sJobsKeyDir = "/credentials"
	// k8sInitCLIPath is where the init container keeps the CLI it logs in with.
	k8sInitCLIPath = "/tmp/replication-manager-cli"
)

// k8sJobsKeyInUse says whether the database Deployment maps the jobs key: as OpenSVC, when replication-manager has a
// persistent key. Without one the Deployment is what it was before (the init container fetches its config with the
// admin login and the jobs log in with the database password).
func (cluster *Cluster) k8sJobsKeyInUse() bool {
	return cluster.GetJobsKey() != ""
}

// k8sPublishJobsKey writes the jobs key into the cluster's Secret before a Deployment maps it, so that no Pod is ever
// created with a volume item the Secret does not hold. Through the Secret's store writer: an unchanged value is not
// written again and a changed one keeps the superseded value in JOBS_API_KEY_OLD. The key is derived, so it is not
// reported to the credential stores. Nothing without a persistent key. The value is never logged (F10).
func (cluster *Cluster) k8sPublishJobsKey(client kubernetes.Interface) error {
	key := cluster.GetJobsKey()
	if key == "" {
		return nil
	}
	if _, err := cluster.k8sStore().writeWith(context.TODO(), client, []credstore.StoreItem{{
		Entry: orchestratorStoreEntry, Key: k8sSecretKeyJobsKey, Version: cluster.selfVersion(key),
	}}, cluster.storeTimeout()); err != nil {
		return fmt.Errorf("publish the jobs key: %w", err)
	}
	return nil
}

// k8sJobsKeyInitParts are the pieces of the database init container that change when the jobs key is mapped.
type k8sJobsKeyInitParts struct {
	// prep gets the CLI before the authenticated fetch: the public copy of the running replication-manager, else the one
	// a previous start left on the volume. It follows a " ; " and ends with one.
	prep string
	// need, fetch replace the admin-login need-config-fetch and config download: `replication-manager-cli job config`
	// asks need-config-fetch, logs in with the jobs key (a token scoped to this server) and writes the archive.
	need, fetch string
	// tail keeps the CLI on the volume for the dbjobs container and the next start.
	tail string
	// env names the server to the CLI (no credential).
	env []apiv1.EnvVar
}

// k8sJobsKeyInit builds the init container's jobs-key fetch, following OpenSVC's bootstrap
// (share/dashboard/static/configurator/opensvc/bootstrap): the CLI is fetched first from the public /static route
// and must run (`version`), an unusable download leaves the previous copy, and the config is fetched with
// `replication-manager-cli job config`. serverPath and baseRef are the values the admin-login fetch uses.
// The CLI's own messages reach the container log (the surrounding `2>/dev/null` of the fetch would hide why a login
// was refused); they carry no key.
func (cluster *Cluster) k8sJobsKeyInit(s *ServerMonitor, serverPath, noCheckCert, baseRef string) k8sJobsKeyInitParts {
	cli := k8sInitCLIPath
	cached := "/docker-entrypoint-initdb.d/replication-manager-cli"
	return k8sJobsKeyInitParts{
		prep: "if wget" + noCheckCert + " -T 8 -qO " + cli + ".new " + baseRef + "/static/configurator/bin/replication-manager-cli 2>/dev/null" +
			" && chmod +x " + cli + ".new && " + cli + ".new version >/dev/null 2>&1 ; then mv " + cli + ".new " + cli +
			" ; else rm -f " + cli + ".new ; [ -x " + cached + " ] && cp " + cached + " " + cli + " 2>/dev/null ; fi ; ",
		need:  "[ -x " + cli + " ]",
		fetch: "{ REPLICATION_MANAGER_URL=" + baseRef + " " + cli + " job config /tmp/config.tar.gz 2>&3 ; } 3>&2",
		tail:  " ; [ -x " + cli + " ] && cp " + cli + " " + cached + " 2>/dev/null",
		env: []apiv1.EnvVar{
			{Name: "REPLICATION_MANAGER_CLUSTER_NAME", Value: cluster.Name},
			{Name: "REPLICATION_MANAGER_HOST_NAME", Value: serverPath},
			{Name: "REPLICATION_MANAGER_HOST_PORT", Value: s.Port},
		},
	}
}

// k8sJobsKeyVolume projects the jobs key alone from the cluster's Secret: the database password and the admin login of
// the same Secret are not in the volume. A volume, not a subPath mount, so that the kubelet refreshes the file when the
// key is written again.
//
// The file is 0400 and belongs to root: restricted access comparable to OpenSVC's jobs volume (owner 99, mode 600,
// directory 700; OpenSVCGetJobsVolumeSecret), not identical to it (a Secret file cannot belong to uid 99). It is readable
// by root in the two containers that mount it. The dbjobs container runs as root only when the identity is managed
// (dbIdentityManaged), the same rule as OpenSVC's; otherwise it runs as the image's own USER, which then cannot read the
// file, and the shared CLI uses the database password without a message (JobsKeyFromEnv ignores a read error). Which
// users can read the file differs between the orchestrators, so this is not claimed to behave identically for every uid:
// not every DBJobs configuration authenticates with the jobs key, and the fallback is not fail-closed. No Pod-level
// fsGroup (it also applies to the data volume) and the jobs container is not forced to root.
//
// The mode does not give the cross-container isolation OpenSVC has when the Pod shares its process namespace
// (monitoring-system-resources, K8SProvisionDatabaseService), which OpenSVC does not do: the sharing predates the key,
// but the key is one more credential reachable through it. See doc/implementation/security/JOB_LOGIN.md.
func (cluster *Cluster) k8sJobsKeyVolume() apiv1.Volume {
	mode := int32(0400)
	return apiv1.Volume{
		Name: k8sJobsKeyVolumeName,
		VolumeSource: apiv1.VolumeSource{
			Secret: &apiv1.SecretVolumeSource{
				SecretName:  k8sClusterSecretName(cluster.Name),
				Items:       []apiv1.KeyToPath{{Key: k8sSecretKeyJobsKey, Path: k8sSecretKeyJobsKey}},
				DefaultMode: &mode,
			},
		},
	}
}

// k8sAddJobsKey maps the jobs key into the two containers that log in to replication-manager as the jobs, the init
// container and the dbjobs sidecar, and into no other (the database container never sees it). Called on the finished
// Deployment, as k8sAddXtrabackupBundle: a Deployment without the key is untouched.
func (cluster *Cluster) k8sAddJobsKey(dep *appsv1.Deployment, s *ServerMonitor) {
	if !cluster.k8sJobsKeyInUse() {
		return
	}
	spec := &dep.Spec.Template.Spec
	spec.Volumes = append(spec.Volumes, cluster.k8sJobsKeyVolume())
	mount := apiv1.VolumeMount{Name: k8sJobsKeyVolumeName, MountPath: k8sJobsKeyDir, ReadOnly: true}
	for i := range spec.InitContainers {
		if spec.InitContainers[i].Name == s.Name+"-init" {
			spec.InitContainers[i].VolumeMounts = append(spec.InitContainers[i].VolumeMounts, mount)
		}
	}
	for i := range spec.Containers {
		if spec.Containers[i].Name == s.Name+"-dbjobs" {
			spec.Containers[i].VolumeMounts = append(spec.Containers[i].VolumeMounts, mount)
		}
	}
}

// k8sInitUsesJobsKey says whether the init container of a database Deployment logs in with the jobs key: it maps the
// jobs-key volume and fetches its config with the CLI. It reads the Deployment itself: the cluster's persistent key does
// not say which authentication an existing Deployment uses (provisioning keeps one that exists, whatever it was rendered
// with, and only a Deployment rendered by this code maps the key).
func k8sInitUsesJobsKey(dep *appsv1.Deployment) bool {
	if dep == nil {
		return false
	}
	for _, c := range dep.Spec.Template.Spec.InitContainers {
		mounted := false
		for _, m := range c.VolumeMounts {
			mounted = mounted || m.Name == k8sJobsKeyVolumeName
		}
		if mounted && strings.Contains(strings.Join(c.Command, " "), k8sInitCLIPath+" job config") {
			return true
		}
	}
	return false
}

// k8sDeploymentInEffect is the Deployment whose init container will log in: the one Create returned, or, when Create
// reported that it already exists, the existing one. nil when it cannot be read.
func (cluster *Cluster) k8sDeploymentInEffect(client kubernetes.Interface, created *appsv1.Deployment, name string, alreadyExists bool) *appsv1.Deployment {
	if !alreadyExists {
		return created
	}
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	return dep
}

// k8sAdminGrantWarning is the warning that the admin login of the init container will be refused, or "". The admin login
// fetches the config only with the db-config-flag grant (api-credentials-secure-config): a Deployment that logs in with
// the jobs key does not need it, so it gets no warning; a Deployment that does not (a legacy one, or one rendered without
// a persistent key) keeps it. When the Deployment cannot be read the path is unknown and the warning is given as before.
func (cluster *Cluster) k8sAdminGrantWarning(s *ServerMonitor, dep *appsv1.Deployment) string {
	if !cluster.Conf.APISecureConfig || k8sInitUsesJobsKey(dep) {
		return ""
	}
	if u, ok := cluster.GetAPIUsers()["admin"]; ok && u.Grants != nil && u.Grants[config.GrantDBConfigFlag] {
		return ""
	}
	return fmt.Sprintf("api-credentials-secure-config is enabled but the admin user lacks the db-config-flag grant: the Kubernetes DB init-container config fetch will get HTTP 403, so %s will not bootstrap", s.Name)
}
