#!/bin/bash
# This script is given as sample and will be overwrite on upgrade

ZONE_ID=$1
API_TOKEN=$2
ZONE_DNS=$3
ZONE_GATEWAY=$4


curl https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records \
-H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_TOKEN" 	
