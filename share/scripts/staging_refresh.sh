#!/bin/bash

function help {
	echo "Required Environment:" >&2
	echo "  REPLICATION_MANAGER_USER" >&2
	echo "  REPLICATION_MANAGER_PASSWORD" >&2
	echo "  REPLICATION_MANAGER_URL" >&2
	echo "  REPLICATION_MANAGER_CLUSTER_NAME" >&2
	# echo "  REPLICATION_MANAGER_HOST_NAME" >&2
	# echo "  REPLICATION_MANAGER_HOST_PORT" >&2
}

# Variables
[ -z $REPLICATION_MANAGER_USER ] && help && exit 1
[ -z $REPLICATION_MANAGER_PASSWORD ] && help && exit 1
[ -z $REPLICATION_MANAGER_URL ] && help && exit 1
[ -z $REPLICATION_MANAGER_CLUSTER_NAME ] && help && exit 1

# REPLICATION_MANAGER_HOST_NAME=$1
# REPLICATION_MANAGER_HOST_PORT="3306"
NB_SLAVES=0 

##### This bloc is for getting the replication-manager token 
GET="wget -q --no-check-certificate -O- --header Content-Type:application/json"
AUTH_DATA="{\"username\": \"$REPLICATION_MANAGER_USER\", \"password\": \"$REPLICATION_MANAGER_PASSWORD\"}"
TOKEN=$($GET --post-data "$AUTH_DATA" --header Accept:text/html $REPLICATION_MANAGER_URL/api/login)

function get {
	$GET --header Accept:application/json --header "Authorization: Bearer $TOKEN" "$@"
}

# Counting the slaves, depending, we will play one of the two following scenarios 
NB_SLAVES=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/slaves/count) 
echo $NB_SLAVES

# Scenario 1 : 2 slaves, then we will stop the replication on one that will be the "staging"  
if [ "$NB_SLAVES" -eq 2 ]; then
  echo "picking first slave \n"
  ID=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/slaves/index/1/attr/id | sed 's/"//g' )
  PORT=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/slaves/index/1/attr/port)
  echo "$ID:$PORT"
  
  echo "Stopping first server slave replication \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/actions/stop-slave

  loop=true	
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/attr/state | sed 's/"//g')
    if [ "$SV_STATE" != "Slave" ]; then
      loop=false

      echo "Server $ID slave threads stopped \n"
    fi
  done
 
  echo "Getting and saving slaves initial statuses \n"	
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/slaves/index/1/attr/replications >replications.save

  echo "Reseting first slave \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/actions/reset-slave-all
  sleep 2
  
  loop=true	
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/attr/state | sed 's/"//g')
    if [ "$SV_STATE" == "StandAlone" ]; then
      loop=false
    fi
  done

  echo "$ID:$PORT is now standalone \n"
fi

# Scenario 2 : 1 slave, then we will stop the replication on one that will be the "staging"
if [ "$NB_SLAVES" -eq 1 ]; then
  echo "picking last slave and standalone id \n"
  ID=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/state/standalone/index/0/attr/id | sed 's/"//g')
  ID_SLAVE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/state/slave/index/0/attr/id | sed 's/"//g')

  if [ -z "$ID" ]; then
    echo "No standalone found \n"
    exit 1
  fi

  # Get the last available slave
  echo "last slave found for staging $ID_SLAVE \n"
  echo "Stopping replication on last slave \n"
  
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID_SLAVE/actions/stop-slave
  
  loop=true	
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID_SLAVE/attr/state | sed 's/"//g')
    if [ "$SV_STATE" != "Slave" ]; then
      loop=false

      echo "Server $ID_SLAVE slave threads stopped \n"
    fi
  done

  echo "Saving replication info in replication.save \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/topology/servers/$ID_SLAVE/attr/replications > replications.save

  echo "Reset all replication information \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID_SLAVE/actions/reset-slave-all

  loop=true	
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID_SLAVE/attr/state | sed 's/"//g')
    if [ "$SV_STATE" == "StandAlone" ]; then
      loop=false
    fi
  done

  echo "$ID:$PORT is now standalone \n"

# Restore old standalone server to slave
  echo "Restore old standalone server $ID to slave \n"
  echo "reseting master position on standalone \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/actions/reset-master
  echo "setup replication manager for reseeding \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autoseed/on
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autorejoin-logical-backup/on
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autorejoin-force-restore/on
  sleep 2
  echo "Stopping database server $ID \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/actions/stop
  echo "Waiting for database server $ID to status failed \n"
  loop=true
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/attr/state | sed 's/"//g')
    if [ "$SV_STATE" == "Failed" ]; then
      loop=false
    fi
  done
  echo "Start database server $ID \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/actions/start
  
  loop=true
  while $loop; do
    SV_STATE=$(get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$ID/attr/state | sed 's/"//g')
    if [ "$SV_STATE" == "Slave" ]; then
      loop=false
    fi
  done

  echo "Database server $ID is now a slave \n"

  echo "Resetting replication manager settings for reseed = false \n"
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autoseed/off
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autorejoin-logical-backup/off
  get $REPLICATION_MANAGER_URL/api/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/settings/actions/switch/autorejoin-force-restore/off

###### Now set last slave as standalone
fi