#!/usr/bin/env bash
# updaterepo.sh — regenerate share/repo/repos.json, the docker image tag catalog
# embedded in the binary. It is the fallback the GUI/configurator uses when no
# back-office plugins/data/repos.json has been pushed to the instance.
#
# Mirrors the back-office generate-docker-repos.sh scrape logic — Docker Hub
# registry API v2, paginated to get ALL tags, with a zero-tag guard — so the
# embedded fallback matches what the BO delivers. Scrape-only: no DB, no
# eligibility, no git push.
#
# Run from the Makefile:  make repos
# Requires: bash 4+, curl, jq
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT="${SCRIPT_DIR}/../share/repo/repos.json"
HUB="https://registry.hub.docker.com/v2/repositories"

# display-name | Docker Hub repository path  (same image set as the configurator/BO)
IMAGES=(
  "mariadb|library/mariadb"
  "mysql|library/mysql"
  "percona|library/percona"
  "proxysql|proxysql/proxysql"
  "maxscale|mariadb/maxscale"
  "haproxy|library/haproxy"
  "sphinx|leodido/sphinxsearch"
  "postgres|library/postgres"
)

log() { echo "[updaterepo] $*" >&2; }

TMP="$(mktemp)"
{
  echo '{"repos": ['
  first=true
  for entry in "${IMAGES[@]}"; do
    IFS='|' read -r name image <<< "$entry"
    if $first; then first=false; else echo ','; fi
    log "fetching $name ($image)..."
    # Docker Hub caps page_size at 100 — paginate via .next to get every tag.
    all_results="[]"
    next_url="${HUB}/${image}/tags/?page_size=100"
    page=0
    while [[ -n "$next_url" && "$next_url" != "null" ]]; do
      page=$((page + 1))
      page_data=$(curl -s "$next_url" 2>/dev/null || echo '{"results":[]}')
      if ! echo "$page_data" | jq empty 2>/dev/null; then
        log "  WARNING: invalid JSON on page $page for $image"
        break
      fi
      # Keep only the tag name -- repman's TagResult struct is {name} and ignores
      # the rest, so dropping Docker Hub's per-tag metadata (per-arch images,
      # digests, timestamps) shrinks the embedded catalog from ~17MB to ~100KB.
      page_results=$(echo "$page_data" | jq '[.results[]? | {name}]')
      all_results=$(echo "$all_results $page_results" | jq -s '.[0] + .[1]')
      next_url=$(echo "$page_data" | jq -r '.next // empty')
    done
    tag_count=$(echo "$all_results" | jq 'length')
    log "  $tag_count tags ($page pages)"
    display_image="${image#library/}"
    echo "{\"name\": \"$name\", \"image\": \"$display_image\", \"tags\": {\"count\": $tag_count, \"results\": $all_results}}"
  done
  echo ']}'
} > "$TMP"

# Validate + guard: never overwrite the committed catalog with broken/empty
# output (a transient Docker Hub failure must not blank the embedded fallback).
if ! jq empty "$TMP" 2>/dev/null; then
  log "ERROR: generated JSON invalid — keeping existing $OUTPUT"
  rm -f "$TMP"; exit 1
fi
total_tags=$(jq '[.repos[].tags.count] | add' "$TMP")
if [[ -z "$total_tags" || "$total_tags" == "null" || "$total_tags" -eq 0 ]]; then
  log "ERROR: 0 tags scraped — Docker Hub API likely failed. Keeping existing $OUTPUT"
  rm -f "$TMP"; exit 1
fi

mv "$TMP" "$OUTPUT"
log "wrote $OUTPUT ($(jq '.repos | length' "$OUTPUT") repos, ${total_tags} tags)"
