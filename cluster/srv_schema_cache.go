package cluster

func (server *ServerMonitor) maybeLoadSchemaCache() {
	if server == nil || server.DBVersion == nil {
		return
	}

	if len(server.DictTables.ToNewMap()) > 0 {
		server.SchemaCacheLoaded = true
		server.SchemaCacheChecked = true
		server.SchemaCacheVersion = server.DBVersion.ToString()
		server.SchemaCacheFlavor = server.DBVersion.Flavor
		return
	}

	if server.SchemaCacheChecked && server.SchemaCacheVersion == server.DBVersion.ToString() && server.SchemaCacheFlavor == server.DBVersion.Flavor {
		return
	}

	server.SchemaCacheChecked = true
	server.SchemaCacheVersion = server.DBVersion.ToString()
	server.SchemaCacheFlavor = server.DBVersion.Flavor

	loaded, err := server.ClusterGroup.LoadSchemaCache(server)
	if err == nil && loaded {
		server.SchemaCacheLoaded = true
	}
}
