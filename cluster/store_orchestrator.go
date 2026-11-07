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

// withinTimeout runs fn — a blocking OpenSVC collector call that has no context of its own — and
// returns its error, or a timeout error if it outlasts timeout. It bounds each call so one
// unreachable agent cannot make WriteAll (and the secret worker's WaitGroup behind it) hang
// (F2). The detached goroutine ends when the collector's own HTTP timeout returns, so it does
// not leak beyond that. The message names the key, never a value (F10).
func withinTimeout(timeout credstore.Timeout, what string, fn func() error) error {
	d := time.Duration(timeout)
	if d <= 0 {
		d = time.Duration(credstore.DefaultStoreWriteTimeout)
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		return fmt.Errorf("orchestrator %s timed out after %s", what, d)
	}
}

// openSVCStore is the OpenSVC `sec` object as a write-only copy sink (credstore.StoreWriter).
type openSVCStore struct {
	cluster *Cluster
}

func (cluster *Cluster) openSVCStore() *openSVCStore { return &openSVCStore{cluster: cluster} }

// ReadCurrent reads the value the agent holds for key, for the WARN0275 drift reconciliation
// only — never as a Chain source (rule 7). A v2 agent has no key read: it returns ok=false, so
// a v2 drift is seen at the next rotation. The read is bounded by the store timeout (F2).
func (s *openSVCStore) ReadCurrent(key string) (value string, ok bool, err error) {
	svc := s.cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return "", false, nil
	}
	var b []byte
	err = withinTimeout(s.cluster.storeTimeout(), "read "+key, func() error {
		var e error
		b, e = svc.GetSecretKeyValueV3(s.cluster.Name, orchestratorEnvObject, key)
		return e
	})
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// WriteAll writes each item's value into the cluster's env secret, each call bounded by timeout,
// and returns the items it could not write (the queue requeues them) with the first error.
// Before it overwrites a key whose value changed, it copies the current value to <KEY>_OLD
// (one-deep history; v3 only, since it needs a read). It stops on ctx cancellation. The value is
// plain, as the agent needs it, and never reaches a log (F10).
func (s *openSVCStore) WriteAll(ctx context.Context, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	svc := s.cluster.OpenSVCConnect()
	v3 := svc.IsV3()
	var failed []credstore.StoreItem
	var firstErr error
	for i, it := range items {
		select {
		case <-ctx.Done():
			failed = append(failed, items[i:]...)
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			return failed, firstErr
		default:
		}
		key, val := it.Key, it.Version.Value
		if v3 && !strings.HasSuffix(key, oldKeySuffix) {
			var old string
			rerr := withinTimeout(timeout, "read "+key, func() error {
				b, e := svc.GetSecretKeyValueV3(s.cluster.Name, orchestratorEnvObject, key)
				old = string(b)
				return e
			})
			if rerr == nil {
				skip, writeOld := orchestratorOldAndSkip(key, val, old, true)
				if skip {
					continue // already current: no redundant write, no _OLD
				}
				if writeOld {
					// best effort: the _OLD slot must never block the current write
					_ = withinTimeout(timeout, "write "+key+oldKeySuffix, func() error {
						return svc.CreateSecretKeyValue(s.cluster.Name, orchestratorEnvObject, key+oldKeySuffix, old)
					})
				}
			}
		}
		werr := withinTimeout(timeout, "write "+key, func() error {
			if v3 {
				// same call prov_opensvc.go uses on a v3 agent (openSVCCreateMapsV3)
				return svc.CreateSecretKeyValue(s.cluster.Name, orchestratorEnvObject, key, val)
			}
			return svc.CreateSecretKeyValueV2(s.cluster.Name, orchestratorEnvObject, key, val)
		})
		if werr != nil {
			failed = append(failed, it)
			if firstErr == nil {
				firstErr = werr
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
