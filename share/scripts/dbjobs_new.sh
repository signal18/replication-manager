#!/bin/bash
# This script is given as sample and might be overwritten on upgrade
# The real script is auto generated based on compliance json and overwrite by go embed

# %%ENV:GENLINE%%

########################
# Helper Functions    #
########################

# Function to add square brackets to IPv6 addresses if not already present
# Parameter: hostname or IP address
# Returns: original string if IPv4/hostname, or IPv6 with brackets if IPv6
add_ipv6_brackets() {
    local addr="$1"
    # Check if it contains a colon (IPv6) and doesn't already have brackets
    if [[ "$addr" == *:* ]] && [[ "$addr" != \[*\]* ]]; then
        echo "[$addr]"
    else
        echo "$addr"
    fi
}

########################
# Global Configuration #
########################

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPMAN_CLIENT="$SCRIPT_DIR/replication-manager-cli"

# MySQL/MariaDB Configuration
# Note: %%ENV:...%% placeholders are replaced during script generation
readonly USER="%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%"
readonly PASSWORD="$MYSQL_ROOT_PASSWORD"
readonly MYSQL_PORT="%%ENV:SERVER_PORT%%"
readonly MYSQL_SERVER="%%ENV:SERVER_HOST%%"
readonly CLUSTER_NAME="%%ENV:SVC_NAMESPACE%%"
readonly DB_CONN_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"

# Replication Manager Configuration
readonly REPLICATION_MANAGER_ADDR="%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_ADDR%%"
readonly REPLICATION_MANAGER_URL="%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL%%"
readonly REPLICATION_MANAGER_HOST="$(add_ipv6_brackets "%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL_HOST%%")"
readonly REPLICATION_MANAGER_PORT="%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL_PORT%%"

# Paths
readonly MYSQL_CONF="%%ENV:SVC_CONF_ENV_MYSQL_CONFDIR%%"
readonly DATADIR="%%ENV:SVC_CONF_ENV_MYSQL_DATADIR%%"
readonly CLIENT_BASEDIR="%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%"

# Owner for files written into the database volume. The orchestrator gives the
# datadir the database owner UID/GID (prov-db-volume-uid, 1001 for Percona Server
# images), which can differ from the image's "mysql" account; $1 is
# the legacy owner, used when the datadir is missing or owned by root.
db_owner() {
    local owner
    owner=$(stat -c '%u:%g' "$DATADIR" 2>/dev/null)
    if [[ -z "$owner" || "$owner" == 0:* ]]; then
        owner="$1"
    fi
    printf '%s' "$owner"
}

# MariaDB binaries
readonly MARIADB_CLIENT="${CLIENT_BASEDIR}/mariadb"
readonly MARIADB_CHECK="${CLIENT_BASEDIR}/mariadb-check"
readonly MARIADB_DUMP="${CLIENT_BASEDIR}/mariadb-dump"
readonly MARIADB_BACKUP="${CLIENT_BASEDIR}/mariabackup"

# MySQL binaries
readonly MYSQL_CLIENT="${CLIENT_BASEDIR}/mysql"
readonly MYSQL_CHECK="${CLIENT_BASEDIR}/mysqlcheck"
readonly MYSQL_DUMP="${CLIENT_BASEDIR}/mysqldump"
# the image's own xtrabackup first; else the one on the PATH (the bundle injected by prov-db-docker-xtrabackup-img,
# which the container definition puts on the PATH)
XTRABACKUP="${CLIENT_BASEDIR}/xtrabackup"
[[ -x "$XTRABACKUP" ]] || XTRABACKUP="$(command -v xtrabackup || echo "$XTRABACKUP")"
readonly XTRABACKUP
readonly INNODBACKUPEX="${CLIENT_BASEDIR}/innobackupex"

# xtrabackup_undo_args fills the array XB_UNDO_ARGS with --innodb-undo-directory=<value>, the running server's own
# setting, for MySQL 8.0 and 8.4 (the series replication-manager's default_path.cnf has a version group for). xtrabackup
# reads the [mysqld] group of my.cnf but not the version specific [mysqld-8.0] / [mysqld-8.4] groups, and the
# default_path.cnf puts the undo tablespaces under .system/innodb/undo in [mysqld] but keeps them in the datadir for
# MySQL 8 (it does not scan hidden folders, #1857): without the server's real setting xtrabackup looks in the wrong
# place and stops with "Cannot create .../undo_001 because ./undo_001 already uses Space ID" (xb_load_tablespaces
# error 110). The value is passed AS IT IS (./ for MySQL 8 here), never turned into an absolute path, and as ONE
# argument (an array, so a space or a glob in it is not split or expanded): xtrabackup writes it into backup-my.cnf, and
# an absolute path would point a server started from that backup at the live server's undo files. The array stays empty
# for another series, or when the server cannot be asked (a warning, with no secret in it, is posted to the job log).
# NOTE: share/scripts/tests/xtrabackup_undo/*.sh extract this function with sed ('/^xtrabackup_undo_args() {/,/^}/'): keep its
# name, and its closing brace alone at the start of a line.
xtrabackup_undo_args() {
    local version undo
    XB_UNDO_ARGS=()
    if ! version=$($BINARY_CLIENT -N -B -e "SELECT @@version" 2>/dev/null); then
        send_lines_to_api "Cannot read the server version: the xtrabackup backup runs without --innodb-undo-directory." "xtrabackup" "$LVL_WARN"
        return 0
    fi
    if [[ -z "$version" ]]; then
        send_lines_to_api "The server version query returned nothing: the xtrabackup backup runs without --innodb-undo-directory." "xtrabackup" "$LVL_WARN"
        return 0
    fi
    case "$version" in
    8.0.* | 8.4.*) ;;
    *) return 0 ;;
    esac
    if ! undo=$($BINARY_CLIENT -N -B -e "SELECT @@innodb_undo_directory" 2>/dev/null); then
        send_lines_to_api "Cannot read innodb_undo_directory: the xtrabackup backup runs without --innodb-undo-directory." "xtrabackup" "$LVL_WARN"
        return 0
    fi
    if [[ -z "$undo" ]]; then
        send_lines_to_api "innodb_undo_directory is empty on this server: the xtrabackup backup runs without --innodb-undo-directory." "xtrabackup" "$LVL_WARN"
        return 0
    fi
    XB_UNDO_ARGS=("--innodb-undo-directory=$undo")
    return 0
}

# Network Configuration
SOCAT_BIND="$(add_ipv6_brackets "%%ENV:SERVER_IP%%")"
if [[ "$SOCAT_BIND" == \[*\]* ]]; then
    SOCAT_BIND="$SOCAT_BIND,ipv6only=0"
fi
readonly SOCAT_BIND

readonly SST_RECEIVER_PORT="%%ENV:SVC_CONF_ENV_SST_RECEIVER_PORT%%"

# Logs
readonly AUDITLOG="%%ENV:SVC_CONF_ENV_AUDIT_LOG%%"
readonly SQLERRORLOG="%%ENV:SVC_CONF_ENV_SQL_ERROR_LOG%%"
readonly ERRORLOG="%%ENV:SVC_CONF_ENV_ERROR_LOG%%"
readonly SLOWLOG="%%ENV:SVC_CONF_ENV_SLOW_LOG%%"

# Directories
readonly BACKUPDIR="${DATADIR}/.system/backup"
readonly TMP_DIR="%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%"
readonly LOG_DIR="${TMP_DIR}"
readonly CHECKPOINT_DIR="${TMP_DIR}/checkpoints"
readonly LOCK_DIR="${TMP_DIR}/locks"

# Constants
readonly BATCH_SIZE=5
readonly MAX_RETRIES=3
readonly JOB_STATE_MAX_RETRIES=5
readonly API_LOG_FILE="${LOG_DIR}/api_calls.log"
readonly LOG_MAX_SIZE=1048576  # 1MB

# Job dispatch mode: "sql" (poll jobs table) or "api" (check cookies via API)
# Container mode: %%ENV:SVC_CONF_ENV_JOBS_MODE%% is substituted at tarball build
# SSH mode: REPLICATION_MANAGER_JOBS_MODE is exported by GetSshEnv()
# Resolve jobs mode: env var (SSH mode) → template placeholder (container mode) → default "sql"
_JOBS_MODE="${REPLICATION_MANAGER_JOBS_MODE:-%%ENV:SVC_CONF_ENV_JOBS_MODE%%}"
# If template was not substituted, fall back to sql
if [[ "$_JOBS_MODE" == *"%%ENV:"* ]]; then
    _JOBS_MODE="sql"
fi
readonly JOBS_MODE="$_JOBS_MODE"

# Job types
readonly -a JOBS=(
    "xtrabackup" "mariabackup" "errorlog" "slowquery"
    "auditlog" "sqlerrorlog" "zfssnapback" "optimize"
    "reseedxtrabackup" "reseedmariabackup"
    "flashbackxtrabackup" "flashbackmariabackup"
    "stop" "restart" "start"
)

# Logging levels
readonly LVL_ERROR="ERROR"
readonly LVL_WARN="WARN"
readonly LVL_INFO="INFO"
readonly LVL_DEBUG="DEBUG"

# Binary selection (determined later)
BINARY_CLIENT=""
BINARY_CHECK=""
BINARY_DUMP=""

# Partial restore status tracking
PR_STATUS=0
PR_LOG=""
PR_SKIPPED=()
PR_RUN_ID=""
PR_DEFS=""
PR_IS_REPLICA=0
PR_EVENTS_READABLE=1
PR_FILEBASE=""
PR_NEWFILES=""

TOKEN=""

# OSX support
export PATH="$PATH:/usr/local/bin"

########################
# Binary Selection     #
########################

select_database_binaries() {
    if [[ -x "$MARIADB_CLIENT" ]]; then
        # Use command-line params to preserve existing my.cnf SSL/TLS settings
        BINARY_CLIENT="$MARIADB_CLIENT $DB_CONN_PARAMETERS"
        BINARY_CHECK="$MARIADB_CHECK"
        BINARY_DUMP="$MARIADB_DUMP"
        send_lines_to_api "Using MariaDB binaries." "main" "$LVL_DEBUG"
    elif [[ -x "$MYSQL_CLIENT" ]]; then
        # Use command-line params to preserve existing my.cnf SSL/TLS settings
        BINARY_CLIENT="$MYSQL_CLIENT $DB_CONN_PARAMETERS"
        BINARY_CHECK="$MYSQL_CHECK"
        BINARY_DUMP="$MYSQL_DUMP"
        send_lines_to_api "Using MySQL binaries." "main" "$LVL_DEBUG"
    else
        send_lines_to_api "Neither MariaDB nor MySQL binaries available. Exiting." "main" "$LVL_ERROR"
        return 1
    fi
}
########################
# Utility Functions    #
########################

# Ensure required directories exist
ensure_directories() {
    local -a dirs=("$LOG_DIR" "$CHECKPOINT_DIR" "$LOCK_DIR")
    for dir in "${dirs[@]}"; do
        mkdir -p "$dir" || {
            echo "ERROR: Failed to create directory: $dir" >&2
            return 1
        }
    done
}

# Validate required environment variables
validate_environment() {
    local -a required_vars=(
        "MYSQL_ROOT_PASSWORD"
        "MYSQL_SERVER"
        "MYSQL_PORT"
        "CLUSTER_NAME"
    )

    local missing=()
    for var in "${required_vars[@]}"; do
        if [[ -z "${!var:-}" ]]; then
            missing+=("$var")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        echo "ERROR: Missing required environment variables: ${missing[*]}" >&2
        return 1
    fi
}

########################
# Function Definitions #
########################

pad_pkcs7() {
    local data="$1"
    local blocksize=32
    local len=$(printf "%s" "$data" | wc -c)
    local pad_len=$((blocksize - (len % blocksize)))
    local padding=$(printf "%${pad_len}s" | tr ' ' '\x01')
    printf "%s%s" "$data" "$padding"
}

derive_key() {
    local password="${1:-$MYSQL_ROOT_PASSWORD}"
    local key=$(echo -n "$password" | sha256sum | awk '{print $1}')
    echo "$key"
}

derive_iv() {
    local password="${1:-$MYSQL_ROOT_PASSWORD}"
    local iv=$(echo -n "$password" | md5sum | awk '{print $1}')
    echo "$iv"
}

# Function to encrypt data using AES-256 in CBC mode
encrypt_data() {
    local data="$1"
    local password="${2:-$MYSQL_ROOT_PASSWORD}"
    local key=$(derive_key "$password")
    local iv=$(derive_iv "$password")
    local padded=$(pad_pkcs7 "$data")
    local encrypted=$(echo -n "$padded" | openssl aes-256-cbc -a -nosalt -K "$key" -iv "$iv" | tr -d '\n')
    echo "$encrypted"
}

########################
# HTTP Helper Functions #
########################

# Determine if port requires SSL/TLS
is_ssl_port() {
    local port="$1"
    [[ "$port" == "443" || "$port" == "10005" ]]
}

# Extract HTTP status code from response
extract_http_code() {
    local response="$1"
    echo "$response" | grep -i "^HTTP" | head -n1 | awk '{print $2}'
}

# Extract body from HTTP response
extract_http_body() {
    local response="$1"
    echo "$response" | awk 'BEGIN{body=0} /^(\r)?$/ {body=1; next} body {print}' | tr -d '\r'
}

# Send HTTP/HTTPS request (consolidated)
# Usage: send_http_request "METHOD" "host" "port" "endpoint" ["data"] ["accept_header"] ["auth_token"] ["timeout"]
send_http_request() {
    local method="$1"
    local host="$2"
    local port="$3"
    local endpoint="$4"
    local data="${5:-}"
    local accept="${6:-application/json}"
    local auth_token="${7:-}"
    local timeout="${8:-60}"  # Default 60 seconds timeout

    # Default to port 10005 if not specified
    if [[ -z "$port" || "$port" == "$host" ]]; then
        port=10005
    fi

    # Use HTTP/1.0 for binary downloads to avoid chunked transfer encoding corruption
    # Use HTTP/1.1 for JSON/text responses to benefit from persistent connections
    local http_version="HTTP/1.1"
    if [[ "$accept" == "application/octet-stream" ]]; then
        http_version="HTTP/1.0"
    fi

    # Build request (use original host with brackets for HTTP Host header)
    local request="$method $endpoint $http_version\r\nHost: $host\r\n"
    request+="Accept: $accept\r\n"

    # Add Authorization header if token provided
    if [[ -n "$auth_token" ]]; then
        request+="Authorization: Bearer $auth_token\r\n"
    fi

    if [[ -n "$data" ]]; then
        request+="Content-Type: application/json\r\n"
        request+="Content-Length: ${#data}\r\n"
        request+="\r\n$data"
    else
        request+="\r\n"
    fi

    # Choose protocol based on port (IPv6 brackets are REQUIRED for socat)
    local socat_target
    # Only use connect-timeout, do NOT use readbytes (it truncates large files)
    local socat_opts="connect-timeout=$timeout"

    if is_ssl_port "$port"; then
        socat_target="OPENSSL:$host:$port,verify=0,$socat_opts"
    else
        socat_target="TCP:$host:$port,$socat_opts"
    fi

    echo -en "$request" | socat - "$socat_target" 2> >(grep -v "refusing to set empty SNI host name" >&2)
}

########################
# API Functions        #
########################

# Send HTTP GET request (reuses send_http_request, passes TOKEN if available)
send_http_get() {
    local host="$1"
    local port="$2"
    local endpoint="$3"
    send_http_request "GET" "$host" "$port" "$endpoint" "" "application/json" "$TOKEN"
}

# Send authenticated HTTP GET request with Bearer token
# Usage: send_http_get_authenticated "host" "port" "endpoint" "token"
send_http_get_authenticated() {
    local host="$1"
    local port="$2"
    local endpoint="$3"
    local token="$4"

    send_http_request "GET" "$host" "$port" "$endpoint" "" "application/json" "$token"
}

# Generic function to send encrypted data to API endpoint
# Usage: send_encrypted_api_request "host" "port" "/api/path" "raw_data" ["password"]
send_encrypted_api_request() {
    local host="$1"
    local port="$2"
    local api_endpoint="$3"
    local raw_data="$4"
    local password="${5:-$MYSQL_ROOT_PASSWORD}"

    # Default to port 10005 if not specified
    if [[ -z "$port" || "$port" == "$host" ]]; then
        port=10005
    fi

    # Encrypt the data if provided
    local encrypted_data=""
    if [[ -n "$raw_data" ]]; then
        encrypted_data=$(encrypt_data "$raw_data" "$password")
    fi

    local json_data="{\"data\":\"$encrypted_data\"}"
    send_http_request "POST" "$host" "$port" "$api_endpoint" "$json_data" "application/json" "$TOKEN"
}

# Rotate log file if it exceeds size limit
rotate_log_file() {
    local log_file="$1"
    local max_size="${2:-1048576}"  # Default 1MB

    if [[ ! -f "$log_file" ]]; then
        return 0
    fi

    local filesize=$(stat -c%s "$log_file" 2>/dev/null || echo 0)
    if ((filesize > max_size)); then
        cp -f "$log_file" "${log_file}.bak"
        : > "$log_file"
    fi
}

# Send data to API with retry logic
# Usage: send_to_api_with_retry "host" "port" "/api/path" "raw_data" ["max_retries"] ["password"] ["log_file"]
send_to_api_with_retry() {
    local api_host="$1"
    local api_port="$2"
    local api_endpoint="$3"
    local raw_data="$4"
    local max_retries="${5:-3}"
    local password="${6:-$MYSQL_ROOT_PASSWORD}"
    local log_file="${7:-$LOG_DIR/api_calls.log}"

    local attempt=0

    while ((attempt < max_retries)); do
        local response=$(send_encrypted_api_request "$api_host" "$api_port" "$api_endpoint" "$raw_data" "$password")
        local http_code=$(extract_http_code "$response")

        if [[ "$http_code" == "200" ]]; then
            return 0
        fi

        ((attempt++))
        [[ $attempt -lt $max_retries ]] && sleep 2
    done

    # Log failure
    mkdir -p "$(dirname "$log_file")"
    rotate_log_file "$log_file"

    {
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] API call failed after $max_retries attempts"
        echo "Destination: $api_host:$api_port Endpoint: $api_endpoint"
        echo "Response: $response"
        echo "---"
    } >> "$log_file"

    return 1
}

# Check if a specific log level is enabled
# Usage: check_log_level "cluster" "taskname" "log_level"
check_log_level() {
    local cluster="$1"
    local taskname="$2"
    local log_level="$3"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    local endpoint="/api/clusters/${cluster}/jobs-log-level/${taskname}/${log_level}"
    local response=$(send_encrypted_api_request "$api_host" "$api_port" "$endpoint" "")

    local http_code=$(extract_http_code "$response")
    local body=$(extract_http_body "$response" | tr -d '\n')

    if [[ "$http_code" == "200" && "$body" == "true" ]]; then
        return 0
    elif [[ "$http_code" == "500" && "$body" == "false" ]]; then
        return 1
    else
        return 2
    fi
}

# Send log lines to API (checks log level first)
# Usage: send_lines_to_api "log_lines" "job_name" "log_level"
send_lines_to_api() {
    local lines="$1"
    local job="${2:-main}"
    local level="${3:-$LVL_DEBUG}"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    check_log_level "$CLUSTER_NAME" "$job" "$level" || return 0

    local data="{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\",\"secret\":\"$MYSQL_ROOT_PASSWORD\",\"log\":\"$lines\",\"level\":\"$level\"}"
    local api_endpoint="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/write-log/$job"

    send_to_api_with_retry "$api_host" "$api_port" "$api_endpoint" "$data" "$MAX_RETRIES"
}

# Check if a specific task is needed
# Usage: check_task_needs "cluster" "server" "port" "taskname"
# Returns: 0=needed, 1=not needed, 2=error
check_task_needs() {
    local cluster="$1"
    local server="$2"
    local port="$3"
    local taskname="$4"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/needs/${taskname}"
    local response=$(send_encrypted_api_request "$api_host" "$api_port" "$endpoint" "{\"server\":\"$server:$port\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    local http_code=$(extract_http_code "$response")
    local body=$(extract_http_body "$response" | tr -d '\n')

    if [[ "$http_code" == "200" && "$body" == "true" ]]; then
        return 0
    elif [[ "$http_code" == "500" && "$body" == "false" ]]; then
        return 1
    else
        return 2
    fi
}

# Get receiver address from repman API for tasks that need socat streaming.
# Sets RECEIVER_ADDRESS variable. Returns 0 on success.
# Usage: get_task_receiver "cluster" "server" "port" "taskname"
get_task_receiver() {
    local cluster="$1"
    local server="$2"
    local port="$3"
    local taskname="$4"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/receive-task/${taskname}"
    local response=$(send_encrypted_api_request "$api_host" "$api_port" "$endpoint" "{\"server\":\"$server:$port\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    local http_code=$(extract_http_code "$response")
    local body=$(extract_http_body "$response" | tr -d '\n')

    if [[ "$http_code" == "200" ]]; then
        if [[ "$body" == "NO_RECEIVER_NEEDED" ]]; then
            RECEIVER_ADDRESS=""
            return 0
        fi
        local rcv_port=$(echo "$body" | sed -n 's/RECEIVER_PORT=//p')
        if [[ -n "$rcv_port" ]]; then
            RECEIVER_ADDRESS="${api_host}:${rcv_port}"
            return 0
        fi
    fi
    RECEIVER_ADDRESS=""
    return 1
}

# Report job state to repman API (api mode only).
# Usage: report_job_state "taskname" "state"
# States: processing, done, error, waiting
report_job_state() {
    local taskname="$1"
    local jobstate="$2"
    # Optional: a JSON object (e.g. PARTIAL_RESTORE_JSON) merged into the
    # POST body under "restore", read by handlerMuxServerJobState
    # (server/api_database.go) for a physical reseed/flashback task's "done"
    # report -- API mode's only transport for this metadata, since there is
    # no jobs-table payload column to write it to in this mode.
    local restore_json="$3"

    # An empty state means the caller has a bug (e.g. an unset result
    # variable) — the route requires a non-empty {jobstate} path segment, so
    # sending this would just 404 against a URL like .../job-state/task/.
    # Fail fast locally instead of spending retries on a request that can
    # never succeed.
    if [[ -z "$jobstate" ]]; then
        send_lines_to_api "report_job_state called with empty state for $taskname" "$taskname" "$LVL_ERROR"
        return 1
    fi

    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"
    local endpoint="/api/clusters/${CLUSTER_NAME}/servers/${MYSQL_SERVER}/${MYSQL_PORT}/actions/job-state/${taskname}/${jobstate}"
    local data="{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\",\"secret\":\"$MYSQL_ROOT_PASSWORD\""
    if [[ -n "$restore_json" ]]; then
        data="${data},\"restore\":${restore_json}"
    fi
    data="${data}}"

    # Retry like send_lines_to_api already does via send_to_api_with_retry.
    # done/error are terminal — a dropped report there is what leaves a job
    # stuck with no resolution (the #1690 symptom) — so they get a larger
    # budget to ride out restart/startup timing. processing/waiting use the
    # same budget as log lines; losing one of those doesn't strand the job,
    # since the terminal report (or startup reconciliation) still resolves
    # it later.
    local retries="$MAX_RETRIES"
    case "$jobstate" in
        done|error) retries="$JOB_STATE_MAX_RETRIES" ;;
    esac

    if send_to_api_with_retry "$api_host" "$api_port" "$endpoint" "$data" "$retries"; then
        return 0
    fi

    send_lines_to_api "Failed to report job state $jobstate for $taskname after $retries attempts" "$taskname" "$LVL_ERROR"
    return 1
}

##################################
# Print Defaults Functions       #
##################################

# Login using secret
# Usage: secret_login "cluster" "server" "port" "encrypted_secret"
# Returns: token on success, empty string on failure
# Exit codes: 0=success, 1=wrong credentials, 2=API error
secret_login() {
    local cluster="$1"
    local server="$2"
    local port="$3"
    local encrypted_secret="$(encrypt_data "{\"server\":\"$server:$port\", \"secret\":\"$MYSQL_ROOT_PASSWORD\"}")"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    # Build endpoint
    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/secret-login"

    # Prepare JSON payload
    local json_data="{\"data\":\"$encrypted_secret\"}"

    # Send request
    local response=$(send_http_request "POST" "$api_host" "$api_port" "$endpoint" "$json_data")
    local http_code=$(extract_http_code "$response")
    local body=$(extract_http_body "$response")

    # Handle response codes
    if [[ "$http_code" == "200" ]]; then
        # Extract token from JSON response
        local token=$(echo "$body" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

        if [[ -n "$token" ]]; then
            echo "$token"
            return 0
        else
            return 2  # Failed to parse token
        fi
    elif [[ "$http_code" == "403" || "$http_code" == "401" ]]; then
        # Wrong credentials
        return 1
    else
        # Other error
        return 2
    fi
}

# resolve_dbu_cgroup: locate the cgroup v2 directory whose memory.current /
# cpu.stat / io.stat describe this database's resource use. Two sources,
# orchestrator-agnostic:
#   1. /svc-cgroup -- an explicit read-only bind the orchestrator provides
#      (OpenSVC binds the service pg slice there). Preferred when present.
#   2. Auto-discovery via the database process's own cgroup: find mariadbd/mysqld
#      and read /proc/<pid>/cgroup (a single "0::<path>" line in cgroup v2), then
#      /sys/fs/cgroup<path>. Works on-premise (the job runs on the host, so it
#      sees the DB process and the host cgroupfs) and under Kubernetes when the
#      pod shares its PID namespace and mounts the host cgroupfs.
# Echoes the directory, or nothing when neither source is usable (caller skips).
resolve_dbu_cgroup() {
    if [[ -r /svc-cgroup/memory.current ]]; then
        echo /svc-cgroup
        return 0
    fi
    # the OpenSVC cgroup tree bound at /svc-cgroup-root (a namespace or service name systemd
    # escapes, a dash is \x2d, cannot be bound as the exact slice): the slice by decoded name
    if [[ -d /svc-cgroup-root && -n "${REPLICATION_MANAGER_CLUSTER_NAME:-}" ]]; then
        local ns="$REPLICATION_MANAGER_CLUSTER_NAME" svc="${REPLICATION_MANAGER_HOST_NAME%%.*}" d s
        # The REAL slice is the systemd-escaped spelling (a dash is \x2d, kept literally in
        # the directory name). Next to it the tree may hold EMPTY look-alikes -- the plain
        # dashed name and the backslash-less one (om3 drops the backslash of a bind it
        # cannot spell, and a bind creates the cgroup it names): no processes, no memory
        # controller, cpu.stat at zero. Matched by decoded name the phantom came first and
        # the PostgreSQL sensors reported nothing (preprod 2026-10-07). The escaped path is
        # tried first; a decoded match must carry a readable memory.current.
        s="/svc-cgroup-root/opensvc-ns.${ns//-/\\x2d}.slice/opensvc-ns.${ns//-/\\x2d}-svc.${svc//-/\\x2d}.slice"
        [[ -r "$s/memory.current" ]] && { printf '%s\n' "$s"; return 0; }
        for d in /svc-cgroup-root/opensvc-ns.*.slice; do
            [[ "${d##*/}" == "opensvc-ns.${ns//-/\\x2d}.slice" ]] || continue
            for s in "$d"/opensvc-ns.*-svc.*.slice; do
                [[ "${s##*/}" == "opensvc-ns.${ns//-/\\x2d}-svc.${svc//-/\\x2d}.slice" && -r "$s/memory.current" ]] && { printf '%s\n' "$s"; return 0; }
            done
        done
    fi
    local pid sub base
    pid=$(pgrep -x mariadbd 2>/dev/null | head -1)
    [[ -z "$pid" ]] && pid=$(pgrep -x mysqld 2>/dev/null | head -1)
    [[ -n "$pid" ]] || return 0
    sub=$(awk -F: '$1=="0"{print $3; exit}' "/proc/$pid/cgroup" 2>/dev/null)
    [[ -n "$sub" ]] || return 0
    # Read through the DB process's own mount view first: this reaches the
    # database container's cgroup under Kubernetes (via a shared PID namespace)
    # AND the host cgroupfs on-premise (where root is the host). Fall back to the
    # host cgroupfs path directly.
    for base in "/proc/$pid/root/sys/fs/cgroup" "/sys/fs/cgroup"; do
        [[ -r "${base}${sub}/memory.current" ]] && { echo "${base}${sub}"; return 0; }
    done
}

# read_net_counters: "<rx_octets> <tx_octets>" summed over every interface but lo, from
# the database process's own network namespace view when the process is visible
# (/proc/<pid>/net/dev), else this process's (/proc/net/dev). Both resolve to the same
# pod interface under an orchestrator and to the host NICs on premise. Echoes "0 0" when
# nothing is readable so the caller never breaks.
read_net_counters() {
    local pid f=/proc/net/dev
    pid=$(pgrep -x mariadbd 2>/dev/null | head -1)
    [[ -z "$pid" ]] && pid=$(pgrep -x mysqld 2>/dev/null | head -1)
    [[ -n "$pid" && -r "/proc/$pid/net/dev" ]] && f="/proc/$pid/net/dev"
    awk -F'[: ]+' 'NR>2 && $2!="lo" {rx+=$3; tx+=$11} END{printf "%d %d\n", rx+0, tx+0}' "$f" 2>/dev/null || echo "0 0"
}

# collect_dbu: thin DBU sensor. Reads the database cgroup (see resolve_dbu_cgroup:
# an orchestrator bind at /svc-cgroup, or the DB process's own cgroup discovered
# via /proc) plus the datadir df, and pushes the four raw per-axis maxima to
# repman, which computes the DBU (normalise/pivot/binding) so the client DB CPU
# is never spent on it. Runs once per dbjobs_new invocation (~60s launcher
# cadence). cpu/io are rates vs the previous run's cumulative counters, persisted
# in a checkpoint. Fail-soft: any missing piece just skips the push, never breaks
# the job run.
# read_cgroup_wait_counters <cgroup dir>: the cumulative wait counters of the service's
# cgroup v2 as one JSON object -- cpu.stat nr_periods/nr_throttled/throttled_usec (the
# quota refusing cycles) and the "some"/"full" totals (microseconds) of cpu.pressure,
# io.pressure, memory.pressure (PSI: time tasks were stalled). A missing file reads as 0.
read_cgroup_wait_counters() {
    local cg="$1"
    local np nt tu
    np=$(awk '/^nr_periods/{print $2}' "$cg/cpu.stat" 2>/dev/null)
    nt=$(awk '/^nr_throttled/{print $2}' "$cg/cpu.stat" 2>/dev/null)
    tu=$(awk '/^throttled_usec/{print $2}' "$cg/cpu.stat" 2>/dev/null)
    psi() { awk -v k="$2" '$1==k {for(i=2;i<=NF;i++) if($i ~ /^total=/){sub("total=","",$i); print $i}}' "$1" 2>/dev/null; }
    local cs cf is if_ ms mf
    cs=$(psi "$cg/cpu.pressure" some); cf=$(psi "$cg/cpu.pressure" full)
    is=$(psi "$cg/io.pressure" some);  if_=$(psi "$cg/io.pressure" full)
    ms=$(psi "$cg/memory.pressure" some); mf=$(psi "$cg/memory.pressure" full)
    printf '{"cpuNrPeriods":%d,"cpuNrThrottled":%d,"cpuThrottledUsec":%d,"cpuPressureSomeUsec":%d,"cpuPressureFullUsec":%d,"ioPressureSomeUsec":%d,"ioPressureFullUsec":%d,"memPressureSomeUsec":%d,"memPressureFullUsec":%d}' \
        "${np:-0}" "${nt:-0}" "${tu:-0}" "${cs:-0}" "${cf:-0}" "${is:-0}" "${if_:-0}" "${ms:-0}" "${mf:-0}"
}

collect_dbu() {
    local cg
    cg=$(resolve_dbu_cgroup)
    [[ -n "$cg" && -r "$cg/memory.current" ]] || return 0   # no readable cgroup -> skip

    local now_epoch mem cpu_usec io_ops disk
    now_epoch=$(date +%s)
    mem=$(cat "$cg/memory.current" 2>/dev/null || echo 0)
    cpu_usec=$(awk '/^usage_usec/{print $2}' "$cg/cpu.stat" 2>/dev/null || echo 0)
    # io: sum rios+wios across all block devices (operation counts -> iops)
    io_ops=$(awk '{for(i=1;i<=NF;i++){if($i ~ /^rios=/){sub("rios=","",$i);r+=$i} if($i ~ /^wios=/){sub("wios=","",$i);w+=$i}}} END{printf "%d", r+w+0}' "$cg/io.stat" 2>/dev/null || echo 0)
    # disk: sum df used over mounts UNDER the datadir only (statfs, no du); this
    # excludes host bind-mounts (e.g. zoneinfo) and the initdb volume.
    disk=$(df -B1 2>/dev/null | awk -v d="$DATADIR" 'NR>1 && $NF ~ ("^" d) {s+=$3} END{printf "%d", s+0}')

    local ckpt="$CHECKPOINT_DIR/dbu.checkpoint"
    local prev_epoch="" prev_cpu="" prev_io=""
    [[ -s "$ckpt" ]] && read -r prev_epoch prev_cpu prev_io < "$ckpt"
    echo "$now_epoch $cpu_usec $io_ops" > "$ckpt"

    # First run (no baseline) or clock skew -> just seed the checkpoint, no push.
    [[ -z "$prev_epoch" ]] && return 0
    local dt=$((now_epoch - prev_epoch))
    ((dt <= 0)) && return 0

    local cpu_cores io_iops
    cpu_cores=$(awk -v c="$cpu_usec" -v p="$prev_cpu" -v dt="$dt" 'BEGIN{printf "%.4f", (c-p)/(dt*1000000)}')
    io_iops=$(awk -v c="$io_ops" -v p="$prev_io" -v dt="$dt" 'BEGIN{printf "%.4f", (c-p)/dt}')

    local ws we
    ws=$(date -u -d "@$prev_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
    we=$(date -u -d "@$now_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

    # Internal network: cumulative rx/tx octets of the database's own interfaces, read
    # through the DB process's network namespace (/proc/<pid>/net/dev): the pod eth0 under
    # an orchestrator (the jobs container shares the pod netns), the host NICs on premise.
    # Raw counters only -- repman derives the Mb/s and absorbs resets (cluster_net.go).
    local net_rx net_tx
    read -r net_rx net_tx < <(read_net_counters)
    # cgroup waits: cpu.stat quota throttling and the PSI some/full totals (cpu, io,
    # memory), raw cumulative counters -- repman rates them against its previous sample
    # (srv_cgroup_wait.go) and graphs them as dbu.<cluster>.<host>.wait_*. They say
    # whether the service WAITED, what the CPU usage alone never shows. Engine-agnostic.
    local wait_json
    wait_json=$(read_cgroup_wait_counters "$cg")
    local data="{\"windowStart\":\"$ws\",\"windowEnd\":\"$we\",\"memMaxBytes\":$mem,\"cpuMaxCores\":$cpu_cores,\"ioMaxIops\":$io_iops,\"diskMaxBytes\":$disk,\"wait\":$wait_json,\"netRxBytes\":${net_rx:-0},\"netTxBytes\":${net_tx:-0}}"
    local endpoint="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/dbu"
    send_http_request "POST" "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$endpoint" "$data" "application/json" "$TOKEN" >/dev/null 2>&1 || true
}

# Fetch config receiver information
fetch_config_receiver() {
    local urlpost="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/config-receiver"
    local response

    # Use authenticated request if TOKEN is available
    if [[ -n "$TOKEN" ]]; then
        response=$(send_http_get_authenticated "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$urlpost" "$TOKEN")
    else
        response=$(send_http_get "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$urlpost")
    fi

    local http_code=$(extract_http_code "$response")

    if [[ "$http_code" != "200" ]]; then
        send_lines_to_api "ERROR: Failed to fetch config receiver, HTTP $http_code" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    extract_http_body "$response"
}

# Check if config refresh is needed
need_refresh_config() {
    local urlpost="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/need-config-refresh"
    local response=$(send_http_get "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$urlpost")
    local http_code=$(extract_http_code "$response")

    [[ "$http_code" == "200" ]]
}

# Fetch and extract configuration
# Usage: fetch_and_extract_config "extract_dir" ["token"]
# Uses cookie-based push mechanism for async config delivery via SST
fetch_and_extract_config() {
    local extract_dir="$1"
    local token="${2:-}"
    local config_sender_url="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/config-dummy-sender"

    send_lines_to_api "Requesting config send via cookie-based push mechanism..." "print-defaults" "$LVL_DEBUG"

    # Remove existing directory and create new one
    rm -rf "$extract_dir"
    mkdir -p "$extract_dir"

    # Step 1: POST to queue the config send and get SST port info
    local request_timeout=10  # API should respond quickly (just queues request)

    send_lines_to_api "Connecting to: $REPLICATION_MANAGER_HOST:$REPLICATION_MANAGER_PORT" "print-defaults" "$LVL_DEBUG"

    local response
    if [[ -n "$token" ]]; then
        send_lines_to_api "Using authenticated POST request with token" "print-defaults" "$LVL_DEBUG"
        response=$(send_http_request "POST" "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$config_sender_url" "" "application/json" "$token" "$request_timeout" 2>"$LOG_DIR/config_request.err")
    else
        send_lines_to_api "Using non-authenticated POST request" "print-defaults" "$LVL_DEBUG"
        response=$(send_http_request "POST" "$REPLICATION_MANAGER_HOST" "$REPLICATION_MANAGER_PORT" "$config_sender_url" "" "application/json" "" "$request_timeout" 2>"$LOG_DIR/config_request.err")
    fi

    if [[ -z "$response" ]]; then
        send_lines_to_api "ERROR: No response received from API" "print-defaults" "$LVL_ERROR"
        send_lines_to_api "Connection: $REPLICATION_MANAGER_HOST:$REPLICATION_MANAGER_PORT" "print-defaults" "$LVL_ERROR"

        if [ -f "$LOG_DIR/config_request.err" ]; then
            local err_content=$(cat "$LOG_DIR/config_request.err" 2>/dev/null)
            if [[ -n "$err_content" ]]; then
                send_lines_to_api "Error details: $err_content" "print-defaults" "$LVL_ERROR"
            fi
        fi
        return 1
    fi

    # Extract HTTP status and body
    local http_status=$(extract_http_code "$response")
    local json_body=$(extract_http_body "$response")

    send_lines_to_api "HTTP Status: $http_status" "print-defaults" "$LVL_DEBUG"

    if [[ "$http_status" != "200" ]]; then
        send_lines_to_api "ERROR: API returned status $http_status" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Parse SST port and host from JSON response
    local sst_port=$(echo "$json_body" | grep -o '"sst_port":"[^"]*"' | cut -d'"' -f4)
    local sst_host=$(echo "$json_body" | grep -o '"sst_host":"[^"]*"' | cut -d'"' -f4)
    local status=$(echo "$json_body" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)

    if [[ -z "$sst_port" || -z "$sst_host" ]]; then
        send_lines_to_api "ERROR: Failed to parse SST port/host from API response" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    send_lines_to_api "Config send queued (status: $status)" "print-defaults" "$LVL_INFO"
    send_lines_to_api "Config will be sent to $sst_host:$sst_port" "print-defaults" "$LVL_DEBUG"

    # Step 2: Open TCP listener on the SST port to receive the config
    local config_file="$extract_dir/config.tar.gz"
    local listener_timeout=300  # 5 minutes timeout for config delivery

    send_lines_to_api "Opening TCP listener on port $sst_port..." "print-defaults" "$LVL_DEBUG"

    # Use socat to listen and save received data to file
    # Format: socat -u TCP-LISTEN:port,reuseaddr,fork OPEN:file,creat,trunc
    if ! command -v socat >/dev/null 2>&1; then
        send_lines_to_api "ERROR: 'socat' command not found - required for receiving config" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Start listener in background and capture PID
    timeout "$listener_timeout" socat -u "TCP-LISTEN:$sst_port,reuseaddr" "OPEN:$config_file,creat,trunc" > "$LOG_DIR/socat_listener.log" 2>&1 &
    local socat_pid=$!

    send_lines_to_api "Listener started (PID: $socat_pid), waiting for config delivery (timeout: ${listener_timeout}s)..." "print-defaults" "$LVL_DEBUG"

    # Wait for socat to complete or timeout
    wait $socat_pid
    local socat_exit=$?

    if [[ $socat_exit -eq 124 ]]; then
        send_lines_to_api "ERROR: Timeout waiting for config delivery after ${listener_timeout}s" "print-defaults" "$LVL_ERROR"
        return 1
    elif [[ $socat_exit -ne 0 ]]; then
        send_lines_to_api "ERROR: Listener failed with exit code $socat_exit" "print-defaults" "$LVL_ERROR"
        if [ -f "$LOG_DIR/socat_listener.log" ]; then
            send_lines_to_api "Listener log: $(cat "$LOG_DIR/socat_listener.log")" "print-defaults" "$LVL_ERROR"
        fi
        return 1
    fi

    # Verify config file was received
    if [[ ! -s "$config_file" ]]; then
        local filesize=$(stat -c%s "$config_file" 2>/dev/null || echo 0)
        send_lines_to_api "ERROR: Received config file is empty (size: $filesize bytes)" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    local filesize=$(stat -c%s "$config_file" 2>/dev/null || echo 0)
    send_lines_to_api "Received config file: $filesize bytes" "print-defaults" "$LVL_DEBUG"

    # Check file type before extraction
    local file_type=$(file -b "$config_file" 2>/dev/null || echo "unknown")
    send_lines_to_api "File type: $file_type" "print-defaults" "$LVL_DEBUG"

    # Check first few bytes (magic numbers)
    local first_bytes=$(head -c 20 "$config_file" | od -An -tx1 | tr -d ' \n')
    send_lines_to_api "First bytes (hex): ${first_bytes:0:40}" "print-defaults" "$LVL_DEBUG"

    # Check if it looks like an HTTP error response
    if head -c 100 "$config_file" | grep -q "^HTTP\|^<html\|^{"; then
        send_lines_to_api "ERROR: Received file appears to be an HTTP error response, not a tarball" "print-defaults" "$LVL_ERROR"
        send_lines_to_api "First 200 bytes: $(head -c 200 "$config_file")" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Try to extract tarball
    local tar_output
    send_lines_to_api "Attempting gzip extraction (tar xzf)..." "print-defaults" "$LVL_DEBUG"

    if tar_output=$(tar xzf "$config_file" -C "$extract_dir" 2>&1); then
        send_lines_to_api "Successfully extracted as gzip compressed tar" "print-defaults" "$LVL_DEBUG"
    else
        send_lines_to_api "Gzip extraction failed: $tar_output" "print-defaults" "$LVL_WARN"

        # Try without gzip (maybe it's already uncompressed)
        send_lines_to_api "Trying uncompressed extraction (tar xf)..." "print-defaults" "$LVL_DEBUG"

        if tar_output=$(tar xf "$config_file" -C "$extract_dir" 2>&1); then
            send_lines_to_api "Successfully extracted as uncompressed tar" "print-defaults" "$LVL_DEBUG"
        else
            send_lines_to_api "ERROR: All extraction methods failed" "print-defaults" "$LVL_ERROR"
            send_lines_to_api "Final tar error: $tar_output" "print-defaults" "$LVL_ERROR"

            # Show what we actually got
            send_lines_to_api "First 200 chars of file: $(head -c 200 "$config_file" | cat -v)" "print-defaults" "$LVL_ERROR"

            # Try to identify the file type more specifically
            if command -v file >/dev/null 2>&1; then
                local detailed_type=$(file "$config_file" 2>/dev/null)
                send_lines_to_api "Detailed file type: $detailed_type" "print-defaults" "$LVL_ERROR"
            fi

            return 1
        fi
    fi

    # Set ownership if running as root
    if [[ "$(id -u)" == "0" && -d "$extract_dir/etc/mysql" ]]; then
        chown -R "$(db_owner 999:999)" "$extract_dir/etc/mysql" 2>/dev/null || true
    fi

    return 0
}

# Create dummy config file
create_dummy_config_file() {
    local base_dir="$1"
    local dummy_config_file="$base_dir/dummy.cnf"

    cat > "$dummy_config_file" <<EOF
[mysqld]
!includedir $base_dir/etc/mysql/conf.d
!includedir $base_dir/etc/mysql/custom.d
EOF

    if [[ $? -ne 0 ]]; then
        send_lines_to_api "ERROR: Failed to create dummy.cnf" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Set ownership if running as root
    if [[ "$(id -u)" == "0" ]]; then
        chown "$(db_owner 999:999)" "$dummy_config_file" 2>/dev/null || true
    fi

    return 0
}

# Send MariaDB defaults over TCP
send_mariadb_defaults() {
    local defaults_file="$1"
    local receiver_addr="$2"
    local log_file="$3"

    # Check if mariadbd exists, otherwise use mysqld
    local command="mariadbd"
    if ! command -v "$command" >/dev/null 2>&1; then
        send_lines_to_api "'mariadbd' not found, falling back to 'mysqld'" "print-defaults" "$LVL_INFO"
        command="mysqld"
    fi

    if ! command -v "$command" >/dev/null 2>&1; then
        send_lines_to_api "ERROR: Neither 'mariadbd' nor 'mysqld' found in PATH" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Run command and capture output
    local output=$($command --defaults-file="$defaults_file" --print-defaults 2>&1)

    if [[ $? -ne 0 ]]; then
        send_lines_to_api "ERROR: Failed to execute $command: $output" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Process output: replace ' --' with newline
    local processed_output=$(echo "$output" | sed 's/ --/\n--/g')

    # Detect unknown variables via --help --verbose and append them
    # so repman can present them in the configurator for user action.
    # The grep+sed pattern matches MariaDB/MySQL "unknown variable 'name=value'" format.
    # If the message format changes in future versions, this silently produces no output.
    local unknown_vars=$($command --defaults-file="$defaults_file" --help --verbose 2>&1 | grep "unknown variable" | sed "s/.*unknown variable '\\([^']*\\)'.*/\\1/" || true)
    if [[ -n "$unknown_vars" ]]; then
        while IFS= read -r bad_var; do
            if [[ -n "$bad_var" ]]; then
                processed_output="${processed_output}
# unknown:${bad_var}"
                send_lines_to_api "Unknown variable detected: $bad_var" "print-defaults" "$LVL_WARN"
            fi
        done <<< "$unknown_vars"
    else
        send_lines_to_api "No unknown variables detected in config" "print-defaults" "$LVL_DEBUG"
    fi

    # Send over TCP using socat
    local recv_host="${receiver_addr%%:*}"
    local recv_port="${receiver_addr##*:}"

    if ! echo "$processed_output" | socat -u STDIN "TCP:$recv_host:$recv_port"; then
        send_lines_to_api "ERROR: Failed to send data to $receiver_addr" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Log output
    echo "$processed_output" > "$log_file"

    send_lines_to_api "Output sent to $receiver_addr and logged to $log_file" "print-defaults" "$LVL_DEBUG"
    return 0
}

# Read command line arguments from /proc
read_cmdline_args() {
    local pid="$1"
    local cmdline_path="/proc/$pid/cmdline"

    if [[ ! -f "$cmdline_path" ]]; then
        send_lines_to_api "ERROR: Cannot read cmdline for PID $pid" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    tr '\0' '\n' < "$cmdline_path"
}

# Print defaults from PID file
print_defaults_from_pidfile() {
    local monitor_addr="$1"
    local current_port="$2"
    local current_pid_file="$3"
    local default_config_path="$4"
    local log_file="$5"

    # If pid_file doesn't exist, use default config
    if [[ -z "$current_pid_file" || ! -f "$current_pid_file" ]]; then
        send_lines_to_api "PID file not found, using default config path" "print-defaults" "$LVL_INFO"
        send_mariadb_defaults "$default_config_path" "$monitor_addr:$current_port" "$log_file"
        return $?
    fi

    # Read PID from file
    local pid=$(cat "$current_pid_file" | tr -d '[:space:]')

    if [[ -z "$pid" ]]; then
        send_lines_to_api "PID file is empty" "print-defaults" "$LVL_WARN"
        return 1
    fi

    # Check if process exists
    if [[ ! -d "/proc/$pid" ]]; then
        send_lines_to_api "Process with PID $pid not found" "print-defaults" "$LVL_WARN"
        return 1
    fi

    # Read command line arguments
    local defaults_file=""
    while IFS= read -r arg; do
        if [[ "$arg" == --defaults-file=* ]]; then
            defaults_file="${arg#--defaults-file=}"
            break
        fi
    done < <(read_cmdline_args "$pid")

    # Use default if not found
    if [[ -z "$defaults_file" ]]; then
        defaults_file="$default_config_path"
    fi

    # Send defaults
    send_mariadb_defaults "$defaults_file" "$monitor_addr:$current_port" "$log_file"
}

# Print defaults for fetch (dummy config)
print_defaults_for_fetch() {
    local monitor_addr="$1"
    local dummy_port="$2"
    local default_config_path="$3"
    local log_file="$4"

    local extract_dir="/bootstrap/dummy"

    # Fetch and extract configuration (use global TOKEN if available)
    if ! fetch_and_extract_config "$extract_dir" "$TOKEN"; then
        send_lines_to_api "ERROR: Failed to fetch and extract configuration" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Create dummy config file
    if ! create_dummy_config_file "$extract_dir"; then
        send_lines_to_api "ERROR: Failed to create dummy.cnf file" "print-defaults" "$LVL_ERROR"
        return 1
    fi

    # Run mariadbd with dummy configuration
    if send_mariadb_defaults "$extract_dir/dummy.cnf" "$monitor_addr:$dummy_port" "$log_file"; then
        send_lines_to_api "Dry run (dummy) completed successfully" "print-defaults" "$LVL_DEBUG"
        return 0
    else
        send_lines_to_api "ERROR: Dry run (dummy) failed" "print-defaults" "$LVL_ERROR"
        return 1
    fi
}

# Parse JSON field (simple grep-based parser for portability)
parse_json_field() {
    local json="$1"
    local field="$2"
    echo "$json" | grep -o "\"$field\":\"[^\"]*\"" | cut -d'"' -f4
}

# Main function to run config print jobs
run_config_print_jobs() {
    send_lines_to_api "Fetching config receiver information..." "print-defaults" "$LVL_DEBUG"

    # Fetch receiver info
    local receiver_json=$(fetch_config_receiver) || return 1

    # Parse JSON response
    local monitor_address=$(parse_json_field "$receiver_json" "monitor_address")
    local dummy_config_port=$(parse_json_field "$receiver_json" "dummy_config_port")
    local current_config_port=$(parse_json_field "$receiver_json" "current_config_port")
    local current_pid_file=$(parse_json_field "$receiver_json" "current_pid_file")
    local default_config_path=$(parse_json_field "$receiver_json" "default_config_dir")

    send_lines_to_api "Monitor Address: $monitor_address, Dummy Port: $dummy_config_port, Current Port: $current_config_port" "print-defaults" "$LVL_DEBUG"

    # Prepare log files
    local dummy_log="$LOG_DIR/dummy.log"
    local current_log="$LOG_DIR/current.log"

    # Run both jobs in parallel
    print_defaults_for_fetch "$monitor_address" "$dummy_config_port" "$default_config_path" "$dummy_log" &
    local pid_dummy=$!

    print_defaults_from_pidfile "$monitor_address" "$current_config_port" "$current_pid_file" "$default_config_path" "$current_log" &
    local pid_current=$!

    # Wait for both to complete
    wait $pid_dummy
    local status_dummy=$?

    wait $pid_current
    local status_current=$?

    # Log results
    send_lines_to_api "Both configuration checks completed." "print-defaults" "$LVL_INFO"
    send_lines_to_api "Dummy result: $([ $status_dummy -eq 0 ] && echo 'Success' || echo 'Failed') - Output in $dummy_log" "print-defaults" "$LVL_INFO"
    send_lines_to_api "Current result: $([ $status_current -eq 0 ] && echo 'Success' || echo 'Failed') - Output in $current_log" "print-defaults" "$LVL_INFO"

    return 0
}

#########################
# Check Functions         #
#########################

# Check jobs receiver (returns receiver port if successful)
check_jobs_receiver() {
    local cluster="$1"
    local server="$2"
    local port="$3"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"

    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/receive-jobs-check"
    local response=$(send_encrypted_api_request "$api_host" "$api_port" "$endpoint" "{\"server\":\"$server:$port\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    local http_code=$(extract_http_code "$response")
    local body=$(extract_http_body "$response")

    # Process successful response
    if [[ "$http_code" == "200" && "$body" == RECEIVER_PORT=* ]]; then
        local recv_port="${body#RECEIVER_PORT=}"

        # Validate the port is numeric and within range
        if [[ "$recv_port" =~ ^[0-9]+$ && "$recv_port" -ge 1 && "$recv_port" -le 65535 ]]; then
            echo "$recv_port"
            return 0
        else
            echo "error"
            return 2  # invalid port
        fi
    else
        echo "error"
        return 1  # API or server error
    fi
}

# Request jobs upgrade
request_jobs_upgrade() {
    local cluster="$1"
    local server="$2"
    local port="$3"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"
    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/send-jobs-upgrade"
    local response=$(send_encrypted_api_request "$api_host" "$api_port" "$endpoint" "{\"server\":\"$server:$port\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    local http_code=$(extract_http_code "$response")
    [[ "$http_code" == "200" ]]
}

################################
# Log Processing Functions     #
################################

# Function to create a manual lock file
create_log_lock_file() {
    local lock_file="$1"
    local job="$2"
    if [ -e "$lock_file" ]; then
        send_lines_to_api "Lock file $lock_file for $job exists. Exiting." "$job" "$LVL_DEBUG"
        return 1
    fi
    touch "$lock_file"
    return 0
}

# Function to remove a manual lock file
remove_log_lock_file() {
    local lock_file="$1"
    if [ -e "$lock_file" ]; then
        rm -f "$lock_file"
    fi
}

# Function to remove a run directory lock file
remove_run_lockdir() {
    local job="$1"
    local sleeptime=$2
    local run_lockdir="$LOG_DIR/$job.run"
    local lock_file="$LOCK_DIR/${job}_lockfile"

    local wait=0
    local max_wait=10  # Maximum wait time in seconds

    # Wait for the lock file before removing the run lockdir
    if [ -e "$lock_file" ]; then
        while [ -e "$lock_file" ] && [ $wait -lt $max_wait ]; do
            sleep 1
            ((wait++))
        done
    fi

    # Remove the run lockdir if it exists (with the owner PID a job's
    # lockdir holds, see recoverDeadJobs)
    if [ -d "$run_lockdir" ]; then
        rm -f "$run_lockdir/pid" "$run_lockdir/start"
        rmdir "$run_lockdir"
    fi

    # Give some time for log processing to complete
    if [ -n "$sleeptime" ]; then
        sleep "$sleeptime"
    fi
}

# Function to wait for the .run file with a timeout
wait_for_run_lockdir() {
    local run_lockdir="$1"
    local job="$2"
    local timeout=30
    local start_time=$(date +%s)

    send_lines_to_api "Waiting for $run_lockdir directory...\n" "$job" "$LVL_DEBUG"
    while [[ ! -d "$run_lockdir" ]]; do
        sleep 0.5
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))
        if ((elapsed >= timeout)); then
            send_lines_to_api "Timeout reached while waiting for $job.run lockdir.\n" "$job" "$LVL_ERROR"
            return 1
        fi
    done
    send_lines_to_api "$run_lockdir directory found...\n" "$job" "$LVL_DEBUG"
    return 0
}

# Function to wait for the .run file with a timeout
wait_for_log_file() {
    local logfile="$1"
    local job="$2"
    local timeout=60
    local start_time=$(date +%s)

    send_lines_to_api "Waiting for $logfile file...\n" "$job" "$LVL_DEBUG"
    while [[ ! -f "$logfile" ]]; do
        sleep 0.5
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))
        if ((elapsed >= timeout)); then
            send_lines_to_api "Timeout reached while waiting for $logfile file. Please check log manually if needed. \n" "$job" "$LVL_ERROR"
            return 1
        fi
    done
    send_lines_to_api "$logfile file found...\n" "$job" "$LVL_DEBUG"
    return 0
}

read_log_file() {
    local log_file="$1"
    local checkpoint_file="$2"
    local job="$3"
    local run_lockdir="$4"
    local batch_var="$5"

    local last_read=0
    local current_line=$((last_read + 1))
    local num_lines=$(wc -l < "$log_file")
    local batch=""

    if [[ -s "$checkpoint_file" ]]; then
        last_read=$(cat "$checkpoint_file")
        current_line=$((last_read + 1))
    fi

    if [ "$current_line" -gt "$num_lines" ]; then
        return
    fi

    if [ -f "$log_file" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')

            if [[ ! -d "$run_lockdir" ]]; then
                send_lines_to_api "Run file has been deleted. Processing remaining lines.\n" "$job" "$LVL_DEBUG"
                break
            fi

            batch+="$escaped\n"
            if ((current_line % BATCH_SIZE == 0)); then
                send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
                batch=""
            fi

            echo "$current_line" >"$checkpoint_file"
            ((current_line++))

        done < <(sed -n "${current_line},\$p" "$log_file")

        # Send any remaining lines in the batch after the first loop
        if [[ -n "$batch" ]]; then
            send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
        fi
    fi
}

# Function to process a log file
process_log_file() {
    local job="$1"
    local log_file
    case "$job" in
    "mariabackup"|"xtrabackup")
        log_file="$LOG_DIR/backup.out"
        ;;
    "reseedmariabackup"|"reseedxtrabackup")
        log_file="$LOG_DIR/reseed.out"
        ;;
    "flashbackmariabackup"|"flashbackxtrabackup")
        log_file="$LOG_DIR/flash.out"
        ;;
    *)
        log_file="$LOG_DIR/$job.process.out"
        ;;
    esac

    local checkpoint_file="$CHECKPOINT_DIR/$job.checkpoint"
    local run_lockdir="$LOG_DIR/$job.run"
    local lock_file="$LOCK_DIR/${job}_lockfile"

    if ! create_log_lock_file "$lock_file" "$job"; then
        return
    fi

    # Ensure lock file is removed on script exit. Using trap to handle unexpected exit. This will not interfere with other traps since it's a separate subshell.
    trap 'remove_log_lock_file "$lock_file"' EXIT

    if ! wait_for_run_lockdir "$run_lockdir" "$job"; then
        remove_log_lock_file "$lock_file"
        return
    fi

    if ! wait_for_log_file "$log_file" "$job"; then
        remove_log_lock_file "$lock_file"
        return
    fi

    local last_line=0
    if [[ -f "$checkpoint_file" ]]; then
        last_line=$(cat "$checkpoint_file")
    fi

    send_lines_to_api "Last checkpoint on "$checkpoint_file" is: $last_line.\n" "$job" "$LVL_DEBUG"

    local exec_once=1

    # processing until the end of the file and loop until run file deleted
    while [[ -d "$run_lockdir" ]] || [[ "$exec_once" -eq 1 ]]; do
        exec_once=0
        read_log_file "$log_file" "$checkpoint_file" "$job" "$run_lockdir"
    done

    # Final pass: process any remaining lines after run lockdir is deleted
    local last_read=0
    local current_line=1
    local num_lines=$(wc -l < "$log_file")
    local batch=""

    if [[ -s "$checkpoint_file" ]]; then
        last_read=$(cat "$checkpoint_file")
        current_line=$((last_read + 1))
    fi

    if [ "$current_line" -le "$num_lines" ] && [ -f "$log_file" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')

            batch+="$escaped\n"
            if ((current_line % BATCH_SIZE == 0)); then
                send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
                batch=""
            fi

            echo "$current_line" >"$checkpoint_file"
            ((current_line++))
        done < <(sed -n "${current_line},\$p" "$log_file")

        if [[ -n "$batch" ]]; then
            send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
        fi
    fi

    send_lines_to_api "Removing checkpoint file.\n" "$job" "$LVL_DEBUG"
    rm -f "$checkpoint_file"

    remove_log_lock_file "$lock_file"
}

#################################
# Job Related Functions     #
#################################

dblogfile() {
    local DBLOG="$1"
    local JOB="$2"
    local STATEFILE="$TMP_DIR/${JOB}.state"

    # Ensure log file exists
    [ ! -f "$DBLOG" ] && touch "$DBLOG"

    local LAST_LINE=""
    [ -f "$STATEFILE" ] && LAST_LINE=$(cat "$STATEFILE")

    local NEXT_LINE=1

    if [ -n "$LAST_LINE" ]; then
        # Find the last occurrence of LAST_LINE
        if grep -Fxn -- "$LAST_LINE" "$DBLOG" >$TMP_DIR/match_pos; then
            local LAST_MATCH_LINE
            LAST_MATCH_LINE=$(cut -d: -f1 $TMP_DIR/match_pos | tail -n 1)
            NEXT_LINE=$((LAST_MATCH_LINE + 1))
        fi
    fi

    # Extract new lines into temporary file (TMP_DIR, not the unset TMPDIR)
    local TMPLOG="$TMP_DIR/${JOB}.newlines"
    tail -n +"$NEXT_LINE" "$DBLOG" > "$TMPLOG"

    # If DB log has new content
    if [ -s "$TMPLOG" ]; then
        # Send content via socat
        if socat -u stdio TCP:$ADDRESS < "$TMPLOG" &>"$LOG_DIR/$JOB.process.out"; then
            # Persist the streaming offset: remember the last non-empty line we
            # just streamed so the next run resumes AFTER it (grep -Fxn on the
            # source) instead of re-sending the whole file from line 1. Without
            # this the state file never exists, NEXT_LINE stays 1, and every run
            # re-streams the entire log -> the collected copy inflates ~100x.
            local LAST_SENT
            LAST_SENT=$(grep -v '^$' "$TMPLOG" | tail -n 1)
            [ -n "$LAST_SENT" ] && printf '%s\n' "$LAST_SENT" > "$STATEFILE"
        fi
    else
        # Send empty payload
        echo -n | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$JOB.process.out"
    fi
}

##################################

socatCleaner() {
    local pid=$(lsof -t -i:$SST_RECEIVER_PORT -sTCP:LISTEN 2>/dev/null)
    if [[ -n "$pid" ]]; then
        kill -9 $pid
    fi
}

doneJob() {
    jobstate=3
    done=1
    case "$job" in
    mariabackup | xtrabackup )
        # mariabackup: "YYYY-MM-DD HH:MM:SS completed OK!"
        # xtrabackup 8.x: "<iso timestamp> 0 [Note] [MY-011825] [Xtrabackup] completed OK!"
        matches=$(grep -E '([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}|\[Xtrabackup\]) completed OK!' $LOG_DIR/backup.out)
        if [ ! -n "$matches" ]; then
            jobstate=5
            done=0
            echo "No successful record (complete OK!) found in $LOG_DIR/backup.out." >>$LOG_DIR/$job.out
        fi
        ;;
    esac

    if [[ "$job" == reseed* || "$job" == flashback* ]]; then
        if [ "$PR_STATUS" -ne 0 ]; then
            jobstate=5
            done=0
        fi
    fi

    if [ $jobstate -eq 3 ]; then
        send_lines_to_api "Job $job ended with state: Finished" "$job" "$LVL_INFO"
    else
        send_lines_to_api "Job $job ended with state: Error" "$job" "$LVL_ERROR"
    fi
    # Not backgrounded (no trailing &): this is the final completion write
    # for the job row. Backgrounding it let the script move on to the next
    # loop iteration (or exit) before the write landed — if the process
    # group got reaped at that point (cron/systemd/container exit), the row
    # was left at state=1/done=0 forever, indistinguishable from a job still
    # actually running. Nothing after this call does other useful work
    # concurrently, so there's no benefit to backgrounding it.
    $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set end=NOW(), state=$jobstate, result=LOAD_FILE('$LOG_DIR/$job.out'), done=$done  WHERE id='$ID';"
}

# receiveBackup unpacks the backup stream repman sends into $BACKUPDIR, on the
# datadir volume, with the given unpack tool (mbstream or xbstream). The
# unpacked size is not known in advance (repman only knows the compressed
# file), and a reseed must never fill the disk the running server writes to:
# RECEIVE_MIN_FREE_PCT percent of the volume stays free. The stream is capped
# at the free space above that reserve, minus an allowance for what the job
# writes after the stream (the prepare/export files and the temporary
# definition server's redo and Aria logs), measured when it starts (head -c,
# so no transfer speed can overshoot it). The allowance is twice the redo log
# size of this server (the backup's own configuration, unknown before it
# arrives, is normally the same), at least RECEIVE_RESERVE_MIB. When that size
# cannot be read the transfer is not started. Free space is
# also checked while it arrives (other writers use the volume too) and again
# after the prepare and after the definitions were read (pr_disk_ok). The floor is therefore a
# checked bound at those points, not a reservation: a volume written by
# another process can still cross it between two checks. When a check stops
# the job, the partial backup is removed and PR_STATUS=1; returns 1. A broken
# stream is otherwise caught later by the prepare check.
readonly RECEIVE_MIN_FREE_PCT=10
readonly RECEIVE_RESERVE_MIB=512

# receiveReserveBytes prints the allowance kept free on top of the floor:
# twice the redo log size of the local server (innodb_redo_log_capacity, else
# innodb_log_file_size), at least RECEIVE_RESERVE_MIB.
# Each query has a wall-clock limit (GNU timeout, when the image has it). It
# prints nothing and returns 1 when neither value can be read.
receiveReserveBytes() {
    local min=$((RECEIVE_RESERVE_MIB * 1048576)) redo t=()
    command -v timeout >/dev/null 2>&1 && t=(timeout 15)
    redo=$("${t[@]}" $BINARY_CLIENT --connect-timeout=10 -N -e "SELECT @@innodb_redo_log_capacity" 2>/dev/null) ||
        redo=$("${t[@]}" $BINARY_CLIENT --connect-timeout=10 -N -e "SELECT @@innodb_log_file_size" 2>/dev/null)
    [[ "$redo" =~ ^[0-9]+$ ]] || return 1
    echo $((redo * 2 > min ? redo * 2 : min))
}

receiveBackup() {
    local unpack="$1" sub total avail floor reserve budget count="$LOG_DIR/$job.received" got i
    if ! pr_paths_ok; then
        receiveBackupAbort "Backup transfer not started: the data directory is not set. No database or table was changed."
        return 1
    fi
    read -r total avail < <(df -P -B1 "$DATADIR" 2>/dev/null | awk 'NR==2{print $2, $4}')
    floor=$((${total:-0} * RECEIVE_MIN_FREE_PCT / 100))
    # Without the redo log size the allowance cannot be sized: stop before a
    # backup of unknown cost is received (the restore needs this server anyway).
    if ! reserve=$(receiveReserveBytes); then
        receiveBackupAbort "Backup transfer not started: the redo log size of this server could not be read, so the free space needed after the transfer cannot be sized. No database or table was changed."
        return 1
    fi
    budget=$((${avail:-0} - floor - reserve))
    if [[ -z "$avail" || $budget -le 0 ]]; then
        receiveBackupStop "$avail" "$total"
        return 1
    fi
    rm -f "$count"
    (socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,accept-timeout=600,bind=$SOCAT_BIND STDOUT |
        head -c "$budget" | tee >(wc -c >"$count") | $unpack -x -C "$BACKUPDIR") &
    sub=$!
    while kill -0 "$sub" 2>/dev/null; do
        read -r total avail < <(df -P -B1 "$DATADIR" 2>/dev/null | awk 'NR==2{print $2, $4}')
        if [[ -n "$avail" && "$avail" -lt "$floor" ]]; then
            # socat, head, tee and the unpack tool are children of the
            # subshell (read from /proc: ps is not in every image).
            kill $(grep -l "^PPid:[[:space:]]*$sub\$" /proc/[0-9]*/status 2>/dev/null | cut -d/ -f3) "$sub" 2>/dev/null
            wait "$sub" 2>/dev/null
            socatCleaner
            receiveBackupStop "$avail" "$total"
            return 1
        fi
        sleep 1
    done
    wait "$sub" 2>/dev/null
    for i in $(seq 1 50); do
        [[ -s "$count" ]] && break
        sleep 0.1
    done
    got=$(cat "$count" 2>/dev/null)
    rm -f "$count"
    if [[ "${got:-0}" -ge "$budget" ]]; then
        # The numbers of the last poll can be old: report the current ones.
        read -r total avail < <(df -P -B1 "$DATADIR" 2>/dev/null | awk 'NR==2{print $2, $4}')
        receiveBackupStop "${avail:-0}" "${total:-0}"
        return 1
    fi
    return 0
}

receiveBackupStop() {
    local avail="${1:-0}" total="${2:-0}"
    receiveBackupAbort "Backup transfer stopped: it would leave less than ${RECEIVE_MIN_FREE_PCT}% free on the datadir volume ($((avail / 1048576)) MiB free of $((total / 1048576)) MiB when stopped). The partial backup was removed; no database or table was changed. Free space, or restore on a larger volume."
}

# receiveBackupAbort removes the partial backup, reports $1 as the job's error
# and sets PR_STATUS=1.
receiveBackupAbort() {
    local msg="$1"
    pr_rm_backupdir
    echo "$msg" >>"$LOG_DIR/$job.out"
    case "$job" in
    reseed*) echo "$msg" >>"$LOG_DIR/reseed.out" ;;
    flashback*) echo "$msg" >>"$LOG_DIR/flash.out" ;;
    esac
    send_lines_to_api "$msg" "$job" "$LVL_ERROR"
    PR_STATUS=1
}

# proc_start_ticks prints the start time of process $1 (field 22 of
# /proc/$1/stat, in clock ticks since boot): with the PID it identifies a
# process, since a PID can be reused. Returns 1 when the process is gone.
proc_start_ticks() {
    local s
    s=$(cat "/proc/$1/stat" 2>/dev/null) || return 1
    s=${s##*) }
    set -- $s
    printf '%s' "${20}"
}

# recoverDeadJobs ends every job whose dbjobs run died without ending it
# (SIGKILL, OOM kill, a timeout): its .run lockdir is left behind holding the
# PID of a process that no longer runs dbjobs. repman would otherwise keep
# the job open forever and refuse every new run of it ("Concurrent reseed
# blocked"). The job is reported as failed through the usual channel, so
# repman clears its state exactly as for any job ending in error. Its log
# lock file goes too: the dead run's log follower never removed it, and it
# would stop the next run of the job from streaming its log. A reseed or
# flashback killed while reading the backup's definitions also leaves its
# temporary server running; it is stopped here. The lockdir stays until the
# report was accepted (repman or the jobs table can be down at that moment),
# so the next run retries. A lockdir without a PID (written by an older
# dbjobs) is left alone.
recoverDeadJobs() {
    local d job pid cmd msg reported started
    for d in "$LOG_DIR"/*.run; do
        [[ -d "$d" && -f "$d/pid" ]] || continue
        job=$(basename "$d" .run)
        [[ " ${JOBS[*]} " == *" $job "* ]] || continue
        pid=$(cat "$d/pid" 2>/dev/null)
        [[ "$pid" =~ ^[0-9]+$ ]] || continue
        cmd=$(tr '\0' ' ' 2>/dev/null <"/proc/$pid/cmdline")
        # Still running (a concurrent dbjobs run owns it). A reused PID that now
        # belongs to another dbjobs run is told apart by the start time the job
        # recorded next to its PID.
        if [[ "$pid" != "$$" && "$cmd" == *dbjobs* ]]; then
            started=$(cat "$d/start" 2>/dev/null)
            if [[ -z "$started" || "$started" == "$(proc_start_ticks "$pid")" ]]; then
                continue
            fi
        fi
        msg="Job $job was interrupted: its dbjobs run (pid $pid) ended without finishing it."
        # A report that fails again at every launch must not grow the log.
        grep -qxF -- "$msg" "$LOG_DIR/$job.out" 2>/dev/null || echo "$msg" >>"$LOG_DIR/$job.out"
        send_lines_to_api "$msg" "$job" "$LVL_ERROR"
        case "$job" in
        reseed* | flashback*)
            PR_LOG="$LOG_DIR/$job.out"
            pr_stop_stale_definition_server
            # Its folders hold a copy of the backup's system files and redo log:
            # taken back at once, not at the next restore.
            pr_paths_ok && rm -rf -- "$DATADIR/.system/mrm_defs_ro" "$DATADIR/.system/mrm_defs_run"
            PR_LOG=""
            ;;
        esac
        if [[ "$JOBS_MODE" == "api" ]]; then
            reported=0
            report_job_state "$job" "error" && reported=1
        else
            reported=0
            $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs SET end=NOW(), state=5, done=0, result='$msg' WHERE task='$job' AND done=0 AND state IN (1,2);" && reported=1
        fi
        # The marker is the only durable record that this job still has to be
        # reported: kept while repman or the jobs table is unreachable, so
        # the next launch retries.
        [[ $reported -eq 1 ]] || continue
        rm -f "$d/pid" "$d/start"
        rmdir "$d" 2>/dev/null
        rm -f "$LOCK_DIR/${job}_lockfile"
    done
}

pauseJob() {
    if [[ "$JOBS_MODE" == "api" ]]; then
        report_job_state "$job" "waiting"
    else
        $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set state=2, result='waiting' WHERE id='$ID';" &
    fi
}

pr_log() {
    local l="$1"
    local t
    t=$(date '+%Y-%m-%d %H:%M:%S')
    [[ -z "$PR_LOG" ]] && PR_LOG="$LOG_DIR/$job.out"
    echo "[$t] $l" >>"$PR_LOG"
}

pr_cmd() {
    local d="$1"
    shift
    pr_log "CMD: $d"
    "$@" >>"$PR_LOG" 2>&1
    local r=$?
    if [[ $r -ne 0 ]]; then
        pr_log "ERROR: $d (exit $r)"
        PR_STATUS=1
    fi
}

pr_pipe() {
    local d="$1"
    local p="$2"
    pr_log "CMD: $d"
    bash -o pipefail -c "$p" >>"$PR_LOG" 2>&1
    local r=$?
    if [[ $r -ne 0 ]]; then
        pr_log "ERROR: $d (exit $r)"
        PR_STATUS=1
    fi
}

# pr_try runs a restore step whose failure the caller handles itself (a
# skip-and-report via pr_skip) instead of counting it against PR_STATUS the
# way pr_cmd does.
pr_try() {
    local d="$1"
    shift
    pr_log "CMD: $d"
    "$@" >>"$PR_LOG" 2>&1
    local r=$?
    if [[ $r -ne 0 ]]; then
        pr_log "FAILED: $d (exit $r)"
    fi
    return $r
}

# pr_skip records a table the partial restore could not bring back. Its
# backup files stay in $BACKUPDIR and nothing of it is left in $DATADIR.
pr_skip() {
    pr_log "SKIPPED $1: $2"
    PR_SKIPPED+=("$1")
    PR_STATUS=1
}

# Every restore statement runs outside the binlog and without foreign key
# checks: tables come back one by one, so a child table is created, discarded
# or imported while the parent it references may not be back yet.
readonly PR_SQL_INIT="set sql_log_bin=0;set foreign_key_checks=0;"

# pr_list_databases prints the databases of the prepared backup to restore,
# as listed by the backup's own server (pr_export_definitions), never from
# the backup's directory layout: MySQL 8 keeps internal folders there
# (#innodb_redo, #innodb_temp) that must never be taken for a database.
# Prints nothing until the list has been exported.
pr_list_databases() {
    [[ -f "$PR_DEFS/.databases" ]] && cat "$PR_DEFS/.databases"
}

# pr_export_definitions reads the exact definition of every table and view of
# the prepared backup from the backup itself: a temporary read-only server is
# started on it (socket only, no grants, no binlog, innodb_read_only) and each
# SHOW CREATE is saved under $PR_DEFS/<db>/, with a per-database list of
# "name<TAB>type<TAB>engine". This is the backup-time definition -- foreign
# keys, partitioning, generated columns and views included -- so it cannot
# drift from the tablespaces being imported, whatever changed on the master
# since. The server runs on its own directory (a copy of the small mysql/
# schema, links to everything else) so the prepared backup is not modified.
# pr_stop_stale_definition_server stops a temporary definition server left
# running by an earlier restore that died (killed job, timeout): nothing else
# would ever stop it. It is recognised by its --datadir, which only that
# server uses, so the live server is never touched.
pr_stop_stale_definition_server() {
    local ro="$DATADIR/.system/mrm_defs_ro" p pids=() i
    for p in /proc/[0-9]*; do
        tr '\0' '\n' <"$p/cmdline" 2>/dev/null | grep -qxF -- "--datadir=$ro" && pids+=("${p#/proc/}")
    done
    [[ ${#pids[@]} -eq 0 ]] && return 0
    pr_log "Stopping ${#pids[@]} temporary server(s) left by an earlier restore: ${pids[*]}"
    kill -TERM "${pids[@]}" 2>/dev/null
    for i in $(seq 1 60); do
        kill -0 "${pids[@]}" 2>/dev/null || return 0
        sleep 0.5
    done
    kill -9 "${pids[@]}" 2>/dev/null
    return 0
}

# pr_filter_backup_cnf prints the server configuration of a backup
# (backup-my.cnf, $1) reduced to [section] and option lines, options prefixed
# loose-. Anything else (!include, !includedir, other directives) is dropped.
pr_filter_backup_cnf() {
    sed -nE -e '/^\[[A-Za-z0-9_.-]+\][[:space:]]*$/p' \
        -e 's/^([A-Za-z_][A-Za-z0-9_-]*)/loose-\1/p' "$1"
}

pr_export_definitions() {
    if ! pr_paths_ok; then
        pr_log "ERROR: the data directory is not set."
        return 1
    fi
    local ro="$DATADIR/.system/mrm_defs_ro" run="$DATADIR/.system/mrm_defs_run" sock="$DATADIR/.system/mrm_defs_run/mariadbd.sock" bin f n db t type engine
    bin=$(command -v mariadbd || command -v mysqld || ls /usr/sbin/mariadbd /usr/sbin/mysqld 2>/dev/null | head -1)
    if [[ ! -x "$bin" ]]; then
        pr_log "ERROR: no mariadbd/mysqld binary to read the backup definitions."
        return 1
    fi
    # Everything the server writes -- pid file, error log, socket, temporary
    # files, Aria log copies -- lives in its own mysql-owned directory on the
    # datadir volume ($DATADIR/.system itself is root-owned), never in the
    # container's /tmp, which is not a declared volume.
    pr_stop_stale_definition_server
    rm -rf "$ro" "$run" "$PR_DEFS"
    mkdir -p "$ro" "$run/aria" "$run/tmp" "$PR_DEFS"
    # The socket (open without grants in the fallback) is reachable only by
    # the owner of the directory.
    chmod 700 "$run"
    # MySQL 8 (xtrabackup) prepared backups carry no redo log (an empty
    # #innodb_redo/), and a --innodb-read-only server cannot create one. Its
    # small system tablespaces are then copied, and a first "priming" start
    # without the user databases creates the redo log in the temporary
    # directory; the user databases are only linked in for the read-only start.
    local prime=0
    [[ -d "$BACKUPDIR/#innodb_redo" ]] && ! compgen -G "$BACKUPDIR/#innodb_redo/*" >/dev/null && prime=1
    local userdirs=()
    for f in "$BACKUPDIR"/*; do
        n=$(basename "$f")
        case "$n" in
        mysql) cp -a "$f" "$ro/mysql" ;;
        # Copies of the backup's Aria logs, so the copied mysql/ Aria tables
        # match them: with an empty Aria log they count as moved from another
        # server and mysql.proc (read by views calling stored functions, e.g.
        # sys) fails to open.
        aria_log*) cp -a "$f" "$run/aria/$n" ;;
        backup-my.cnf | xtrabackup_* | mariadb_backup_* | binlog.* | ib_buffer_pool | ibtmp1) ;;
        "#innodb_redo" | "#innodb_temp") mkdir -p "$ro/$n" ;;
        *)
            if [[ $prime -eq 1 && -d "$f" ]]; then
                userdirs+=("$n")
            elif [[ $prime -eq 1 ]]; then
                cp -a "$f" "$ro/$n"
            else
                ln -s "$f" "$ro/$n"
            fi
            ;;
        esac
    done
    # The server is started with the backup's own accounts and the event
    # scheduler OFF (no event can run, but event definitions stay readable;
    # --skip-grant-tables disables the scheduler entirely and hides them).
    # backup-my.cnf holds the InnoDB layout of the backup (page size, log and
    # undo settings), plus keys only the backup tool knows (xtrabackup 8 writes
    # server_uuid): every option is made loose- so unknown ones are ignored.
    # Only [section] and option lines are kept: an !include/!includedir (or any
    # other directive) would make the temporary server read configuration from
    # outside the backup.
    pr_filter_backup_cnf "$BACKUPDIR/backup-my.cnf" >"$run/backup.cnf"
    local args=(--defaults-file="$run/backup.cnf" --datadir="$ro" --socket="$sock" --skip-networking
        --event-scheduler=OFF --innodb-read-only=1 --read-only=1 --skip-log-bin
        --loose-skip-slave-start --loose-skip-replica-start --loose-mysqlx=OFF
        --innodb-buffer-pool-size=64M --pid-file="$run/mariadbd.pid" --log-error="$run/mariadbd.err" --tmpdir="$run/tmp"
        --loose-skip-ssl)
    if [[ $isr -eq 1 ]]; then
        chown -R mysql:mysql "$ro" "$run"
        chown -h mysql:mysql "$ro"/*
        args+=(--user=mysql)
    fi
    args+=(--loose-aria-log-dir-path="$run/aria")
    # No TLS on the socket-only server: MariaDB >= 11.4 would otherwise
    # generate a certificate at startup that its own client verifies and can
    # refuse ("certificate is not yet valid" on a small clock step). Clients
    # that do not know the option ignore it (loose-).
    local cli=("${BINARY_CLIENT%% *}" --loose-skip-ssl) up=0 i attempt variant
    # The client of the temporary server, as an array: a password with spaces
    # or shell characters stays one argument.
    PR_RO_CMD=()
    ro_sql() { "${PR_RO_CMD[@]}" "$@"; }
    if [[ $prime -eq 1 ]]; then
        local pargs=("${args[@]}")
        pargs=("${pargs[@]/--innodb-read-only=1/--innodb-read-only=0}")
        pr_log "CMD: Priming start on the backup's system tablespaces (creates the redo log; no user database attached)"
        "$bin" "${pargs[@]}" --loose-innodb-buffer-pool-load-at-startup=OFF --loose-innodb-buffer-pool-dump-at-shutdown=OFF >>"$PR_LOG" 2>&1 &
        local ppid=$!
        for i in $(seq 1 240); do
            [[ -S "$sock" ]] && break
            kill -0 $ppid 2>/dev/null || break
            sleep 0.5
        done
        kill -TERM $ppid 2>/dev/null
        for i in $(seq 1 240); do
            kill -0 $ppid 2>/dev/null || break
            sleep 0.5
        done
        if kill -0 $ppid 2>/dev/null || ! compgen -G "$ro/#innodb_redo/*" >/dev/null; then
            pr_log "ERROR: the priming start did not create a redo log."
            kill -9 $ppid 2>/dev/null
            rm -rf "$ro" "$run"
            return 1
        fi
        for n in "${userdirs[@]}"; do
            ln -s "$BACKUPDIR/$n" "$ro/$n"
        done
        [[ $isr -eq 1 ]] && chown -h mysql:mysql "$ro"/*
    fi
    PR_EVENTS_READABLE=1
    for attempt in accounts skip-grants; do
        if [[ "$attempt" == "skip-grants" ]]; then
            # None of our credentials opens the backup's accounts (e.g. the
            # root password changed since the backup). MariaDB cannot show
            # events in this mode (MySQL 8 can: checked once it is up).
            pr_log "No login into the backup's accounts; restarting it with --skip-grant-tables (MariaDB cannot show events that way)."
            args+=(--skip-grant-tables)
            PR_EVENTS_READABLE=0
        fi
        pr_log "CMD: Start read-only server on the prepared backup to read its definitions ($attempt)"
        "$bin" "${args[@]}" >>"$PR_LOG" 2>&1 &
        up=0
        for i in $(seq 1 240); do
            [[ -S "$sock" ]] && { up=1; break; }
            sleep 0.5
        done
        [[ $up -eq 0 ]] && break
        # The socket can appear before the server accepts logins: retry
        # until a login works or every credential is actually refused.
        local out denied
        for i in $(seq 1 60); do
            denied=0
            for variant in root account; do
                if [[ "$variant" == "root" ]]; then
                    PR_RO_CMD=("${cli[@]}" -uroot -S "$sock")
                else
                    PR_RO_CMD=("${cli[@]}" -u"$USER" -p"$PASSWORD" -S "$sock")
                fi
                if out=$(ro_sql -N -e "SELECT 1" 2>&1); then
                    break 3
                fi
                [[ "$out" == *"ERROR 1045"* || "$out" == *"ERROR 1698"* ]] && denied=$((denied + 1))
            done
            [[ $denied -eq 2 ]] && break
            sleep 0.5
        done
        PR_RO_CMD=()
        [[ "$attempt" == "skip-grants" ]] && break
        kill "$(cat "$run/mariadbd.pid" 2>/dev/null)" 2>/dev/null
        for i in $(seq 1 120); do
            [[ -S "$sock" ]] || break
            sleep 0.5
        done
    done
    [[ ${#PR_RO_CMD[@]} -eq 0 ]] && up=0
    # MySQL 8 keeps events in its data dictionary (no mysql.event table) and
    # shows them in this mode. MariaDB answers information_schema.events
    # with no rows instead of an error, so an empty answer proves nothing:
    # with a mysql.event table present the events stay unreadable.
    if [[ $up -eq 1 && $PR_EVENTS_READABLE -eq 0 ]] &&
        ! ro_sql -N -e "SELECT 1 FROM mysql.event LIMIT 0" >/dev/null 2>&1 &&
        ro_sql -N -e "SELECT COUNT(*) FROM information_schema.events" >/dev/null 2>>"$PR_LOG"; then
        pr_log "Events are readable in --skip-grant-tables mode on this server (data dictionary)."
        PR_EVENTS_READABLE=1
    fi
    local rc=0
    if [[ $up -eq 0 ]]; then
        pr_log "ERROR: the read-only server on the prepared backup did not start."
        rc=1
    else
        # sys is created by the server itself and tied to its version.
        if ! ro_sql -N -B -e "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','performance_schema','information_schema','sys','replication_manager_schema') ORDER BY schema_name" >"$PR_DEFS/.databases" 2>>"$PR_LOG"; then
            pr_log "ERROR: cannot list the databases of the backup."
            rm -f "$PR_DEFS/.databases"
            rc=1
        fi
        for db in $(pr_list_databases); do
            mkdir -p "$PR_DEFS/$db"
            if ! ro_sql -N -B -e "SELECT table_name, table_type, IFNULL(engine,'') FROM information_schema.tables WHERE table_schema='$db' ORDER BY table_name" >"$PR_DEFS/$db/.list" 2>>"$PR_LOG"; then
                pr_log "ERROR: cannot list the tables of $db in the backup."
                rc=1
                continue
            fi
            while IFS=$'\t' read -r t type engine; do
                [[ -z "$t" ]] && continue
                # Names are put in SQL below: refuse the unsafe ones first.
                if pr_unsafe_name "$t"; then
                    pr_log "ERROR: $db.$t has a quote, backtick or backslash in its name: it cannot be restored safely."
                    rc=1
                    continue
                fi
                if [[ "$type" == "VIEW" ]]; then
                    ro_sql -N -B -r -e "SHOW CREATE VIEW \`$db\`.\`$t\`" 2>>"$PR_LOG" |
                        sed -e '1s/^[^\t]*\t//' -e '$s/\t[^\t]*\t[^\t]*$//' >"$PR_DEFS/$db/$t.sql"
                else
                    ro_sql -N -B -r -e "SHOW CREATE TABLE \`$db\`.\`$t\`" 2>>"$PR_LOG" |
                        sed -e '1s/^[^\t]*\t//' >"$PR_DEFS/$db/$t.sql"
                fi
                if [[ ! -s "$PR_DEFS/$db/$t.sql" ]]; then
                    pr_log "ERROR: no definition for $db.$t in the backup."
                    rc=1
                fi
            done <"$PR_DEFS/$db/.list"
            # DROP DATABASE also drops the stored routines and events of db,
            # so they are exported too: "type<TAB>name" in .routines, the
            # CREATE statement in .routines.d/<type>.<name>.sql and its
            # sql_mode in .routines.d/<type>.<name>.mode.
            mkdir -p "$PR_DEFS/$db/.routines.d"
            # Triggers are exported as SQL too: MySQL 8 keeps them in its data
            # dictionary (no .TRG files); one method for both keeps it simple.
            local trg_q="SELECT 'TRIGGER', trigger_name FROM information_schema.triggers WHERE trigger_schema='$db'"
            if [[ $PR_EVENTS_READABLE -eq 1 ]]; then
                ro_sql -N -B -e "SELECT routine_type, routine_name FROM information_schema.routines WHERE routine_schema='$db' UNION ALL SELECT 'EVENT', event_name FROM information_schema.events WHERE event_schema='$db' UNION ALL $trg_q" >"$PR_DEFS/$db/.routines" 2>>"$PR_LOG" || rc=1
            else
                ro_sql -N -B -e "SELECT routine_type, routine_name FROM information_schema.routines WHERE routine_schema='$db' UNION ALL $trg_q" >"$PR_DEFS/$db/.routines" 2>>"$PR_LOG" || rc=1
                ro_sql -N -B -e "SELECT name FROM mysql.event WHERE db='$db'" >"$PR_DEFS/$db/.events_unreadable" 2>>"$PR_LOG" || rc=1
            fi
            local rtype rname out cols
            while IFS=$'\t' read -r rtype rname; do
                [[ -z "$rname" ]] && continue
                if pr_unsafe_name "$rname"; then
                    pr_log "ERROR: $rtype $db.$rname has a quote, backtick or backslash in its name: it cannot be restored safely."
                    rc=1
                    continue
                fi
                out="$PR_DEFS/$db/.routines.d/$rtype.$rname"
                # SHOW CREATE {FUNCTION|PROCEDURE|TRIGGER}: name, sql_mode, CREATE,
                # then 3 charset columns (TRIGGER: plus a creation time column).
                # SHOW CREATE EVENT: name, sql_mode, time_zone, CREATE, 3 charset columns.
                cols=2
                [[ "$rtype" == "EVENT" ]] && cols=3
                ro_sql -N -B -r -e "SHOW CREATE $rtype \`$db\`.\`$rname\`" >"$out.raw" 2>>"$PR_LOG"
                head -1 "$out.raw" | cut -f2 >"$out.mode"
                local tail_cols=3
                [[ "$rtype" == "TRIGGER" ]] && tail_cols=4
                sed -e "1s/^\([^\t]*\t\)\{$cols\}//" -e "\$s/\(\t[^\t]*\)\{$tail_cols\}\$//" "$out.raw" >"$out.sql"
                rm -f "$out.raw"
                if [[ ! -s "$out.sql" ]]; then
                    pr_log "ERROR: no definition for $rtype $db.$rname in the backup."
                    rc=1
                fi
            done <"$PR_DEFS/$db/.routines"
        done
        ro_sql -e "SHUTDOWN" >/dev/null 2>&1
    fi
    # Whatever happened above, the server is asked to stop by signal too
    # (a refused SHUTDOWN would otherwise leave it running).
    kill -TERM "$(cat "$run/mariadbd.pid" 2>/dev/null)" 2>/dev/null
    for i in $(seq 1 120); do
        [[ -f "$run/mariadbd.pid" ]] && kill -0 "$(cat "$run/mariadbd.pid" 2>/dev/null)" 2>/dev/null || break
        sleep 0.5
    done
    if [[ -f "$run/mariadbd.pid" ]] && kill -0 "$(cat "$run/mariadbd.pid" 2>/dev/null)" 2>/dev/null; then
        pr_log "ERROR: the read-only server did not stop; killing it."
        kill -9 "$(cat "$run/mariadbd.pid")" 2>/dev/null
        rc=1
    fi
    cat "$run/mariadbd.err" >>"$PR_LOG" 2>/dev/null
    rm -rf "$ro" "$run" "$sock"
    return $rc
}

pr_create_from_definition() {
    local db="$1" t="$2" file="$PR_DEFS/$1/$2.sql"
    pr_log "CMD: Create $db.$t from its backup definition"
    { printf '%sUSE `%s`;\n' "$PR_SQL_INIT" "$db"; cat "$file"; printf ';\n'; } | $BINARY_CLIENT >>"$PR_LOG" 2>&1
    local r=$?
    if [[ $r -ne 0 ]]; then
        pr_log "FAILED: Create $db.$t from its backup definition (exit $r)"
    fi
    return $r
}

# pr_master_host prints the configured master of this server, empty when
# it is not a replica. SHOW REPLICA STATUS (MariaDB >= 10.5, MySQL >= 8.0.22,
# the only form MySQL 8.4 knows) first, SHOW SLAVE STATUS for older servers;
# the column is Master_Host on MariaDB, Source_Host on MySQL.
pr_master_host() {
    local st h
    st=$($BINARY_CLIENT -e "SHOW REPLICA STATUS\G" 2>/dev/null) || st=$($BINARY_CLIENT -e "SHOW SLAVE STATUS\G" 2>>"$PR_LOG")
    h=$(echo "$st" | awk -F': ' '/^ *(Master|Source)_Host:/{print $2; exit}')
    [[ "$h" == "NULL" ]] && h=""
    echo "$h"
}

# pr_restore_routines recreates the stored functions, procedures and events
# of db from their backup definitions, each with its original sql_mode. On a
# replica an event is created DISABLE ON SLAVE, as replication itself does:
# an enabled event would run, and write, on the replica.
pr_restore_routines() {
    local db="$1" rtype rname base mode def disable variants done_ok
    if [[ -s "$PR_DEFS/$db/.events_unreadable" ]]; then
        while read -r rname; do
            [[ -n "$rname" ]] && pr_skip "EVENT $db.$rname" "event definitions cannot be read without the backup's accounts"
        done <"$PR_DEFS/$db/.events_unreadable"
    fi
    [[ -s "$PR_DEFS/$db/.routines" ]] || return 0
    while IFS=$'\t' read -r rtype rname; do
        [[ -z "$rname" ]] && continue
        base="$PR_DEFS/$db/.routines.d/$rtype.$rname"
        mode=$(cat "$base.mode" 2>/dev/null)
        def=$(cat "$base.sql")
        variants=("$def")
        if [[ "$rtype" == "EVENT" && $PR_IS_REPLICA -eq 1 ]]; then
            # The spelling depends on the server: MariaDB and MySQL 8.0 know
            # DISABLE ON SLAVE, newer MySQL DISABLE ON REPLICA. A refused
            # CREATE changes nothing, so each is simply tried in turn.
            variants=()
            for disable in "DISABLE ON SLAVE" "DISABLE ON REPLICA"; do
                variants+=("$(printf '%s' "$def" | sed -E "0,/ (ENABLE|DISABLE ON SLAVE|DISABLE ON REPLICA|DISABLE) (ON COMPLETION|COMMENT|DO)/s// $disable \\2/")")
            done
        fi
        pr_log "CMD: Create $rtype $db.$rname from its backup definition"
        done_ok=0
        for def in "${variants[@]}"; do
            if printf '%s\nDELIMITER ;;\nSET SESSION sql_mode=%s;;\nUSE `%s`;;\n%s;;\n' "$PR_SQL_INIT" "'$mode'" "$db" "$def" | $BINARY_CLIENT >>"$PR_LOG" 2>&1; then
                done_ok=1
                break
            fi
        done
        [[ $done_ok -eq 1 ]] || pr_skip "$rtype $db.$rname" "cannot recreate it from its backup definition"
    done <"$PR_DEFS/$db/.routines"
}

# pr_recreate_database drops and recreates db. An earlier failed restore can
# leave files the server does not know (orphan .ibd/.cfg, stub .frm), which
# make DROP DATABASE fail with errno 39 after it has dropped every table it
# knows. Only once the server lists no table left in db are those files
# moved to a quarantine directory and the drop retried.
pr_recreate_database() {
    local db="$1"
    pr_try "Drop database $db" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP DATABASE IF EXISTS \`$db\`"
    # Files the server does not know (orphans of an earlier failed restore)
    # outlive the drop: MariaDB then fails the DROP (errno 39), MySQL 8 drops
    # the schema but leaves the folder and refuses the CREATE (ERROR 3678).
    # Only once the server lists no table left in db are they moved to a
    # quarantine directory.
    # Quarantine only ever applies to a real database folder of the backup:
    # never to server-internal folders (#innodb_redo holds the live redo
    # log), hidden folders, system schemas or links.
    local may_quarantine=1
    case "$db" in
    "#"* | .* | mysql | sys | performance_schema | information_schema | replication_manager_schema) may_quarantine=0 ;;
    esac
    grep -qxF -- "$db" "$PR_DEFS/.databases" 2>/dev/null || may_quarantine=0
    [[ -L "$DATADIR/$db" ]] && may_quarantine=0
    if [[ $may_quarantine -eq 1 && -d "$DATADIR/$db" ]]; then
        local left
        left=$($BINARY_CLIENT -N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$db'" 2>>"$PR_LOG")
        if [[ "$left" == "0" ]]; then
            local q="$DATADIR/.system/orphan-quarantine-$PR_RUN_ID/$db"
            mkdir -p "$q"
            pr_try "Quarantine leftover files of $db into $q" bash -c "shopt -s dotglob nullglob; f=(\"$DATADIR/$db\"/*); [[ \${#f[@]} -eq 0 ]] || mv \"\${f[@]}\" \"$q\"/"
            pr_try "Drop database $db (after quarantine)" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP DATABASE IF EXISTS \`$db\`"
            rmdir "$DATADIR/$db" 2>/dev/null
        fi
    fi
    pr_cmd "Create database $db" $BINARY_CLIENT -e "${PR_SQL_INIT}CREATE DATABASE \`$db\`"
}

# pr_import_partitions fills the partitions of the (empty, just recreated)
# partitioned table db.t from the backup. MariaDB cannot DISCARD/IMPORT the
# tablespace of a partitioned table as a whole (ERROR 1031), so each partition
# is imported into a non-partitioned staging table of the same structure and
# swapped in with EXCHANGE PARTITION -- still a hot replace, on every version.
# Returns non-zero on the first failure; the caller drops the table.
pr_import_partitions() {
    local db="$1" t="$2" src="$BACKUPDIR/$1" dst="$DATADIR/$1"
    shift 2
    local f p stage ext moved
    for f in "$@"; do
        p="${f#*#[Pp]#}"
        stage="mrm_pivo_${RANDOM}${RANDOM}"
        moved=()
        if ! pr_try "Create staging table for $db.$t partition $p" $BINARY_CLIENT -e "${PR_SQL_INIT}CREATE TABLE \`$db\`.\`$stage\` LIKE \`$db\`.\`$t\`; ALTER TABLE \`$db\`.\`$stage\` REMOVE PARTITIONING; ALTER TABLE \`$db\`.\`$stage\` DISCARD TABLESPACE"; then
            pr_try "Drop staging table $db.$stage" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$stage\`"
            return 1
        fi
        for ext in ibd cfg exp; do
            if [[ -f "$src/$f.$ext" ]] && pr_try "Move $f.$ext for $db.$t" mv "$src/$f.$ext" "$dst/$stage.$ext"; then
                moved+=("$ext")
            fi
        done
        if pr_try "Import partition $p of $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}ALTER TABLE \`$db\`.\`$stage\` IMPORT TABLESPACE" &&
            pr_try "Exchange partition $p of $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}ALTER TABLE \`$db\`.\`$t\` EXCHANGE PARTITION \`$p\` WITH TABLE \`$db\`.\`$stage\`"; then
            pr_try "Drop staging table $db.$stage" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$stage\`"
            continue
        fi
        for ext in "${moved[@]}"; do
            mv "$dst/$stage.$ext" "$src/$f.$ext" 2>>"$PR_LOG"
        done
        pr_try "Drop staging table $db.$stage" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$stage\`"
        return 1
    done
    return 0
}

# pr_create_and_locate creates db.t from its backup definition and finds the
# file base name the server gave it: a table whose name is not plain ASCII is
# stored encoded on disk (n-dash as n@002ddash, "n space" as n@0020space), so
# the backup's files cannot be found from the table name. The files that
# appear in the database folder with the CREATE are the server's own answer.
# Sets PR_FILEBASE and PR_NEWFILES; fails, dropping the table again, unless
# exactly one base name appeared -- it never guesses which files to import.
pr_create_and_locate() {
    local db="$1" t="$2" dst="$DATADIR/$1" before after base
    before=$(ls -1A "$dst" 2>/dev/null | sort)
    pr_create_from_definition "$db" "$t" || return 1
    after=$(ls -1A "$dst" 2>/dev/null | sort)
    # Not the table's own files: MySQL 8 serialized dictionary files
    # (<table>_<id>.sdi) and InnoDB's auxiliary FULLTEXT index tablespaces
    # (FTS_<table id>_*.ibd), which the index rebuilds on its own.
    PR_NEWFILES=$(comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | grep -v -e '\.sdi$' -e '^FTS_')
    base=$(printf '%s\n' "$PR_NEWFILES" | grep . | sed -E 's/\.[^.]+$//; s/#[Pp]#.*$//' | sort -u)
    if [[ $(printf '%s\n' "$base" | grep -c .) -ne 1 ]]; then
        pr_log "ERROR: cannot tell which files belong to $db.$t (created: $(printf '%s ' $PR_NEWFILES))."
        pr_try "Drop $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
        return 1
    fi
    PR_FILEBASE="$base"
    return 0
}

# pr_restore_innodb_table recreates db.t from its backup definition and
# imports its tablespace (every partition of a partitioned table). The backup
# must hold exactly the tablespaces the recreated table has -- same file base
# name, same partitions -- or nothing is imported. A table whose import is
# refused is dropped again and reported, and its files are taken back out of
# the datadir, so neither an orphan tablespace nor a half-imported table
# remains.
pr_restore_innodb_table() {
    local db="$1" t="$2" src="$BACKUPDIR/$1" dst="$DATADIR/$1"
    local f ext enc names=() moved=() created_parts backup_parts

    if ! pr_create_and_locate "$db" "$t"; then
        pr_skip "$db.$t" "cannot create the table from its backup definition"
        return
    fi
    enc="$PR_FILEBASE"
    created_parts=$(printf '%s\n' "$PR_NEWFILES" | grep -E "#[Pp]#.*\.ibd$" | sed 's/\.ibd$//' | sort)
    backup_parts=$(cd "$src" && ls -1 -- "$enc#P#"*.ibd "$enc#p#"*.ibd 2>/dev/null | sed 's/\.ibd$//' | sort)
    if [[ -n "$created_parts" || -n "$backup_parts" ]]; then
        if [[ "$created_parts" != "$backup_parts" ]]; then
            pr_try "Drop $db.$t (partitions differ from the backup)" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
            pr_skip "$db.$t" "the backup's partition tablespaces do not match its definition"
            return
        fi
        mapfile -t names <<<"$backup_parts"
        if pr_import_partitions "$db" "$t" "${names[@]}"; then
            pr_log "Restored partitioned $db.$t (${#names[@]} partitions)."
        else
            pr_try "Drop $db.$t after failed partition import" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
            pr_skip "$db.$t" "partition import failed"
        fi
        return
    fi
    if [[ ! -f "$src/$enc.ibd" ]]; then
        pr_try "Drop $db.$t (no tablespace in the backup)" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
        pr_skip "$db.$t" "no tablespace $enc.ibd in the backup"
        return
    fi
    if ! pr_try "Discard tablespace $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}ALTER TABLE \`$db\`.\`$t\` DISCARD TABLESPACE"; then
        pr_try "Drop $db.$t after failed discard" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
        pr_skip "$db.$t" "DISCARD TABLESPACE failed"
        return
    fi
    for ext in ibd cfg exp; do
        if [[ -f "$src/$enc.$ext" ]] && pr_try "Move $enc.$ext for $db.$t" mv "$src/$enc.$ext" "$dst/$enc.$ext"; then
            moved+=("$enc.$ext")
        fi
    done
    if pr_try "Import tablespace for $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}ALTER TABLE \`$db\`.\`$t\` IMPORT TABLESPACE"; then
        return
    fi
    for f in "${moved[@]}"; do
        mv "$dst/$f" "$src/$f" 2>>"$PR_LOG"
    done
    pr_try "Drop $db.$t after failed import" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
    pr_skip "$db.$t" "IMPORT TABLESPACE failed"
}

# pr_restore_file_table restores a non-InnoDB table (MyISAM, Aria, CSV, ...).
# The table is first created from its backup definition to learn the file
# name the server uses for it (names can be stored encoded). With a .frm in
# the backup (MariaDB), that probe table is dropped again and the backup's own
# files -- .frm included -- are moved in as they are: an Aria table keeps its
# identity in them, and a fresh table given another server's data files is
# reported corrupt. Without a .frm (MySQL 8), the table must live in the data
# dictionary, so the created table stays and only its data files are
# replaced. Either way CHECK TABLE must pass before it counts as restored.
pr_restore_file_table() {
    local db="$1" t="$2" engine="$3" src="$BACKUPDIR/$1" dst="$DATADIR/$1" enc f n=0
    if ! pr_create_and_locate "$db" "$t"; then
        pr_skip "$db.$t" "cannot create the table from its backup definition"
        return
    fi
    enc="$PR_FILEBASE"
    if [[ -f "$src/$enc.frm" ]]; then
        pr_try "Drop the probe table $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
        for f in "$src/$enc".*; do
            [[ -e "$f" ]] || continue
            pr_try "Move $(basename "$f") for $db.$t" mv -f "$f" "$dst/" && n=$((n + 1))
        done
        # An Aria table copied from another server still carries that
        # server's log sequence numbers and is refused as corrupt until
        # zerofilled -- MariaDB's documented step for moving Aria tables
        # between servers. The rows are not changed.
        if [[ "$engine" == "Aria" ]]; then
            local aria_chk
            aria_chk=$(command -v aria_chk || command -v mariadb-aria-chk)
            if [[ -n "$aria_chk" ]]; then
                pr_try "Zerofill Aria table $db.$t" "$aria_chk" --zerofill --silent "$dst/$enc"
                [[ $isr -eq 1 ]] && chown mysql:mysql "$dst/$enc".*
            else
                pr_log "No aria_chk binary to zerofill $db.$t."
            fi
        fi
    else
        pr_try "Close $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}FLUSH TABLE \`$db\`.\`$t\`"
        for f in "$src/$enc".*; do
            [[ -e "$f" ]] || continue
            case "$f" in *.sdi | *.par) continue ;; esac
            pr_try "Replace $(basename "$f") for $db.$t" mv -f "$f" "$dst/" && n=$((n + 1))
        done
    fi
    pr_try "Flush table $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}FLUSH TABLE \`$db\`.\`$t\`"
    local chk
    chk=$($BINARY_CLIENT -N -B -e "${PR_SQL_INIT}CHECK TABLE \`$db\`.\`$t\`" 2>>"$PR_LOG" | awk -F'\t' 'END{print $3" "$4}')
    if [[ $n -eq 0 || "$chk" != "status OK" ]]; then
        pr_log "CHECK TABLE $db.$t: ${chk:-no result}; $n file(s) from the backup."
        pr_try "Drop $db.$t" $BINARY_CLIENT -e "${PR_SQL_INIT}DROP TABLE IF EXISTS \`$db\`.\`$t\`"
        pr_skip "$db.$t" "$engine files missing from the backup or failing CHECK TABLE"
    fi
}

# pr_restore_memory_table recreates a MEMORY table. Its rows only ever live in
# RAM, so no backup holds them (a server restart empties it just the same).
# It is opened once here, inside the restore's binlog-free session: MariaDB
# logs an implicit DELETE the first time a MEMORY table is opened, which on a
# replica would otherwise land in its binlog as an errant transaction.
pr_restore_memory_table() {
    local db="$1" t="$2"
    if ! pr_create_from_definition "$db" "$t"; then
        pr_skip "$db.$t" "cannot create the table from its backup definition"
        return
    fi
    pr_try "Open MEMORY table $db.$t without binary logging" $BINARY_CLIENT -e "${PR_SQL_INIT}SELECT 1 FROM \`$db\`.\`$t\` LIMIT 0"
    pr_log "MEMORY table $db.$t recreated empty: its rows are never part of a backup."
}

# pr_verify_restore compares what now exists on the server with what the
# backup contained -- every table, sequence, view, routine, event and
# trigger of every restored database. Anything missing fails the restore, so
# repman does not put the node back into replication incomplete.
pr_verify_restore() {
    local db want have missing rc=0
    for db in $(pr_list_databases); do
        want=$(cut -f1 "$PR_DEFS/$db/.list" | sort)
        have=$($BINARY_CLIENT -N -B -e "SELECT table_name FROM information_schema.tables WHERE table_schema='$db'" 2>>"$PR_LOG" | sort)
        missing=$(comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$have") | grep .)
        if [[ -s "$PR_DEFS/$db/.routines" ]]; then
            want=$(sort "$PR_DEFS/$db/.routines")
            have=$($BINARY_CLIENT -N -B -e "SELECT routine_type, routine_name FROM information_schema.routines WHERE routine_schema='$db' UNION ALL SELECT 'EVENT', event_name FROM information_schema.events WHERE event_schema='$db' UNION ALL SELECT 'TRIGGER', trigger_name FROM information_schema.triggers WHERE trigger_schema='$db'" 2>>"$PR_LOG" | sort)
            missing+=$'\n'$(comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$have") | grep . | tr '\t' ' ')
        fi
        missing=$(printf '%s\n' "$missing" | grep .)
        if [[ -n "$missing" ]]; then
            pr_log "VERIFY FAILED for $db, missing: $(printf '%s; ' $missing)"
            rc=1
        fi
    done
    if [[ $rc -eq 0 ]]; then
        pr_log "Verified: every table, view, routine, event and trigger of the backup exists on the server."
    else
        PR_STATUS=1
    fi
    return $rc
}


# pr_prepare_ok and pr_preflight check, before anything in the datadir is
# touched, that the prepare succeeded (before a server is ever started on the
# backup) and that every table of the backup has a definition and a
# supported layout. A failure leaves the server exactly as it was.
pr_prepare_ok() {
    if ! grep -q "completed OK!" "$1" 2>/dev/null; then
        pr_log "ERROR: the backup prepare did not complete (no 'completed OK!' in $1); nothing was changed."
        return 1
    fi
}

# pr_paths_ok succeeds when the directories the restore removes and replaces
# are set and absolute: an empty DATADIR or BACKUPDIR (a missing template
# value) would turn "$DATADIR/.system/..." into a path from the root of the
# filesystem, and a relative one into a path under the current directory.
pr_paths_ok() {
    [[ "$DATADIR" == /?* && "$DATADIR" != "/" && "$BACKUPDIR" == /?* && "$BACKUPDIR" != "/" ]]
}

# pr_rm_backupdir removes the unpacked backup; with unset directories it
# removes nothing and returns 1. reset_backupdir does the same and recreates
# the folder for a new transfer. Every removal of $BACKUPDIR goes through them.
pr_rm_backupdir() {
    pr_paths_ok || return 1
    rm -rf -- "$BACKUPDIR"
}

reset_backupdir() {
    pr_rm_backupdir && mkdir -p -- "$BACKUPDIR"
}

# pr_disk_ok checks, between the phases that write on the datadir volume after
# the stream was received, that RECEIVE_MIN_FREE_PCT percent of it is still
# free. When it is not, the backup is removed (it would keep the volume full)
# and PR_STATUS=1; returns 1. Called before anything in the datadir changes.
pr_disk_ok() {
    local phase="$1" total avail floor msg
    read -r total avail < <(df -P -B1 "$DATADIR" 2>/dev/null | awk 'NR==2{print $2, $4}')
    floor=$((${total:-0} * RECEIVE_MIN_FREE_PCT / 100))
    [[ -n "$avail" && "$avail" -ge "$floor" ]] && return 0
    msg="Partial restore stopped $phase: less than ${RECEIVE_MIN_FREE_PCT}% is free on the datadir volume ($((${avail:-0} / 1048576)) MiB free of $((${total:-0} / 1048576)) MiB). The backup was removed; no database was changed. Free space, or restore on a larger volume."
    pr_log "ERROR: $msg"
    pr_rm_backupdir
    PR_STATUS=1
    return 1
}

# pr_unsafe_name succeeds for an object name that cannot be put in the SQL
# the restore builds (quoted identifier, string literal) without escaping.
pr_unsafe_name() {
    [[ "$1" == *[\`\'\"\\]* ]]
}

pr_preflight() {
    local db t type engine rc=0
    if [[ ! -f "$PR_DEFS/.databases" ]]; then
        pr_log "ERROR: no database list was exported from the backup."
        return 1
    fi
    for db in $(pr_list_databases); do
        # A name the server stores encoded on disk (e.g. my-db as my@002ddb)
        # has no folder of that name: refuse rather than guess the files.
        if [[ ! -d "$BACKUPDIR/$db" || -L "$BACKUPDIR/$db" ]]; then
            pr_log "ERROR: database $db has no folder of that name in the backup."
            rc=1
            continue
        fi
        while IFS=$'\t' read -r t type engine; do
            [[ -z "$t" ]] && continue
            if pr_unsafe_name "$t"; then
                pr_log "ERROR: $db.$t has a quote, backtick or backslash in its name: it cannot be restored safely."
                rc=1
                continue
            fi
            if [[ ! -s "$PR_DEFS/$db/$t.sql" ]]; then
                pr_log "ERROR: no backup definition for $db.$t."
                rc=1
            fi
            if [[ "$engine" == "InnoDB" ]] && { compgen -G "$BACKUPDIR/$db/$t#P#*#SP#*.ibd" || compgen -G "$BACKUPDIR/$db/$t#p#*#sp#*.ibd"; } >/dev/null; then
                pr_log "ERROR: $db.$t is subpartitioned; EXCHANGE PARTITION cannot hot-replace subpartitions."
                rc=1
            fi
        done <"$PR_DEFS/$db/.list"
        # Routines, triggers and events are named in SQL later too.
        while IFS=$'\t' read -r type t; do
            if [[ -n "$t" ]] && pr_unsafe_name "$t"; then
                pr_log "ERROR: $type $db.$t has a quote, backtick or backslash in its name: it cannot be restored safely."
                rc=1
            fi
        done <"$PR_DEFS/$db/.routines" 2>/dev/null
    done
    return $rc
}

partialRestore() {
    send_lines_to_api "Starting partial restore..." "$job" "$LVL_INFO"
    # Deliberately NOT seeded from the prepare command's own exit status: that
    # was tried (folding $? from the mariabackup/xtrabackup --prepare --export
    # invocation in here) and reverted -- mariabackup/xtrabackup --prepare
    # --export can return a nonzero exit code from a harmless warning in its
    # internal --bootstrap sub-invocation while still producing a fully usable
    # export, so treating that exit code as authoritative caused false-positive
    # errors on restores that actually succeeded (confirmed: partialRestore
    # below ran to completion normally in the case that surfaced this). What
    # actually happened to the prepared files is only knowable by the restore
    # steps below actually touching them, so PR_STATUS is deliberately judged
    # solely on those (pr_cmd/pr_pipe), matching SQL mode's doneJob() check.
    PR_STATUS=0
    case "$job" in
    reseed*)
        PR_LOG="$LOG_DIR/reseed.out"
        ;;
    flashback*)
        PR_LOG="$LOG_DIR/flash.out"
        ;;
    *)
        PR_LOG="$LOG_DIR/$job.out"
        ;;
    esac

    if ! pr_paths_ok; then
        PR_STATUS=1
        echo "Partial restore not started: the data directory is not set. No database or table was changed." >>"$LOG_DIR/$job.out"
        send_lines_to_api "Partial restore not started: the data directory is not set." "$job" "$LVL_ERROR"
        return $PR_STATUS
    fi
    pr_log "Partial restore started for job $job."

    local isr=0
    [[ "$(id -u)" == "0" ]] && isr=1

    if [[ $isr -eq 1 ]]; then
        pr_cmd "Chown backup directory" chown -R "$(db_owner mysql:mysql)" "$BACKUPDIR"
    else
        pr_log "Skipping chown of backup directory; not running as root."
    fi

    PR_SKIPPED=()
    PR_RUN_ID=$(date -u +%Y%m%d%H%M%S)
    PR_DEFS="$BACKUPDIR/.mrm_defs"

    # No database or table is touched until the backup is known to be
    # prepared and every definition has been read from it. (Before that the
    # job already wrote on the datadir volume: the unpacked backup and the
    # temporary definition server's folders, removed when it stops.)
    send_lines_to_api "Reading table definitions from the backup..." "$job" "$LVL_DEBUG"
    if ! pr_prepare_ok "$PR_LOG" || ! pr_disk_ok "after the backup was prepared" || ! pr_export_definitions ||
        ! pr_disk_ok "after the definitions were read" || ! pr_preflight; then
        PR_STATUS=1
        pr_log "Partial restore aborted before any database or table change."
        echo "Partial restore aborted before any database or table change. See $PR_LOG." >>"$LOG_DIR/$job.out"
        send_lines_to_api "Partial restore aborted before any database or table change." "$job" "$LVL_ERROR"
        return $PR_STATUS
    fi

    local db t type engine views=() mh
    PR_IS_REPLICA=0
    mh=$(pr_master_host)
    [[ -n "$mh" ]] && PR_IS_REPLICA=1
    for db in $(pr_list_databases); do
        pr_log "Restoring database $db."
        send_lines_to_api "Restoring $db..." "$job" "$LVL_DEBUG"
        pr_recreate_database "$db"
        while IFS=$'\t' read -r t type engine; do
            [[ -z "$t" ]] && continue
            if [[ "$type" == "VIEW" ]]; then
                views+=("$db.$t")
            elif [[ "$engine" == "InnoDB" ]]; then
                pr_restore_innodb_table "$db" "$t"
            elif [[ "$engine" == "MEMORY" ]]; then
                pr_restore_memory_table "$db" "$t"
            else
                pr_restore_file_table "$db" "$t" "$engine"
            fi
        done <"$PR_DEFS/$db/.list"
    done
    # Routines before views: a view may call a stored function.
    for db in $(pr_list_databases); do
        pr_restore_routines "$db"
    done
    # Views last: they may select from tables of any database. A view on
    # another view can fail until that one exists, hence a few passes.
    local pass pending=("${views[@]}") retry
    for pass in 1 2 3 4 5; do
        retry=()
        for t in "${pending[@]}"; do
            db="${t%%.*}"
            if ! pr_create_from_definition "$db" "${t#*.}"; then
                retry+=("$t")
            fi
        done
        pending=("${retry[@]}")
        [[ ${#pending[@]} -eq 0 ]] && break
    done
    for t in "${pending[@]}"; do
        pr_skip "$t" "view could not be recreated"
    done
    pr_verify_restore
    if [[ ${#PR_SKIPPED[@]} -gt 0 ]]; then
        pr_log "Objects not restored (${#PR_SKIPPED[@]}): ${PR_SKIPPED[*]}"
        send_lines_to_api "Partial restore could not restore ${#PR_SKIPPED[@]} object(s): ${PR_SKIPPED[*]}" "$job" "$LVL_ERROR"
    fi
    # MyISAM tables of the mysql schema (MariaDB <= 10.3 layouts; Aria and
    # InnoDB since) are taken from the backup, except the account and grant
    # tables: the target's users and privileges are kept.
    for file in $(find $BACKUPDIR/mysql/ -name "*.MYD" | xargs -r -n 1 basename | cut -d'.' --complement -f2-); do
        case "$file" in
        user | db | host | global_priv | tables_priv | columns_priv | procs_priv | proxies_priv | roles_mapping | default_roles | role_edges | password_history) continue ;;
        esac
        pr_cmd "Move MyISAM files for mysql.$file" mv "$BACKUPDIR/mysql/$file."* "$DATADIR/mysql/"
        pr_cmd "Flush table mysql.$file" $BINARY_CLIENT -e "set sql_log_bin=0;FLUSH TABLE mysql.$file"
    done
    send_lines_to_api "Extracting GTID of the last change..." "$job" "$LVL_DEBUG"
    local g="" binfile="" binpos=""
    local f
    for f in "$BACKUPDIR/mariadb_backup_binlog_info" "$BACKUPDIR/xtrabackup_binlog_info"; do
        if [[ -f "$f" ]]; then
            binfile=$(awk '{print $1}' "$f" 2>>"$PR_LOG")
            binpos=$(awk '{print $2}' "$f" 2>>"$PR_LOG")
            g=$(awk '{print $3}' "$f" 2>>"$PR_LOG")
            [[ -n "$g" ]] && break
        fi
    done
    if [[ -z "$g" ]]; then
        for f in "$BACKUPDIR/mariadb_backup_info" "$BACKUPDIR/xtrabackup_info"; do
            if [[ -f "$f" ]]; then
                local l
                l=$(grep -m1 '^binlog_pos' "$f" 2>>"$PR_LOG")
                if [[ -n "$l" ]]; then
                    g=$(echo "$l" | awk -F, '{print $3}')
                    if [[ -n "$g" ]]; then
                        g=$(echo "$g" | sed -e 's/.*GTID of the last change[: ]*//')
                    else
                        g=$(echo "$l" | awk -F'= ' '{print $2}' | awk '{print $3}')
                    fi
                fi
                [[ -n "$g" ]] && break
            fi
        done
    fi
    if [[ -n "$g" ]]; then
        g=$(echo "$g" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
    fi

    if [[ -z "$g" ]]; then
        pr_log "No GTID info found in prepared backup."
    fi
    local vinfo=""
    vinfo=$($BINARY_CLIENT -N -e "SELECT VERSION();" 2>>"$PR_LOG")
    local vendor="mariadb"
    [[ "$vinfo" != *MariaDB* ]] && vendor="mysql"

    # GTID reset/apply and channel restart are NOT done here in either job
    # mode. This shell script only extracts the restore-confirmed position
    # and reports it, structured; repman is the vendor/topology-aware owner
    # that resets, applies the GTID, and restarts channels once this job
    # reports done -- via two different transports depending on job mode:
    #
    # SQL mode: this job has a jobs-table row, so the metadata goes in its
    # payload column; repman's AfterJobProcess (cluster/srv_job_backup.go),
    # driven by the SQL-mode-only terminal-job reconciliation
    # JobsCheckFinished, reads it from there.
    #
    # API mode: there is no jobs-table row (no $ID), so PARTIAL_RESTORE_JSON
    # (set here, NOT local -- it must survive after this function returns)
    # is picked up by the "done" report_job_state call below in the main
    # dispatch loop and sent in that HTTP callback's body instead; repman's
    # handlerMuxServerJobState (server/api_database.go) reads it from there
    # and calls the same RecoverPhysicalRestore repman uses for SQL mode.
    PARTIAL_RESTORE_JSON=$(printf '{"vendor":"%s","gtid":"%s","binLogFile":"%s","binLogPos":"%s"}' \
        "$vendor" "$g" "$binfile" "$binpos")
    if [[ "$JOBS_MODE" != "api" && -n "$ID" ]]; then
        pr_cmd "Report restore metadata" $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs SET payload='$PARTIAL_RESTORE_JSON' WHERE id=$ID;"
    fi

    send_lines_to_api "Flushing privileges..." "$job" "$LVL_DEBUG"
    pr_cmd "Flush privileges" $BINARY_CLIENT -e "set sql_log_bin=0;flush privileges;"

    # Replication restart is intentionally NOT done here, in either job mode
    # -- repman is the sole restart authority (SQL mode: AfterJobProcess off
    # the payload column; API mode: handlerMuxServerJobState off this job's
    # "done" callback body, see PARTIAL_RESTORE_JSON above), and restarts
    # every channel by its real ConnectionName, symmetric with the
    # StopAllSlaves() call made before the restore began.
    local mh=""
    mh=$(pr_master_host)
    if [[ -n "$mh" && "$mh" != "NULL" ]]; then
        pr_log "Master_Host configured ($mh); leaving channel restart to repman."
    else
        pr_log "No Master_Host configured; leaving channel restart to repman."
    fi

    if [[ "$PR_STATUS" -eq 0 ]]; then
        # The backup is no longer needed: without this it would hold about
        # its whole size on the datadir volume until the next reseed. After a
        # failure it is kept, to investigate. Quarantined leftovers of
        # earlier failed restores are bounded to the newest three.
        pr_try "Remove the restored backup $BACKUPDIR" pr_rm_backupdir
        ls -1dt "$DATADIR"/.system/orphan-quarantine-* 2>/dev/null | tail -n +4 | while read -r f; do
            pr_try "Remove old quarantine $f" rm -rf "$f"
        done
        echo "Partial restore completed successfully. See $PR_LOG." >>"$LOG_DIR/$job.out"
        send_lines_to_api "Partial restore done." "$job" "$LVL_INFO"
    else
        echo "Partial restore completed with errors. See $PR_LOG." >>"$LOG_DIR/$job.out"
        send_lines_to_api "Partial restore completed with errors." "$job" "$LVL_ERROR"
    fi
    return $PR_STATUS
}

jobsCheck() {
    if [ -f "$LOG_DIR/jobs-check.process.out" ]; then
        rm -f "$LOG_DIR/jobs-check.process.out"
    fi

    if [ -f "$LOG_DIR/jobs-check.out" ]; then
        rm -f "$LOG_DIR/jobs-check.out"
    fi

    if [ -d "$CHECKPOINT_DIR/jobs-check.checkpoint" ]; then
        rm -f "$CHECKPOINT_DIR/jobs-check.checkpoint"
    fi

    mkdir -p "$LOG_DIR/jobs-check.run"

    # Ensure run lockdir for current job is removed on script exit. Intended to replace the previous job trap which already removed at the end of loop entry.
    trap 'remove_run_lockdir "jobs-check"' EXIT

    check_task_needs "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "jobs-check"
    checkresult=$?
    if [ "$checkresult" != "0" ]; then
        if [ "$checkresult" = "2" ]; then
            echo "Failed to check task needs from API." >"$LOG_DIR/jobs-check.process.out"
        else
            echo "No need to run jobs-check." >"$LOG_DIR/jobs-check.process.out"
        fi

        remove_run_lockdir "jobs-check" 1
        trap - EXIT
        return $checkresult
    fi

    socatCleaner

    export LOG_DIR CHECKPOINT_DIR LOCK_DIR  # ensure subshells inherit them
    process_log_file "jobs-check" &

    echo "Waiting for receiver port." >"$LOG_DIR/jobs-check.process.out"

    #Send this script to the monitoring server using socat (using different variables to avoid confusion)
    RCV_PORT=$(check_jobs_receiver "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT")
    checkresult=$?

    if [ "$checkresult" != "0" ] || [ "$RCV_PORT" == "error" ]; then
        echo "Failed to get a valid receiver port from the monitoring server." >>"$LOG_DIR/jobs-check.process.out"
        remove_run_lockdir "jobs-check" 1
        trap - EXIT
        return 2 # error
    fi

    echo "Sending new script." >>"$LOG_DIR/jobs-check.process.out"
    socat -u FILE:"${BASH_SOURCE[0]}",rdonly TCP:$REPLICATION_MANAGER_HOST:$RCV_PORT,reuseaddr,bind=$SOCAT_BIND 2>>"$LOG_DIR/jobs-check.process.out"

    if [ $? -ne 0 ]; then
        echo "Failed to send the script via socat." >>"$LOG_DIR/jobs-check.process.out"
    else
        echo "Script sent successfully via socat." >>"$LOG_DIR/jobs-check.process.out"
    fi

    remove_run_lockdir "jobs-check" 5

    trap - EXIT
    return 0
}

jobsUpgrade() {
    if [ -f "$LOG_DIR/jobs-upgrade.process.out" ]; then
        rm -f "$LOG_DIR/jobs-upgrade.process.out"
    fi

    if [ -f "$LOG_DIR/jobs-upgrade.out" ]; then
        rm -f "$LOG_DIR/jobs-upgrade.out"
    fi

    if [ -d "$CHECKPOINT_DIR/jobs-upgrade.checkpoint" ]; then
        rm -f "$CHECKPOINT_DIR/jobs-upgrade.checkpoint"
    fi

    mkdir -p "$LOG_DIR/jobs-upgrade.run"
    # Ensure run lockdir for current job is removed on script exit. Intended to replace the previous job trap which already removed at the end of loop entry.
    trap 'remove_run_lockdir "jobs-upgrade"' EXIT

    check_task_needs "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "jobs-upgrade"

    checkresult=$?
    if [ "$checkresult" != "0" ]; then
        if [ "$checkresult" = "2" ]; then
            echo "Failed to check task needs from API." >"$LOG_DIR/jobs-upgrade.process.out"
        else
            echo "No need to run jobs-upgrade." >"$LOG_DIR/jobs-upgrade.process.out"
        fi

        remove_run_lockdir "jobs-upgrade" 1
        trap - EXIT
        return
    fi

    socatCleaner

    export LOG_DIR CHECKPOINT_DIR LOCK_DIR  # ensure subshells inherit them
    process_log_file "jobs-upgrade" &

    echo "Waiting new script." >"$LOG_DIR/jobs-upgrade.process.out"

    # Open receiver port to get the new script to a temporary file
    TEMP_FILE="${BASH_SOURCE[0]}.tmp"
    timeout 120 socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND - > "$TEMP_FILE" 2>>"$LOG_DIR/jobs-upgrade.process.out" &
    SOCAT_PID=$!

    # Request the upgrade
    request_jobs_upgrade "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT"

    # Wait for socat to finish
    wait $SOCAT_PID
    SOCAT_EXIT_CODE=$?

    # Only replace if the socat command succeeded
    if [ $SOCAT_EXIT_CODE -eq 0 ] && [ -s "$TEMP_FILE" ]; then
        # Check if the new script has # EOF marker
        if ! grep -q '^# EOF$' "$TEMP_FILE"; then
            send_lines_to_api "Received script is invalid (missing EOF marker)." "jobs-upgrade" "$LVL_ERROR"
            rm -f "$TEMP_FILE"
        else
            # Replace the current script
            chmod +x "$TEMP_FILE"
            cp "$TEMP_FILE" "${BASH_SOURCE[0]}"

            send_lines_to_api "Script updated. Re-executing with the new version." "jobs-upgrade" "$LVL_INFO"

            remove_run_lockdir "jobs-upgrade" 5
            trap - EXIT

            # Re-execute with the new version
            exec bash "${BASH_SOURCE[0]}" "$@"
        fi
    else
        send_lines_to_api "Failed to receive the new script via socat." "jobs-upgrade" "$LVL_ERROR"
        rm -f "$TEMP_FILE"
    fi

    remove_run_lockdir "jobs-upgrade" 1
    trap - EXIT
}

#######################
# JOB START HERE
#######################

validate_environment || exit 1

ensure_directories || exit 1

select_database_binaries || exit 1

TOKEN="$(secret_login "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT")"
if [ "$TOKEN" == "error" ]; then
    echo "Failed to authenticate with the replication manager API."
    exit 1
fi

# DBU sensor: push the service cgroup + datadir maxima once per run (~60s
# launcher cadence), BEFORE the job dispatch, so it still fires on a cycle where
# a backup would later block or early-exit. Thin + fail-soft (see collect_dbu).
collect_dbu || true

# Clear previous temporary files
echo "" > "$LOG_DIR/curl_response.txt"
echo "" > "$LOG_DIR/request.txt"
echo "" > "$LOG_DIR/encrypt.txt"

recoverDeadJobs

jobsCheck
checkresult=$?
if [ "$checkresult" != "2" ]; then
    # If jobsCheck did not error, proceed to jobsUpgrade
    jobsUpgrade "$@"
fi

####################
# Check if the configuration file has changed
####################
if need_refresh_config; then
    send_lines_to_api "Config refresh needed, running print jobs..." "print-defaults" "$LVL_INFO"
    run_config_print_jobs
else
    send_lines_to_api "No need to refresh configuration." "print-defaults" "$LVL_DEBUG"
fi

#####################
# OTHER JOBS
#####################

for job in "${JOBS[@]}"; do

    TASK_NEEDED=false
    ADDRESS=""
    ID=""

    if [[ "$JOBS_MODE" == "api" ]]; then
        # API mode: check cookies via the needs API endpoint
        if check_task_needs "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "$job"; then
            TASK_NEEDED=true
            # For tasks that stream data, get the receiver address from the API
            case "$job" in
                mariabackup|xtrabackup|reseedmariabackup|reseedxtrabackup|flashbackmariabackup|flashbackxtrabackup|errorlog|slowquery|auditlog|sqlerrorlog)
                    if get_task_receiver "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "$job"; then
                        ADDRESS="$RECEIVER_ADDRESS"
                    else
                        send_lines_to_api "Failed to get receiver for $job" "$job" "$LVL_ERROR"
                        TASK_NEEDED=false
                    fi
                    ;;
            esac
        fi
    else
        # SQL mode (default): poll the jobs table
        TASK=($(echo "SELECT concat(id,'@',server,':',port) FROM replication_manager_schema.jobs WHERE task='$job' and done=0 AND state=0 order by id desc limit 1" | $BINARY_CLIENT -N))
        ADDRESS=($(echo $TASK | awk -F@ '{ print $2 }'))
        ID=($(echo $TASK | awk -F@ '{ print $1 }'))
        if [ "$ID" != "" ]; then
            TASK_NEEDED=true
        fi
    fi

    if $TASK_NEEDED; then
        send_lines_to_api "Job $job initiated. Clearing previous logs..." "$job" "$LVL_INFO"
        case "$job" in
            mariabackup|xtrabackup)
                rm -f "$LOG_DIR/backup.out"
                ;;
            reseedmariabackup|reseedxtrabackup)
                rm -f "$LOG_DIR/reseed.out"
                ;;
            flashbackmariabackup|flashbackxtrabackup)
                rm -f "$LOG_DIR/flash.out"
                ;;
        esac

        rm -f "$LOG_DIR/$job.out"
        rm -f "$CHECKPOINT_DIR/$job.checkpoint"

        mkdir -p "$LOG_DIR/$job.run"
        echo "$$" >"$LOG_DIR/$job.run/pid"
        proc_start_ticks "$$" >"$LOG_DIR/$job.run/start"
        process_log_file "$job" &
        trap 'remove_run_lockdir "$job"' EXIT
        echo "Processing $job"

        # Report job as processing
        if [[ "$JOBS_MODE" == "api" ]]; then
            report_job_state "$job" "processing"
        elif [[ -n "$ID" ]]; then
            $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set done=1 WHERE done=0 AND task='$job' AND ID<>$ID;"
            $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set state=1, result='processing' WHERE task='$job' AND ID=$ID;"
        fi

        case "$job" in
        reseedxtrabackup)
            reset_backupdir
            echo "Waiting for the backup stream." >>"$LOG_DIR/reseed.out"
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            if receiveBackup xbstream; then
                $XTRABACKUP --prepare --export --target-dir=$BACKUPDIR 2>>"$LOG_DIR/reseed.out"
                partialRestore
            fi
            ;;
        reseedmariabackup)
            reset_backupdir
            echo "Waiting for the backup stream." >>"$LOG_DIR/reseed.out"
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            if receiveBackup mbstream; then
                # mbstream -p, --parallel
                $MARIADB_BACKUP --prepare --export --target-dir=$BACKUPDIR 2>>"$LOG_DIR/reseed.out"
                partialRestore
            fi
            ;;
        flashbackxtrabackup)
            reset_backupdir
            echo "Waiting for the backup stream." >>"$LOG_DIR/flash.out"
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            if receiveBackup xbstream; then
                $XTRABACKUP --prepare --export --target-dir=$BACKUPDIR 2>>"$LOG_DIR/flash.out"
                partialRestore
            fi
            ;;
        flashbackmariabackup)
            reset_backupdir
            echo "Waiting for the backup stream." >>"$LOG_DIR/flash.out"
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            if receiveBackup xbstream; then
                $MARIADB_BACKUP --prepare --export --target-dir=$BACKUPDIR 2>>"$LOG_DIR/flash.out"
                partialRestore
            fi
            ;;
        xtrabackup)
            cd /docker-entrypoint-initdb.d
            xtrabackup_undo_args
            $XTRABACKUP --defaults-file=$MYSQL_CONF/my.cnf --backup "${XB_UNDO_ARGS[@]}" -u$USER -H$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT --stream=xbstream --target-dir=$LOG_DIR/ 2>"$LOG_DIR/backup.out" | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
            ;;
        mariabackup)
            cd /docker-entrypoint-initdb.d
            $MARIADB_BACKUP --defaults-file="$MYSQL_CONF/my.cnf" --backup --databases-exclude=.system --protocol=TCP --user="$USER" --host="$MYSQL_SERVER" --password="$PASSWORD" --port="$MYSQL_PORT" --stream=xbstream --target-dir="$LOG_DIR/" 2>"$LOG_DIR/backup.out" | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
            ;;
        errorlog)
            dblogfile "$ERRORLOG" "$job"
            ;;
        slowquery)
            dblogfile "$SLOWLOG" "$job"
            ;;
        auditlog)
            dblogfile "$AUDITLOG" "$job"
            ;;
        sqlerrorlog)
            dblogfile "$SQLERRORLOG" "$job"
            ;;
        zfssnapback)
            LASTSNAP=$(zfs list -r -t all | grep zp%%ENV:SERVICES_SVCNAME%%_pod01 | grep daily | sort -r | head -n 1 | cut -d" " -f1)
            %%ENV:SERVICES_SVCNAME%% stop
            zfs rollback $LASTSNAP
            %%ENV:SERVICES_SVCNAME%% start
            ;;
        optimize)
            $BINARY_CHECK -o $DB_CONN_PARAMETERS --all-databases --skip-write-binlog &>"$LOG_DIR/$job.process.out"
            ;;
        restart)
            systemctl restart mysql
            journalctl -u mysql >"$LOG_DIR/$job.process.out"
            ;;
        stop)
            systemctl stop mysql
            journalctl -u mysql >"$LOG_DIR/$job.process.out"
            ;;
        esac

        # Report job completion
        if [[ "$JOBS_MODE" == "api" ]]; then
            # Check backup success the same way doneJob does.
            # Not `local`: this code runs in the top-level JOBS loop, not
            # inside a function — `local` here prints "local: can only be
            # used in a function" and does not assign. The script has no
            # set -e, so execution continues with api_job_result left
            # unset/stale for any job that doesn't hit the case branches
            # below (e.g. auditlog, sqlerrorlog), which then calls
            # report_job_state with an empty state and 404s on the route's
            # non-empty {jobstate} segment.
            api_job_result="done"
            case "$job" in
            mariabackup|xtrabackup)
                if ! grep -qE '([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}|\[Xtrabackup\]) completed OK!' "$LOG_DIR/backup.out" 2>/dev/null; then
                    api_job_result="error"
                fi
                ;;
            reseedmariabackup|reseedxtrabackup|flashbackmariabackup|flashbackxtrabackup)
                # Mirrors doneJob()'s SQL-mode check above: PR_STATUS is the
                # rollup partialRestore() computes from every actual restore
                # step (pr_cmd/pr_pipe), so it's the authoritative signal here.
                # Do NOT grep reseed.out/flash.out for "completed OK!" like the
                # plain mariabackup|xtrabackup case above does -- that banner
                # is emitted by --backup mode, not guaranteed by the --prepare
                # --export invocation these tasks use, so a fully successful
                # partial restore can still lack that exact line.
                if [ "$PR_STATUS" -ne 0 ]; then
                    api_job_result="error"
                fi
                ;;
            esac
            # Physical reseed/flashback: forward the restore metadata
            # partialRestore() extracted (PARTIAL_RESTORE_JSON, set inside it
            # unconditionally, not local) so repman can run the same
            # GTID-apply/channel-restart recovery this mode has no
            # jobs-table row to carry it through otherwise. See the
            # PARTIAL_RESTORE_JSON comment in partialRestore().
            case "$job" in
            reseedmariabackup|reseedxtrabackup|flashbackmariabackup|flashbackxtrabackup)
                report_job_state "$job" "$api_job_result" "$PARTIAL_RESTORE_JSON"
                ;;
            *)
                report_job_state "$job" "$api_job_result"
                ;;
            esac
            if [[ "$api_job_result" == "done" ]]; then
                send_lines_to_api "Job $job completed" "$job" "$LVL_INFO"
            else
                send_lines_to_api "Job $job ended with error" "$job" "$LVL_ERROR"
            fi
        else
            doneJob "$job"
        fi
        remove_run_lockdir "$job"
        trap - EXIT
    else
        # No task needed — handle special start case
        case "$job" in
        start)
            if [ "curl -so /dev/null -w '%{response_code}'   http://$REPLICATION_MANAGER_ADDR/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/need-start" == "200" ]; then
                curl http://$REPLICATION_MANAGER_ADDR/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/config | tar xzvf etc/* - -C $CONFDIR/../..
                systemctl start mysql
            fi
            ;;
        esac
    fi
done

# EOF
