package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

type schemaCachePayload struct {
	Flavor    string                     `json:"flavor"`
	Version   string                     `json:"version"`
	UpdatedAt int64                      `json:"updatedAt"`
	Tables    []dbhelper.Table           `json:"tables"`
	Dict      map[string]*dbhelper.Table `json:"dict"`
}

func (cluster *Cluster) schemaCacheDir() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "schema_cache")
}

func (cluster *Cluster) schemaCachePath(server *ServerMonitor) string {
	name := fmt.Sprintf("%s_%s.json", server.Host, server.Port)
	return filepath.Join(cluster.schemaCacheDir(), name)
}

func (cluster *Cluster) SaveSchemaCache(server *ServerMonitor) error {
	if server == nil || server.DBVersion == nil {
		return fmt.Errorf("schema cache: server not ready")
	}
	if len(server.DictTables.ToNewMap()) == 0 {
		return fmt.Errorf("schema cache: empty table dictionary")
	}

	if err := os.MkdirAll(cluster.schemaCacheDir(), 0o755); err != nil {
		return err
	}

	payload := schemaCachePayload{
		Flavor:    server.DBVersion.Flavor,
		Version:   server.DBVersion.ToString(),
		UpdatedAt: time.Now().Unix(),
		Tables:    server.Tables,
		Dict:      server.DictTables.ToNewMap(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return os.WriteFile(cluster.schemaCachePath(server), data, 0o644)
}

func (cluster *Cluster) LoadSchemaCache(server *ServerMonitor) (bool, error) {
	if server == nil || server.DBVersion == nil {
		return false, fmt.Errorf("schema cache: server not ready")
	}

	path := cluster.schemaCachePath(server)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	var payload schemaCachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, err
	}

	if payload.Version != server.DBVersion.ToString() || payload.Flavor != server.DBVersion.Flavor {
		return false, fmt.Errorf("schema cache: version mismatch")
	}
	if len(payload.Dict) == 0 {
		return false, fmt.Errorf("schema cache: empty payload")
	}

	cluster.Lock()
	server.Tables = payload.Tables
	server.DictTables = dbhelper.FromNormalTablesMap(server.DictTables, payload.Dict)
	cluster.Unlock()

	return true, nil
}
