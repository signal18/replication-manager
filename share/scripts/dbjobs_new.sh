#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPMAN_CLIENT="$SCRIPT_DIR/replication-manager-cli"
ENC_KEY=$(encrypt_data "{\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")

USER=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%
PASSWORD=$MYSQL_ROOT_PASSWORD
MYSQL_PORT=%%ENV:SERVER_PORT%%
MYSQL_SERVER=%%ENV:SERVER_HOST%%
CLUSTER_NAME=%%ENV:SVC_NAMESPACE%%
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

# Determine which binary to use (prefer MariaDB, fallback to MySQL)
if [ -x "$MARIADB_CLIENT" ]; then
    BINARY_CLIENT="$MARIADB_CLIENT $BINARY_CLIENT_PARAMETERS"
    BINARY_CHECK=$MARIADB_CHECK
    BINARY_DUMP=$MARIADB_DUMP
    echo "Using MariaDB binaries."
elif [ -x "$MYSQL_CLIENT" ]; then
    BINARY_CLIENT="$MYSQL_CLIENT $BINARY_CLIENT_PARAMETERS"
    BINARY_CHECK=$MYSQL_CHECK
    BINARY_DUMP=$MYSQL_DUMP
    echo "Using MySQL binaries."
else
    echo "Neither MariaDB nor MySQL binaries are available."
    exit 1
fi

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

create_jobs_table() {
    if ! table_exists "replication_manager_schema" "jobs"; then
        echo "Creating jobs table..."
        echo "set sql_log_bin=0;CREATE DATABASE IF NOT EXISTS replication_manager_schema;CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, state tinyint not null default 0, result MEDIUMTEXT, start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task), KEY idx3 (task, state), UNIQUE(task)) engine=innodb;set sql_log_bin=1;" | $BINARY_CLIENT
    fi
}

# OSX need socat extra path
export PATH=$PATH:/usr/local/bin

pad_pkcs7() {
    local data="$1"
    local blocksize=32
    local len=$(printf "%s" "$data" | wc -c)
    local pad_len=$((blocksize - (len % blocksize)))
    local padding=$(printf "%${pad_len}s" | tr ' ' '\x01')
    printf "%s%s" "$data" "$padding"
}

derive_key() {
    local key=$(echo -n "$MYSQL_ROOT_PASSWORD" | sha256sum | awk '{print $1}')
    echo "$key"
}

derive_iv() {
    local iv=$(echo -n "$MYSQL_ROOT_PASSWORD" | md5sum | awk '{print $1}')
    echo "$iv"
}

# Function to encrypt data using AES-128 in CFB mode
encrypt_data() {
    local key=$(derive_key)
    local iv=$(derive_iv)
    local padded=$(pad_pkcs7 "$1")
    local encrypted=$(echo -n "$padded" | openssl aes-256-cbc -a -nosalt -K "$key" -iv "$iv" | tr -d '\n')
    # echo "$encrypted" >> $LOG_DIR/encrypted.txt
    echo "$encrypted"
}

# Function to send encrypted data to a JSON API using socat over HTTP
send_encrypted_data_http() {
    local api_host="$1"
    local api_port="$2"
    local api_host_port="$1:$2"
    local api_endpoint="$3"
    local data=$(encrypt_data "$4")
    local json_data="{\"data\":\"$data\"}"

    local request="POST $api_endpoint HTTP/1.1\r\nHost: $api_host\r\nContent-Type: application/json\r\nContent-Length: ${#json_data}\r\n\r\n$json_data"
    # Use socat to send the request over HTTP
    local response=$(echo -en "$request" | socat - TCP:$api_host_port)
    # echo "$request" >> $LOG_DIR/request.txt
    echo "$response"
}

# Function to send encrypted data to a JSON API using socat over HTTPS
send_encrypted_data_https() {
    local api_host="$1"
    local api_port="$2"
    local api_host_port="$1:$2"
    local api_endpoint="$3"
    local data=$(encrypt_data "$4")
    local json_data="{\"data\":\"$data\"}"

    local request="POST $api_endpoint HTTP/1.1\r\nHost: $api_host\r\nContent-Type: application/json\r\nContent-Length: ${#json_data}\r\n\r\n$json_data"
    # Use socat with SSL and no verification to send the request over HTTPS
    local response=$(echo -en "$request" | socat - OPENSSL:$api_host_port,verify=0)
    # echo "$request" >> $LOG_DIR/request.txt
    echo "$response"
}

# Wrapper function to choose between HTTP and HTTPS based on port
send_encrypted_data() {
    local api_host=$(echo "$1" | cut -d":" -f1)
    local port=10005
    local task="$2"
    local data="$3"
    local api_endpoint="/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/write-log/$task"

    if [ "$port" = "10005" ]; then
        send_encrypted_data_https "$api_host" "$port" "$api_endpoint" "$data"
    else
        send_encrypted_data_http "$api_host" "$port" "$api_endpoint" "$data"
    fi
}

# Function to send a batch of lines to the API and check for success with retry logic
send_lines_to_api() {
    local lines="$1"
    local job="$2"
    local address="${REPLICATION_MANAGER_URL}"
    local data="{\"server\":\"$MYSQL_SERVER:$MYSQL_PORT\",\"log\":\"$lines\"}"

    local max_retries=3
    local attempt=0
    local success=false

    while ((attempt < max_retries)); do
        # Capture response and HTTP status code
        local response
        response=$(send_encrypted_data "$address" "$job" "$data")

        # echo "$response" >> $LOG_DIR/curl_response.txt
        
        # Extract HTTP status code
        local http_code=$(echo "$response" | grep -oP '(?<=HTTP/1.1 )[0-9]{3}')

        if [[ "$http_code" == "200" ]]; then
            echo "API call successful for job: $job"
            success=true
            break
        else
            echo "API call failed for job: $job with status code: $http_code"
            cat $LOG_DIR/curl_response.txt
            ((attempt++))
            sleep 2 # Wait before retrying
        fi
    done

    if [ "$success" = false ]; then
        echo "API call failed after $max_retries attempts for job: $job"
    fi
}

# Function to create a manual lock file
create_lock_file() {
    local lock_file="$1"
    if [ -e "$lock_file" ]; then
        echo "Lock file exists. Exiting."
        return 1
    fi
    touch "$lock_file"
    return 0
}

# Function to remove a manual lock file
remove_lock_file() {
    local lock_file="$1"
    rm -f "$lock_file"
}

# Function to wait for the .run file with a timeout
wait_for_run_lockdir() {
    local run_lockdir="$1"
    local timeout=30
    local start_time=$(date +%s)

    send_lines_to_api "Waiting for $run_lockdir file...\n" "$job"
    while [[ ! -d "$run_lockdir" ]]; do
        sleep 0.5
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))
        if ((elapsed >= timeout)); then
            send_lines_to_api "Timeout reached while waiting for .run file.\n" "$job"
            return 1
        fi
    done
    send_lines_to_api "$run_lockdir file found...\n" "$job"
    return 0
}

# Function to wait for the .run file with a timeout
wait_for_log_file() {
    local logfile="$1"
    local timeout=60
    local start_time=$(date +%s)

    send_lines_to_api "Waiting for $logfile file...\n" "$job"
    while [[ ! -f "$logfile" ]]; do
        sleep 0.5
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))
        if ((elapsed >= timeout)); then
            send_lines_to_api "Timeout reached while waiting for $logfile file. Please check log manually if needed. \n" "$job"
            return 1
        fi
    done
    send_lines_to_api "$logfile file found...\n" "$job"
    return 0
}

read_log_file() {
    local logfile="$1"
    local checkpoint_file=$2
    local last_read=0
    local current_line=$((last_read + 1))

    if [[ -f "$checkpoint_file" ]]; then
        last_read=$(cat "$checkpoint_file")
        current_line=$((last_read + 1))
    fi

    if [ -f "$logfile" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')
            ((current_line++))

            if [[ ! -d "$run_lockdir" ]]; then
                send_lines_to_api "Run file has been deleted. Processing remaining lines.\n" "$job"
                break
            fi

        batch+="$escaped\n"
        if ((current_line % BATCH_SIZE == 0)); then
            send_lines_to_api "$batch" "$job"
            batch=""
        fi
        echo "$current_line" >"$checkpoint_file"

        done < <(sed -n "$current_line,${p}" "$log_file")

        # Send any remaining lines in the batch after the first loop
        if [[ -n "$batch" ]]; then
            send_lines_to_api "$batch" "$job"
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
        log_file="$LOG_DIR/$job.out"
        ;;
    esac

    local checkpoint_file="$CHECKPOINT_DIR/$job.checkpoint"
    local run_lockdir="$LOG_DIR/$job.run"
    local lock_file="$LOCK_DIR/${job}_lockfile"

    if ! create_lock_file "$lock_file"; then
        return
    fi

    # Ensure lock file is removed on script exit
    trap 'remove_lock_file "$lock_file"' EXIT

    if ! wait_for_run_lockdir "$run_lockdir"; then
        remove_lock_file "$lock_file"
        return
    fi

    if ! wait_for_log_file "$log_file"; then
        remove_lock_file "$lock_file"
        return
    fi

    local last_line=0
    if [[ -f "$checkpoint_file" ]]; then
        last_line=$(cat "$checkpoint_file")
    fi

    send_lines_to_api "Last checkpoint on "$checkpoint_file" is: $last_line.\n" "$job"

    local current_line=0
    local batch=""
    local exec_once=1

    # processing until the end of the file and loop until run file deleted
    while [[ -d "$run_lockdir" ]] || [[ "$exec_once" -eq 1 ]]; do
        exec_once=0
        read_log_file "$log_file" "$checkpoint_file"
    done

    # If the run file was deleted, continue processing until the end of the file
    if [ -f "$log_file" ]; then
        while IFS= read -r line; do
            escaped=$(printf '%s' "$line" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')
            ((current_line++))
            batch+="$escaped\n"
            if ((current_line % BATCH_SIZE == 0)); then
                send_lines_to_api "$batch" "$job"
                batch=""
            fi
            echo "$current_line" >"$checkpoint_file"
        done < <(tail -n +"$((current_line - last_line))" "$log_file")
    fi

    if [[ -n "$batch" ]]; then
        send_lines_to_api "$batch" "$job"
    fi

    send_lines_to_api "Removing checkpoint file.\n" "$job"
    rm -f "$checkpoint_file"

    remove_lock_file "$lock_file"
}

socatCleaner() {
    kill -9 $(lsof -t -i:$SST_RECEIVER_PORT -sTCP:LISTEN)
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
        send_lines_to_api "Job $job ended with state: Finished" "$job" 
    else
        send_lines_to_api "Job $job ended with state: Error" "$job"
    fi
    $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set end=NOW(), state=$jobstate, result=LOAD_FILE('$LOG_DIR/$job.out'), done=$done  WHERE id='$ID';" &
}

pauseJob() {
    $BINARY_CLIENT -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set state=2, result='waiting' WHERE id='$ID';" &
}

partialRestore() {
    send_lines_to_api "Starting partial restore..." "$job" 
    chown -R mysql:mysql $BACKUPDIR 
    $BINARY_CLIENT -e "set sql_log_bin=0;install plugin BLACKHOLE soname 'ha_blackhole.so'"
    for dir in $(ls -d $BACKUPDIR/*/ | xargs -n 1 basename | grep -vE 'mysql|performance_schema|replication_manager_schema'); do
        send_lines_to_api "Restoring $dir..." "$job" 
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
    send_lines_to_api "Setting GTID of the last change..." "$job" 
    cat $BACKUPDIR/xtrabackup_info | grep binlog_pos | awk -F, '{ print $3 }' | sed -e 's/GTID of the last change/set sql_log_bin=0;set global gtid_slave_pos=/g' | $BINARY_CLIENT
    send_lines_to_api "Flushing privileges..." "$job" 
    $BINARY_CLIENT -e"set sql_log_bin=0;flush privileges;start slave;"
}

#######################
# JOB START HERE
#######################

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
    ENC_KEY=$(encrypt_data "{\"secret\":\"$MYSQL_ROOT_PASSWORD\"}")
    $REPMAN_CLIENT "print-defaults" --host="$REPLICATION_MANAGER_HOST" --port="$REPLICATION_MANAGER_PORT" --cluster="$CLUSTER_NAME" --srv-host="$MYSQL_SERVER" --srv-port="$MYSQL_PORT" --enc-secret="$ENC_KEY" > $LOG_DIR/repman.out
fi

#####################
# OTHER JOBS
#####################

for job in "${JOBS[@]}"; do

    TASK=($(echo "SELECT concat(id,'@',server,':',port) FROM replication_manager_schema.jobs WHERE task='$job' and done=0 AND state=0 order by id desc limit 1" | $BINARY_CLIENT -N))

    ADDRESS=($(echo $TASK | awk -F@ '{ print $2 }'))
    ID=($(echo $TASK | awk -F@ '{ print $1 }'))

    if [ "$ID" != "" ]; then
        send_lines_to_api "Job $job initiated. Clearing previous logs..." "$job" 
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
            if [ "curl -so /dev/null -w '%{response_code}'   https://$REPLICATION_MANAGER_URL/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/need-start" == "200" ]; then
                curl https://$REPLICATION_MANAGER_URL/api/clusters/$CLUSTER_NAME/servers/$MYSQL_SERVER/$MYSQL_PORT/config | tar xzvf etc/* - -C $CONFDIR/../..
                systemctl start mysql
            fi
            ;;
        esac
    else

        mkdir -p "$LOG_DIR/$job.run"
        process_log_file "$job" &
        trap 'rmdir "$LOG_DIR/$job.run"' EXIT
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
                cat $ERROLOG >> $ERRROLOG'_'$(date '+%Y-%m-%d')
                cat $ERROLOG | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
                if [ -f $ERROLOG'_'$(date -d "1 day ago" '+%Y-%m-%d') ]; then
                gzip $ERROLOG'_'$(date -d "1 day ago" '+%Y-%m-%d')  
                fi
                if [ -f $ERROLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz ]; then
                rm -f $ERROLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz  
                fi
                >$ERROLOG
            else 
                echo "Error log file is empty" | socat -u stdio TCP:$ADDRESS
            fi
            ;;
        slowquery)
            if [ ! -f $SLOWLOG ]; then
              touch $SLOWLOG
            fi

            if [ -s $SLOWLOG ]; then
                cat $SLOWLOG >> $SLOWLOG'_'$(date '+%Y-%m-%d')
                cat $SLOWLOG | socat -u stdio TCP:$ADDRESS &>"$LOG_DIR/$job.out"
                if [ -f $SLOWLOG'_'$(date -d "1 day ago" '+%Y-%m-%d') ]; then
                gzip $SLOWLOG'_'$(date -d "1 day ago" '+%Y-%m-%d')  
                fi
                if [ -f $SLOWLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz ]; then
                rm -f $SLOWLOG'_'$(date -d "8 day ago" '+%Y-%m-%d').gz  
                fi
                >$SLOWLOG
            else 
                echo "Slow log file is empty" | socat -u stdio TCP:$ADDRESS
            fi
            ;;
        zfssnapback)
            LASTSNAP=$(zfs list -r -t all | grep zp%%ENV:SERVICES_SVCNAME%%_pod01 | grep daily | sort -r | head -n 1 | cut -d" " -f1)
            %%ENV:SERVICES_SVCNAME%% stop
            zfs rollback $LASTSNAP
            %%ENV:SERVICES_SVCNAME%% start
            ;;
        optimize)
            $BINARY_CHECK -o $BINARY_CLIENT_PARAMETERS --all-databases --skip-write-binlog &>"$LOG_DIR/$job.out"
            ;;
        restart)
            systemctl restart mysql
            journalctl -u mysql >"$LOG_DIR/$job.out"
            ;;
        stop)
            systemctl stop mysql
            journalctl -u mysql >"$LOG_DIR/$job.out"
            ;;
        esac
        doneJob "$job"
        sleep 1 && rmdir "$LOG_DIR/$job.run" &
    fi
done