#!/bin/sh
set -eu

CONFIG_FILE="/etc/replication-manager/config.toml"

first_nonempty() {
  for var_name in "$@"; do
    eval "var_value=\${$var_name-}"
    if [ -n "$var_value" ]; then
      printf '%s' "$var_value"
      return 0
    fi
  done
  return 1
}

escape_toml() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

HAS_ENV_CONFIG=0
for env_name in \
  ARBITRATOR_TITLE \
  ARBITRATOR_BIND_ADDRESS \
  ARBITRATOR_DRIVER \
  DB_SERVERS_HOSTS \
  DB_SERVERS_CREDENTIAL \
  MONITORING_DATADIR \
  REPLICATION_MANAGER_ARBITRATOR_TITLE \
  REPLICATION_MANAGER_ARBITRATOR_ARBITRATOR_BIND_ADDRESS \
  REPLICATION_MANAGER_ARBITRATOR_ARBITRATOR_DRIVER \
  REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_HOSTS \
  REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_CREDENTIAL \
  REPLICATION_MANAGER_DEFAULT_MONITORING_DATADIR
do
  eval "env_value=\${$env_name-}"
  if [ -n "$env_value" ]; then
    HAS_ENV_CONFIG=1
    break
  fi
done

if [ "$HAS_ENV_CONFIG" = "1" ]; then
  TITLE=$(first_nonempty ARBITRATOR_TITLE REPLICATION_MANAGER_ARBITRATOR_TITLE || printf 'arbitrator')
  BIND_ADDRESS=$(first_nonempty ARBITRATOR_BIND_ADDRESS REPLICATION_MANAGER_ARBITRATOR_ARBITRATOR_BIND_ADDRESS || printf '0.0.0.0:10001')
  DRIVER=$(first_nonempty ARBITRATOR_DRIVER REPLICATION_MANAGER_ARBITRATOR_ARBITRATOR_DRIVER || printf 'sqlite')
  DB_HOSTS=$(first_nonempty DB_SERVERS_HOSTS REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_HOSTS || printf '')
  DB_CREDENTIAL=$(first_nonempty DB_SERVERS_CREDENTIAL REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_CREDENTIAL || printf '')
  MONITORING_DATADIR=$(first_nonempty MONITORING_DATADIR REPLICATION_MANAGER_DEFAULT_MONITORING_DATADIR || printf '/var/lib/replication-manager')

  if [ "$DRIVER" = "mysql" ] && { [ -z "$DB_HOSTS" ] || [ -z "$DB_CREDENTIAL" ]; }; then
    echo "error: mysql arbitrator backend requires DB_SERVERS_HOSTS and DB_SERVERS_CREDENTIAL (or REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_HOSTS and REPLICATION_MANAGER_ARBITRATOR_DB_SERVERS_CREDENTIAL)" >&2
    exit 1
  fi

  mkdir -p "$MONITORING_DATADIR"

  {
    printf '[arbitrator]\n'
    printf 'title = "%s"\n' "$(escape_toml "$TITLE")"
    printf 'arbitrator-bind-address = "%s"\n' "$(escape_toml "$BIND_ADDRESS")"
    printf 'arbitrator-driver = "%s"\n' "$(escape_toml "$DRIVER")"
    if [ -n "$DB_HOSTS" ]; then
      printf 'db-servers-hosts = "%s"\n' "$(escape_toml "$DB_HOSTS")"
    fi
    if [ -n "$DB_CREDENTIAL" ]; then
      printf 'db-servers-credential = "%s"\n' "$(escape_toml "$DB_CREDENTIAL")"
    fi
    printf '[default]\n'
    printf 'monitoring-datadir = "%s"\n' "$(escape_toml "$MONITORING_DATADIR")"
  } > "$CONFIG_FILE"
fi

exec "$@"
