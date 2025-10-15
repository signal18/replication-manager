#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPMAN_CLIENT="$SCRIPT_DIR/replication-manager-cli"

USER=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%
PASSWORD=$MYSQL_ROOT_PASSWORD
MYSQL_PORT=%%ENV:SERVER_PORT%%
MYSQL_SERVER=%%ENV:SERVER_HOST%%
CLUSTER_NAME=%%ENV:SVC_NAMESPACE%%
REPLICATION_MANAGER_ADDR=%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_ADDR%%
REPLICATION_MANAGER_URL=%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL%%
REPLICATION_MANAGER_HOST=$(echo "$REPLICATION_MANAGER_URL" | cut -d":" -f1)
REPLICATION_MANAGER_PORT=$(echo "$REPLICATION_MANAGER_URL" | cut -d":" -f2)
MYSQL_CONF=%%ENV:SVC_CONF_ENV_MYSQL_CONFDIR%%
DATADIR=%%ENV:SVC_CONF_ENV_MYSQL_DATADIR%%
BINARY_CLIENT_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"

# MariaDB binary paths
MARIADB_CLIENT="%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mariadb"
MARIADB_CHECK=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mariadb-check
MARIADB_DUMP=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mariadb-dump

# MySQL binary paths
MYSQL_CLIENT="%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mysql"
MYSQL_CHECK=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mysqlcheck
MYSQL_DUMP=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mysqldump

SST_RECEIVER_PORT=%%ENV:SVC_CONF_ENV_SST_RECEIVER_PORT%%
SOCAT_BIND=%%ENV:SERVER_IP%%
MARIADB_BACKUP=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/mariabackup
XTRABACKUP=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/xtrabackup
INNODBACKUPEX=%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%/innobackupex

ERROLOG=%%ENV:SVC_CONF_ENV_ERROR_LOG%%
SLOWLOG=%%ENV:SVC_CONF_ENV_SLOW_LOG%%
BACKUPDIR=$DATADIR/.system/backup
TMP_DIR=%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%

# Directory where the logs are stored
LOG_DIR="$TMP_DIR"
# Directory where the checkpoints are stored
CHECKPOINT_DIR="$TMP_DIR/checkpoints"
# Directory where lock files are stored
LOCK_DIR="$TMP_DIR/locks"
BATCH_SIZE=5
JOBS=("xtrabackup" "mariabackup" "errorlog" "slowquery" "zfssnapback" "optimize" "reseedxtrabackup" "reseedmariabackup" "flashbackxtrabackup" "flashbackmariadbackup" "stop" "restart" "start")

# OSX need socat extra path
export PATH=$PATH:/usr/local/bin

# Logging levels
LVL_ERROR="ERROR"
LVL_WARN="WARN"
LVL_INFO="INFO"
LVL_DEBUG="DEBUG"

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

# Function to send encrypted data via HTTP
send_encrypted_data_http() {
    local api_host="$1"
    local api_port="$2"
    local api_endpoint="$3"
    local data="$4"
    local json_data="{\"data\":\"$data\"}"
    local api_host_port="$api_host:$api_port"

    local request="POST $api_endpoint HTTP/1.1\r\nHost: $api_host\r\nContent-Type: application/json\r\nContent-Length: ${#json_data}\r\n\r\n$json_data"
    local response=$(echo -en "$request" | socat - TCP:$api_host_port)
    echo "$response"
}

# Function to send encrypted data via HTTPS
send_encrypted_data_https() {
    local api_host="$1"
    local api_port="$2"
    local api_endpoint="$3"
    local data="$4"
    local json_data="{\"data\":\"$data\"}"
    local api_host_port="$api_host:$api_port"

    local request="POST $api_endpoint HTTP/1.1\r\nHost: $api_host\r\nContent-Type: application/json\r\nContent-Length: ${#json_data}\r\n\r\n$json_data"
    local response=$(echo -en "$request" | socat - OPENSSL:$api_host_port,verify=0)
    echo "$response"
}

# Generic function to send encrypted data to any API endpoint
# Usage: send_encrypted_api_request "host:port" "/api/path" "raw_data" ["password"]
send_encrypted_api_request() {
    local host_port="$1"
    local api_endpoint="$2"
    local raw_data="$3"
    local password="${4:-$MYSQL_ROOT_PASSWORD}"
    
    local api_host=$(echo "$host_port" | cut -d":" -f1)
    local api_port=$(echo "$host_port" | cut -d":" -f2)
    
    # Default to port 80 if not specified
    if [ -z "$api_port" ] || [ "$api_port" = "$api_host" ]; then
        api_port=80
    fi
    
    # Encrypt the data
    local encrypted_data=$(encrypt_data "$raw_data" "$password")
    
    # Choose protocol based on port (443 and 10005 use HTTPS, others use HTTP)
    if [ "$api_port" = "443" ] || [ "$api_port" = "10005" ]; then
        send_encrypted_data_https "$api_host" "$api_port" "$api_endpoint" "$encrypted_data"
    else
        send_encrypted_data_http "$api_host" "$api_port" "$api_endpoint" "$encrypted_data"
    fi
}

# Backward compatible wrapper for the original MySQL replication manager use case
send_encrypted_data() {
    local api_host=$(echo "$1" | cut -d":" -f1)
    local port=10005
    local task="$2"
    local data="$3"
    local api_endpoint="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/write-log/$task"

    if [ "$port" = "10005" ]; then
        local encrypted_data=$(encrypt_data "$data")
        send_encrypted_data_https "$api_host" "$port" "$api_endpoint" "$encrypted_data"
    else
        local encrypted_data=$(encrypt_data "$data")
        send_encrypted_data_http "$api_host" "$port" "$api_endpoint" "$encrypted_data"
    fi
}

# Generic function to send data to API with retry logic
# Usage: send_to_api_with_retry "host:port" "/api/path" "raw_data" ["max_retries"] ["password"]
send_to_api_with_retry() {
    local host_port="$1"
    local api_endpoint="$2"
    local raw_data="$3"
    local max_retries="${4:-3}"
    local password="${5:-$MYSQL_ROOT_PASSWORD}"
    local log_file="${6:-$LOG_DIR/api_calls.log}"
    
    local attempt=0
    local success=false
    local http_code
    local response

    while ((attempt < max_retries)); do
        response=$(send_encrypted_api_request "$host_port" "$api_endpoint" "$raw_data" "$password")
        # Handles HTTP/1.0, HTTP/1.1, HTTP/2, etc.
        http_code=$(echo "$response" | grep HTTP | head -n1 | awk '{print $2}')

        if [[ "$http_code" == "200" ]]; then
            success=true
            break
        else
            ((attempt++))
            sleep 2
        fi
    done

    if [ "$success" = false ]; then
        # Create log directory if it doesn't exist
        mkdir -p "$(dirname "$log_file")"
        
        # Rotate log file if it exceeds 1MB
        if [ -f "$log_file" ]; then
            local filesize=$(stat -c%s "$log_file")
            if ((filesize > 1048576)); then
                cp -f "$log_file" "${log_file}.bak"
                : > "$log_file"
            fi
        fi

        echo "[$(date '+%Y-%m-%d %H:%M:%S')] API call failed with code: $http_code after $max_retries attempts" >> "$log_file"
        echo "Destination: $host_port Endpoint: $api_endpoint" >> "$log_file"
        echo "Response: $response" >> "$log_file"
        echo "---" >> "$log_file"
        return 1
    fi
    
    return 0
}

# Backward compatible function for MySQL replication manager logs
send_lines_to_api() {
    local lines="$1"
    local job="${2:-main}"
    local level="${3:-$LVL_DEBUG}"
    local address="${REPLICATION_MANAGER_URL}"
    local max_retries=3

    local data="{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\",\"secret\":\"$MYSQL_ROOT_PASSWORD\",\"log\":\"$lines\",\"level\":\"$level\"}"
    local api_endpoint="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/write-log/$job"
    
    send_to_api_with_retry "$address" "$api_endpoint" "$data" "$max_retries"
}

########################
# Usage Examples       #
########################

# Example 1: Send to custom API endpoint
# send_encrypted_api_request "api.example.com:443" "/api/v1/data" '{"key":"value"}'

# Example 2: Send with retry logic to custom endpoint
# send_to_api_with_retry "api.example.com:8080" "/api/submit" '{"data":"test"}' 5

# Example 3: Use original replication manager function
# send_lines_to_api "Log message here" "backup" "INFO"

#########################
# Check Binaries         #
#########################

# Determine which binary to use (prefer MariaDB, fallback to MySQL)
if [ -x "$MARIADB_CLIENT" ]; then
    BINARY_CLIENT="$MARIADB_CLIENT $BINARY_CLIENT_PARAMETERS"
    BINARY_CHECK=$MARIADB_CHECK
    BINARY_DUMP=$MARIADB_DUMP
    send_lines_to_api "Job start: Using MariaDB binaries."
elif [ -x "$MYSQL_CLIENT" ]; then
    BINARY_CLIENT="$MYSQL_CLIENT $BINARY_CLIENT_PARAMETERS"
    BINARY_CHECK=$MYSQL_CHECK
    BINARY_DUMP=$MYSQL_DUMP
    send_lines_to_api "Job start: Using MySQL binaries."
else
    send_lines_to_api "Neither MariaDB nor MySQL binaries are available. Exiting job script." "main" "$LVL_ERROR"
    exit 1
fi

#########################
# Check Functions         #
#########################

# Function to check if a specific task is needed for a server
check_task_needs() {
    local host_port="$1"
    local cluster="$2"
    local server="$3"
    local port="$4"
    local taskname="$5"
    
    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/needs/${taskname}"
    
    local response
    response=$(send_encrypted_api_request "$host_port" "$endpoint" "{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\",\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    # Extract HTTP status code
    local http_code
    http_code=$(echo "$response"  | grep HTTP | head -1 | sed -E 's/.*HTTP\/[0-9.]+ ([0-9]{3}).*/\1/')

    # Extract body after blank line
    local body
    body=$(echo "$response" | awk 'BEGIN{body=0} /^(\r)?$/ {body=1; next} body {print}' | tr -d '\r' | tr -d '\n')
    
    # echo "$http_code: $body" >> "$LOG_DIR/$taskname.process.out"

    # Determine output
    if [ "$http_code" = "200" ] && [ "$body" = "true" ]; then
        return 0
    elif [ "$http_code" = "500" ] && [ "$body" = "false" ]; then
        return 1
    else
        return 2
    fi
}

# Function to check the current script status and return the receiver port if successful
check_jobs_receiver() {
    local host_port="$1"
    local cluster="$2"
    local server="$3"
    local port="$4"

    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/receive-jobs-check"

    local response
    response=$(send_encrypted_api_request "$host_port" "$endpoint" "{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\", \"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    # Extract HTTP status code
    local http_code
    http_code=$(echo "$response" | grep HTTP | head -n1 | awk '{print $2}')

    # Extract body (after the first blank line)
    local body
    body=$(echo "$response" | awk 'BEGIN{body=0} /^(\r)?$/ {body=1; next} body {print}' | tr -d '\r')

    # echo "receiver $http_code: $body" >> "$LOG_DIR/jobs-check.process.out"

    # Process successful response
    if [ "$http_code" = "200" ] && [[ "$body" == RECEIVER_PORT=* ]]; then
        local recv_port="${body#RECEIVER_PORT=}"

        # Validate the port is numeric and within range
        if [[ "$recv_port" =~ ^[0-9]+$ ]] && [ "$recv_port" -ge 1 ] && [ "$recv_port" -le 65535 ]; then
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

##########################
# Upgrade Functions        #
##########################

request_jobs_upgrade() {
    local host_port="$1"
    local cluster="$2"
    local server="$3"
    local port="$4"

    local endpoint="/api/clusters/${cluster}/servers/${server}/${port}/actions/send-jobs-upgrade"

    local response
    response=$(send_encrypted_api_request "$host_port" "$endpoint" "{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\", \"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

    # Extract HTTP status code
    local http_code
    http_code=$(echo "$response" | head -n1 | awk '{print $2}')

    # Extract body after blank line
    local body
    body=$(echo "$response" | awk 'BEGIN{body=0} /^(\r)?$/ {body=1; next} body {print}' | tr -d '\r' | tr -d '\n')

    # Determine output
    if [ "$http_code" = "200" ]; then
        return 0
    else
        return 1
    fi
}


# Function to check if a table exists in a given database
table_exists() {
    local db="$1"
    local table="$2"

    # Ask information_schema if the table exists
    local result
    result=$(echo "SELECT COUNT(*) 
                   FROM information_schema.tables 
                   WHERE table_schema='${db}' 
                     AND table_name='${table}';" \
             | $BINARY_CLIENT -N 2>/dev/null)

    if [ "$result" = "1" ]; then
        return 0  # true, table exists
    else
        return 1  # false, table missing
    fi
}

# Function to create the jobs table if it doesn't exist
create_jobs_table() {
    if ! table_exists "replication_manager_schema" "jobs"; then
        send_lines_to_api "Creating jobs table..." "main" "$LVL_INFO"
        echo "set sql_log_bin=0;CREATE DATABASE IF NOT EXISTS replication_manager_schema;CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, state tinyint not null default 0, result MEDIUMTEXT, start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task), KEY idx3 (task, state), UNIQUE(task)) engine=innodb;set sql_log_bin=1;" | $BINARY_CLIENT
    fi
}

################################
# Log Processing Functions     #
################################

# Function to create a manual lock file
create_lock_file() {
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
remove_lock_file() {
    local lock_file="$1"
    if [ -e "$lock_file" ]; then
        rm -f "$lock_file"
    fi
}

# Function to remove a run directory lock file
remove_run_lockdir() {
    local run_lockdir="$1"
    if [ -d "$run_lockdir" ]; then
        rmdir "$run_lockdir"
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
    local logfile="$1"
    local checkpoint_file=$2
    local job="$3"
    local last_read=0
    local current_line=$((last_read + 1))

    if [[ -s "$checkpoint_file" ]]; then
        last_read=$(cat "$checkpoint_file")
        current_line=$((last_read + 1))
    fi


    if [ -f "$logfile" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')
            ((current_line++))

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

    if ! create_lock_file "$lock_file" "$job"; then
        return
    fi

    # Ensure lock file is removed on script exit. Using trap to handle unexpected exit. This will not interfere with other traps since it's a separate subshell.
    trap 'remove_lock_file "$lock_file"' EXIT

    if ! wait_for_run_lockdir "$run_lockdir" "$job"; then
        remove_lock_file "$lock_file"
        return
    fi

    if ! wait_for_log_file "$log_file" "$job"; then
        remove_lock_file "$lock_file"
        return
    fi

    local last_line=0
    if [[ -f "$checkpoint_file" ]]; then
        last_line=$(cat "$checkpoint_file")
    fi

    send_lines_to_api "Last checkpoint on "$checkpoint_file" is: $last_line.\n" "$job" "$LVL_DEBUG"

    local current_line=0
    local batch=""
    local exec_once=1

    # processing until the end of the file and loop until run file deleted
    while [[ -d "$run_lockdir" ]] || [[ "$exec_once" -eq 1 ]]; do
        exec_once=0
        read_log_file "$log_file" "$checkpoint_file" "$job"
    done

    # If the run file was deleted, continue processing until the end of the file
    if [ -f "$log_file" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')
            ((current_line++))
            batch+="$escaped\n"
            if ((current_line % BATCH_SIZE == 0)); then
                send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
                batch=""
            fi
            echo "$current_line" >"$checkpoint_file"
        done < <(tail -n +"$((current_line - last_line))" "$log_file")
    fi

    if [[ -n "$batch" ]]; then
        send_lines_to_api "$batch" "$job" "$LVL_DEBUG"
    fi

    send_lines_to_api "Removing checkpoint file.\n" "$job" "$LVL_DEBUG"
    rm -f "$checkpoint_file"

    remove_lock_file "$lock_file"
}

#################################
# Job Related Functions     #
#################################

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
    process_log_file "jobs-check" &
    # Ensure run lockdir for current job is removed on script exit. Intended to replace the previous job trap which already removed at the end of loop entry.
    trap 'remove_run_lockdir "$LOG_DIR/jobs-check.run"' EXIT

    check_task_needs "${REPLICATION_MANAGER_URL}" "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "jobs-check"
    checkresult=$?
    if [ "$checkresult" != "0" ]; then
        if [ "$checkresult" = "2" ]; then
            echo "Failed to check task needs from API." >"$LOG_DIR/jobs-check.process.out"
        else
            echo "No need to run jobs-check." >"$LOG_DIR/jobs-check.process.out"
        fi

        sleep 1 && remove_run_lockdir "$LOG_DIR/jobs-check.run" &
        trap - EXIT
        return $checkresult
    fi

    socatCleaner

    echo "Waiting for receiver port." >"$LOG_DIR/jobs-check.process.out"

    #Send this script to the monitoring server using socat (using different variables to avoid confusion)
    RCV_PORT=$(check_jobs_receiver "${REPLICATION_MANAGER_URL}" "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT")
    checkresult=$?

    if [ "$checkresult" != "0" ] || [ "$RCV_PORT" == "error" ]; then
        echo "Failed to get a valid receiver port from the monitoring server." >>"$LOG_DIR/jobs-check.process.out"
        sleep 1 && remove_run_lockdir "$LOG_DIR/jobs-check.run" &
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

    sleep 1 && remove_run_lockdir "$LOG_DIR/jobs-check.run" &
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
    process_log_file "jobs-upgrade" &
    # Ensure run lockdir for current job is removed on script exit. Intended to replace the previous job trap which already removed at the end of loop entry.
    trap 'remove_run_lockdir "$LOG_DIR/jobs-upgrade.run"' EXIT

    check_task_needs "${REPLICATION_MANAGER_URL}" "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT" "jobs-upgrade"

    checkresult=$?
    if [ "$checkresult" != "0" ]; then
        if [ "$checkresult" = "2" ]; then
            echo "Failed to check task needs from API." >"$LOG_DIR/jobs-upgrade.process.out"
        else
            echo "No need to run jobs-upgrade." >"$LOG_DIR/jobs-upgrade.process.out"
        fi

        sleep 1 && remove_run_lockdir "$LOG_DIR/jobs-upgrade.run" &
        trap - EXIT
        return
    fi

    socatCleaner
    
    echo "Waiting new script." >"$LOG_DIR/jobs-upgrade.process.out"

    # Open receiver port to get the new script to a temporary file
    TEMP_FILE="${BASH_SOURCE[0]}.tmp"
    socat -u TCP-LISTEN:$SST_RECEIVER_PORT,reuseaddr,bind=$SOCAT_BIND - > "$TEMP_FILE" 2>>"$LOG_DIR/jobs-upgrade.process.out" &
    SOCAT_PID=$!

    # Request the upgrade
    request_jobs_upgrade "${REPLICATION_MANAGER_URL}" "$CLUSTER_NAME" "$MYSQL_SERVER" "$MYSQL_PORT"

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

            # Re-execute with the new version
            exec "${BASH_SOURCE[0]}" "$@"
        fi
    else
        send_lines_to_api "Failed to receive the new script via socat." "jobs-upgrade" "$LVL_ERROR"
        rm -f "$TEMP_FILE"
    fi

    sleep 1 && remove_run_lockdir "$LOG_DIR/jobs-upgrade.run" &
    trap - EXIT
}

#######################
# JOB START HERE
#######################

jobsCheck
checkresult=$?
if [ "$checkresult" != "2" ]; then
    # If jobsCheck did not error, proceed to jobsUpgrade
    jobsUpgrade "$@"
fi

create_jobs_table

mkdir -p "$CHECKPOINT_DIR"
mkdir -p "$LOCK_DIR"
echo "" > $LOG_DIR/curl_response.txt
echo "" > $LOG_DIR/request.txt
echo "" > $LOG_DIR/encrypt.txt

####################
# Check if the configuration file has changed using repman client
####################
if [ ! -f "$REPMAN_CLIENT" ]; then 
    NEW_CLIENT=$(which replication-manager-cli)
    if [ $? -eq 0 ]; then
        REPMAN_CLIENT="$NEW_CLIENT"
    fi
fi

if [ -f "$REPMAN_CLIENT" ]; then
    ENC_KEY=$(encrypt_data "{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\", \"secret\":\"$MYSQL_ROOT_PASSWORD\"}")
    $REPMAN_CLIENT "print-defaults" --host="$REPLICATION_MANAGER_HOST" --port="$REPLICATION_MANAGER_PORT" --cluster="$CLUSTER_NAME" --srv-host="$MYSQL_SERVER" --srv-port="$MYSQL_PORT" --enc-secret="$ENC_KEY" --log-dir="$LOG_DIR" > $LOG_DIR/repman.out
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
        trap 'remove_run_lockdir "$LOG_DIR/$job.run"' EXIT
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
            $MARIADB_BACKUP --innobackupex --defaults-file=$MYSQL_CONF/my.cnf --databases-exclude=.system --protocol=TCP $BINARY_CLIENT_PARAMETERS --stream=xbstream 2>"$LOG_DIR/backup.out" | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
            ;;
        errorlog)
            if [ ! -f $ERROLOG ]; then
              touch $ERROLOG
            fi
            
            # Check if the error log is not empty
            if [ -s $ERROLOG ]; then
                cat $ERROLOG >> $ERROLOG'_'$(date '+%Y-%m-%d')
                cat $ERROLOG | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.process.out"
                if [ -f $ERROLOG'_'$(date -d "1 day ago" '+%Y-%m-%d') ]; then
                gzip $ERROLOG'_'$(date -d "1 day ago" '+%Y-%m-%d')  
                fi
                if [ -f $ERROLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz ]; then
                rm -f $ERROLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz  
                fi
                >$ERROLOG
            else 
                echo -n | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.process.out"
            fi
            ;;
        slowquery)
            if [ ! -f $SLOWLOG ]; then
              touch $SLOWLOG
            fi

            if [ -s $SLOWLOG ]; then
                cat $SLOWLOG >> $SLOWLOG'_'$(date '+%Y-%m-%d')
                cat $SLOWLOG | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.process.out"
                if [ -f $SLOWLOG'_'$(date -d "1 day ago" '+%Y-%m-%d') ]; then
                gzip $SLOWLOG'_'$(date -d "1 day ago" '+%Y-%m-%d')  
                fi
                if [ -f $SLOWLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz ]; then
                rm -f $SLOWLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz  
                fi
                >$SLOWLOG
            else 
                echo -n | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.process.out"
            fi
            ;;
        zfssnapback)
            LASTSNAP=$(zfs list -r -t all | grep zp%%ENV:SERVICES_SVCNAME%%_pod01 | grep daily | sort -r | head -n 1 | cut -d" " -f1)
            %%ENV:SERVICES_SVCNAME%% stop
            zfs rollback $LASTSNAP
            %%ENV:SERVICES_SVCNAME%% start
            ;;
        optimize)
            $BINARY_CHECK -o $BINARY_CLIENT_PARAMETERS --all-databases --skip-write-binlog &>"$LOG_DIR/$job.process.out"
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
        sleep 1 && remove_run_lockdir "$LOG_DIR/$job.run" &
        trap - EXIT
    fi
done

# EOF