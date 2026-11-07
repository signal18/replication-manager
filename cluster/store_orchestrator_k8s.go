// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"strings"

	"github.com/signal18/replication-manager/config/credstore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// k8sStore is the Kubernetes Secret as a write-only copy sink (credstore.StoreWriter, #2356),
// the twin of openSVCStore for a K8s-orchestrated cluster. Same contract: the version queue
// drives it, it never reads a credential back as a Chain source (CREDENTIAL_STORES.md rule 7),
// ReadCurrent is for the WARN0275 drift reconciliation only, and a rotation keeps a one-deep
// <KEY>_OLD history.
type k8sStore struct {
	cluster *Cluster
}

func (cluster *Cluster) k8sStore() *k8sStore { return &k8sStore{cluster: cluster} }

// ReadCurrent reads the value the cluster Secret holds for key, for the drift reconciliation
// only. The Secret's read side is Data (decoded bytes), StringData being write-only.
func (s *k8sStore) ReadCurrent(key string) (value string, ok bool, err error) {
	client, err := s.cluster.K8SConnectAPI()
	if err != nil {
		return "", false, err
	}
	sec, err := client.CoreV1().Secrets(s.cluster.Name).Get(context.TODO(), k8sClusterSecretName(s.cluster.Name), metav1.GetOptions{})
	if err != nil {
		return "", false, err
	}
	b, present := sec.Data[key]
	if !present {
		return "", false, nil
	}
	return string(b), true, nil
}

// WriteAll patches the cluster Secret with the items' values in one merge Patch, within
// timeout. For each key whose stored value changed it also writes <KEY>_OLD = the superseded
// value (one-deep history, read from the Secret's Data). A failed patch requeues every item.
func (s *k8sStore) WriteAll(ctx context.Context, items []credstore.StoreItem, timeout credstore.Timeout) ([]credstore.StoreItem, error) {
	client, err := s.cluster.K8SConnectAPI()
	if err != nil {
		return items, err
	}
	// current values, once, for the _OLD slot (best effort: absent Secret => no _OLD)
	var cur map[string][]byte
	if sec, gerr := client.CoreV1().Secrets(s.cluster.Name).Get(ctx, k8sClusterSecretName(s.cluster.Name), metav1.GetOptions{}); gerr == nil && sec != nil {
		cur = sec.Data
	}
	values := make(map[string]string, len(items))
	for _, it := range items {
		key, val := it.Key, it.Version.Value
		if cur != nil && !strings.HasSuffix(key, oldKeySuffix) {
			if old, present := cur[key]; present && string(old) != "" && string(old) != val {
				values[key+oldKeySuffix] = string(old)
			}
		}
		values[key] = val
	}
	if len(values) == 0 {
		return nil, nil
	}
	if err := s.cluster.k8sPatchSecretValuesRaw(client, values); err != nil {
		return items, err
	}
	return nil, nil
}

// compile-time check: k8sStore is a credstore.StoreWriter.
var _ credstore.StoreWriter = (*k8sStore)(nil)
