// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// k8sStore is the Kubernetes Secret as a write-only copy sink (credstore.StoreWriter, #2356),
// the twin of openSVCStore for a K8s-orchestrated cluster. Same contract: the version queue
// drives it, it never reads a credential back as a Chain source (CREDENTIAL_STORES.md rule 7),
// ReadCurrent is for the WARN0275 drift reconciliation only, and a rotation keeps a one-deep
// <KEY>_OLD history. The Kubernetes client is context-native, so each call is bounded by the
// store timeout through the context (F2).
type k8sStore struct {
	cluster   *Cluster
	namespace string
	secret    string
	// prefixed: the key written is <item.Path>_<item.Key> (a server entry), else item.Key
	prefixed bool
}

func (cluster *Cluster) k8sStore() *k8sStore {
	return &k8sStore{cluster: cluster, namespace: cluster.Name, secret: k8sClusterSecretName(cluster.Name)}
}

// ReadCurrent reads the value the cluster Secret holds for key, for the drift reconciliation
// only. The Secret's read side is Data (decoded bytes), StringData being write-only.
func (s *k8sStore) ReadCurrent(key string) (value string, ok bool, err error) {
	if s.prefixed {
		return "", false, errPrefixedReadCurrent
	}
	client, err := s.cluster.K8SConnectAPI()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := orchestratorCallContext(context.Background(), s.cluster.storeTimeout())
	defer cancel()
	sec, err := client.CoreV1().Secrets(s.namespace).Get(ctx, s.secret, metav1.GetOptions{})
	if err != nil {
		return "", false, err
	}
	b, present := sec.Data[key]
	if !present {
		return "", false, nil
	}
	return string(b), true, nil
}

// WriteAll patches the cluster Secret with the items' values in one merge Patch, bounded by the
// store timeout. A key whose stored value already equals the new one is skipped (no redundant
// patch, no spurious _OLD); for a key whose value changed it also writes <KEY>_OLD = the
// superseded value (one-deep history, read from the Secret's Data). A failed patch requeues
// every item.
func (s *k8sStore) WriteAll(ctx context.Context, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	client, err := s.cluster.K8SConnectAPI()
	if err != nil {
		return items, err
	}
	return s.writeWith(ctx, client, items, timeout)
}

// writeWith is WriteAll on a given client. It is the only writer of the Secret's keys that keeps
// <KEY>_OLD: the rotation goes through it too, a direct patch first would leave nothing to save.
func (s *k8sStore) writeWith(ctx context.Context, client kubernetes.Interface, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	// current values, once, for the _OLD slot and the unchanged-skip (best effort: an absent
	// Secret means no current value, so everything is written)
	// one budget for the read and the patch, as on OpenSVC
	bctx, cancel := orchestratorCallContext(ctx, timeout)
	defer cancel()
	var cur map[string][]byte
	if sec, gerr := client.CoreV1().Secrets(s.namespace).Get(bctx, s.secret, metav1.GetOptions{}); gerr == nil && sec != nil {
		cur = sec.Data
	} else if gerr != nil && !apierrors.IsNotFound(gerr) {
		// best effort: everything is written and no _OLD is kept, which loses the rollback copy:
		// a warning, the error naming no value (F10). An absent Secret (the first provision) has
		// nothing to keep.
		s.cluster.LogModulePrintf(s.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Kubernetes secret read before write failed, writing without _OLD: %s", gerr)
	}
	values := make(map[string]string, len(items))
	for _, it := range items {
		key, val := storeKey(s.prefixed, it), it.Version.Value
		curVal, curKnown := "", false
		if cur != nil {
			if b, present := cur[key]; present {
				curVal, curKnown = string(b), true
			}
		}
		skip, writeOld := orchestratorOldAndSkip(key, val, curVal, curKnown)
		if skip {
			continue // already current: skip the redundant patch (and _OLD)
		}
		if writeOld {
			values[key+oldKeySuffix] = curVal
		}
		values[key] = val
	}
	if len(values) == 0 {
		return nil, nil
	}
	if err := s.cluster.k8sPatchNamedSecretRaw(bctx, client, s.namespace, s.secret, values); err != nil {
		return items, err
	}
	return nil, nil
}

// compile-time check: k8sStore is a credstore.StoreWriter.
var _ credstore.StoreWriter = (*k8sStore)(nil)
