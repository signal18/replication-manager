package share

import (
	"embed"
	_ "embed"
)

//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json opensvc/moduleset_mariadb.svc.mrm.proxy.json dashboard  repo serviceplan.csv whitelist.conf.grafana whitelist.conf.minimal blacklist.conf.template grafana
var EmbededDbModuleFS embed.FS
