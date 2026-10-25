# Cloud18 registration without GitLab password nor CRM call (#2159)

## Why

GitLab ends the password grant (ROPC): replication-manager can no longer get a GitLab token from the
GitLab password of the admin, which the registration used twice (proving the person to the CRM, then
getting its git token and repositories) and the rotation once. The Cloud18 identity realm (Keycloak)
replaces the password. Registration stays **declarative**: repman declares the instance in GitLab, the BO
does the business side (subscription, billing customer, chat user, settlement credential) from GitLab.

Words: a **Cloud18 account** is anyone (partner infrastructure consumers too) and needs no GitLab;
**registration** is the repman admin registering the instance to Cloud18 and needs a GitLab account
linked to the Cloud18 account; **login** (a person entering repman with SSO) is designed separately.

## Flow (`server/server_cloud18_register.go`, `cloud18Register`)

`POST /api/register {uri, plan}` (GUI Cloud18 settings, Register; `replication-manager-cli register
--uri`), status in `GET /api/register/status`:

1. **Cloud18 login** of the admin, when the instance has none: the device login of the realm's public
   client `replication-manager` (no secret, no redirect URI, PKCE S256, CLOUD18_IDENTITY.md). State
   `login` carries the code and the page; the admin approves in any browser.
2. **GitLab token** of that person from the realm (`config.Cloud18GitLabBrokerToken`: identity broker
   stored token of the provider `gitlab-repman`, scope `api`; the client holds the `broker/read-token`
   scope mapping). Used only for the calls below, **never stored**, also for a GitLab admin. No GitLab
   account linked: state `link` with the realm's Linked accounts page; the admin links one (signing up on
   GitLab when needed: sign-up is open, email confirmed) and registers again.
3. **Domain group** (`githelper.GitLabEnsureDomainGroup`): created private when absent (every GitLab user
   may create groups), else the person must be a member (Developer or more): otherwise state `taken`.
4. **Projects** `{subdomain}-{zone}` and `-pull` (`githelper.GitLabCreateProjectIn`). GitLab refusing an
   existing path ("has already been taken") **is the URI's uniqueness**. An existing config project is a
   **reconnect** when its `default.toml` holds no encrypted value yet (a registration that did not finish)
   or when this instance's encryption key decrypts its values (`config.OwnsEncryptedValues`: AES-CFB has
   no integrity check, a wrong key gives random bytes, so every value must come back printable). The key is
   never in git (`WriteKeyToWorkingDir` has no caller; git stages only `<cluster>/*.toml`, a few JSON files,
   `default.toml`, the event logs): only the instance and its peers have it. Anything else: state `taken`,
   nothing changed.
5. **Project tokens**, one per project (`write_repository`, `self_rotate`, maintainer, a year): the only
   GitLab credentials the instance keeps — `git-acces-token` (config repository), `git-pull-access-token`
   (pull repository), `cloud18-git-project-id`, `cloud18-git-pull-project-id`, `cloud18-git-token-expires`,
   all instance-local (never replicated to peers). Then `cloud18`, the URI, `cloud18-gitlab-user` (the
   person's email), the plan, `InitGitConfig`, save.

## Tokens renew themselves (`config/cloud18_git.go`)

`RotateCloud18GitTokens` (`/projects/:id/access_tokens/self/rotate` with the token itself): daily check
(30 days before the end) and when GitLab refuses a push (`RotateGitAccessToken`). No password, no realm,
no CRM. A token GitLab already refuses cannot renew itself: `GWARN022` asks to register again (same URI:
a reconnect).

## Older instances

An instance registered with the GitLab password keeps that path untouched (`InitGitConfig`, rotation)
while GitLab accepts it. When GitLab refuses the password, `GWARN022` asks to register again through
Cloud18; the reconnect keeps its repositories. `GWARN021` concerns only instances registered through
Cloud18 whose Cloud18 login ended (their SSO storages and vault use it).

## Realm and GitLab setup

- GitLab OAuth application "Cloud18 replication-manager registration" (scopes `openid profile email api`,
  redirect `<realm>/broker/gitlab-repman/endpoint`).
- Realm identity provider `gitlab-repman`: store tokens, default scope `openid profile email api`, PKCE,
  mapper `sub` → `gitlab_id`, hardcoded role `broker/read-token` (sync FORCE) so only people who log in or
  link through it get the role; the role is not a default role (clients with full scope would get it).
- Public client `replication-manager`: device grant only, PKCE S256, consent, no redirect URI, full scope
  off, default scope `cloud18-storage` (not `basic`), scope mapping `broker/read-token`.

## Validated live (2026-10-25, Keycloak 26.8.1, GitLab 18.11)

Device login with PKCE (a public client with PKCE refuses a request without `code_challenge`); broker
token 403 until the client maps `broker/read-token` (full scope off), then 200 with scope `api`, 2 h; with
it: project 201, same path 400 "has already been taken", project token 201, git push, self-rotation 200
(the old token refused, the new one pushes).
