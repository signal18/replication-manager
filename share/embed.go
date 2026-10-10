//go:build !clients
// +build !clients

package share

import (
	"embed"
	_ "embed"
)

//go:embed opensvc/moduleset_mariadb.svc.mrm.db.json opensvc/moduleset_mariadb.svc.mrm.proxy.json opensvc/moduleset_postgres.svc.mrm.db.json app dashboard repo serviceplan.csv whitelist.conf.grafana whitelist.conf.minimal blacklist.conf.template grafana scripts/staging_refresh.sh scripts/dbjobs_new.sh scripts/xtrabackup_bundle.sh scripts/app_job.sh scripts/postgres_job.sh scripts/postgres_start.sh mysql_defaults.cnf plugins/data/enterprise-dochelp-variables.json plugins/data/enterprise-security-issues.json plugins/data/enterprise-replication-issues.json plugins/data/enterprise-workload-issues.json plugins/data/db_distributions.json
var EmbededDbModuleFS embed.FS
