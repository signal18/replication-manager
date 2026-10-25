// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"os"
	"path/filepath"

	"github.com/signal18/replication-manager/utils/releases"
)

// ImageCatalog builds the configurator's image list catalog: the release table and the
// tags of the image list (the back-office copy first, else the embedded one). A cluster
// reads it with its configuration, the self-service gate with the instance's (#2165).
func (conf *Config) ImageCatalog() (*releases.Catalog, error) {
	table, _, err := releases.Load(conf.ShareDir + "/plugins/data")
	if err != nil {
		return nil, err
	}
	repos, err := conf.GetDockerRepos(conf.ShareDir+"/repo/repos.json", conf.Test)
	if err != nil {
		return nil, err
	}
	tags := map[string][]releases.Tag{}
	for _, r := range repos {
		for _, t := range r.Tags.Results {
			tags[r.Image] = append(tags[r.Image], releases.Tag{Name: t.Name, Digest: t.Digest})
		}
	}
	source := "embedded image list"
	if st, err := os.Stat(filepath.Join(conf.ShareDir, "plugins", "data", "repos.json")); err == nil && st.Size() > 0 {
		source = "plugins/data/repos.json (back office)"
	}
	return &releases.Catalog{Table: table, Tags: tags, Source: source}, nil
}
