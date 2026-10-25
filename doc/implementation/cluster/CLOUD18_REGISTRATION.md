# Cloud18 registration with the Cloud18 account, no GitLab password, no CRM call (#2159)

## Why

GitLab ends its password grant (ROPC): replication-manager can no longer get a GitLab token from the
admin's GitLab password, which the registration used to prove the person to the CRM and to get its git
token and repositories. Cloud18 (the identity realm, Keycloak) replaces it: `register` keeps the logic the
GitLab one had, against Cloud18 — the account exists: email and password, then a token; it does not:
fill the sign-up, wait for the email confirmation, then a token. Registration stays **declarative**:
repman declares the instance in GitLab, the BO does the business side (subscription, billing customer,
chat user, settlement credential) from GitLab. No CRM call, no browser (a CLI user is often on a remote
host), no admin right.

Words: a **Cloud18 account** is anyone (partner infrastructure consumers too); **registration** is the
repman admin registering the instance to Cloud18; **login** (a person entering repman with SSO) is
designed separately.

## GitLab trusts Cloud18

GitLab has the realm as identity provider (OmniAuth OpenID Connect, button "Cloud18", realm client
`gitlab`): a person logs in to GitLab with the Cloud18 account, GitLab creates the account at the first
login (an existing GitLab account is linked by its email, verified by the realm). So a Cloud18 account is
enough: no second sign-up on GitLab.

## Flow (`server/server_cloud18_register.go`, `cloud18Register`)

`POST /api/register {uri, plan, email, password, signUp}` (GUI Cloud18 settings, Register;
`replication-manager-cli register --uri --email`, the password asked hidden), status in
`GET /api/register/status`:

1. **GitLab token of the Cloud18 account** (`config.Cloud18GitLabLogin`): the repman server walks over
   HTTPS what a browser would: GitLab's OAuth authorization (public application
   `ConstCloud18GitLabOAuthClientID`, PKCE S256, trusted so no consent page, redirect
   `http://127.0.0.1/cloud18-register` never opened: GitLab answers a non-https redirect with its
   "Redirecting" page carrying the code), "Sign in with Cloud18", the realm's login form with the email
   and password, back to GitLab, the code traded for a GitLab token (scope `api`, 2 h). The password and
   that token are **never stored**, also for a GitLab admin. The password is posted only to a form of
   the realm's own host over https, and no redirect may leave https (the loopback alone in clear); a page
   whose form is missing reports that the GitLab or realm page layout changed.
   - the realm refuses the password: state `credentials`; with `signUp` (first name, last name, company,
     phone) and an unknown email, **`config.Cloud18SignUp`** fills the realm's own sign-up form, as anyone
     can on the site. The realm creates the account **without password** (it ignores one sent) with the
     actions verify email and choose password: the confirmation email's link does both. State `verify`;
     the person registers again with the chosen password. An existing email: state `credentials`.
   - the account waits for its email confirmation: state `verify`.
2. **Domain group** (`githelper.GitLabEnsureDomainGroup`): created private when absent (every GitLab user
   may create groups), else the person must be a member (Developer or more): otherwise state `taken`.
3. **Projects** `{subdomain}-{zone}` and `-pull` (`githelper.GitLabCreateProjectIn`). GitLab refusing an
   existing path ("has already been taken") **is the URI's uniqueness**. An existing config project is a
   **reconnect** when its `default.toml` holds no encrypted value yet (a registration that did not finish)
   or when this instance's encryption key decrypts its values (`config.OwnsEncryptedValues`: AES-CFB has
   no integrity check, a wrong key gives random bytes, so every value must come back printable). The key is
   never in git: only the instance and its peers have it. Anything else: state `taken`, nothing changed.
4. **Project tokens**, one per project (`write_repository`, `self_rotate`, maintainer, a year): the only
   GitLab credentials the instance keeps — `git-acces-token`, `git-pull-access-token`,
   `cloud18-git-project-id`, `cloud18-git-pull-project-id`, `cloud18-git-token-expires`, all instance-local
   (never replicated to peers). Then `cloud18`, the URI, `cloud18-gitlab-user` (the person's email), the
   plan, `InitGitConfig`, save.

The MCP tools do not register: that needs the person's Cloud18 password, which an assistant never
handles; they answer where to register.

## Tokens renew themselves (`config/cloud18_git.go`)

`RotateCloud18GitTokens` (`/projects/:id/access_tokens/self/rotate` with the token itself): daily check
(30 days before the end) and when GitLab refuses a push (`RotateGitAccessToken`). No password, no realm,
no CRM. A token GitLab already refuses cannot renew itself: `GWARN022` asks to register again (same URI:
a reconnect).

## Older releases and older instances

- **Releases before this one** register and rotate with the GitLab password grant. They keep working as
  long as **our** GitLab accepts that grant (checked 2026-10-25 on GitLab 18.11 after adding Cloud18: the
  token endpoint still has `password`, web and git password login on, every existing account keeps its
  GitLab password). **Rule: our GitLab is not upgraded to a version without the password grant before the
  fleet runs a release with this registration.** An account created through Cloud18 has no GitLab
  password, so it cannot register an older release.
- **An instance registered with the password moves to its project tokens by itself** at start
  (`cloud18MigrateToProjectTokens`, from `InitGitConfig`): nobody has to do anything. Its stored GitLab
  user and password go through **GitLab's own web sign-in form** (`config.GitLabPasswordLogin`, not the
  password grant GitLab ends), then GitLab's OAuth as above gives the token, and the declaration finds its
  repositories (its key decrypts their config): a reconnect, its own project tokens from then on. Validated
  live 2026-10-25 with a throwaway GitLab user (sign-in form, code, token `api`). When it cannot (a 2FA
  account, a captcha, a changed password), the instance keeps the password path untouched and the log
  says why; `GWARN022` asks to register again once GitLab refuses that password too. A password GitLab's
  sign-in refused is not tried again until it changes (GitLab locks an account after a few failures);
  any other failure waits an hour. After the move the stored GitLab password stays (encrypted, unused):
  it lets the instance roll back to an older release.

## Setup (realm and GitLab)

- Realm client `gitlab` (confidential, standard flow, PKCE, redirect
  `https://gitlab.signal18.io/users/auth/openid_connect/callback`); GitLab `gitlab.rb` OmniAuth
  `openid_connect` "Cloud18" (`omniauth_block_auto_created_users` false, `omniauth_auto_link_user`).
- GitLab OAuth application `replication-manager`: public, trusted, scope `api`, redirect
  `http://127.0.0.1/cloud18-register`.

## Validated live (2026-10-25, Keycloak 26.8.1, GitLab 18.11)

With a throwaway person: GitLab's "Sign in with Cloud18" created the GitLab account (active, confirmed, not
admin, may create groups, identity `openid_connect`); the repman-side walk above gave a GitLab token
(`api`, 2 h, refreshable) and created a group; the realm sign-up with and without a posted password both
gave an account without password and the actions verify email and choose password. GitLab's token
endpoint has no token exchange (`authorization_code`, `password`, `client_credentials`, `device_code`
only): a realm token is not accepted by GitLab's API or git (401).
