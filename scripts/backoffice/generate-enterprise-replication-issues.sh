#!/usr/bin/env bash
# generate-enterprise-replication-issues.sh
#
# Generates enterprise-replication-issues.json and pushes it to the GitLab pull
# repository of every repman instance on a paid plan (support, support-services,
# or partner).  Free-plan instances are skipped — they never receive the file,
# so the static embedded default (empty no-op) applies and the plugin emits
# nothing.
#
# Architecture:
#   - The back-office database (BO DB) stores one row per registered repman
#     instance: domain, subdomain, zone, gitlab_namespace, plan.
#   - Each instance has a dedicated GitLab pull repo:
#       gitlab.signal18.io/{domain}/{subdomain}-{zone}-pull
#   - This script queries the BO DB, builds the advisory JSON, then commits
#     plugins/data/enterprise-replication-issues.json to each eligible pull repo
#     via the GitLab Files API.
#   - repman's existing git-pull sync picks up the file and writes it to
#       {ShareDir}/plugins/data/enterprise-replication-issues.json
#   - The built-in EnterpriseReplicationPlugin reads from that path and emits
#     findings matching the server's version range.
#
# Usage:
#   ./generate-enterprise-replication-issues.sh [OPTIONS]
#
# Options:
#   --output <path>          Where to write the merged JSON locally
#                            (default: plugin source tree; used as deploy source)
#   --github-token <token>   GitHub PAT for higher rate limits (or GITHUB_TOKEN env)
#   --dry-run                Generate JSON and show eligible instances but do not push
#   --force                  Bypass the once-per-day guard
#
# DB access:
#   Uses the same MariaDB client pattern as inject_plugins.sh / inject_db_from_git.sh:
#     mysql -hproxy-portal-1.s18.svc.cloud18 -p<MYSQL_ROOT_PASSWORD> -uroot dbaas
#   Password from OpenSVC secret: om s18/sec/env decode --key MYSQL_ROOT_PASSWORD
#
# Plan lookup:
#   The subscription plan is stored in each instance's TOML config, extracted by
#   inject_db_from_git.sh into the clusters_config table:
#     SELECT DISTINCT domain, subdomain, value AS plan
#     FROM clusters_config
#     WHERE variable = 'cloud18-subscription-plan'
#       AND value IN ('support', 'support-services', 'partner')
#
# Eligible plans: support, support-services, partner
# Skipped plans:  free (and any unknown value)
#
# Deploy:
#   For each eligible instance, copies enterprise-replication-issues.json to
#   ../repos/{domain}/{subdomain}-pull/{cluster}/plugins/data/ and git pushes,
#   following the same pattern as inject_plugins.sh.
#
# Daily guard:
#   Records last successful run in .enterprise-replication-last-run.
#   Called from portal_cron.sh alongside the other inject scripts, it will
#   only actually run once per 24 hours. Use --force to bypass.
#
# Requirements:
#   - bash 4+, curl, jq, mysql client, om (OpenSVC CLI)
#
# CSV source (replication-entries.csv, same directory):
#   id,cve,mariadb_jira,github_issue,severity,title,description,flavor,affected_from,fixed_in,remediation_description,remediation_risk
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DEFAULT_OUTPUT="${REPO_ROOT}/cluster/logplugin/plugins/plugin-enterprise-replication/enterprise-replication-issues.json"
CSV_FILE="${SCRIPT_DIR}/replication-entries.csv"

OUTPUT="${DEFAULT_OUTPUT}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
DRY_RUN=false
FORCE=false

# MariaDB client — same connection pattern as inject_plugins.sh / inject_db_from_git.sh.
# Password comes from OpenSVC secret store. Deferred: only evaluated when DB access is needed.
mariadb_client() {
  mysql -hproxy-portal-1.s18.svc.cloud18 -p"$(om s18/sec/env decode --key MYSQL_ROOT_PASSWORD)" -uroot dbaas "$@"
}

# Stamp file records the epoch of the last successful run.
# The script exits early if less than 24 hours have passed since the last run.
# Use --force to bypass.
STAMP_FILE="${SCRIPT_DIR}/.enterprise-replication-last-run"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)        OUTPUT="$2";        shift 2 ;;
    --github-token)  GITHUB_TOKEN="$2";  shift 2 ;;
    --dry-run)       DRY_RUN=true;       shift ;;
    --force)         FORCE=true;         shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Daily run guard — skip if last successful run was less than 24h ago
# ---------------------------------------------------------------------------

if ! $FORCE && [[ -f "${STAMP_FILE}" ]]; then
  LAST_RUN=$(cat "${STAMP_FILE}" 2>/dev/null || echo 0)
  NOW=$(date +%s)
  ELAPSED=$(( NOW - LAST_RUN ))
  if [[ ${ELAPSED} -lt 86400 ]]; then
    HOURS_AGO=$(( ELAPSED / 3600 ))
    echo "[enterprise-replication-issues] Last run was ${HOURS_AGO}h ago — skipping (use --force to override)" >&2
    exit 0
  fi
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() { echo "[enterprise-replication-issues] $*" >&2; }

gh_curl() {
  local url="$1"
  local headers=(-H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28")
  [[ -n "${GITHUB_TOKEN}" ]] && headers+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  curl -fsSL "${headers[@]}" "$url"
}

gl_curl() {
  local method="$1" url="$2"
  shift 2
  curl -fsSL -X "${method}" \
    -H "Authorization: Bearer ${GITLAB_TOKEN}" \
    -H "Content-Type: application/json" \
    "$@" "${url}"
}

strip_quotes() { local v="$1"; v="${v#\"}"; v="${v%\"}"; echo "$v"; }

# ---------------------------------------------------------------------------
# 1. Collect issues from CSV
# ---------------------------------------------------------------------------

CSV_ISSUES_JSON="[]"

if [[ -f "${CSV_FILE}" ]]; then
  log "Reading CVE entries from ${CSV_FILE}..."
  tmp_csv=$(mktemp)
  tail -n +2 "${CSV_FILE}" | while IFS=, read -r id cve mariadb_jira github_issue severity title description flavor affected_from fixed_in remediation_description remediation_risk; do
    id="$(strip_quotes "$id")"
    [[ -z "$id" ]] && continue
    jq -n \
      --arg id             "$(strip_quotes "$id")" \
      --arg cve            "$(strip_quotes "$cve")" \
      --arg mariadb_jira   "$(strip_quotes "$mariadb_jira")" \
      --arg github_issue   "$(strip_quotes "$github_issue")" \
      --arg severity       "$(strip_quotes "$severity")" \
      --arg title          "$(strip_quotes "$title")" \
      --arg description    "$(strip_quotes "$description")" \
      --arg flavor         "$(strip_quotes "$flavor")" \
      --arg affected_from  "$(strip_quotes "$affected_from")" \
      --arg fixed_in       "$(strip_quotes "$fixed_in")" \
      --arg rem_desc       "$(strip_quotes "$remediation_description")" \
      --arg rem_risk       "$(strip_quotes "$remediation_risk")" \
      '{id:$id,cve:$cve,mariadb_jira:$mariadb_jira,github_issue:$github_issue,
        severity:$severity,title:$title,description:$description,flavor:$flavor,
        affected_from:$affected_from,fixed_in:$fixed_in,
        remediations:[{type:"repman_config",description:$rem_desc,risk:$rem_risk}]}'
  done | jq -s '.' > "${tmp_csv}"
  CSV_ISSUES_JSON="$(cat "${tmp_csv}")"
  rm -f "${tmp_csv}"
else
  log "No replication-entries.csv found — skipping CSV entries"
fi

# ---------------------------------------------------------------------------
# 2. Fetch CVEs from NIST NVD for MariaDB and MySQL
#    NVD API 2.0: https://services.nvd.nist.gov/rest/json/cves/2.0
#    Rate limit: 5 req/30s without API key, 50 with key.
#    Each CVE may have multiple affected version ranges → one issue per range.
# ---------------------------------------------------------------------------

NVD_API="https://services.nvd.nist.gov/rest/json/cves/2.0"
NVD_ISSUES_JSON="[]"

# nvd_fetch_cves <keyword> <flavor> <cpe_vendor>
# Paginates through NVD results, filters to MariaDB/MySQL CPE entries with
# version ranges, emits one JSON issue per (CVE, version-range) pair.
nvd_fetch_cves() {
  local keyword="$1" flavor="$2" cpe_vendor="$3"
  local start_index=0 total=0 page_size=200 page=0

  log "  Fetching NVD CVEs for ${flavor} (keyword=${keyword})..."
  while :; do
    local url="${NVD_API}?keywordSearch=${keyword}&resultsPerPage=${page_size}&startIndex=${start_index}"
    local raw
    raw=$(curl -fsSL --retry 2 --retry-delay 6 "${url}" 2>/dev/null) || {
      log "    WARNING: NVD API request failed at offset ${start_index} — stopping"
      break
    }

    if [[ ${page} -eq 0 ]]; then
      total=$(echo "$raw" | jq '.totalResults')
      log "    NVD reports ${total} total result(s) for '${keyword}'"
    fi

    # Extract MariaDB/MySQL CPE matches with version ranges.
    # Each CVE × version-range becomes a separate issue entry so the plugin
    # can match per-branch (e.g. 10.4.x fixed at 10.4.26, 10.5.x at 10.5.17).
    echo "$raw" | jq --arg flavor "$flavor" --arg cpe_vendor "$cpe_vendor" '
      [.vulnerabilities[]? |
       .cve as $cve |
       # CVSS score: prefer v3.1, then v3.0, then v2
       ($cve.metrics.cvssMetricV31[0].cvssData.baseScore //
        $cve.metrics.cvssMetricV30[0].cvssData.baseScore //
        $cve.metrics.cvssMetricV2[0].cvssData.baseScore // 0) as $score |
       ($cve.descriptions[] | select(.lang=="en") | .value) as $desc |
       # Walk all CPE matches and keep only the target vendor with version ranges
       $cve.configurations[]?.nodes[]?.cpeMatch[]? |
       select(.criteria | test("cpe:2\\.3:a:" + $cpe_vendor + ":")) |
       select(.versionEndExcluding != null) |
       {
         id: ($cve.id + "-" + ($flavor | ascii_downcase) + "-" +
              (if .versionStartIncluding then (.versionStartIncluding | split(".") | .[0:2] | join("."))
               else "0" end)),
         cve: $cve.id,
         mariadb_jira: "",
         github_issue: "",
         severity: (if $score >= 9 then "SECURITY"
                    elif $score >= 7 then "SECURITY"
                    elif $score >= 4 then "SECURITY"
                    else "WARNING" end),
         title: ($flavor + " " + $cve.id + " (CVSS " + ($score | tostring) + ")"),
         description: ("Server {server_url} is running {flavor} {version} which is affected by " +
                        $cve.id + " (CVSS " + ($score | tostring) + ") — " +
                        ($desc | if length > 200 then .[:200] + "..." else . end)),
         flavor: $flavor,
         affected_from: (.versionStartIncluding // ""),
         fixed_in: .versionEndExcluding,
         remediations: [{
           type: "repman_config",
           description: ("Upgrade " + $flavor + " to " + .versionEndExcluding + " or later."),
           risk: "disruptive"
         }]
       }
      ]' >> /tmp/nvd_batch.json

    start_index=$((start_index + page_size))
    page=$((page + 1))
    [[ ${start_index} -ge ${total} ]] && break

    # Respect NVD rate limit (5 req / 30s without API key)
    sleep 6
  done
}

rm -f /tmp/nvd_batch.json
touch /tmp/nvd_batch.json

nvd_fetch_cves "mariadb+replication"       "MariaDB" "mariadb"
sleep 6
nvd_fetch_cves "mysql+replication+oracle"  "MySQL"   "oracle"

# Merge all NVD batches, deduplicate by id, and exclude any CVEs already in CSV
# (CSV entries are hand-curated and take priority).
CSV_CVE_IDS=$(echo "${CSV_ISSUES_JSON}" | jq -r '.[].cve | select(. != "")' | sort -u)
NVD_ISSUES_JSON=$(cat /tmp/nvd_batch.json | jq -s 'add // []' | jq --arg csv_cves "${CSV_CVE_IDS}" '
  ($csv_cves | split("\n") | map(select(. != "")) | INDEX(.; .)) as $skip |
  [.[] | select(.cve as $c | $skip[$c] | not)] |
  unique_by(.id) |
  sort_by(.cve)
')
NVD_COUNT=$(echo "$NVD_ISSUES_JSON" | jq 'length')
log "  Collected ${NVD_COUNT} NVD issue(s) (after dedup, excluding CSV overrides)"
rm -f /tmp/nvd_batch.json

# ---------------------------------------------------------------------------
# 4. Fetch open GitHub security issues from signal18/replication-manager
# ---------------------------------------------------------------------------

log "Fetching GitHub security issues..."
GH_ISSUES_JSON="[]"
if GH_RAW=$(gh_curl "https://api.github.com/repos/signal18/replication-manager/issues?labels=replication&state=open&per_page=100" 2>/dev/null); then
  GH_ISSUES_JSON=$(echo "$GH_RAW" | jq '[
    .[] |
    {
      id: ("GHENT" + (.number | tostring)),
      cve: "", mariadb_jira: "",
      github_issue: ("signal18/replication-manager#" + (.number | tostring)),
      severity: "WARNING",
      title: .title,
      description: ("Open security issue: " + .title +
        " — see https://github.com/signal18/replication-manager/issues/" +
        (.number | tostring) +
        ". Server {server_url} running {flavor} {version} may be affected."),
      flavor: "", affected_from: "", fixed_in: "",
      remediations: [{
        type: "repman_config",
        description: ("Review signal18/replication-manager#" + (.number | tostring)),
        risk: "moderate"
      }]
    }
  ]')
  log "Found $(echo "$GH_ISSUES_JSON" | jq 'length') open GitHub security issue(s)"
else
  log "WARNING: Cannot fetch GitHub issues — skipping"
fi

# ---------------------------------------------------------------------------
# 5. Merge and write local output
# ---------------------------------------------------------------------------

GENERATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
MERGED=$(jq -n \
  --arg version      "1" \
  --arg generated_at "${GENERATED_AT}" \
  --arg source       "signal18-backoffice" \
  --argjson csv      "${CSV_ISSUES_JSON}" \
  --argjson nvd      "${NVD_ISSUES_JSON}" \
  --argjson gh       "${GH_ISSUES_JSON}" \
  '{version:$version,generated_at:$generated_at,source:$source,issues:($csv+$nvd+$gh)}')

log "$(echo "$MERGED" | jq '.issues|length') total issue(s) in advisory database"

if $DRY_RUN; then
  log "Dry-run: showing merged JSON and eligible instances (no git push)"
  echo "$MERGED"
fi

echo "$MERGED" > "${OUTPUT}"
log "Written to ${OUTPUT}"

# ---------------------------------------------------------------------------
# 6. Query the dbaas DB for eligible instances
#
# The plan is stored in each repman instance's TOML config, extracted by
# inject_db_from_git.sh into the clusters_config table:
#   variable = 'cloud18-subscription-plan', value = 'support'|'support-services'|'partner'|'free'
#
# clusters_config.domain / .subdomain / .cluster are generated columns derived
# from the TOML file path: ../repos/{domain}/{subdomain}/{cluster}/config.toml
#
# The pull repo is at: ../repos/{domain}/{subdomain}-pull  (same as inject_plugins.sh)
# ---------------------------------------------------------------------------

log "Querying dbaas for eligible instances (plan: support / support-services / partner)..."

mariadb_client -N -B -e "
    SELECT DISTINCT cc.domain, cc.subdomain, cc.value AS plan
    FROM clusters_config cc
    WHERE cc.variable = 'cloud18-subscription-plan'
      AND cc.value IN ('support', 'support-services', 'partner')
    ORDER BY cc.domain, cc.subdomain
" > /tmp/bo_instances.tsv 2>&1 || {
    log "ERROR: DB query failed:" >&2
    cat /tmp/bo_instances.tsv >&2
    rm -f /tmp/bo_instances.tsv
    exit 1
  }

TOTAL_INSTANCES=$(grep -c . /tmp/bo_instances.tsv 2>/dev/null || echo 0)
log "Found ${TOTAL_INSTANCES} eligible instance(s)"

if [[ "${TOTAL_INSTANCES}" -eq 0 ]]; then
  log "No eligible instances — nothing to push"
  rm -f /tmp/bo_instances.tsv
  date +%s > "${STAMP_FILE}"
  exit 0
fi

# ---------------------------------------------------------------------------
# 7. Copy advisory file into each eligible instance's pull repo and push
#    The JSON is global (same for all clusters on an instance), so it goes
#    once at the pull repo root: plugins/data/enterprise-replication-issues.json
#    repman syncs it to ShareDir/plugins/data/ on git pull.
# ---------------------------------------------------------------------------

PUSHED=0
FAILED=0

while IFS=$'\t' read -r domain subdomain plan; do
  [[ -z "$domain" ]] && continue

  pull_repo="$SCRIPT_DIR/../repos/$domain/$subdomain-pull"

  if [ ! -d "$pull_repo" ]; then
    log "  pull repo $pull_repo not found — skipping $domain/$subdomain (plan: $plan)"
    FAILED=$((FAILED + 1))
    continue
  fi

  if $DRY_RUN; then
    log "  [dry-run] would copy to $pull_repo/plugins/data/enterprise-replication-issues.json (plan: $plan)"
    continue
  fi

  git -C "$pull_repo" pull --quiet 2>/dev/null || true

  mkdir -p "$pull_repo/plugins/data"
  cp "${OUTPUT}" "$pull_repo/plugins/data/enterprise-replication-issues.json"

  log "  Committing and pushing to $domain/$subdomain-pull (plan: $plan)..."

  git -C "$pull_repo" add "plugins/data/enterprise-replication-issues.json"
  git -C "$pull_repo" commit -m "backoffice: update enterprise-replication-issues.json (${GENERATED_AT})" 2>/dev/null || {
    log "    No changes to commit (file unchanged)"
    continue
  }
  git -C "$pull_repo" push --quiet 2>/dev/null && {
    log "    Pushed"
    PUSHED=$((PUSHED + 1))
  } || {
    log "    ERROR: git push failed for $domain/$subdomain-pull"
    FAILED=$((FAILED + 1))
  }

done < /tmp/bo_instances.tsv

rm -f /tmp/bo_instances.tsv

log ""
log "Deploy complete: ${PUSHED} pushed, ${FAILED} failed"

# Record successful run timestamp
date +%s > "${STAMP_FILE}"

[[ "${FAILED}" -gt 0 ]] && exit 1 || exit 0
