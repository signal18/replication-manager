#!/bin/bash
# This script is given as sample and might be overwritten on upgrade
# The real script is auto generated based on compliance json 

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

# MariaDB binaries
readonly MARIADB_CLIENT="${CLIENT_BASEDIR}/mariadb"
readonly MARIADB_CHECK="${CLIENT_BASEDIR}/mariadb-check"
readonly MARIADB_DUMP="${CLIENT_BASEDIR}/mariadb-dump"
readonly MARIADB_BACKUP="${CLIENT_BASEDIR}/mariabackup"

# MySQL binaries
readonly MYSQL_CLIENT="${CLIENT_BASEDIR}/mysql"
readonly MYSQL_CHECK="${CLIENT_BASEDIR}/mysqlcheck"
readonly MYSQL_DUMP="${CLIENT_BASEDIR}/mysqldump"
readonly XTRABACKUP="${CLIENT_BASEDIR}/xtrabackup"
readonly INNODBACKUPEX="${CLIENT_BASEDIR}/innobackupex"

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
readonly API_LOG_FILE="${LOG_DIR}/api_calls.log"
readonly LOG_MAX_SIZE=1048576  # 1MB

# Job types
readonly -a JOBS=(
    "xtrabackup" "mariabackup" "errorlog" "slowquery" 
    "auditlog" "sqlerrorlog" "zfssnapback" "optimize" 
    "reseedxtrabackup" "reseedmariabackup" 
    "flashbackxtrabackup" "flashbackmariadbackup" 
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

# Extract a single HTTP header value from response
# Usage: extract_http_header "response" "Header-Name"
extract_http_header() {
    local response="$1"
    local header_name="$2"
    echo "$response" | awk -v hdr="$header_name" 'BEGIN { IGNORECASE=1 }
        /^(\r)?$/ { exit }
        {
            line=$0
            sub(/\r$/, "", line)
            split(line, parts, ":")
            if (tolower(parts[1]) == tolower(hdr)) {
                value=substr(line, index(line, ":") + 1)
                sub(/^[ \t]+/, "", value)
                print value
                exit
            }
        }'
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
    # Use connect-timeout for connect phase and socat -T for read/write timeout.
    # Do NOT use readbytes (it truncates large files).
    local socat_opts="connect-timeout=$timeout"
    
    if is_ssl_port "$port"; then
        socat_target="OPENSSL:$host:$port,verify=0,$socat_opts"
    else
        socat_target="TCP:$host:$port,$socat_opts"
    fi
    
    echo -en "$request" | socat -T "$timeout" - "$socat_target" 2> >(grep -v "refusing to set empty SNI host name" >&2)
}

########################
# API Functions        #
########################

# Send HTTP GET request (reuses send_http_request)
send_http_get() {
    local host="$1"
    local port="$2"
    local endpoint="$3"
    send_http_request "GET" "$host" "$port" "$endpoint"
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
    send_http_request "POST" "$host" "$port" "$api_endpoint" "$json_data"
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
        chown -R 999:999 "$extract_dir/etc/mysql" 2>/dev/null || true
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
        chown 999:999 "$dummy_config_file" 2>/dev/null || true
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

# Pull jobs-upgrade script directly from API (v2), with v1 push fallback handled by caller
# Usage: pull_jobs_upgrade_script "target_file"
pull_jobs_upgrade_script() {
    local target_file="$1"
    local cluster="$CLUSTER_NAME"
    local server="$MYSQL_SERVER"
    local port="$MYSQL_PORT"
    local api_host="$REPLICATION_MANAGER_HOST"
    local api_port="$REPLICATION_MANAGER_PORT"
    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/get-jobs-upgrade-script"

    local encrypted_data
    encrypted_data=$(encrypt_data "{\"server\":\"$server:$port\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")
    local json_data="{\"data\":\"$encrypted_data\"}"

    # Use octet-stream accept to force HTTP/1.0 mode in send_http_request,
    # avoiding chunked transfer framing corruption for script download.
    local response
    response=$(send_http_request "POST" "$api_host" "$api_port" "$endpoint" "$json_data" "application/octet-stream")
    local http_code=$(extract_http_code "$response")

    if [[ "$http_code" != "200" ]]; then
        return 1
    fi

    extract_http_body "$response" >"$target_file"

    if [[ ! -s "$target_file" ]]; then
        return 1
    fi

    local expected_sha256=$(extract_http_header "$response" "X-Repman-Script-Sha256")
    if [[ -n "$expected_sha256" ]] && command -v sha256sum >/dev/null 2>&1; then
        local actual_sha256
        actual_sha256=$(sha256sum "$target_file" | awk '{print $1}')
        if [[ "$actual_sha256" != "$expected_sha256" ]]; then
            send_lines_to_api "Pulled jobs-upgrade script checksum mismatch (expected $expected_sha256, got $actual_sha256)." "jobs-upgrade" "$LVL_ERROR"
            return 1
        fi
    fi

    return 0
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

    # Remove the run lockdir if it exists
    if [ -d "$run_lockdir" ]; then
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

    # Extract new lines into temporary file
    local TMPLOG="$TMPDIR/${JOB}.newlines"
    tail -n +"$NEXT_LINE" "$DBLOG" > "$TMPLOG"

    # If DB log has content
    if [ -s "$TMPLOG" ]; then
        # Send content via socat
        socat -u stdio TCP:$ADDRESS < "$TMPLOG" &>"$LOG_DIR/$JOB.process.out"
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
        matches=$(sed -n '/[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\} [0-9]\{2\}:[0-9]\{2\}:[0-9]\{2\} completed OK!/p' $LOG_DIR/backup.out)
        if [ ! -n "$matches" ]; then
            jobstate=5
            done=0
            echo "No successful record (complete OK!) found in $LOG_DIR/backup.out." >>$LOG_DIR/$job.out
        fi
        ;;
        reseedmariabackup | reseedxtrabackup)
        matches=$(sed -n '/[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\} [0-9]\{2\}:[0-9]\{2\}:[0-9]\{2\} completed OK!/p' $LOG_DIR/reseed.out)
        if [ ! -n "$matches" ]; then
            jobstate=5
            done=0
            echo "No successful record (complete OK!) found in $LOG_DIR/reseed.out." >>$LOG_DIR/$job.out
        fi
        ;;
        flashbackmariabackup | flashbackxtrabackup)
        matches=$(sed -n '/[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\} [0-9]\{2\}:[0-9]\{2\}:[0-9]\{2\} completed OK!/p' $LOG_DIR/flash.out)
        if [ ! -n "$matches" ]; then
            jobstate=5
            done=0
            echo "No successful record (complete OK!) found in $LOG_DIR/flash.out." >>$LOG_DIR/$job.out
        fi
        ;;
    esac
    
    if [ $jobstate -eq 3 ]; then
        send_lines_to_api "Job $job ended with state: Finished" "$job" "$LVL_INFO"
    else
        send_lines_to_api "Job $job ended with state: Error" "$job" "$LVL_ERROR"
    fi
    $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set end=NOW(), state=$jobstate, result=LOAD_FILE('$LOG_DIR/$job.out'), done=$done  WHERE id='$ID';" &
}

pauseJob() {
    $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set state=2, result='waiting' WHERE id='$ID';" &
}

partialRestore() {
    send_lines_to_api "Starting partial restore..." "$job" "$LVL_INFO"
    chown -R mysql:mysql $BACKUPDIR 
    $BINARY_CLIENT -e "set sql_log_bin=0;install plugin BLACKHOLE soname 'ha_blackhole.so'"
    for dir in $(ls -d $BACKUPDIR/*/ | xargs -n 1 basename | grep -vE 'mysql|performance_schema|replication_manager_schema'); do
        send_lines_to_api "Restoring $dir..." "$job" "$LVL_DEBUG"
        $BINARY_CLIENT -e "set sql_log_bin=0;drop database IF EXISTS $dir; CREATE DATABASE $dir;"

        for file in $(find $BACKUPDIR/$dir/ -name "*.ibd" | xargs -n 1 basename | cut -d'.' --complement -f2-); do
            cat $BACKUPDIR/$dir/$file.frm | sed -e 's/\x06\x00\x49\x6E\x6E\x6F\x44\x42\x00\x00\x00/\x09\x00\x42\x4C\x41\x43\x4B\x48\x4F\x4C\x45/g' >$DATADIR/$dir/mrm_pivo.frm
            chown mysql:mysql $DATADIR/$dir/mrm_pivo.frm
            $BINARY_CLIENT -e "set sql_log_bin=0;ALTER TABLE $dir.mrm_pivo  engine=innodb;RENAME TABLE $dir.mrm_pivo TO $dir.$file; ALTER TABLE $dir.$file DISCARD TABLESPACE;"
            mv $BACKUPDIR/$dir/$file.ibd $DATADIR/$dir/$file.ibd
            mv $BACKUPDIR/$dir/$file.exp $DATADIR/$dir/$file.exp
            mv $BACKUPDIR/$dir/$file.cfg $DATADIR/$dir/$file.cfg
            mv $BACKUPDIR/$dir/$file.TRG $DATADIR/$dir/$file.TRG
            $BINARY_CLIENT -e "set sql_log_bin=0;ALTER TABLE $dir.$file IMPORT TABLESPACE"
        done
        for file in $(find $BACKUPDIR/$dir/ -name "*.MYD" | xargs -n 1 basename | cut -d'.' --complement -f2-); do
            mv $BACKUPDIR/$dir/$file.* $DATADIR/$dir/
            $BINARY_CLIENT -e "set sql_log_bin=0;FLUSH TABLE $dir.$file"
        done
        for file in $(find $BACKUPDIR/$dir/ -name "*.CSV" | xargs -n 1 basename | cut -d'.' --complement -f2-); do
            mv $BACKUPDIR/$dir/$file.* $DATADIR/$dir/
            $BINARY_CLIENT -e "set sql_log_bin=0;FLUSH TABLE $dir.$file"
        done
    done
    for file in $(find $BACKUPDIR/mysql/ -name "*.MYD" | xargs -n 1 basename | cut -d'.' --complement -f2-); do
        mv $BACKUPDIR/mysql/$file.* $DATADIR/mysql/
        $BINARY_CLIENT -e "set sql_log_bin=0;FLUSH TABLE mysql.$file"
    done
    send_lines_to_api "Setting GTID of the last change..." "$job" "$LVL_DEBUG"
    cat $BACKUPDIR/xtrabackup_info | grep binlog_pos | awk -F, '{ print $3 }' | sed -e 's/GTID of the last change/set sql_log_bin=0;set global gtid_slave_pos=/g' | $BINARY_CLIENT
    send_lines_to_api "Flushing privileges..." "$job" "$LVL_DEBUG"
    $BINARY_CLIENT -e"set sql_log_bin=0;flush privileges;start slave;"
    send_lines_to_api "Partial restore done." "$job" "$LVL_INFO"
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
    local listener_timeout="${JOBS_UPGRADE_LISTENER_TIMEOUT:-900}"
    if ! [[ "$listener_timeout" =~ ^[0-9]+$ ]]; then
        listener_timeout=900
    fi

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

    # First try pull-based upgrade (v2, server-first compatible)
    TEMP_FILE="${BASH_SOURCE[0]}.tmp"
    if pull_jobs_upgrade_script "$TEMP_FILE"; then
        send_lines_to_api "Pulled new jobs script from API." "jobs-upgrade" "$LVL_INFO"

        if ! grep -q '^# EOF$' "$TEMP_FILE"; then
            echo "Received pull-based script is invalid (missing EOF marker)." >>"$LOG_DIR/jobs-upgrade.process.out"
            send_lines_to_api "Received pull-based script is invalid (missing EOF marker)." "jobs-upgrade" "$LVL_ERROR"
            rm -f "$TEMP_FILE"
        else
            chmod +x "$TEMP_FILE"
            cp "$TEMP_FILE" "${BASH_SOURCE[0]}"

            send_lines_to_api "Script updated via pull API. Re-executing with the new version." "jobs-upgrade" "$LVL_INFO"
            remove_run_lockdir "jobs-upgrade" 5
            trap - EXIT
            exec bash "${BASH_SOURCE[0]}" "$@"
        fi
    else
        send_lines_to_api "Pull API unavailable or failed, falling back to legacy push listener." "jobs-upgrade" "$LVL_DEBUG"
    fi

    # Open receiver port to get the new script to a temporary file
    socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND - > "$TEMP_FILE" 2>>"$LOG_DIR/jobs-upgrade.process.out" &
    SOCAT_PID=$!

    # Request the upgrade
    request_jobs_upgrade "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT"
    local REQUEST_EXIT_CODE=$?

    if [ $REQUEST_EXIT_CODE -ne 0 ]; then
        echo "Failed to request jobs-upgrade from API." >>"$LOG_DIR/jobs-upgrade.process.out"
        send_lines_to_api "Failed to request jobs-upgrade from API." "jobs-upgrade" "$LVL_ERROR"
        kill "$SOCAT_PID" 2>/dev/null
        wait "$SOCAT_PID" 2>/dev/null
        rm -f "$TEMP_FILE"
        remove_run_lockdir "jobs-upgrade" 1
        trap - EXIT
        return 1
    fi

    # Wait for socat to finish with bounded timeout
    local elapsed=0
    while kill -0 "$SOCAT_PID" 2>/dev/null; do
        if [ $elapsed -ge $listener_timeout ]; then
            kill "$SOCAT_PID" 2>/dev/null
            wait "$SOCAT_PID" 2>/dev/null
            echo "Timeout waiting for jobs-upgrade script after ${listener_timeout}s." >>"$LOG_DIR/jobs-upgrade.process.out"
            send_lines_to_api "Timeout waiting for jobs-upgrade script after ${listener_timeout}s." "jobs-upgrade" "$LVL_ERROR"
            rm -f "$TEMP_FILE"
            remove_run_lockdir "jobs-upgrade" 1
            trap - EXIT
            return 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    wait "$SOCAT_PID"
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
        echo "Failed to receive the new script via socat." >>"$LOG_DIR/jobs-upgrade.process.out"
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

# Clear previous temporary files
echo "" > "$LOG_DIR/curl_response.txt"
echo "" > "$LOG_DIR/request.txt"
echo "" > "$LOG_DIR/encrypt.txt"

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

    TASK=($(echo "SELECT concat(id,'@',server,':',port) FROM replication_manager_schema.jobs WHERE task='$job' and done=0 AND state=0 order by id desc limit 1" | $BINARY_CLIENT -N))

    ADDRESS=($(echo $TASK | awk -F@ '{ print $2 }'))
    ID=($(echo $TASK | awk -F@ '{ print $1 }'))

    if [ "$ID" != "" ]; then
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
    fi


    if [ "$ADDRESS" == "" ]; then
        # echo "No $job needed"
        case "$job" in
        start)
            if [ "curl -so /dev/null -w '%{response_code}'   http://$REPLICATION_MANAGER_ADDR/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/need-start" == "200" ]; then
                curl http://$REPLICATION_MANAGER_ADDR/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/config | tar xzvf etc/* - -C $CONFDIR/../..
                systemctl start mysql
            fi
            ;;
        esac
    else

        mkdir -p "$LOG_DIR/$job.run"
        process_log_file "$job" &
        # Ensure run lockdir for current job is removed on script exit. Intended to replace the previous job trap which already removed at the end of loop entry.
        trap 'remove_run_lockdir "$job"' EXIT
        echo "Processing $job"
        
        #purge de past
        $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set done=1 WHERE done=0 AND task='$job' AND ID<>$ID;"
        $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set state=1, result='processing' WHERE task='$job' AND ID=$ID;"
        case "$job" in
        reseedxtrabackup)
            rm -rf $BACKUPDIR
            mkdir -p $BACKUPDIR
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND STDOUT | xbstream -x -C $BACKUPDIR
            $XTRABACKUP --prepare --export --target-dir=$BACKUPDIR 2>"$LOG_DIR/reseed.out"
            partialRestore
            ;;
        reseedmariabackup)
            rm -rf $BACKUPDIR
            mkdir -p $BACKUPDIR
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND STDOUT | mbstream -x -C $BACKUPDIR
            # mbstream -p, --parallel
            $MARIADB_BACKUP --prepare --export --target-dir=$BACKUPDIR 2>"$LOG_DIR/reseed.out"
            partialRestore
            ;;
        flashbackxtrabackup)
            rm -rf $BACKUPDIR
            mkdir -p $BACKUPDIR
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND STDOUT | xbstream -x -C $BACKUPDIR
            $XTRABACKUP --prepare --export --target-dir=$BACKUPDIR 2>"$LOG_DIR/flash.out"
            partialRestore
            ;;
        flashbackmariadbackup)
            rm -rf $BACKUPDIR
            mkdir -p $BACKUPDIR
            socatCleaner
            echo "Waiting backup." >"$LOG_DIR/$job.out"
            pauseJob "$job"
            socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND STDOUT | xbstream -x -C $BACKUPDIR
            $MARIADB_BACKUP --prepare --export --target-dir=$BACKUPDIR 2>"$LOG_DIR/flash.out"
            partialRestore
            ;;
        xtrabackup)
            cd /docker-entrypoint-initdb.d
            $XTRABACKUP --defaults-file=$MYSQL_CONF/my.cnf --backup -u$USER -H$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT --stream=xbstream --target-dir=$LOG_DIR/ 2>"$LOG_DIR/backup.out" | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
            ;;
        mariabackup)
            cd /docker-entrypoint-initdb.d
            $MARIADB_BACKUP --innobackupex --defaults-file=$MYSQL_CONF/my.cnf --databases-exclude=.system --protocol=TCP $DB_CONN_PARAMETERS --stream=xbstream 2>"$LOG_DIR/backup.out" | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
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
        doneJob "$job"
        remove_run_lockdir "$job" 
        trap - EXIT
    fi
done

# EOF
