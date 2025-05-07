//go:build clients
// +build clients

package share

import "embed"

// EmbededDbModuleFS is an empty FS when built with 'clients' tag
//
//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json opensvc/moduleset_mariadb.svc.mrm.proxy.json repo serviceplan.csv
var EmbededDbModuleFS embed.FS
