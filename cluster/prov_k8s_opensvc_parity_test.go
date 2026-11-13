package cluster

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"testing"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Parity of the Kubernetes database memory limit with what OpenSVC renders for the same
// configuration (#2369). The reference is OpenSVC's container#db run_args:
// `--memory=<GetDBContainerMemoryCapMB>m --memory-swap=<same>m` when prov-db-docker-run-args-limit
// is on, nothing when it is off; and a jobs container that carries no memory option unless the
// operator adds one. Kubernetes must render the same number, from the same helper, at every
// stage that renders it.

var (
	dockerMemoryRe     = regexp.MustCompile(`--memory=(\d+)m`)
	dockerMemorySwapRe = regexp.MustCompile(`--memory-swap=(\d+)m`)
)

// lastDockerMiB returns the last `--memory=<n>m` (docker takes the last occurrence) of a run_args string.
func lastDockerMiB(t *testing.T, re *regexp.Regexp, runArgs string) (int, bool) {
	t.Helper()
	m := re.FindAllStringSubmatch(runArgs, -1)
	if len(m) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(m[len(m)-1][1])
	if err != nil {
		t.Fatalf("bad docker memory value %q", m[len(m)-1][1])
	}
	return n, true
}

// k8sParityCluster is a cluster of `nodes` identical servers at `dbu` DBU per node, with the
// ResourceManager wired (its default Database ratio: 1 DBU = 4096M) and the settings both
// orchestrators read.
func k8sParityCluster(dbu int, provMem, align string, nodes int, limitOn bool) (*Cluster, *ServerMonitor) {
	cl := &Cluster{Name: "k8sparity", Conf: &config.Config{
		ProvType:                 "docker",
		ProvDbDbu:                dbu,
		ProvMem:                  provMem,
		ProvCores:                "1",
		ProvIops:                 "300",
		ProvDisk:                 "20G",
		ProvDBResourceAlign:      align,
		ProvDBDockerRunArgsLimit: limitOn,
		ProvDBDockerTmpfsSize:    "256",
		ProvDBDockerRunArgs:      "--ulimit nofile=262144:262144",
		ProvDBJobsDockerRunArgs:  "--ulimit nofile=262144:262144",
	}, resources: NewResourceManager()}
	s := &ServerMonitor{Name: "db1", Port: "3306", ClusterGroup: cl}
	cl.Servers = []*ServerMonitor{s}
	for i := 1; i < nodes; i++ {
		cl.Servers = append(cl.Servers, &ServerMonitor{Name: "db" + strconv.Itoa(i+1), Port: "3306", ClusterGroup: cl})
	}
	return cl, s
}

// Every behaviorally distinct combination of DBU plan, prov-db-memory and alignment mode:
// the plan above the configured memory, the configured memory above the plan, and `up` / `off`
// (tier from the config, or no tier). wantMiB is the expected OpenSVC `--memory` for the default
// ResourceManager ratios (1 DBU = 4096M), stated so that a change of the helper is noticed.
func TestK8sDatabaseMemoryEqualsOpenSVCContainerMemory(t *testing.T) {
	cases := []struct {
		name    string
		dbu     int
		provMem string
		align   string
		wantMiB int
	}{
		{"1 DBU, 1G configured, plan", 1, "1024M", config.ConstResourceAlignPlan, 4096},
		{"1 DBU, 4G configured, plan", 1, "4096M", config.ConstResourceAlignPlan, 4096},
		{"2 DBU, 4G configured (plan above configured memory)", 2, "4096M", config.ConstResourceAlignPlan, 8192},
		{"2 DBU, 8G configured, plan", 2, "8192M", config.ConstResourceAlignPlan, 8192},
		{"4 DBU, 4G configured, plan", 4, "4096M", config.ConstResourceAlignPlan, 16384},
		{"1 DBU, 8G configured (configured above the plan)", 1, "8192M", config.ConstResourceAlignPlan, 8192},
		{"default alignment (empty = plan)", 2, "4096M", "", 8192},
		{"2 DBU, 4G configured, up (tier from the configured resources)", 2, "4096M", config.ConstResourceAlignUp, 4096},
		{"2 DBU, 4G configured, off (cap = prov-db-memory)", 2, "4096M", config.ConstResourceAlignOff, 4096},
	}
	for _, tc := range cases {
		for _, nodes := range []int{1, 3} {
			t.Run(tc.name+"/nodes="+strconv.Itoa(nodes), func(t *testing.T) {
				cl, s := k8sParityCluster(tc.dbu, tc.provMem, tc.align, nodes, true)

				// OpenSVC reference: the rendered container#db and container#jobs run_args.
				db := s.OpenSVCGetDBContainerSection()["run_args"]
				osMem, ok := lastDockerMiB(t, dockerMemoryRe, db)
				if !ok {
					t.Fatalf("OpenSVC renders no --memory on container#db with the limit on: %q", db)
				}
				if swap, _ := lastDockerMiB(t, dockerMemorySwapRe, db); swap != osMem {
					t.Fatalf("OpenSVC --memory-swap %d differs from --memory %d", swap, osMem)
				}
				if osMem != tc.wantMiB {
					t.Fatalf("fixture: OpenSVC --memory=%dm, want %dm", osMem, tc.wantMiB)
				}
				if _, jobs := lastDockerMiB(t, dockerMemoryRe, s.OpenSVCGetJobsContainerSection()["run_args"]); jobs {
					t.Fatal("OpenSVC renders a --memory on the jobs container by default")
				}

				// Kubernetes: the helper, the Deployment builder and the restart patch.
				if mb, ok := cl.k8sDatabaseMemoryMiB(); !ok || mb != osMem {
					t.Fatalf("k8sDatabaseMemoryMiB() = %d, %v; OpenSVC --memory = %d", mb, ok, osMem)
				}
				dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")
				want := resource.MustParse(strconv.Itoa(osMem) + "Mi")
				dbRes := dep.Spec.Template.Spec.Containers[0].Resources
				if got := dbRes.Limits[apiv1.ResourceMemory]; got.Cmp(want) != 0 {
					t.Fatalf("Deployment DB memory limit = %s, OpenSVC --memory = %dm", got.String(), osMem)
				}
				if got := dbRes.Requests[apiv1.ResourceMemory]; got.Cmp(want) != 0 {
					t.Fatalf("Deployment DB memory request = %s, want it equal to the limit %s", got.String(), want.String())
				}
				if jr := dep.Spec.Template.Spec.Containers[1].Resources; len(jr.Requests) != 0 || len(jr.Limits) != 0 {
					t.Fatalf("Deployment dbjobs resources = %#v, OpenSVC renders none", jr)
				}
				if mb := cl.GetDBContainerMemoryCapMB(); mb != osMem {
					t.Fatalf("GetDBContainerMemoryCapMB() = %d, OpenSVC --memory = %d", mb, osMem)
				}
			})
		}
	}
}

// With the limit off OpenSVC renders no --memory (and no docker cpus) on container#db, and
// Kubernetes sets no container resources: the flag-off layout is byte-identical to before.
func TestK8sDatabaseMemoryLimitOffRendersNothingLikeOpenSVC(t *testing.T) {
	cl, s := k8sParityCluster(2, "4096M", config.ConstResourceAlignPlan, 3, false)

	if _, ok := lastDockerMiB(t, dockerMemoryRe, s.OpenSVCGetDBContainerSection()["run_args"]); ok {
		t.Fatal("OpenSVC renders --memory on container#db with the limit off")
	}
	dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")
	for i, c := range dep.Spec.Template.Spec.Containers {
		if len(c.Resources.Requests) != 0 || len(c.Resources.Limits) != 0 {
			t.Fatalf("container %d (%s) carries resources with the limit off: %#v", i, c.Name, c.Resources)
		}
	}
	if patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1"); patch != nil {
		t.Fatalf("restart patch must be nil with the limit off, got %#v", patch)
	}
}

// The restart patch of a Deployment built before this change: DB to the cap, dbjobs memory
// request and limit deleted. Marshalled, the dbjobs fragment holds explicit nulls (a strategic
// merge removes a key set to null).
func TestK8sContainerMemoryResourcesPatchRendersTheOpenSVCContract(t *testing.T) {
	cl, s := k8sParityCluster(2, "4096M", config.ConstResourceAlignPlan, 3, true)
	dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")
	k8sGiveDBJobsLegacyMemory(dep) // what develop built: dbjobs request and limit 128Mi

	patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1")
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"name":"db1","resources":{"limits":{"memory":"8192Mi"},"requests":{"memory":"8192Mi"}}},` +
		`{"name":"db1-dbjobs","resources":{"limits":{"memory":null},"requests":{"memory":null}}}]`
	if string(raw) != want {
		t.Fatalf("restart patch\n got %s\nwant %s", raw, want)
	}
}

// k8sGiveDBJobsLegacyMemory gives the dbjobs container of dep the request and limit develop built.
func k8sGiveDBJobsLegacyMemory(dep *appsv1.Deployment) {
	q := resource.MustParse("128Mi")
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "db1-dbjobs" {
			dep.Spec.Template.Spec.Containers[i].Resources = apiv1.ResourceRequirements{
				Requests: apiv1.ResourceList{apiv1.ResourceMemory: q},
				Limits:   apiv1.ResourceList{apiv1.ResourceMemory: q},
			}
		}
	}
}

// With no legacy dbjobs resources in the template nothing is sent for dbjobs: the patch (and so
// the rollout it triggers) holds the database entry only, not a no-op null.
func TestK8sContainerMemoryResourcesPatchSendsNothingForCleanDBJobs(t *testing.T) {
	cl, s := k8sParityCluster(2, "4096M", config.ConstResourceAlignPlan, 3, true)
	dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")

	patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1")
	if len(patch) != 1 || patch[0]["name"] != "db1" {
		t.Fatalf("expected the database entry only, got %#v", patch)
	}
}

// Flag off: the database memory is not handled (no entry for it), but a legacy dbjobs request and
// limit are still removed, as OpenSVC's jobs container carries none in either mode.
func TestK8sContainerMemoryResourcesPatchFlagOffOnlyCleansDBJobs(t *testing.T) {
	cl, s := k8sParityCluster(2, "4096M", config.ConstResourceAlignPlan, 3, false)
	dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")
	if patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1"); patch != nil {
		t.Fatalf("flag off with a clean template: want nil, got %#v", patch)
	}

	k8sGiveDBJobsLegacyMemory(dep)
	patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1")
	if len(patch) != 1 || patch[0]["name"] != "db1-dbjobs" {
		t.Fatalf("flag off with legacy dbjobs: want the dbjobs clean-up only, got %#v", patch)
	}
}

// Without a dbjobs container in the Deployment only the DB entry is patched (a patch naming
// a container the Deployment lacks would conjure an incomplete one).
func TestK8sContainerMemoryResourcesPatchWithoutDBJobsContainer(t *testing.T) {
	cl, s := k8sParityCluster(2, "4096M", config.ConstResourceAlignPlan, 3, true)
	dep := cl.k8sDatabaseDeployment(s, 3306, "node-a")
	dep.Spec.Template.Spec.Containers = dep.Spec.Template.Spec.Containers[:1]

	patch := cl.k8sContainerMemoryResourcesPatch(dep, "db1")
	if len(patch) != 1 || patch[0]["name"] != "db1" {
		t.Fatalf("expected a single DB entry, got %#v", patch)
	}
}

// The resizer's memory target is the same limit: a 2-DBU plan holds 8192M for a 512M configuration.
// A Pod already at that cap is not a resize (OpenSVC's docker --memory is not moved by one) and is
// NOT reported as applied -- the OpenSVC step it would stand for, the service-level pg_mem_limit write,
// has no counterpart here -- so a grow follows the shared "no live infra step" outcome (restart
// scheduled, database memory not raised). A Pod that still carries a limit below the cap (a Deployment
// from before this change, not yet restarted) is patched in place to the cap.
func TestK8sResizeTargetsTheOpenSVCContainerMemory(t *testing.T) {
	setup := func(t *testing.T, podLimit string) (*Cluster, *ServerMonitor, *fake.Clientset, func()) {
		cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
		cluster.Conf.ProvDbDbu = 2
		cluster.Conf.ProvMem = "512M"
		cluster.resources = NewResourceManager()
		if got := cluster.GetDBContainerMemoryCapMB(); got != 8192 {
			t.Fatalf("fixture: cap = %d, want 8192", got)
		}
		dep, rs := k8sTestController("k8stest", "db1")
		pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", podLimit, "", rs.UID)
		return cluster, s, fake.NewSimpleClientset(dep, rs, pod), cleanup
	}

	t.Run("pod already at the cap is not reported applied", func(t *testing.T) {
		cluster, s, client, cleanup := setup(t, "8192Mi")
		defer cleanup()
		applied, err := cluster.k8sResizeWithClient(client, s, true)
		if err != nil || applied {
			t.Fatalf("applied=%v err=%v, want applied=false (no live step happened)", applied, err)
		}
		if s.GetPendingK8sMemoryResize() != nil || !s.HasRestartCookie() {
			t.Fatal("no pending resize; the grow is left to a restart")
		}
	})

	t.Run("pod below the cap is patched to it", func(t *testing.T) {
		cluster, s, client, cleanup := setup(t, "384Mi")
		defer cleanup()
		applied, err := cluster.k8sResizeWithClient(client, s, true)
		if err != nil || applied {
			t.Fatalf("applied=%v err=%v, want applied=false (confirmation pending)", applied, err)
		}
		if p := s.GetPendingK8sMemoryResize(); p == nil || p.TargetMB != 8192 {
			t.Fatalf("pending = %#v, want a resize to 8192", p)
		}
		pod, err := client.CoreV1().Pods("k8stest").Get(context.TODO(), "db1-abc", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := pod.Spec.Containers[0].Resources.Limits[apiv1.ResourceMemory]
		if got.Cmp(resource.MustParse("8192Mi")) != 0 {
			t.Fatalf("patched limit = %s, want 8192Mi", got.String())
		}
	})

	t.Run("no derivable limit fails safe", func(t *testing.T) {
		cluster, s, client, cleanup := setup(t, "384Mi")
		defer cleanup()
		cluster.resources = nil
		cluster.Conf.ProvMem = "not-a-size"
		applied, err := cluster.k8sResizeWithClient(client, s, true)
		if applied || err == nil {
			t.Fatalf("applied=%v err=%v, want applied=false and an error", applied, err)
		}
		if s.GetPendingK8sMemoryResize() != nil {
			t.Fatal("no resize may be tracked without a target")
		}
	})
}
