#!/bin/bash
# This script is given as sample and might be overwritten on upgrade

ZONE_ID=$1
API_TOKEN=$2
ZONE_DNS=$3
ZONE_GATEWAY=$4


curl https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records \
    -d '{ "comment": "", "name": "'$ZONE_DNS'", "content": "'$ZONE_GATEWAY'", "proxied": false, "ttl": 3600, "type": "CNAME" }' \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_TOKEN"
