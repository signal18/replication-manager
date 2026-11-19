package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// The Kubernetes database init container and dbjobs sidecar log in with the cluster's jobs key, as the OpenSVC
// services do (doc/implementation/security/JOB_LOGIN.md): the Deployment maps the key only with a persistent key, the
// init container gets no admin login then, and the key reaches nobody else.

const jobsKeyTestSecretKey = "0123456789abcdef0123456789abcdef"

// jobsKeyTestCluster is a cluster whose init container logs in to replication-manager with the admin login when it has
// no jobs key (api-credentials-secure-config on), and with the jobs key once it has a persistent key.
func jobsKeyTestCluster(withKey bool) *Cluster {
	c := newTestCluster("k8stest")
	c.Conf.ProvDbImg = "mariadb:10.11"
	c.Conf.ApiServ = true
	c.Conf.MonitorAddress = "repman.s18.svc.cloud18"
	c.Conf.APIPort = "10005"
	c.Conf.APISecureConfig = true
	c.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	if withKey {
		c.Conf.SecretKey = []byte(jobsKeyTestSecretKey)
	}
	return c
}

func jobsKeyTestServer() *ServerMonitor { return &ServerMonitor{Name: "db1", Port: "3306"} }

func envNames(env []apiv1.EnvVar) []string {
	var names []string
	for _, e := range env {
		names = append(names, e.Name)
	}
	return names
}

// Without a persistent key nothing changes: the admin login of the init container, no jobs-key volume, no mount.
func TestK8SJobsKey_NoPersistentKeyKeepsTheDeploymentAsItWas(t *testing.T) {
	c := jobsKeyTestCluster(false)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	spec := dep.Spec.Template.Spec
	cmd := strings.Join(spec.InitContainers[0].Command, " ")

	if c.k8sJobsKeyInUse() {
		t.Fatal("a cluster without a persistent key must not use the jobs key")
	}
	if !strings.Contains(cmd, "Authorization: Basic $"+k8sSecretKeyAPIAuthHeader) || !strings.Contains(cmd, "/need-config-fetch") {
		t.Fatalf("the admin login must stay: %s", cmd)
	}
	if strings.Contains(cmd, " job config") || strings.Contains(cmd, "REPLICATION_MANAGER_URL=") {
		t.Fatalf("no jobs-key fetch without a key: %s", cmd)
	}
	for _, v := range spec.Volumes {
		if v.Name == k8sJobsKeyVolumeName || v.Secret != nil {
			t.Fatalf("no Secret volume without a key, got %+v", v)
		}
	}
	for _, ctr := range append(append([]apiv1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, m := range ctr.VolumeMounts {
			if m.MountPath == k8sJobsKeyDir {
				t.Fatalf("container %s mounts %s without a key", ctr.Name, k8sJobsKeyDir)
			}
		}
	}
	if got := envNames(spec.InitContainers[0].Env); len(got) != 1 || got[0] != k8sSecretKeyAPIAuthHeader {
		t.Fatalf("the admin login is the only env of the init container, got %v", got)
	}
}

// With the key: the init container fetches its config through `replication-manager-cli job config`, after the CLI,
// with no admin login, no need-config-fetch call of its own and no secret in its environment.
func TestK8SJobsKey_InitContainerFetchesTheConfigWithTheJobsKey(t *testing.T) {
	c := jobsKeyTestCluster(true)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	init := dep.Spec.Template.Spec.InitContainers[0]
	cmd := strings.Join(init.Command, " ")

	if !c.k8sJobsKeyInUse() {
		t.Fatal("a persistent key means the jobs key")
	}
	if !strings.Contains(cmd, k8sInitCLIPath+" job config /tmp/config.tar.gz") {
		t.Fatalf("the config is fetched with the CLI: %s", cmd)
	}
	if strings.Contains(cmd, "Authorization") || strings.Contains(cmd, "/need-config-fetch") || strings.Contains(cmd, "/servers/") {
		t.Fatalf("no admin login, no direct API call: %s", cmd)
	}
	if download, fetch := strings.Index(cmd, "/static/configurator/bin/replication-manager-cli"), strings.Index(cmd, "job config"); download < 0 || fetch < 0 || download > fetch {
		t.Fatalf("the CLI is downloaded before the authenticated fetch (%d, %d): %s", download, fetch, cmd)
	}
	if !strings.Contains(cmd, "REPLICATION_MANAGER_URL=https://repman.s18.svc.cloud18:10005 ") {
		t.Fatalf("the CLI is told the replication-manager URL: %s", cmd)
	}
	want := map[string]string{"REPLICATION_MANAGER_CLUSTER_NAME": "k8stest", "REPLICATION_MANAGER_HOST_NAME": "db1", "REPLICATION_MANAGER_HOST_PORT": "3306"}
	if len(init.Env) != len(want) {
		t.Fatalf("only the names of the server are in the environment, got %v", init.Env)
	}
	for _, e := range init.Env {
		if e.ValueFrom != nil || e.Value != want[e.Name] {
			t.Fatalf("env %s = %q (from %v), want %q", e.Name, e.Value, e.ValueFrom, want[e.Name])
		}
	}
	if !strings.Contains(cmd, "cp "+k8sInitCLIPath+" /docker-entrypoint-initdb.d/replication-manager-cli") {
		t.Fatalf("the CLI fetched is kept for the dbjobs container: %s", cmd)
	}
}

// The config server's own server name carries the domain with prov-net-cni, as the admin-login URL did.
func TestK8SJobsKey_InitContainerNamesTheServerLikeTheAdminLoginURL(t *testing.T) {
	c := jobsKeyTestCluster(true)
	c.Conf.ProvNetCNI = true
	c.Conf.ProvOrchestratorCluster = "cluster.local"
	s := jobsKeyTestServer()
	withKey := c.k8sDatabaseDeployment(s, 3306, "node-a").Spec.Template.Spec.InitContainers[0].Env
	c.Conf.SecretKey = nil
	adminURL := strings.Join(c.k8sDatabaseDeployment(s, 3306, "node-a").Spec.Template.Spec.InitContainers[0].Command, " ")
	var host string
	for _, e := range withKey {
		if e.Name == "REPLICATION_MANAGER_HOST_NAME" {
			host = e.Value
		}
	}
	if host == "" || !strings.Contains(adminURL, "/servers/"+host+"/3306/config") {
		t.Fatalf("the jobs login names %q, the admin-login URL is %s", host, adminURL)
	}
}

// No credential is written in the Deployment: not the jobs key, not the admin password, not its Basic Auth value.
func TestK8SJobsKey_NoSecretInTheDeployment(t *testing.T) {
	c := jobsKeyTestCluster(true)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	b, err := json.Marshal(dep)
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range map[string]string{
		"jobs key":        c.GetJobsKey(),
		"admin password":  "s3cr3t",
		"admin Basic":     base64.StdEncoding.EncodeToString([]byte("admin:s3cr3t")),
		"persistent key":  jobsKeyTestSecretKey,
		"auth header key": k8sSecretKeyAPIAuthHeader,
	} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("the Deployment contains the %s", name)
		}
	}
	if c.GetJobsKey() == "" {
		t.Fatal("the test key is empty")
	}
}

// The volume projects the jobs key alone (not the database password or the admin login of the same Secret), 0400 and
// root-owned: restricted access comparable to, not identical to, OpenSVC's mode 600 / directory 700 / owner 99. A dbjobs
// container that runs as a non-root image user (identity not managed) cannot read it and the shared CLI then uses the
// database password: not every configuration authenticates with the key, and the mode is no isolation between containers
// that share a process namespace.
func TestK8SJobsKey_VolumeProjectsOnlyTheKeyReadableByRootOnly(t *testing.T) {
	c := jobsKeyTestCluster(true)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	var vol *apiv1.Volume
	for i, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == k8sJobsKeyVolumeName {
			vol = &dep.Spec.Template.Spec.Volumes[i]
		}
	}
	if vol == nil || vol.Secret == nil {
		t.Fatal("expected a Secret volume for the jobs key")
	}
	if vol.Secret.SecretName != k8sClusterSecretName("k8stest") {
		t.Fatalf("the key is read from the cluster's Secret, got %q", vol.Secret.SecretName)
	}
	if len(vol.Secret.Items) != 1 || vol.Secret.Items[0].Key != "JOBS_API_KEY" || vol.Secret.Items[0].Path != "JOBS_API_KEY" {
		t.Fatalf("only JOBS_API_KEY is projected, got %+v", vol.Secret.Items)
	}
	if vol.Secret.DefaultMode == nil || *vol.Secret.DefaultMode != 0400 {
		t.Fatalf("the file is 0400 (as OpenSVC's 600), got %v", vol.Secret.DefaultMode)
	}
	if vol.Secret.Optional != nil && *vol.Secret.Optional {
		t.Fatal("a Pod must not start without the key it maps")
	}
}

// Only the init container and the dbjobs sidecar map the key, read-only, in a directory (so the kubelet refreshes it
// when the key is written again): never the database container.
func TestK8SJobsKey_OnlyInitAndDBJobsMountTheKey(t *testing.T) {
	c := jobsKeyTestCluster(true)
	dep := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a")
	spec := dep.Spec.Template.Spec
	mounted := map[string]bool{}
	for _, ctr := range append(append([]apiv1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, m := range ctr.VolumeMounts {
			if m.Name == k8sJobsKeyVolumeName {
				mounted[ctr.Name] = true
				if m.MountPath != k8sJobsKeyDir || !m.ReadOnly || m.SubPath != "" {
					t.Fatalf("%s: the key is a read-only directory mount at %s without subPath, got %+v", ctr.Name, k8sJobsKeyDir, m)
				}
			}
		}
	}
	if len(mounted) != 2 || !mounted["db1-init"] || !mounted["db1-dbjobs"] {
		t.Fatalf("only db1-init and db1-dbjobs map the key, got %v", mounted)
	}
	// the jobs still need the database password for the database itself: unchanged
	for _, ctr := range spec.Containers {
		if ctr.Name == "db1-dbjobs" {
			found := false
			for _, e := range ctr.Env {
				found = found || e.Name == k8sSecretKeyRootPassword
			}
			if !found {
				t.Fatal("the dbjobs sidecar keeps MYSQL_ROOT_PASSWORD for the database connection")
			}
		}
	}
}

// With the key the commands are valid shell in the variants the init container is built in: the pair's URLs
// ($B), api-server off (plain HTTP), prov-net-cni.
func TestK8SJobsKey_InitCommandIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for name, setup := range map[string]func(*Cluster){
		"default": func(c *Cluster) {},
		"dr": func(c *Cluster) {
			c.SetBootstrapDRURL("https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005")
		},
		"api off":     func(c *Cluster) { c.Conf.ApiServ = false; c.Conf.HttpPort = "10001" },
		"cni":         func(c *Cluster) { c.Conf.ProvNetCNI = true; c.Conf.ProvOrchestratorCluster = "cluster.local" },
		"secure off":  func(c *Cluster) { c.Conf.APISecureConfig = false },
		"owner":       func(c *Cluster) { c.Conf.ProvDBVolumeUID = "1001:1001" },
		"no key left": func(c *Cluster) { c.Conf.SecretKey = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := jobsKeyTestCluster(true)
			setup(c)
			cmd := c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a").Spec.Template.Spec.InitContainers[0].Command
			if out, err := exec.Command(sh, "-n", "-c", cmd[2]).CombinedOutput(); err != nil {
				t.Fatalf("not valid shell: %v\n%s\n%s", err, out, cmd[2])
			}
		})
	}
}

// With the DR pair the CLI fetch and the CLI itself use the URL the init container picked.
func TestK8SJobsKey_UsesTheURLPickedAmongThePair(t *testing.T) {
	c := jobsKeyTestCluster(true)
	c.SetBootstrapDRURL("https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005")
	cmd := strings.Join(c.k8sDatabaseDeployment(jobsKeyTestServer(), 3306, "node-a").Spec.Template.Spec.InitContainers[0].Command, " ")
	if !strings.Contains(cmd, "$B/static/configurator/bin/replication-manager-cli") || !strings.Contains(cmd, "REPLICATION_MANAGER_URL=$B ") {
		t.Fatalf("the CLI and its login use the picked URL: %s", cmd)
	}
}

// The key is in the Secret before a Deployment maps it; the other keys are left alone, a value that did not change is
// not written again, a changed one keeps the superseded copy.
func TestK8SJobsKey_PublishedBeforeTheDeploymentMapsIt(t *testing.T) {
	name := k8sClusterSecretName("k8stest")
	client := fake.NewSimpleClientset(&apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"},
		Data:       map[string][]byte{k8sSecretKeyRootPassword: []byte("dbpass")},
	})
	c := jobsKeyTestCluster(true)

	if err := c.k8sPublishJobsKey(client); err != nil {
		t.Fatal(err)
	}
	sec, _ := client.CoreV1().Secrets("k8stest").Get(context.TODO(), name, metav1.GetOptions{})
	if sec.StringData[k8sSecretKeyJobsKey] != c.GetJobsKey() || sec.StringData[k8sSecretKeyJobsKey] == "" {
		t.Fatalf("the Secret holds the cluster's jobs key, got %q", sec.StringData[k8sSecretKeyJobsKey])
	}
	if string(sec.Data[k8sSecretKeyRootPassword]) != "dbpass" {
		t.Fatal("the other keys of the Secret are left alone")
	}

	// the API server serves what was written as Data (the fake keeps StringData)
	sec.Data = map[string][]byte{k8sSecretKeyRootPassword: []byte("dbpass"), k8sSecretKeyJobsKey: []byte(sec.StringData[k8sSecretKeyJobsKey])}
	if _, err := client.CoreV1().Secrets("k8stest").Update(context.TODO(), sec, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	patches := func() int {
		n := 0
		for _, a := range client.Actions() {
			if a.GetVerb() == "patch" {
				n++
			}
		}
		return n
	}
	before := patches()
	if err := c.k8sPublishJobsKey(client); err != nil {
		t.Fatal(err)
	}
	if patches() != before {
		t.Fatal("an unchanged key is not written again")
	}

	// the key rotated
	old := c.GetJobsKey()
	c.Conf.SecretKey = []byte("fedcba9876543210fedcba9876543210")
	if err := c.k8sPublishJobsKey(client); err != nil {
		t.Fatal(err)
	}
	sec, _ = client.CoreV1().Secrets("k8stest").Get(context.TODO(), name, metav1.GetOptions{})
	if sec.StringData[k8sSecretKeyJobsKey] != c.GetJobsKey() || sec.StringData[k8sSecretKeyJobsKey+oldKeySuffix] != old {
		t.Fatalf("the new key is published and the superseded one kept, got %v", sec.StringData)
	}
}

// No persistent key: nothing is published, and no Secret is created for it.
func TestK8SJobsKey_NothingPublishedWithoutAPersistentKey(t *testing.T) {
	client := fake.NewSimpleClientset()
	c := jobsKeyTestCluster(false)
	if err := c.k8sPublishJobsKey(client); err != nil {
		t.Fatal(err)
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("no call without a key, got %v", client.Actions())
	}
}

// The monitor's repair of the init credentials already covers the Kubernetes Secret: a lost or changed jobs key is
// written again when the monitor becomes active.
func TestK8SJobsKey_RepairedWithTheOtherInitCredentials(t *testing.T) {
	c := jobsKeyTestCluster(true)
	c.Conf.ProvOrchestrator = "kube"
	var keys []string
	for _, ic := range c.initCredentials() {
		keys = append(keys, ic.key)
		if ic.key == k8sSecretKeyJobsKey && (ic.value != c.GetJobsKey() || ic.onlyIfPresent) {
			t.Fatalf("the jobs key is repaired to its current value even when missing, got %+v", ic)
		}
	}
	if strings.Join(keys, ",") != k8sSecretKeyAPIAuthHeader+","+k8sSecretKeyJobsKey {
		t.Fatalf("init credentials of a Kubernetes cluster: %v", keys)
	}
}

// The admin-login warning follows the Deployment that stands, not the cluster's persistent key.

// adminGrantCluster is a cluster whose admin lacks the db-config-flag grant, with api-credentials-secure-config on.
func adminGrantCluster(withKey bool) *Cluster {
	c := jobsKeyTestCluster(withKey)
	c.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t", Grants: map[string]bool{}}}
	return c
}

func TestK8SAdminGrantWarning_NewJobsKeyDeploymentHasNone(t *testing.T) {
	c := adminGrantCluster(true)
	s := jobsKeyTestServer()
	created := c.k8sDatabaseDeployment(s, 3306, "node-a")
	dep := c.k8sDeploymentInEffect(fake.NewSimpleClientset(), created, s.Name, false)
	if !k8sInitUsesJobsKey(dep) {
		t.Fatal("a Deployment rendered with the key logs in with the jobs key")
	}
	if msg := c.k8sAdminGrantWarning(s, dep); msg != "" {
		t.Fatalf("no admin-grant warning for a jobs-key Deployment, got %q", msg)
	}
}

func TestK8SAdminGrantWarning_DeploymentWithoutAPersistentKeyKeepsIt(t *testing.T) {
	c := adminGrantCluster(false)
	s := jobsKeyTestServer()
	dep := c.k8sDeploymentInEffect(fake.NewSimpleClientset(), c.k8sDatabaseDeployment(s, 3306, "node-a"), s.Name, false)
	if k8sInitUsesJobsKey(dep) {
		t.Fatal("no persistent key: the admin login")
	}
	if msg := c.k8sAdminGrantWarning(s, dep); !strings.Contains(msg, "db-config-flag") || !strings.Contains(msg, "db1 will not bootstrap") {
		t.Fatalf("the admin-grant warning stays for the admin login, got %q", msg)
	}
}

// Provisioning keeps a Deployment that exists: a legacy one, created before the cluster had a persistent key or by an
// older release, still logs in as the admin although this replication-manager now has the key and would render it
// differently. The warning follows the existing Deployment, fetched when Create reports AlreadyExists.
func TestK8SAdminGrantWarning_ExistingLegacyDeploymentKeepsItWhateverTheClusterKey(t *testing.T) {
	legacy := adminGrantCluster(false)
	s := jobsKeyTestServer()
	existing := legacy.k8sDatabaseDeployment(s, 3306, "node-a")
	client := fake.NewSimpleClientset()
	if _, err := client.AppsV1().Deployments("k8stest").Create(context.TODO(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	now := adminGrantCluster(true) // the same cluster, now with a persistent key
	if !now.k8sJobsKeyInUse() {
		t.Fatal("the cluster has a persistent key")
	}
	// what Create would have rendered now is not what stands
	if !k8sInitUsesJobsKey(now.k8sDatabaseDeployment(s, 3306, "node-a")) {
		t.Fatal("a new render maps the key")
	}
	dep := now.k8sDeploymentInEffect(client, nil, s.Name, true)
	if dep == nil || k8sInitUsesJobsKey(dep) {
		t.Fatalf("the existing Deployment is the legacy one, got %v", dep)
	}
	if msg := now.k8sAdminGrantWarning(s, dep); msg == "" {
		t.Fatal("the existing legacy Deployment keeps the admin-grant warning although the cluster has a key")
	}
}

func TestK8SAdminGrantWarning_ExistingJobsKeyDeploymentHasNone(t *testing.T) {
	c := adminGrantCluster(true)
	s := jobsKeyTestServer()
	client := fake.NewSimpleClientset()
	if _, err := client.AppsV1().Deployments("k8stest").Create(context.TODO(), c.k8sDatabaseDeployment(s, 3306, "node-a"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if msg := c.k8sAdminGrantWarning(s, c.k8sDeploymentInEffect(client, nil, s.Name, true)); msg != "" {
		t.Fatalf("an existing jobs-key Deployment needs no admin grant, got %q", msg)
	}
}

// An existing Deployment that cannot be read leaves the path unknown: the warning is given as it was.
func TestK8SAdminGrantWarning_UnreadableExistingDeploymentKeepsTheWarning(t *testing.T) {
	c := adminGrantCluster(true)
	s := jobsKeyTestServer()
	dep := c.k8sDeploymentInEffect(fake.NewSimpleClientset(), nil, s.Name, true)
	if dep != nil {
		t.Fatalf("nothing to read, got %v", dep)
	}
	if msg := c.k8sAdminGrantWarning(s, dep); msg == "" {
		t.Fatal("an unknown path keeps the warning")
	}
}

func TestK8SAdminGrantWarning_NoneWhenTheGrantIsThereOrSecureConfigIsOff(t *testing.T) {
	s := jobsKeyTestServer()
	legacy := adminGrantCluster(false)
	dep := legacy.k8sDatabaseDeployment(s, 3306, "node-a")
	legacy.APIUsers = map[string]APIUser{"admin": {User: "admin", Grants: map[string]bool{"db-config-flag": true}}}
	if msg := legacy.k8sAdminGrantWarning(s, dep); msg != "" {
		t.Fatalf("the admin holds the grant, got %q", msg)
	}
	off := adminGrantCluster(false)
	off.Conf.APISecureConfig = false
	if msg := off.k8sAdminGrantWarning(s, off.k8sDatabaseDeployment(s, 3306, "node-a")); msg != "" {
		t.Fatalf("api-credentials-secure-config is off, got %q", msg)
	}
}

// The provisioning gives the warning only through the Deployment that stands: the unconditional check at its start is gone.
func TestK8SAdminGrantWarning_ProvisioningUsesTheDeploymentInEffect(t *testing.T) {
	b, err := os.ReadFile("prov_k8s_db.go")
	if err != nil {
		t.Skip("source not readable")
	}
	src := string(b)
	i := strings.Index(src, "func (cluster *Cluster) K8SProvisionDatabaseService(")
	if i < 0 {
		t.Fatal("K8SProvisionDatabaseService not found")
	}
	body := src[i:]
	if strings.Contains(body, "lacks the db-config-flag grant") {
		t.Fatal("the message is built by k8sAdminGrantWarning, not inline at the start of the provisioning")
	}
	if !strings.Contains(body, "k8sAdminGrantWarning(s, cluster.k8sDeploymentInEffect(") {
		t.Fatal("the provisioning warns through the Deployment in effect")
	}
}
