// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/opensvc"
)

// Orchestrator credential copy as a StoreWriter (#2356, follow-up #2227).
//
// Before #2356 the OpenSVC `sec` object and the Kubernetes Secret were written by bespoke
// provisioning code (CreateSecretKeyValue*, k8sPatchSecretValues) with their own WARN0275
// retry loop, outside the version queue that drives the Vault entries. This unifies the write
// side: the orchestrator secret is a write-only StoreWriter the same bounded/timeout/requeue
// queue (T18) drives. It does NOT reverse CREDENTIAL_STORES.md rule 7 — the orchestrator stays
// a consumer, never a Chain source. ReadCurrent exists only for the WARN0275 drift
// reconciliation (the drift is observed, never trusted as a source: F5).

// orchestratorEnvObject is the cluster's shared secret object the orchestrator layer keeps
// (the OpenSVC `sec` named "env", the Kubernetes Secret).
const orchestratorEnvObject = "env"

// oldKeySuffix gives the flat orchestrator secret a one-deep history: during a rotation the
// superseded value is copied to <KEY>_OLD before <KEY> takes the new value. Bounded on purpose
// (F4/T18): only _OLD, never _OLD_1/_2. It covers the rotation transition window (a consumer or
// replica still on the previous password reads <KEY>_OLD) and gives a cheap rollback, the way
// the config versions its starting value.
const oldKeySuffix = "_OLD"

// orchestratorOldAndSkip is the one-deep history + unchanged-skip rule, shared by both sinks so
// it lives in one place (#2356). For a key whose current stored value is curVal (curKnown says
// whether it could be read), it reports whether to skip the write entirely (the agent is
// already current) and whether to first copy curVal to <KEY>_OLD. A key that is itself an _OLD
// slot is never given its own _OLD.
func orchestratorOldAndSkip(key, newVal, curVal string, curKnown bool) (skip, writeOld bool) {
	if !curKnown || strings.HasSuffix(key, oldKeySuffix) {
		return false, false
	}
	if curVal == newVal {
		return true, false
	}
	return false, curVal != ""
}

// orchestratorCallContext bounds one orchestrator call by the store timeout (a safe default when
// unset), under the caller's context: the HTTP request itself carries the deadline, so a dead
// agent ends the call, no goroutine is left behind, and a timed-out write cannot land later (F2,
// T18). Shared by the OpenSVC and Kubernetes sinks.
func orchestratorCallContext(parent context.Context, timeout credstore.Timeout) (context.Context, context.CancelFunc) {
	d := time.Duration(timeout)
	if d <= 0 {
		d = time.Duration(credstore.DefaultStoreWriteTimeout)
	}
	return context.WithTimeout(parent, d)
}

// openSVCStore is the OpenSVC `sec` object as a write-only copy sink (credstore.StoreWriter).
type openSVCStore struct {
	cluster *Cluster
}

func (cluster *Cluster) openSVCStore() *openSVCStore { return &openSVCStore{cluster: cluster} }

// certNotLoaded reports that the agent cannot be reached for want of a client certificate (the
// collector-API mode uses none).
func (s *openSVCStore) certNotLoaded() bool {
	return !s.cluster.Conf.ProvOpensvcUseCollectorAPI && s.cluster.openSVCCertFailed()
}

// ReadCurrent reads the value the agent holds for key, for the WARN0275 drift reconciliation
// only — never as a Chain source (rule 7). A v2 agent has no key read: it returns ok=false, so
// a v2 drift is seen at the next rotation. The read is bounded by the store timeout (F2).
func (s *openSVCStore) ReadCurrent(key string) (value string, ok bool, err error) {
	svc := s.cluster.OpenSVCConnect()
	if s.certNotLoaded() {
		return "", false, errOpenSVCCertNotLoaded
	}
	if !svc.IsV3() {
		return "", false, nil
	}
	ctx, cancel := orchestratorCallContext(context.Background(), s.cluster.storeTimeout())
	defer cancel()
	b, err := svc.GetSecretKeyValueV3Ctx(ctx, s.cluster.Name, orchestratorEnvObject, key)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// WriteAll writes each item's value into the cluster's env secret and returns the items it
// could not write (the queue requeues them) with the first error. The whole call is ONE budget:
// a context derived from ctx with the store timeout, passed down to every HTTP request, so N
// items against a dead agent end within one timeout, not 3·N (the remaining items fail at once
// and are requeued). Before it overwrites a key whose value changed, it copies the current value
// to <KEY>_OLD (one-deep history; v3 only, since it needs a read). The value is plain, as the
// agent needs it, and never reaches a log (F10).
func (s *openSVCStore) WriteAll(ctx context.Context, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	return s.writeWith(ctx, s.cluster.OpenSVCConnect(), items, timeout)
}

// writeWith is WriteAll on a given agent client. It is the ONLY writer of the env secret's keys
// that keeps <KEY>_OLD: a caller that wrote the key itself first (the rotation did) left the
// queue nothing to save, the old value was already gone (seen on dev4, #2356).
func (s *openSVCStore) writeWith(ctx context.Context, svc opensvc.Collector, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	if s.certNotLoaded() {
		// no client certificate: every call would fail on TLS after its timeout
		return items, errOpenSVCCertNotLoaded
	}
	v3 := svc.IsV3()
	bctx, cancel := orchestratorCallContext(ctx, timeout)
	defer cancel()
	var failed []credstore.StoreItem
	var firstErr error
	for i, it := range items {
		if err := bctx.Err(); err != nil {
			// budget spent or caller cancelled: requeue the rest without calling the agent
			failed = append(failed, items[i:]...)
			if firstErr == nil {
				firstErr = fmt.Errorf("orchestrator write stopped before %s: %w", it.Key, err)
			}
			return failed, firstErr
		}
		key, val := it.Key, it.Version.Value
		if v3 && !strings.HasSuffix(key, oldKeySuffix) {
			b, rerr := svc.GetSecretKeyValueV3Ctx(bctx, s.cluster.Name, orchestratorEnvObject, key)
			if rerr == nil {
				old := string(b)
				skip, writeOld := orchestratorOldAndSkip(key, val, old, true)
				if skip {
					continue // already current: no redundant write, no _OLD
				}
				if writeOld {
					// best effort: the _OLD slot must never block the current write
					if oerr := svc.CreateSecretKeyValueCtx(bctx, s.cluster.Name, orchestratorEnvObject, key+oldKeySuffix, old); oerr != nil {
						// the key is still written below; the rollback copy is what is lost: say so,
						// the key only, never a value (F10)
						s.cluster.LogModulePrintf(s.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Orchestrator secret %s/sec/%s: %s%s not written, the superseded value is not kept: %s", s.cluster.Name, orchestratorEnvObject, key, oldKeySuffix, oerr)
					}
				}
			}
		}
		// v3: the same call prov_opensvc.go uses (openSVCCreateMapsV3); v2: the v2 key API
		werr := svc.CreateSecretKeyValueCtx(bctx, s.cluster.Name, orchestratorEnvObject, key, val)
		if werr != nil {
			failed = append(failed, it)
			if firstErr == nil {
				// the key, never the value (F10)
				firstErr = fmt.Errorf("orchestrator write %s: %w", key, werr)
			}
		}
	}
	return failed, firstErr
}

// compile-time check: openSVCStore is a credstore.StoreWriter.
var _ credstore.StoreWriter = (*openSVCStore)(nil)

// orchestratorStore is a cluster's orchestrator secret as a write-only copy sink: the version
// queue drives WriteAll; ReadCurrent serves the WARN0275 drift reconciliation only (rule 7).
type orchestratorStore interface {
	credstore.StoreWriter
	ReadCurrent(key string) (value string, ok bool, err error)
}

// orchestratorStoreEntry is the reserved version-queue entry whose writer is the cluster's
// orchestrator secret — not a user-configurable Vault entry (the sponsor cannot turn it off).
const orchestratorStoreEntry = "orchestrator"

// orchestratorStoreWriter returns the sink for the cluster's orchestrator, or nil when the
// cluster is not orchestrated by OpenSVC or Kubernetes (localhost/onpremise/slapos have no
// shared secret object to copy a credential into).
func (cluster *Cluster) orchestratorStoreWriter() orchestratorStore {
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.openSVCStore()
	case config.ConstOrchestratorKubernetes:
		return cluster.k8sStore()
	default:
		return nil
	}
}

// queueOrchestrator queues one env-named credential to the orchestrator sink, so its copy is
// kept current through the same bounded/timeout/requeue queue (T18) as the Vault entries,
// instead of the bespoke per-orchestrator write path. Nothing on a standby monitor, an empty
// value, or a cluster with no orchestrator secret.
func (cluster *Cluster) queueOrchestrator(key, value string) {
	if !cluster.IsActive() || value == "" {
		return
	}
	if cluster.orchestratorStoreWriter() == nil {
		return
	}
	cluster.storeQueue().Push(credstore.StoreItem{Entry: orchestratorStoreEntry, Key: key, Version: cluster.selfVersion(value)})
	cluster.startSecretStores()
}

// seedOrchestratorPasswords writes the provisioning's password keys of the env secret through
// the store writer (#2356): REPLICATION_MANAGER_PASSWORD (the admin API password, copied to the
// orchestrator secret only, never to the Chain/Vault, rule 6), MYSQL_ROOT_PASSWORD and
// SHARDPROXY_ROOT_PASSWORD. A reprovision over an existing secret keeps the superseded value in
// <KEY>_OLD, as a rotation does. The database and proxy passwords then go to the stores as a
// version (reportOrchestratorSecret). It returns the error of each key it could not write.
func (cluster *Cluster) seedOrchestratorPasswords(svc opensvc.Collector) map[string]error {
	errs := make(map[string]error)
	for _, kv := range []struct{ key, value string }{
		{"REPLICATION_MANAGER_PASSWORD", cluster.APIUsers["admin"].Password},
		{"MYSQL_ROOT_PASSWORD", cluster.GetDbPass()},
		{"SHARDPROXY_ROOT_PASSWORD", cluster.GetShardPass()},
	} {
		// written even when empty, as before: a service template may reference the key
		_, err := cluster.openSVCStore().writeWith(context.Background(), svc, []credstore.StoreItem{{
			Entry: orchestratorStoreEntry, Key: kv.key, Version: cluster.selfVersion(kv.value),
		}}, cluster.storeTimeout())
		if err != nil {
			errs[kv.key] = err
			continue
		}
		if kv.key != "REPLICATION_MANAGER_PASSWORD" {
			cluster.reportOrchestratorSecret("env", kv.key, kv.value)
		}
	}
	return errs
}
