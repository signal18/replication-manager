#!/bin/bash
# replication-manager ColumnStore image entrypoint (#2002).
#
# ColumnStore needs its own processes before the server: the plugin talks to the DBRM
# (controllernode, port 8616) and the server aborts without it ("DBRM is read only").
# Outside a container systemd starts them; here CMAPI does, with its container process
# dispatcher: CMAPI owns the processes on every node (the UM and each PM, the same image
# and the same entrypoint), and a node joining the cluster restarts them all through it.
# Local storage (each node's dbroot on its own volume): no StorageManager, which is for
# S3 only.
#
# Start: the volume gets the package's files it lacks, CMAPI's configuration is set
# (container dispatcher, no CMAPI failover: OpenSVC and replication-manager place the
# nodes, API key = the cluster's root password), CMAPI starts as mysql like the
# package's units, the local CMAPI starts this node (block map load, processes in
# order, system catalog on the primary), then the official entrypoint runs the server.
#
# Stop (SIGTERM): the server first, then the node through CMAPI, whose workernode stop
# saves the block map (BRM) to the volume; without the save the next start (on the
# other node after a switch) finds no BRM and no ColumnStore table survives. If CMAPI
# does not answer, the processes are stopped in the package's order and the BRM saved
# here.
set -euo pipefail

CS_LOG_DIR=/var/log/mariadb/columnstore
CS_PACKAGE_DIR=/usr/share/columnstore/var-lib-columnstore
CS_PACKAGE_ETC=/usr/share/columnstore/etc-columnstore
CMAPI_DIR=/usr/share/columnstore/cmapi
CMAPI_CONF=/etc/columnstore/cmapi_server.conf
CMAPI_URL=https://127.0.0.1:8640/cmapi/0.4.0
# the cluster's root password: the UM (database path) and the PMs (prov-app-db-credentials)
# both receive it, a node never takes the key of whoever reaches its CMAPI first
CMAPI_KEY="${CMAPI_API_KEY:-${MYSQL_ROOT_PASSWORD:-${MARIADB_ROOT_PASSWORD:-}}}"

log() { echo "$(date '+%F %T') columnstore-entrypoint: $*"; }

# wait until a local port answers, bounded
wait_port() {
	local port="$1" what="$2" i
	for i in $(seq 1 180); do
		if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
			return 0
		fi
		sleep 1
	done
	log "$what did not open port $port after 180 s"
	return 1
}

# one call to the local CMAPI; the key comes from the environment and is never printed
cmapi() {
	local method="$1" path="$2"
	curl -sk --max-time 600 -X "$method" "$CMAPI_URL/$path" \
		-H "x-api-key: $CMAPI_KEY" -H 'Content-Type: application/json' -d '{"timeout": 300}'
}

prepare_volume() {
	mkdir -p "$CS_LOG_DIR" /var/lib/columnstore /etc/columnstore
	# a fresh volume hides the package's files (workernode aborts without local/module,
	# no Columnstore.xml): copy what is missing, never over what the volume holds
	cp -a --update=none "$CS_PACKAGE_DIR/." /var/lib/columnstore/
	cp -a --update=none "$CS_PACKAGE_ETC/." /etc/columnstore/
	chown -R mysql:mysql "$CS_LOG_DIR" /var/lib/columnstore /etc/columnstore
}

# CMAPI's configuration on the volume: container dispatcher, no CMAPI failover, the key
configure_cmapi() {
	if [ -z "$CMAPI_KEY" ]; then
		log "no CMAPI_API_KEY nor root password in the environment: CMAPI takes the first key it receives"
	fi
	CMAPI_KEY="$CMAPI_KEY" gosu mysql "$CMAPI_DIR/python/bin/python3" - "$CMAPI_CONF" <<'PY'
import configparser, os, sys
path = sys.argv[1]
cfg = configparser.ConfigParser()
cfg.read(path)
def put(section, key, value):
    if not cfg.has_section(section):
        cfg.add_section(section)
    cfg[section][key] = value
# cherrypy reads the values as python literals: strings quoted
put('Dispatcher', 'name', "'container'")
put('Dispatcher', 'path', "''")
put('application', 'auto_failover', 'False')
key = os.environ.get('CMAPI_KEY', '')
if key:
    put('Authentication', 'x-api-key', repr(key))
with open(path, 'w') as f:
    cfg.write(f)
PY
}

# CMAPI reverse-resolves every node name (gethostbyaddr: the primary detection, the
# status, the node add) and OpenSVC's cluster DNS answers forward lookups only: each
# container keeps a block of /etc/hosts with its own name and every node of
# Columnstore.xml, resolved forward through the DNS, refreshed in the background (a node
# added later, an address that changed). Without it the primary did not recognise
# itself, started as a secondary (DBRM_Worker2) and never started controllernode.
HOSTS_BEGIN="# columnstore nodes (columnstore-entrypoint.sh)"
HOSTS_END="# end columnstore nodes"
sync_hosts() {
	local names name ip block="" self self_ip
	# the node itself: its own interface address, the DNS does not know a service that
	# is still starting (the node then did not recognise itself as the primary)
	self="$(hostname -f 2>/dev/null || hostname)"
	self_ip="$(ip -4 -o addr show scope global 2>/dev/null | awk '{split($4, a, "/"); print a[1]; exit}' || true)"
	names="$( { grep -oE '<(Node|ModuleHostName[0-9-]+|IPAddr)>[^<]+' /etc/columnstore/Columnstore.xml 2>/dev/null | sed 's/^<[^>]*>//'; } | sort -u || true)"
	# the block out first: its stale entries would answer before the DNS
	# (/etc/hosts is a bind mount: rewritten in place, never replaced)
	if grep -qxF "$HOSTS_BEGIN" /etc/hosts; then
		sed "/^$HOSTS_BEGIN\$/,/^$HOSTS_END\$/d" /etc/hosts >/tmp/hosts.columnstore
		cat /tmp/hosts.columnstore >/etc/hosts
	fi
	if [ -n "$self_ip" ]; then
		block="$self_ip $self"$'\n'
	fi
	for name in $names; do
		case "$name" in
			localhost | 127.* | ::1 | "$self") continue ;;
		esac
		# an IP address needs no entry
		if [[ "$name" =~ ^[0-9.]+$ ]]; then
			continue
		fi
		ip="$(getent ahostsv4 "$name" 2>/dev/null | awk 'NR==1{print $1}' || true)"
		if [ -n "$ip" ]; then
			block="$block$ip $name"$'\n'
		fi
	done
	printf '%s\n%s%s\n' "$HOSTS_BEGIN" "$block" "$HOSTS_END" >>/etc/hosts
}

sync_hosts_loop() {
	while true; do
		sleep 30
		sync_hosts || log "could not refresh the /etc/hosts block of the cluster's nodes"
	done
}

# the node's name in the cluster: its hostname, the OpenSVC service name, which moves
# with it (the UM's own local PM is db1.<cluster>.svc.<clustername>, a PM app its own).
# A fresh Columnstore.xml says 127.0.0.1, which CMAPI resolves to the container's IP:
# adding the node by name then counted it twice and the transaction failed. CMAPI's own
# rename (node_manipulation._replace_localhost, what its add_node does) runs on a fresh
# config; a config already holding a name is left alone.
name_node() {
	local name i out
	name="$(hostname -f 2>/dev/null || hostname)"
	# the service name may not resolve in the first seconds of the container (OpenSVC
	# registers it while the service starts): retried, bounded
	for i in $(seq 1 30); do
		if out="$(cd "$CMAPI_DIR" && NODE_NAME="$name" gosu mysql env PYTHONPATH="$CMAPI_DIR/deps:$CMAPI_DIR" \
			"$CMAPI_DIR/python/bin/python3" - 2>&1 <<'PY'
import os
from cmapi_server.node_manipulation import _replace_localhost
from mcs_node_control.models.node_config import NodeConfig
path = '/etc/columnstore/Columnstore.xml'
nc = NodeConfig()
root = nc.get_current_config_root(path, upgrade=False)
if _replace_localhost(root, os.environ['NODE_NAME']):
    nc.write_config(root, filename=path)
    print('named ' + os.environ['NODE_NAME'])
PY
		)"; then
			[ -n "$out" ] && log "${out##*$'\n'}"
			return 0
		fi
		sleep 2
	done
	log "could not name the node $name, it stays 127.0.0.1 (a single node): ${out##*$'\n'}"
}

cmapi_pid=""
start_cmapi() {
	log "starting CMAPI"
	(cd "$CMAPI_DIR" && exec gosu mysql env PYTHONPATH="$CMAPI_DIR/deps" \
		"$CMAPI_DIR/python/bin/python3" -m cmapi_server >>"$CS_LOG_DIR/cmapi_server.out" 2>&1) &
	cmapi_pid=$!
	wait_port 8640 CMAPI
}

start_node() {
	local i out
	for i in $(seq 1 30); do
		if out="$(cmapi PUT node/start)" && echo "$out" | grep -q timestamp; then
			log "node started by CMAPI"
			return 0
		fi
		log "CMAPI node/start not done yet: ${out:0:200}"
		sleep 5
	done
	log "CMAPI could not start the node"
	return 1
}

# fallback when CMAPI does not answer: stop one process with SIGTERM, bounded, then SIGKILL
stop_proc() {
	local name="$1" wait="$2" i
	pgrep -x "$name" >/dev/null || return 0
	log "stopping $name"
	pkill -TERM -x "$name" || true
	for i in $(seq 1 "$wait"); do
		pgrep -x "$name" >/dev/null || return 0
		sleep 1
	done
	log "$name still running after $wait s, killed"
	pkill -KILL -x "$name" || true
}

# the package's stop order (mariadb-columnstore-stop.sh) and timeouts (its units), then
# the BRM save and the shared memory cleanup of mcs-workernode's ExecStopPost
stop_columnstore_by_hand() {
	local dbrm_timeout
	dbrm_timeout="$(/usr/bin/mcsGetConfig SystemConfig DBRMTimeout 2>/dev/null || true)"
	stop_proc DMLProc 900
	stop_proc DDLProc 2
	stop_proc WriteEngineServer 20
	stop_proc PrimProc 2
	stop_proc controllernode "${dbrm_timeout:-20}"
	stop_proc workernode 750
	if gosu mysql /usr/bin/mcs-savebrm.py; then
		log "BRM saved"
	else
		log "mcs-savebrm.py failed: the BRM of this run is not saved"
	fi
	gosu mysql clearShm >/dev/null 2>&1 || true
}

stop_columnstore() {
	local out=""
	if out="$(cmapi PUT node/shutdown)" && echo "$out" | grep -q timestamp && ! pgrep -x workernode >/dev/null; then
		log "node stopped by CMAPI (BRM saved)"
	else
		log "CMAPI node/shutdown did not stop the node (${out:0:200}): stopping by hand"
		stop_columnstore_by_hand
	fi
	if [ -n "$cmapi_pid" ]; then
		pkill -TERM -f "python3 -m cmapi_server" 2>/dev/null || true
	fi
}

server_pid=""
on_stop() {
	log "stop requested"
	if [ -n "$server_pid" ]; then
		kill -TERM "$server_pid" 2>/dev/null || true
	fi
}

# only when the server is what runs (not for a shell, a client, --version...)
case "${1:-}" in
	mariadbd | mysqld | -*)
		prepare_volume
		configure_cmapi
		log "starting the node $(hostname -f 2>/dev/null || hostname)"
		sync_hosts || log "could not write the /etc/hosts block of the cluster's nodes"
		name_node
		sync_hosts_loop &
		start_cmapi
		start_node
		wait_port 8616 controllernode
		trap on_stop TERM INT
		docker-entrypoint.sh "$@" &
		server_pid=$!
		# wait returns early on a trapped signal: wait again until the server is gone
		rc=0
		while kill -0 "$server_pid" 2>/dev/null; do
			wait "$server_pid" && rc=0 || rc=$?
		done
		log "server stopped (exit $rc)"
		stop_columnstore
		exit "$rc"
		;;
esac

exec docker-entrypoint.sh "$@"
