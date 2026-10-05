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
printf "include_dir = 'replication-manager.d'\nlisten_addresses = '*'\n" > "$CONF_DIR/postgresql.conf"

# 2. a new primary accepts replication connections from the cluster network (the image's
#    default pg_hba only opens the databases, not the replication protocol)
mkdir -p /docker-entrypoint-initdb.d
cat > /docker-entrypoint-initdb.d/10-replication-manager.sh <<'EOS'
echo "host replication all all scram-sha-256" >> "$PGDATA/pg_hba.conf"
EOS

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
