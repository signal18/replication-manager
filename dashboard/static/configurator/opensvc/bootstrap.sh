#!/bin/bash
function help {
	echo "Required Environment:" >&2
	echo "  REPLICATION_MANAGER_USER" >&2
	echo "  REPLICATION_MANAGER_PASSWORD" >&2
	echo "  REPLICATION_MANAGER_API" >&2
	echo "  REPLICATION_MANAGER_CLUSTER_NAME" >&2
	echo "  REPLICATION_MANAGER_HOST_NAME" >&2
	echo "  REPLICATION_MANAGER_HOST_PORT" >&2
}
[ -z $REPLICATION_MANAGER_USER ] && help && exit 1
[ -z $REPLICATION_MANAGER_PASSWORD ] && help && exit 1
[ -z $REPLICATION_MANAGER_API ] && help && exit 1
[ -z $REPLICATION_MANAGER_CLUSTER_NAME ] && help && exit 1
[ -z $REPLICATION_MANAGER_HOST_NAME ] && help && exit 1
[ -z $REPLICATION_MANAGER_HOST_PORT ] && help && exit 1
GET="curl -s -k -o- -H \"Content-Type: application/json\""
AUTH_DATA="{\"username\": \"$REPLICATION_MANAGER_USER\", \"password\": \"$REPLICATION_MANAGER_PASSWORD\"}"
TOKEN=$($GET --data "$AUTH_DATA" -H "Accept: text/html" $REPLICATION_MANAGER_API/login)
function get {
	$GET -H "Accept: application/json" -H "Authorization: Bearer $TOKEN" $@
}
get $REPLICATION_MANAGER_API/clusters/$REPLICATION_MANAGER_CLUSTER_NAME/servers/$REPLICATION_MANAGER_HOST_NAME/$REPLICATION_MANAGER_HOST_PORT/config | tar xzvf - -C /data/ 
