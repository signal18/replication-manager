//go:build clients
// +build clients

package share

import "embed"

// EmbededDbModuleFS is an empty FS when built with 'clients' tag
//
//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json opensvc/moduleset_mariadb.svc.mrm.proxy.json app repo serviceplan.csv plugins/data/enterprise-dochelp-variables.json
var EmbededDbModuleFS embed.FS
