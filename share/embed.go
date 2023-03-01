package share

import (
	"embed"
	_ "embed"
)

//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json opensvc/moduleset_mariadb.svc.mrm.proxy.json dashboard  repo tests
var EmbededDbModuleFS embed.FS
