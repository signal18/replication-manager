#!/bin/bash
# replication-manager ColumnStore image entrypoint (#2002).
#
# ColumnStore needs its own processes before the server: the plugin talks to the DBRM
# (controllernode, port 8616) and the server aborts without it ("DBRM is read only").
# Outside a container systemd starts them; here they are started in the order of the
# package's own mariadb-columnstore-start.sh and mcs-*.service units, as mysql, then
# the official entrypoint runs the server. Local storage (dbroots on the PM's volume):
# no StorageManager, which is for S3 only.
set -euo pipefail

CS_LOG_DIR=/var/log/mariadb/columnstore
CS_SYSCAT_MARKER=/var/lib/columnstore/.replication-manager-syscat

log() { echo "$(date '+%F %T') columnstore-entrypoint: $*"; }

jemalloc() { ldconfig -p | grep -m1 libjemalloc | awk '{print $NF}'; }

# start one ColumnStore process as mysql, the way its unit does (Type=forking: it
# daemonizes itself)
start_proc() {
	local name="$1"
	shift
	log "starting $name"
	if [ "${CS_PRELOAD_JEMALLOC:-}" = "1" ]; then
		gosu mysql env MALLOC_CONF='' LD_PRELOAD="$(jemalloc)" "$@"
	else
		gosu mysql env MALLOC_CONF='' "$@"
	fi
}

# wait until a local port answers, bounded
wait_port() {
	local port="$1" what="$2" i
	for i in $(seq 1 120); do
		if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
			return 0
		fi
		sleep 1
	done
	log "$what did not open port $port after 120 s"
	return 1
}

start_columnstore() {
	mkdir -p "$CS_LOG_DIR/install" /var/lib/columnstore
	chown -R mysql:mysql "$CS_LOG_DIR" /var/lib/columnstore

	# saved BRM state of a previous run (no-op on a fresh volume), as the unit does
	/usr/bin/mcs-loadbrm.py || log "mcs-loadbrm.py: nothing loaded"

	start_proc workernode /usr/bin/workernode DBRM_Worker1
	start_proc controllernode /usr/bin/controllernode
	wait_port 8616 controllernode
	CS_PRELOAD_JEMALLOC=1 start_proc PrimProc /usr/bin/PrimProc
	CS_PRELOAD_JEMALLOC=1 start_proc WriteEngineServer /usr/bin/WriteEngineServer
	start_proc DMLProc /usr/bin/DMLProc
	start_proc DDLProc /usr/bin/DDLProc

	# the ColumnStore system catalog, once per volume
	if [ ! -f "$CS_SYSCAT_MARKER" ]; then
		local i out rc
		for i in $(seq 1 60); do
			out="$(gosu mysql /usr/bin/dbbuilder 7 2>&1)" && rc=0 || rc=$?
			echo "$out" >>"$CS_LOG_DIR/install/dbbuilder.log"
			# created now, or already there (a volume restored without the marker)
			if [ "$rc" = 0 ] || echo "$out" | grep -qiE "already exist|appears to exist"; then
				gosu mysql touch "$CS_SYSCAT_MARKER"
				log "system catalog ready"
				break
			fi
			sleep 2
		done
		[ -f "$CS_SYSCAT_MARKER" ] || { log "dbbuilder 7 failed, see $CS_LOG_DIR/install/dbbuilder.log"; exit 1; }
	fi
}

# only when the server is what runs (not for a shell, a client, --version...)
case "${1:-}" in
	mariadbd | mysqld | -*)
		start_columnstore
		;;
esac

exec docker-entrypoint.sh "$@"
