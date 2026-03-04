---
allowed-tools: Bash(git log:*), Bash(git tag:*), Bash(git pull:*), Read, Edit, Bash(git describe:*)
description: Update CHANGELOG.md for a new release version
---

Update the CHANGELOG.md for the specified version: $ARGUMENTS

## Process

1. **Determine version scope**
   - If a version is provided (e.g. `3.1.18`), use it directly
   - Otherwise, determine the latest tag with `git describe --tags --abbrev=0`
   - Find the previous tag to scope the commit range

2. **Get commits in range**
   - Run `git log vPREVIOUS..vCURRENT --oneline` to get all commits in the release
   - If the tag doesn't exist locally, run `git pull` first to fetch it
   - Identify the tag date: `git log vCURRENT --format="%ai" -1`

3. **Categorize commits**
   Group commits into changelog sections using conventional commit prefixes and PR numbers:
   - **Added**: New features, new CLI options, new config keys, new API endpoints
   - **Fixed**: Bug fixes, error handling, race conditions, regressions
   - **Changed**: Refactors, dependency upgrades, behavior changes, deprecations
   - **Security**: CVE fixes, injection prevention, encryption, privilege hardening

4. **Write the entry**
   Insert the new `## [VERSION] - DATE` block at the top of the release section in CHANGELOG.md, before the previous version's entry. Follow Keep a Changelog format.

   Entry style:
   - Group related commits into single bullet points
   - Reference PR numbers in parentheses: `(#1234)` or `(#1234, #1235)`
   - Write in past-tense imperative: "Added X", "Fixed Y"
   - Avoid duplicate or redundant bullets
   - Keep bullets focused — one concept per line
   - Omit trivial chore commits (CI tweaks, typo fixes) unless notable

5. **Add link reference**
   At the bottom of CHANGELOG.md, add:
   ```
   [VERSION]: https://github.com/signal18/replication-manager/releases/tag/vVERSION
   ```

6. **Update the series summary**
   Read the existing `## X.Y.x Series` summary block (e.g. `## 3.1.x Series`) and revise it to reflect the current state of the series.

   Rules for the summary:
   - The summary describes the **series as a whole**, not individual releases
   - Keep it to **8–12 bullet points maximum** — prune, merge, or generalize as needed
   - Each bullet covers a **capability area** (e.g. Backup & Restore, Security, Topology), not a specific fix
   - When new capabilities are added, absorb them into existing bullets where they fit; only add a new bullet if the area is genuinely new
   - When multiple small items accumulate under the same area, collapse them into a single concise statement
   - Remove specifics (PR numbers, version numbers, named commits) — the summary is evergreen prose
   - Do not let any bullet exceed two lines

## Constraints

- Do not invent features or fixes not present in the git log
- Do not modify entries for prior versions
- Use the actual tag date, not today's date
