package etc

import (
	"embed"
	_ "embed"
)

//go:embed local/embed/config.toml
var EmbededDbModuleFS embed.FS
