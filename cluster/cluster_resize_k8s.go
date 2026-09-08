// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Kubernetes native memory-resize backend (KUBERNETES_OPENSVC_RESOURCE_PARITY_
// IMPLEMENTATION_PLAN.md, Phase 3/4): the Kubernetes equivalent of OpenSVC's
// live PG cgroup update, using the Pod resize subresource proved live in Kind
// (cluster/smoke_kind_pod_resize_test.go, kindsmoke build tag) -- patch the
// resize subresource, then confirm the kubelet-applied result on a LATER
// monitor tick before ever raising DB memory. Never block the monitor loop:
// every API call here is bounded by k8sAPICallTimeout (prov_k8s_db.go), and
// the pending resize itself is bounded by k8sResizeConfirmTimeout.
//
// Desired-state durability: the confirmed target is NOT persisted to any
// Kubernetes object of its own. cluster.Conf.ProvMem is already the
// established durable/dynamic source (SetDBMemorySize, cluster/cluster_set.go,
// updates it BEFORE a resize is even dispatched, the same way every other
// dynamic prov-db-* setting works), and k8sDatabaseMemoryTargets
// (prov_k8s_db.go) always derives fresh from it -- so a later reprovision or
// restart already picks up the confirmed value with no second source of
// truth to keep in sync or get out of sync.

// K8sMemoryResizeState is the bounded pending state for a native Kubernetes
// Pod memory resize in flight, mirroring server.PendingCgroupShrink's role
// for the deferred-shrink phase. Access ONLY through
// ServerMonitor.GetPendingK8sMemoryResize/SetPendingK8sMemoryResize (srv.go):
// it is written from the resize-dispatch path and read/cleared from the
// monitor tick, which can run on different goroutines. Every dispatch
// (SetPendingK8sMemoryResize) allocates a FRESH *K8sMemoryResizeState -- that
// pointer identity is the operation's version token: clearing/updating code
// always compare-and-swaps against the exact pointer it read
// (clearPendingK8sMemoryResizeIfSame/markPendingK8sMemoryResizeObserved), so a
// reconciliation decision made against an old snapshot can never clear or act
// on a newer pending resize that has since superseded it.
type K8sMemoryResizeState struct {
	TargetMB  int       `json:"targetMB"`
	Grow      bool      `json:"grow"`
	StartedAt time.Time `json:"startedAt"`
	// Observed is true once a monitor tick has actually seen THIS dispatch's
	// Pod carry a non-empty Status.Resize (kubelet genuinely working the
	// request) at least once. A later clear Status.Resize + matching spec is
	// only trusted as real completion when Observed is already true --
	// otherwise a check landing in the brief window between the resize-
	// subresource Patch call returning (which mutates spec immediately, see
	// k8sResizeWithClient) and kubelet reacting to it (which sets
	// Status.Resize) would read "spec matches, status clear" and false-confirm
	// a resize kubelet has not even started applying yet. See
	// k8sMemoryResizeConfirmed's own doc comment for the underlying check.
	Observed bool `json:"observed"`
}

// GetPendingK8sMemoryResize/SetPendingK8sMemoryResize are the only sanctioned
// access to server.pendingK8sMemoryResize -- an atomic.Pointer because the
// resize-dispatch path (API/cron-triggered) and the monitor tick
// (completePendingK8sMemoryResize) can run on different goroutines.
func (server *ServerMonitor) GetPendingK8sMemoryResize() *K8sMemoryResizeState {
	return server.pendingK8sMemoryResize.Load()
}

func (server *ServerMonitor) SetPendingK8sMemoryResize(s *K8sMemoryResizeState) {
	server.pendingK8sMemoryResize.Store(s)
	server.syncPendingK8sMemoryResizeToCluster(s)
}

// syncPendingK8sMemoryResizeToCluster mirrors every pendingK8sMemoryResize
// mutation into Cluster.k8sPendingMemoryResizes, keyed by server.Id (stable
// across a ServerMonitor recreation -- see newServerMonitor, srv.go) so the
// state survives newServerList() rebuilding every *ServerMonitor from
// scratch (finding: reload/config-change must not silently drop a pending
// native resize). Every mutation path funnels through here: SetPending... ,
// markPendingK8sMemoryResizeObserved's successful CAS, and
// clearPendingK8sMemoryResizeIfSame's successful CAS.
func (server *ServerMonitor) syncPendingK8sMemoryResizeToCluster(s *K8sMemoryResizeState) {
	if server.ClusterGroup == nil || server.Id == "" {
		return
	}
	if s == nil {
		server.ClusterGroup.k8sPendingMemoryResizes.Delete(server.Id)
		return
	}
	server.ClusterGroup.k8sPendingMemoryResizes.Store(server.Id, s)
}

// markPendingK8sMemoryResizeObserved records that the exact operation
// identified by the pending pointer the caller loaded has been seen genuinely
// in flight. A compare-and-swap, not an unconditional Store: if a concurrent
// dispatch has already replaced it with a newer target, that newer state is
// authoritative and must not be silently overwritten with a stale Observed
// flag that belongs to the operation it superseded. Returns the now-current
// pending state (the updated copy, a newer one from a concurrent dispatch, or
// nil if it was concurrently cleared).
func (server *ServerMonitor) markPendingK8sMemoryResizeObserved(pending *K8sMemoryResizeState) *K8sMemoryResizeState {
	if pending.Observed {
		return pending
	}
	updated := *pending
	updated.Observed = true
	if server.pendingK8sMemoryResize.CompareAndSwap(pending, &updated) {
		server.syncPendingK8sMemoryResizeToCluster(&updated)
		return &updated
	}
	return server.pendingK8sMemoryResize.Load()
}

// clearPendingK8sMemoryResizeIfSame clears the pending state ONLY if it is
// still the exact operation the caller loaded (pointer identity) -- a
// compare-and-swap, not server.SetPendingK8sMemoryResize(nil), so a
// reconciliation decision made against an old snapshot can never wipe out a
// newer pending resize that has since superseded it.
func (server *ServerMonitor) clearPendingK8sMemoryResizeIfSame(pending *K8sMemoryResizeState) {
	if server.pendingK8sMemoryResize.CompareAndSwap(pending, nil) {
		server.syncPendingK8sMemoryResizeToCluster(nil)
	}
}

// restorePendingK8sMemoryResize restores a pending native Kubernetes Pod
// memory resize onto a freshly (re)constructed ServerMonitor from
// Cluster.k8sPendingMemoryResizes, keyed by server.Id -- called from
// newServerMonitor (srv.go) right after server.Id is computed, so a resize
// genuinely in flight survives newServerList() rebuilding every
// *ServerMonitor from scratch (a live config-set handler, not just startup).
// Observed is deliberately reset to false: this process instance never
// itself witnessed the resize in flight, so the patch-just-issued race guard
// (K8sMemoryResizeState.Observed) applies again from scratch. StartedAt is
// kept as-is so the original k8sResizeConfirmTimeout budget is not silently
// extended by a reload -- if it has already elapsed, the very next monitor
// tick's completePendingK8sMemoryResize falls back to a restart, same as any
// other unconfirmed resize.
func (cluster *Cluster) restorePendingK8sMemoryResize(server *ServerMonitor) {
	v, ok := cluster.k8sPendingMemoryResizes.Load(server.Id)
	if !ok {
		return
	}
	restored, isState := v.(*K8sMemoryResizeState)
	if !isState || restored == nil {
		return
	}
	resumed := *restored
	resumed.Observed = false
	server.pendingK8sMemoryResize.Store(&resumed)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"restored pending Kubernetes Pod memory resize for %s across monitor recreation (target=%dMi)", server.Name, resumed.TargetMB)
}

// k8sResizeConfirmTimeout bounds how long a pending native resize is allowed
// to stay unconfirmed before falling back to the restart-cookie path (mandatory
// safety rule: unconfirmed native resize must not linger forever).
const k8sResizeConfirmTimeout = 2 * time.Minute

// k8sResizer implements ResourceResizer via the Kubernetes Pod resize
// subresource. Only selected by resourceResizer() when
// prov-db-docker-run-args-limit gave the DB container a fixed Requests/Limits
// baseline to resize from (k8sDatabaseContainerResources).
type k8sResizer struct{ cluster *Cluster }

func (r k8sResizer) CanConfigResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

// ConfigResize patches the Pod resize subresource toward the current
// k8sDatabaseMemoryTargets DB share and returns immediately -- it can never
// report applied=true for a freshly-issued patch (mandatory safety rule: API
// acceptance is not a confirmed cgroup update). A genuinely new pending
// resize returns applied=false, nil WITHOUT a restart cookie (confirmation is
// pending, not failed); completePendingK8sMemoryResize (monitor tick)
// confirms or times out later. Only a missing/ambiguous Pod, a missing API
// client, or a patch-call failure falls back to the restart-cookie path
// immediately.
func (r k8sResizer) ConfigResize(server *ServerMonitor, grow bool) (bool, error) {
	cluster := r.cluster
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		server.SetRestartCookie()
		return false, err
	}
	return cluster.k8sResizeWithClient(client, server, grow)
}

// k8sResizeWithClient is the *WithClient testable half of k8sResizer.ConfigResize
// (same split as every other prov_k8s_*.go entry point).
func (cluster *Cluster) k8sResizeWithClient(client kubernetes.Interface, server *ServerMonitor, grow bool) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	pod, err := cluster.k8sFindDatabasePod(ctx, client, server)
	if err != nil {
		server.SetRestartCookie()
		return false, err
	}
	if pod == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"no single live Pod found for %s, scheduling restart", server.URL)
		server.SetRestartCookie()
		return false, nil
	}

	dbMB, _ := cluster.k8sDatabaseMemoryTargets()
	target := resource.MustParse(strconv.Itoa(dbMB) + "Mi")
	existing := server.GetPendingK8sMemoryResize()
	// A resize of OURS is tracked for this exact target but has never been
	// observed genuinely in flight -- do not trust a live spec-match+clear-
	// status as confirmation here (the patch-just-issued race, see
	// K8sMemoryResizeState.Observed); fall through and re-patch instead.
	racy := existing != nil && existing.TargetMB == dbMB && !existing.Observed
	if !racy && k8sMemoryResizeConfirmed(pod, server.Name, target) {
		// The live Pod is ALREADY confirmed (not just requested -- Status.Resize
		// is clear and the applied value matches) at the target: e.g. a duplicate
		// call while a previous resize's confirmation is still catching up on
		// tracked state. Nothing to do. A Pod whose spec already shows the target
		// but is still mid-kubelet-application (Status.Resize non-empty) does NOT
		// take this path -- see k8sMemoryResizeConfirmed.
		if existing != nil {
			server.clearPendingK8sMemoryResizeIfSame(existing)
		}
		return true, nil
	}

	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"containers": []map[string]interface{}{
				{
					"name": server.Name,
					"resources": map[string]interface{}{
						"requests": map[string]string{"memory": target.String()},
						"limits":   map[string]string{"memory": target.String()},
					},
				},
			},
		},
	})
	if err != nil {
		server.SetRestartCookie()
		return false, err
	}
	if _, err := client.CoreV1().Pods(cluster.Name).Patch(ctx, pod.Name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{}, "resize"); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Kubernetes Pod memory resize request failed on %s, scheduling restart: %s", server.URL, err)
		server.SetRestartCookie()
		return false, err
	}
	server.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: dbMB, Grow: grow, StartedAt: time.Now()})
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Kubernetes Pod memory resize requested on %s (target=%dMi), awaiting kubelet confirmation", server.URL, dbMB)
	return false, nil
}

// k8sPodContainerMemory returns the named container's currently-REQUESTED
// memory limit (the field the resize subresource patches) -- this is the
// DESIRED value, not proof kubelet has actually applied it; see
// k8sMemoryResizeConfirmed for the confirmation check.
func k8sPodContainerMemory(pod *apiv1.Pod, containerName string) (resource.Quantity, bool) {
	for _, c := range pod.Spec.Containers {
		if c.Name != containerName {
			continue
		}
		q, ok := c.Resources.Limits[apiv1.ResourceMemory]
		return q, ok
	}
	return resource.Quantity{}, false
}

// k8sMemoryResizeConfirmed is the ONE confirmation check used everywhere a
// native resize's completion matters (k8sResizeWithClient's duplicate-call
// short-circuit AND completePendingK8sMemoryResize's tick reconciliation):
// the Pod's own Status.Resize must be clear (kubelet is not still applying or
// have deferred/failed the change) AND the live container spec must already
// carry the target. Checking the spec value alone is not enough -- a resize
// subresource Patch mutates spec.containers[].resources immediately on
// acceptance, well before kubelet finishes applying it, so a spec match with
// a non-empty Status.Resize means a resize is IN PROGRESS, not confirmed
// (proven live in Kind: kindSmokeWaitForResizeCompletion, cluster/
// smoke_kind_pod_resize_test.go, waits for exactly this pair of conditions).
func k8sMemoryResizeConfirmed(pod *apiv1.Pod, containerName string, target resource.Quantity) bool {
	if pod.Status.Resize != "" {
		return false
	}
	current, ok := k8sPodContainerMemory(pod, containerName)
	return ok && current.Cmp(target) == 0
}

// completePendingK8sMemoryResize is the monitor-tick reconciliation for a
// pending native Kubernetes Pod memory resize: a single bounded API call, or a
// no-op when nothing is pending (F2: never burdens the monitor). Only
// k8sMemoryResizeConfirmed on the live Pod counts as confirmed -- Deferred/
// Infeasible/timeout/missing-Pod/API-failure all fall back to the existing
// restart-cookie path, never a synthesized "applied" result.
func (cluster *Cluster) completePendingK8sMemoryResize(server *ServerMonitor) {
	if server == nil || server.GetPendingK8sMemoryResize() == nil {
		return
	}
	if server.State == stateFailed || server.State == stateUnconn {
		return
	}
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		if pending := server.GetPendingK8sMemoryResize(); pending != nil && time.Since(pending.StartedAt) > k8sResizeConfirmTimeout {
			cluster.k8sCompletePendingMemoryResizeWithClient(nil, server, "Kubernetes API unreachable")
		}
		return
	}
	cluster.k8sCompletePendingMemoryResizeWithClient(client, server, "")
}

// k8sCompletePendingMemoryResizeWithClient is the *WithClient testable half of
// completePendingK8sMemoryResize. forceFailReason, when non-empty, skips the
// API call entirely and goes straight to the restart-cookie fallback (used
// when the caller already knows the API is unreachable and the timeout has
// elapsed -- client is nil in that case and must not be dereferenced).
func (cluster *Cluster) k8sCompletePendingMemoryResizeWithClient(client kubernetes.Interface, server *ServerMonitor, forceFailReason string) {
	pending := server.GetPendingK8sMemoryResize()
	if pending == nil {
		return
	}
	fail := func(reason string) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Kubernetes Pod memory resize on %s did not confirm (%s), scheduling restart", server.URL, reason)
		server.clearPendingK8sMemoryResizeIfSame(pending)
		server.SetRestartCookie()
		cluster.logResize(server, resizeMemory, pending.Grow, false, ResizeYes, nil)
	}
	if forceFailReason != "" {
		fail(forceFailReason)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), k8sAPICallTimeout)
	defer cancel()
	pod, err := cluster.k8sFindDatabasePod(ctx, client, server)
	if err != nil || pod == nil {
		if time.Since(pending.StartedAt) > k8sResizeConfirmTimeout {
			fail("Pod not found")
		}
		return
	}

	if pod.Status.Resize != "" {
		// kubelet is genuinely working (or has deferred/failed) this request --
		// record that this exact dispatch has been seen in flight before
		// evaluating it below, so a LATER tick that sees a clear status can
		// trust it as real completion instead of the patch-just-issued race.
		pending = server.markPendingK8sMemoryResizeObserved(pending)
		if pending == nil {
			return // a concurrent dispatch/tick already resolved this
		}
	}

	switch pod.Status.Resize {
	case apiv1.PodResizeStatusDeferred:
		fail("resize deferred by kubelet")
		return
	case apiv1.PodResizeStatusInfeasible:
		fail("resize infeasible on this node")
		return
	}

	target := resource.MustParse(strconv.Itoa(pending.TargetMB) + "Mi")
	if pending.Observed && k8sMemoryResizeConfirmed(pod, server.Name, target) {
		server.clearPendingK8sMemoryResizeIfSame(pending)
		if pending.Grow {
			cluster.applyConfirmedMemoryGrow(server, ResizeYes)
		} else {
			cluster.logResize(server, resizeMemory, false, true, ResizeYes, nil)
		}
		return
	}

	if time.Since(pending.StartedAt) > k8sResizeConfirmTimeout {
		fail(fmt.Sprintf("timed out after %s", k8sResizeConfirmTimeout))
	}
	// Still in progress (or not yet observed in flight): wait for the next tick.
}
