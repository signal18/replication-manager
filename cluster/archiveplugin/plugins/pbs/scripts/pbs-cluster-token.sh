#!/usr/bin/env bash
# replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
# Copyright 2017-2026 SIGNAL18 CLOUD SAS
# This source code is licensed under the GNU General Public License, version 3.
#
# Creates the PBS side of one cluster's archive-pbs archive, with an admin API token of the
# PBS (PBS_ADMIN_AUTHID, PBS_ADMIN_SECRET in the environment):
#   - the namespace <cluster> in the datastore (kept when it exists);
#   - the user (default repman@pbs, created when missing) granted DatastorePowerUser on that
#     namespace: a token never does more than its user, so allowing purge later is only a
#     token change;
#   - the token <user>!<cluster>, granted DatastoreBackup on that namespace only: backup and
#     restore of its own backups, no prune, no namespace change;
# and prints the archive's settings for replication-manager, no-purge and no-modify on.
# The token's secret is printed once: PBS never shows it again.
#
# usage: pbs-cluster-token.sh <base-url> <datastore> <cluster> [user]
#   PBS_ADMIN_AUTHID='root@pam!admin' PBS_ADMIN_SECRET=... \
#     ./pbs-cluster-token.sh https://pbs.example.com:8007 store1 belair
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=pbs-api.sh
. "$here/pbs-api.sh"

[ $# -ge 3 ] || { sed -n '6,20p' "$0"; exit 2; }
PBS_URL=$1
datastore=$2
cluster=$(pbs_name "$3")
user=${4:-repman@pbs}
ns_path="/datastore/$datastore/$cluster"
token="$user!$cluster"

fail() {
	echo "error: $1: HTTP $PBS_STATUS $PBS_BODY" >&2
	exit 1
}

# the namespace
pbs_api POST "/admin/datastore/$datastore/namespace" "name=$cluster"
case "$PBS_STATUS" in
200) echo "namespace $cluster created in $datastore" ;;
*) printf '%s' "$PBS_BODY" | grep -qi "exist" && echo "namespace $cluster already in $datastore" || fail "create namespace $cluster" ;;
esac

# the user, granted on the namespace what the token may ever need
pbs_api GET "/access/users/$user"
if [ "$PBS_STATUS" != 200 ]; then
	pbs_api POST /access/users "userid=$user" "comment=replication-manager archives"
	[ "$PBS_STATUS" = 200 ] || fail "create user $user"
	echo "user $user created"
fi
pbs_api PUT /access/acl "path=$ns_path" "role=DatastorePowerUser" "auth-id=$user" "propagate=1"
[ "$PBS_STATUS" = 200 ] || fail "grant DatastorePowerUser on $ns_path to $user"

# the token, backup and restore only
pbs_api POST "/access/users/$user/token/$cluster" "comment=replication-manager cluster $3"
[ "$PBS_STATUS" = 200 ] || fail "create token $token (an existing one is deleted on the PBS first)"
secret=$(printf '%s' "$PBS_BODY" | sed -n 's/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "$secret" ] || fail "read the secret of $token"
pbs_api PUT /access/acl "path=$ns_path" "role=DatastoreBackup" "auth-id=$token" "propagate=1"
[ "$PBS_STATUS" = 200 ] || fail "grant DatastoreBackup on $ns_path to $token"

cat <<EOF

archive-pbs settings of cluster $3 (replication-manager, Archives):
  base-url   = ${PBS_URL%/}
  datastore  = $datastore
  namespace  = $cluster
  auth-id    = $token
  no-purge   = true
  no-modify  = true
  secret     = $secret

The secret is shown once: put it in the archive's secret now. The token backs up and
restores its own backups; its retention is the PBS's prune job until
pbs-cluster-allow-purge.sh grants it purge.
EOF
