#!/bin/bash
# Usage (on the first node, copied as /tmp/rmapi.sh): rmapi.sh <METHOD> <API path> [body file]
# Logs in with api-credentials from the lab config; the response body goes to /tmp/rmapi.out.
CRED=$(sudo grep -E "^\s*api-credentials\s*=" /etc/replication-manager/config.toml | head -1 | sed -E "s/^[^=]*=\s*\"?([^\",]*).*/\1/")
U=${CRED%%:*}; P=${CRED#*:}
TOK=$(curl -sk -X POST https://127.0.0.1:10005/api/login -H "Content-Type: application/json" -d "$(python3 -c "import json,sys;print(json.dumps({\"username\":sys.argv[1],\"password\":sys.argv[2]}))" "$U" "$P")" | python3 -c "import json,sys;print(json.load(sys.stdin).get(\"token\",\"\"))")
[ -z "$TOK" ] && { echo "login failed" >&2; exit 1; }
if [ -n "$3" ]; then curl -sk -o /tmp/rmapi.out -w "%{http_code}\n" -X "$1" "https://127.0.0.1:10005$2" -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" --data-binary @"$3"
else curl -sk -o /tmp/rmapi.out -w "%{http_code}\n" -X "$1" "https://127.0.0.1:10005$2" -H "Authorization: Bearer $TOK"; fi
