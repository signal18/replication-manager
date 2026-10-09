package peer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// UnmarshalJSON reads a peer.json cluster entry. The resource sizes are harvested
// replication-manager config values, so they accept exactly what the config accepts:
// memory and disk size go through the same unit parser as the configurator
// (config.ParseUnitMeasurementToInt, "4G" or a bare MB number for memory, "20G" or
// bare GB for disk); cores and IOPS are counts, quoted or not. A "4G" used to fail
// the whole peer.json and empty the peer list (preprod 2026-10-09, tamarin).
func (p *PeerCluster) UnmarshalJSON(b []byte) error {
	type plain PeerCluster
	aux := struct {
		*plain
		ProvDbMemory   json.RawMessage `json:"prov-db-memory"`
		ProvDbCpuCores json.RawMessage `json:"prov-db-cpu-cores"`
		ProvDbDiskIops json.RawMessage `json:"prov-db-disk-iops"`
		ProvDbDiskSize json.RawMessage `json:"prov-db-disk-size"`
	}{plain: (*plain)(p)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	mem, err := parsePeerUnit(aux.ProvDbMemory, "M,bytes,required")
	if err != nil {
		return fmt.Errorf("prov-db-memory: %w", err)
	}
	cores, err := parsePeerCount(aux.ProvDbCpuCores)
	if err != nil {
		return fmt.Errorf("prov-db-cpu-cores: %w", err)
	}
	iops, err := parsePeerCount(aux.ProvDbDiskIops)
	if err != nil {
		return fmt.Errorf("prov-db-disk-iops: %w", err)
	}
	disk, err := parsePeerUnit(aux.ProvDbDiskSize, "G,bytes,required")
	if err != nil {
		return fmt.Errorf("prov-db-disk-size: %w", err)
	}
	p.ProvDbMemory, p.ProvDbCpuCores, p.ProvDbDiskIops, p.ProvDbDiskSize = int(mem), int(cores), iops, disk
	return nil
}

// peerString returns a JSON string or number as its text; "" for empty or null.
func peerString(raw json.RawMessage) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(s), nil
}

// parsePeerUnit reads a size with the config's own unit parser and tag; empty is 0.
func parsePeerUnit(raw json.RawMessage, tag string) (int64, error) {
	s, err := peerString(raw)
	if err != nil || s == "" {
		return 0, err
	}
	v, err := config.ParseUnitMeasurementToInt(tag, s, true)
	return int64(v), err
}

// parsePeerCount reads a plain count, quoted or not; empty is 0.
func parsePeerCount(raw json.RawMessage) (int64, error) {
	s, err := peerString(raw)
	if err != nil || s == "" {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

// DecodePeerList decodes peer.json entry by entry: an entry that cannot be read is
// skipped and reported, never the whole list.
func DecodePeerList(content []byte) ([]*PeerCluster, []string, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(content, &raws); err != nil {
		return nil, nil, err
	}
	peers := make([]*PeerCluster, 0, len(raws))
	var skipped []string
	for i, raw := range raws {
		var pc PeerCluster
		if err := json.Unmarshal(raw, &pc); err != nil {
			var id struct {
				ClusterName string `json:"cluster-name"`
				Domain      string `json:"cloud18-domain"`
			}
			_ = json.Unmarshal(raw, &id)
			skipped = append(skipped, fmt.Sprintf("entry %d (%s/%s): %v", i, id.Domain, id.ClusterName, err))
			continue
		}
		peers = append(peers, &pc)
	}
	return peers, skipped, nil
}
