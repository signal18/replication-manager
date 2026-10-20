#!/usr/bin/env bash
# replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
# Copyright 2017-2026 SIGNAL18 CLOUD SAS
# This source code is licensed under the GNU General Public License, version 3.
#
# Allows a cluster's archive-pbs token to purge: grants it DatastorePowerUser on the cluster's
# namespace (backup, restore and prune of its own backups; still no namespace change), with an
# admin API token of the PBS (PBS_ADMIN_AUTHID, PBS_ADMIN_SECRET). Then set no-purge = false
# on the archive in replication-manager: its retention policy prunes the archive.
#
# When the admin token may not change ACLs (a hosted PBS), the script prints the request for
# the PBS operator: the exact command that grants it.
#
# usage: pbs-cluster-allow-purge.sh <base-url> <datastore> <cluster> [user]
#   PBS_ADMIN_AUTHID='root@pam!admin' PBS_ADMIN_SECRET=... \
#     ./pbs-cluster-allow-purge.sh https://pbs.example.com:8007 store1 belair
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=pbs-api.sh
. "$here/pbs-api.sh"

[ $# -ge 3 ] || { sed -n '6,17p' "$0"; exit 2; }
PBS_URL=$1
datastore=$2
cluster=$(pbs_name "$3")
user=${4:-repman@pbs}
ns_path="/datastore/$datastore/$cluster"
token="$user!$cluster"

pbs_api PUT /access/acl "path=$ns_path" "role=DatastorePowerUser" "auth-id=$token" "propagate=1"
case "$PBS_STATUS" in
200)
	cat <<EOF
$token may now purge on $ns_path (DatastorePowerUser).
Set no-purge = false on the cluster's archive in replication-manager: its retention policy
(backup-keep-last, -hourly, -daily, -weekly, -monthly, -yearly) prunes the archive.
EOF
	;;
401 | 403)
	cat <<EOF
This admin token may not change ACLs on the PBS (HTTP $PBS_STATUS). Request for the PBS operator:

  Allow the replication-manager token $token to prune its own backups in
  the namespace $cluster of the datastore $datastore:

    proxmox-backup-manager acl update $ns_path DatastorePowerUser --auth-id '$token'

  (DatastorePowerUser: backup, restore and prune of its own backups; no namespace change.
  The token never does more than its user: $user needs DatastorePowerUser there as well.)

Once granted, set no-purge = false on the cluster's archive in replication-manager.
EOF
	exit 3
	;;
*)
	echo "error: grant DatastorePowerUser on $ns_path to $token: HTTP $PBS_STATUS $PBS_BODY" >&2
	exit 1
	;;
esac
