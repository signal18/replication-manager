# Cloud18 registration (community onboarding)

Registration is the **community** feature by which any replication-manager instance
joins Cloud18. It is open self-service: a repman authenticates against the shared
GitLab SSO, claims a **domain**, and from then on syncs its cluster configuration to
Cloud18 and receives community data (peers, partners, terms) back. Registration is a
prerequisite for — not the same as — the partner **[marketplace](../peer/MARKETPLACE.md)**;
joining the community is step one, offering clusters for sale is an optional later step.

The trust model is **first-claimant domain ownership**, enforced by GitLab: the first
repman to register a domain owns its group; later participants are invited by that
owner. Email is never an authorization input — a GitLab SSO account is the only
identity that grants anything.

## Prerequisites

A registering repman needs (in its config):

- `cloud18` = true
- `cloud18-domain` — the community domain the operator claims (e.g. an org name)
- `cloud18-sub-domain` + `cloud18-sub-domain-zone` — identify this fleet/site within the domain
- `cloud18-git-user` + `cloud18-gitlab-password` — the operator's GitLab SSO account

The GitLab instance must allow group creation by the registering user
(`can_create_group = true`). This is required, not optional: repman creates the
domain group **as the registering user** so GitLab makes that user the owner. Do not
disable it — that would break registration (there is no server-side group-minting
fallback).

## The flow, A→Z

Driven by `InitGitConfig` (`server/server_git.go`). All paths below are in the
public repman tree.

| # | Step | Code |
|---|---|---|
| A | OAuth login to GitLab SSO → access token; resolve user id | `server_git.go` (`GetGitLabUserId`) |
| B | Resolve domain group: get it, or create-and-own it | `githelper.InitGroupAccessLevel` → `CreateCloud18Domain` |
| C | Issue/rotate a personal access token for the git channel | `githelper.GetGitLabTokenOAuth` / `CreatePersonalAccessTokenCSRF` |
| D | Create the two projects: `<sub>-<zone>` and `<sub>-<zone>-pull` | `githelper.GitLabCreateProject` / `GitLabCreatePullProject` |
| E | Set `GitUrl` (push) and `GitUrlPull` (pull) | `server_git.go:545-546` |
| F | First config sync: commit cluster config, push to the empty repo | `config/manager` `PushConfigToGit` |
| G | BO ingests the pushed config and aggregates the community | BO (see [MARKETPLACE §3](../peer/MARKETPLACE.md)) |
| H | BO ships `peer.json`/`partners.json`/`terms.md` to the `-pull` repo | BO |
| I | repman pulls `-pull`, loads peers/partners | `config/manager` pull; `PeerManager` |

### The two repositories

Each registered fleet owns **two** git repos, with **different writers** — do not
conflate them:

- **`<sub>-<zone>`** — the config repo. **repman writes** its cluster configuration
  here; the BO reads it. This is the one whose first-commit bootstrap matters (below).
- **`<sub>-<zone>-pull`** — the community feed. **The BO writes** here (`peer.json`
  etc.); repman only pulls. Because the BO seeds it, it is never subject to the
  empty-remote trap.

## First-claimant domain ownership (the security boundary)

`InitGroupAccessLevel` GETs the domain group with the **user's** token. GitLab returns
`404` for two indistinguishable reasons — the group does not exist, *or* it exists but
is private and the user is not a member. repman therefore attempts to create it:

- **Group does not exist** → create succeeds → the registering user is the **owner**.
  Open self-registration is safe precisely because a new registrant only ever lands in
  a group they own.
- **Group exists, owned by someone else** → GitLab rejects the create with
  `400 "has already been taken"`. repman surfaces this as
  `ErrDomainAlreadyOwned` and returns an actionable message
  (*"domain already registered to another account; ask its owner to grant you access"*)
  rather than looping on a group it can never see.

Forging an email domain is useless here: it can only ever let you create/own a *fresh*
group named after your own claim, never inject you into an established one. (The BO
does **not** grant group membership from email — that path was removed; membership is
owner-invited.)

### GitLab error surfacing

GitLab returns validation errors under `message`, which may be a plain string or a
`field → [reasons]` object; OAuth endpoints instead use `error`/`error_description`.
`githelper.gitlabErrorMessage()` normalizes all three shapes (falling back to the raw
body) so failures like the taken-path case are legible instead of blank.

## Empty-remote config-repo bootstrap

The config repo (`<sub>-<zone>`) is created empty (no initial commit). The config-sync
worker fetches before it pushes; `git.PlainClone` of a bare remote returns
`transport.ErrEmptyRemoteRepository`, which cannot produce a working tree. If that
case is not handled, the very first push never lands, the repo stays empty forever,
and the git-sync warning **`GWARN002` ("empty remote repository")** re-fires every
cycle.

`cloneRepositoryWithBootstrap` (`config/manager/manager.go`) handles it: on an empty
remote it initializes a local repository pointed at the same URL on branch `master`
(matching `ResolveCurrentLocalBranch`), so the subsequent add/commit/push lands the
first commit and establishes the remote's default branch. GitLab then adopts `master`
as the project default (as it does for any first push into an empty project).

> This path was lost in the config-sync refactor into `ConfigManager` — the earlier
> push path handled empty remotes, the refactored `cloneRepositoryWithBootstrap`
> initially covered only *repository-not-found*. Symptom of the regression: a freshly
> registered cluster's config repo stays empty and GWARN002 recurs; fixed by also
> handling `ErrEmptyRemoteRepository`.

## Operational notes / troubleshooting

- **GWARN002 "empty remote repository"** on a freshly registered cluster: expected
  *once*, on the first sync into the new empty config repo; it must clear after the
  bootstrap lands the first commit. If it **recurs every cycle**, the config repo is
  stuck empty — the running repman predates the empty-remote bootstrap fix; upgrade it.
- **"domain already registered to another account"**: the domain group exists and is
  owned by a different account. Have the domain owner invite this user in GitLab (this
  is the intended community model), or claim a different domain.
- **Config repo empty on GitLab but `-pull` has content**: normal transiently — the BO
  seeds `-pull` independently. If the config repo stays empty, see GWARN002 above; the
  two repos have different writers.
- **Additional operators on an existing domain** are not auto-added (membership is not
  derived from email). The domain owner invites them in GitLab.

## Related

- [Peer Marketplace & Cross-Repman Health](../peer/MARKETPLACE.md) — what registration
  unlocks (the `peer.json` community feed, for-sale listings, delegated access).
