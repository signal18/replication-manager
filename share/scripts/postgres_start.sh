#!/bin/bash
# replication-manager start script of a PostgreSQL service.
#
# Delivered as the config key APP_START_SCRIPT and run by the template's start command in
# the PostgreSQL container, before PostgreSQL itself:
#
#   1. writes the configuration rendered from the app plan (APP_CONFIGURATOR_SCRIPT, the
#      postgres moduleset) and the main configuration file that includes it;
#   2. on a new primary, lets standbys connect for replication (pg_hba, at initdb);
#   3. on a standby (PG_PRIMARY_HOST set) whose data directory is empty, seeds it from the
#      primary with pg_basebackup -R: an online copy, the standby then follows the primary
#      through WAL streaming;
#   4. starts PostgreSQL through the image entrypoint.
#
# A role change armed by the jobs sidecar (replication-manager.next_start in the data
# directory) is applied first: start as a standby of a new primary, or resynchronise from it
# (pg_rewind, else a new copy).
#
# Environment: POSTGRES_PASSWORD (and POSTGRES_USER), PGDATA, PG_PRIMARY_HOST and
# PG_PRIMARY_PORT for a standby.

set -eu

PGDATA="${PGDATA:-/var/lib/postgresql/data}"
CONF_DIR=/etc/postgresql
PG_USER="${POSTGRES_USER:-postgres}"

log() { echo "[postgres_start] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

# 1. configuration
mkdir -p "$CONF_DIR/replication-manager.d"
if [ -n "${APP_CONFIGURATOR_SCRIPT:-}" ]; then
    printenv APP_CONFIGURATOR_SCRIPT | sh
fi
# include_dir is a file directive: it cannot be passed with -c
# wal_log_hints lets pg_rewind resynchronise a former primary without a full copy;
# wal_level = logical lets the server publish for logical replication subscribers
# max_worker_processes: the configurator sets it to the cores of the plan, which leaves no
# background worker for the logical replication apply and table-sync workers ("out of
# background worker slots" on a 2-core plan): cores + 6, at least the PostgreSQL default 8,
# set after the include so it wins. To move into the configurator rule (collector) and drop
# from here.
cores=$(cat "$CONF_DIR"/replication-manager.d/*.conf 2>/dev/null | sed -n 's/^[[:space:]]*max_worker_processes[[:space:]]*=[[:space:]]*\([0-9]*\).*/\1/p' | tail -1)
workers=$(( ${cores:-2} + 6 ))
[ "$workers" -ge 8 ] || workers=8
printf "include_dir = 'replication-manager.d'\nlisten_addresses = '*'\nwal_log_hints = on\nwal_level = logical\nmax_worker_processes = %s\n" "$workers" > "$CONF_DIR/postgresql.conf"

# 2. a new primary accepts replication connections from the cluster network (the image's
#    default pg_hba only opens the databases, not the replication protocol)
mkdir -p /docker-entrypoint-initdb.d
cat > /docker-entrypoint-initdb.d/10-replication-manager.sh <<'EOS'
echo "host replication all all scram-sha-256" >> "$PGDATA/pg_hba.conf"
EOS

# arm_standby <host> <port>: this server starts as a standby of that primary, data kept
arm_standby() {
    # a conninfo value in single quotes escapes \ and ' with a backslash; the
    # configuration file then doubles the single quotes
    local pw conninfo auto
    pw=$(printf '%s' "${POSTGRES_PASSWORD:-}" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g")
    conninfo="user=$PG_USER password='$pw' host=$1 port=$2"
    auto="$PGDATA/postgresql.auto.conf"
    touch "$auto"
    grep -v '^[[:space:]]*primary_conninfo[[:space:]]*=' "$auto" > "$auto.tmp" || true
    printf "primary_conninfo = '%s'\n" "$(printf '%s' "$conninfo" | sed "s/'/''/g")" >> "$auto.tmp"
    mv "$auto.tmp" "$auto"
    touch "$PGDATA/standby.signal"
    chown postgres:postgres "$auto" "$PGDATA/standby.signal"
    chmod 600 "$auto"
}

# 2b. role change armed by the jobs sidecar on request of replication-manager
#     (switchover, rejoin of a former primary): "<standby|reseed> <host> <port>"
NEXT_START="$PGDATA/replication-manager.next_start"
if [ -s "$NEXT_START" ]; then
    read -r next_mode next_host next_port < "$NEXT_START" || true
    find "$NEXT_START" -delete
    case "${next_mode:-}" in
    reseed)
        # A former primary: its timeline forked from the new primary's. pg_rewind keeps
        # the data and rewinds it to the fork point; when it cannot, the data is copied again.
        rewound=1
        if [ -s "$PGDATA/PG_VERSION" ]; then
            PGPASSWORD="${POSTGRES_PASSWORD:-}" gosu postgres pg_rewind --target-pgdata="$PGDATA" \
                --source-server="host=$next_host port=$next_port user=$PG_USER dbname=postgres" \
                > /tmp/pg_rewind.log 2>&1 || rewound=0
            sed 's/^/[pg_rewind] /' /tmp/pg_rewind.log
        else
            rewound=0
        fi
        if [ "$rewound" = 1 ]; then
            log "armed: rewound to the timeline of $next_host:$next_port, starting as its standby"
            arm_standby "$next_host" "$next_port"
        else
            log "armed: re-seed from $next_host:$next_port, the data directory is cleared"
            find "${PGDATA:?}" -mindepth 1 -delete
            PG_PRIMARY_HOST="$next_host"
            PG_PRIMARY_PORT="$next_port"
        fi
        ;;
    standby)
        log "armed: start as a standby of $next_host:$next_port, data kept"
        arm_standby "$next_host" "$next_port"
        ;;
    *)
        log "armed start ignored, unknown mode: ${next_mode:-}"
        ;;
    esac
fi

# 3. standby seeding
if [ -n "${PG_PRIMARY_HOST:-}" ] && [ ! -s "$PGDATA/PG_VERSION" ]; then
    port="${PG_PRIMARY_PORT:-5432}"
    log "empty data directory: seeding this standby from $PG_PRIMARY_HOST:$port"
    mkdir -p "$PGDATA"
    chown -R postgres:postgres "$PGDATA"
    chmod 700 "$PGDATA"
    tries=0
    until PGPASSWORD="${POSTGRES_PASSWORD:-}" gosu postgres pg_basebackup \
        -h "$PG_PRIMARY_HOST" -p "$port" -U "$PG_USER" -D "$PGDATA" -R -X stream -c fast; do
        tries=$((tries + 1))
        if [ "$tries" -ge 60 ]; then
            log "the primary could not be copied after $tries attempts"
            exit 1
        fi
        log "pg_basebackup failed (attempt $tries), the primary may not be up yet: retry in 5 s"
        # pg_basebackup needs an empty directory: clear what a failed attempt left
        find "${PGDATA:?}" -mindepth 1 -delete
        sleep 5
    done
    log "standby seeded from $PG_PRIMARY_HOST:$port"
fi

# 4. PostgreSQL
exec docker-entrypoint.sh postgres \
    -c "config_file=$CONF_DIR/postgresql.conf" \
    -c "hba_file=$PGDATA/pg_hba.conf" \
    -c "ident_file=$PGDATA/pg_ident.conf"
