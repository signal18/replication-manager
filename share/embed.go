package share

import (
	"embed"
	_ "embed"
)

//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json
var EmbededDbModuleFS embed.FS
