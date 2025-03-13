package cluster

import (
	"hash"
)

func (cluster *Cluster) GetChecksumConfig(key string) (hash.Hash, bool) {
	h, ok := cluster.CheckSumConfig[key]
	if !ok {
		return nil, ok
	}
	return h, ok
}

func (cluster *Cluster) SetChecksumConfig(key string, value hash.Hash) {
	cluster.CheckSumConfig[key] = value
}

func (cluster *Cluster) SetIsNeedGitPush(value bool) {
	cluster.IsNeedGitPush = value
}
