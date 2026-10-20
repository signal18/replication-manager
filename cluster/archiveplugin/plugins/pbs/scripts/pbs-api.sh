#!/usr/bin/env bash
# replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
# Copyright 2017-2026 SIGNAL18 CLOUD SAS
# This source code is licensed under the GNU General Public License, version 3.
#
# Sourced by the pbs-cluster-* scripts: one call of the Proxmox Backup Server API with an
# admin API token. The token comes from the environment (PBS_ADMIN_AUTHID, PBS_ADMIN_SECRET)
# and reaches curl on its stdin, never on a command line. PBS_INSECURE=1 accepts a self-signed
# certificate (prefer PBS_CACERT=<file>).

: "${PBS_ADMIN_AUTHID:?set PBS_ADMIN_AUTHID to an admin API token id, user@realm!token}"
: "${PBS_ADMIN_SECRET:?set PBS_ADMIN_SECRET to its secret}"

# pbs_api METHOD PATH [name=value ...]: the parameters go in the query for GET and DELETE, in a
# form body otherwise. Sets PBS_STATUS (the HTTP status) and PBS_BODY (the answer).
pbs_api() {
	local method=$1 path=$2
	shift 2
	local tls=() args=()
	[ "${PBS_INSECURE:-0}" = 1 ] && tls+=(--insecure)
	[ -n "${PBS_CACERT:-}" ] && tls+=(--cacert "$PBS_CACERT")
	for kv in "$@"; do
		args+=(--data-urlencode "$kv")
	done
	local url="${PBS_URL%/}/api2/json${path}"
	local out
	if [ "$method" = GET ] || [ "$method" = DELETE ]; then
		out=$(printf 'header = "Authorization: PBSAPIToken=%s:%s"\n' "$PBS_ADMIN_AUTHID" "$PBS_ADMIN_SECRET" |
			curl -sS -K - "${tls[@]}" -G -X "$method" "${args[@]}" -w '\n%{http_code}' "$url")
	else
		out=$(printf 'header = "Authorization: PBSAPIToken=%s:%s"\n' "$PBS_ADMIN_AUTHID" "$PBS_ADMIN_SECRET" |
			curl -sS -K - "${tls[@]}" -X "$method" "${args[@]}" -w '\n%{http_code}' "$url")
	fi
	PBS_STATUS=${out##*$'\n'}
	PBS_BODY=${out%$'\n'*}
}

# pbs_name NAME: a cluster name as PBS accepts it in a token or namespace name.
pbs_name() {
	local n
	n=$(printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_' | sed 's/^[.-]*//')
	printf '%s' "${n:-repman}"
}
