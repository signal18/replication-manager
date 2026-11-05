# Global settings and the terminal: who may change and open them (#2262)

## Why

A global setting (`scope:"server"`) acts on the whole instance: the logins, the API, the MCP, the
terminal, the gateways. A terminal is a shell, on the repman host or in a database, proxy or app
container. Both were open to any account holding the right grant, a Cloud18 login or an API token
included. Leaked credentials with those grants could then turn a guard back on and open a shell
through the API. Grants are not enough here: the kind of login decides.

## Global settings: the local admin account only

`isGlobalSettingsAdmin` (`server/api_global_settings.go`) lets only the session of the local
`admin` account (`AuthType` `Local`, an account of `api-credentials`) change a global setting:
another local account, a Cloud18 (SSO) login and an API token get a 403, written to the security log
(`global_setting_denied`). It guards `switch`, `set` and `clear` (both the global routes and the
cluster routes that hand a server-scope key to them) and the calibrations that write one
(`calibrate-iops`, `calibrate-smt-gain`, gated before any cluster lookup). Cluster settings are not
concerned: they keep their grants. The MCP's `cluster-set-setting` / `cluster-switch-setting` call the
cluster setter directly: they refuse a global key (`errGlobalSettingThroughCluster`), or they would skip
both checks.

## Immutable global settings: never through the API

A key set in the configuration file, on the command line or in the environment is immutable
(`Conf.ImmuableFlagMap`). It already won over `default.toml` at the next restart, but the API changed
it in memory until then. `setServerSetting` and `switchServerSetting` now refuse it
(`errImmutableGlobalSetting`, a 403 logged as `global_setting_denied`), for every caller, the
admin included: such a setting is changed on the host, in the file.

### In the GUI: a lock, and a rollback lock for the cluster settings (#2268)

`/api/monitor` (the global settings) and `/api/clusters/{name}` (the cluster's) carry
`immutableSettings`: each key the system config sets (the configuration file, the command line,
the environment) with its value there, as text (`Conf.ImmuableFlagValues`). A secret's value is
never sent: it is `SecretMask`, as on every API path, for a key the secret registry knows or whose
name says it holds one (password, credential, secret, token...). `/api/monitor` sends it only to a
logged-in caller (the route also answers without a token); the cluster route is behind its ACL.

The settings pages tag each row of the settings table with the key its control changes:
`globalSetting` on the global pages, `clusterSetting` on the cluster pages (`components/TableType2`,
`components/SettingLock`):

- a **global** setting set in the system config gets a lock, its controls disabled: the API refuses
  the change (`immutableGlobalSetting`). Tooltip: "Locked from system config", and its value there;
- a **cluster** setting set in the system config gets a rollback lock, its controls editable: the
  API accepts the change, for a test, and the next restart rolls it back to the system config
  (`setClusterSetting` logs a warning). Tooltip: "Set in system config: a change here is rolled back
  at the next restart", and the value it rolls back to.

A row whose control goes through a helper that does not take the key as its first `setting`
argument is not tagged yet.

### Durable config writes (`misc.WriteFileDurable`)

The secret store, each cluster's toml and `default.toml` are written through
`misc.WriteFileDurable`: a hidden `.<name>-*.tmp` file in the same directory, fsynced, renamed
over the file, then the directory fsynced. After a crash the file is the old one or the new one,
whole.

- An existing file keeps its mode and owner; a new one is `0640` (`0600` for the secret store).
- A symlink is followed (a dangling one is replaced by a regular file).
- A single-file bind mount cannot be renamed over (`EBUSY`/`EXDEV`): the file is written in place
  and fsynced. Durable, but not atomic: a crash between the truncate and the fsync leaves a short
  file.
- A temp file a crash left (it may hold secrets) is removed by the next write of the same file
  once older than 10 minutes.

### Not Interested in Clusters For Sale

`Config.Cloud18ForSaleOptOutAllowed`: a Developer, Support or Support and Services plan, or a
licence. After a downgrade, `cloud18-disable-for-sale` stays `true` in the saved config but the API
and the MCP ignore it, and the GUI shows it off and locked; it applies again if the plan returns.

## Terminal: the logins `terminal-session-auth` names

`terminal-session-auth` (scope server, default `local`) lists the logins that open a terminal:

- `local`: an account of this repman, logged in here;
- `cloud18-mfa`: a Cloud18 login, only while `cloud18-login-acr` asks a second factor (every Cloud18
  login of the instance then proved it, see `doc/implementation/cluster/CLOUD18_LOGIN.md`).

An API token, another SSO login, and a Cloud18 login without the second factor are refused
(`config.TerminalAuthAllows`, security log `terminal_denied`). Both settings are read at each terminal
request: unsetting `cloud18-login-acr` closes the terminal to Cloud18 logins at once, sessions opened
before included. A session is a Cloud18 one when its repman-signed JWT carries the `cloud18` or
`cloud18_token` claim, which only the three Cloud18 issuers set (the Sign in form through the realm,
Sign in with Cloud18, a verified realm token); another SSO provider never does. The global terminal (the shell on the
repman host) is for the local `admin` account only: the `cloud18-gitlab-user` exception is removed.
`terminal-session-enabled` stays the off switch. `terminal-session-auth = cloud18-mfa` alone also refuses
the local accounts (the host shell stays the local admin's either way): keep `local` in the list unless
that is wanted. Removed behaviour, for the release notes: the Cloud18 service account
(`cloud18-gitlab-user`) no longer opens the host shell.

## Admin mode in the GUI

A person signed in with Cloud18 changes a global setting without signing out: the server's 403 for
want of the local admin carries `X-Repman-Admin-Required: true`, the GUI then asks for the local admin
user and password (`AdminModeModal`), logs in with them (`POST /api/login`) and sends the refused
request again with that session (`services/adminMode.js`, `apiHelper.js`). The admin token is kept in
memory only (a reload or another tab asks again), until its end (`api-token-timeout`) or "Leave";
every other call keeps the person's own session. The host terminal asks for admin mode before it
connects. No server state: the gate stays the local admin session.

## The MCP: no local password login (`mcp-server-auth`)

`mcp-server-auth` (scope server): `cloud18` = a Cloud18 login (a realm token or a Sign in with Cloud18
session, with its second factor when `cloud18-login-acr` asks one) or an API token (scoped, revocable,
grants narrowed), never the session of a local password login; `any` = every login of the API; `auto`
(default) = `cloud18` on an instance registered to Cloud18, `any` otherwise (`config.MCPAuthAllows`,
checked in `AuthenticateMCP`, refusals in the security log as `mcp_auth_failure`). Local accounts keep
the API and the admin zone, not the MCP; clients on API tokens (the website assistant) are unchanged. The value is matched without case or spaces; an unknown value (a typo) acts as `cloud18`: it fails closed, never open. The stdio transport carries no login and starts only with `mcp-auth-enabled=false`, so it is outside this gate by construction.

## The login: Cloud18 only, the admin zone for local accounts (`Cloud18LoginRequired`)

The login follows the MCP's rule (`config.Cloud18LoginOnly`: `mcp-server-auth` resolved to `cloud18`,
so by default an instance registered to Cloud18) only where Sign in with Cloud18 is on
(`config.Cloud18LoginRequired`: `cloud18-sso-login` and a public https address, `Cloud18SSO`). No
setting of its own. An on-premise repman (no Sign in with Cloud18, registered to Cloud18 or not)
keeps its local accounts and their ACL: a company shares one repman between local users with their
own grants. On our public instances those local accounts are refused; their owners sign in with
Cloud18.

- `/api/login` (`loginHandler`): the admin accounts (global-admin-show) and `system` log in with
  their local password as before; every other local account is refused with a **401**, whatever
  its password. Every refusal there, a wrong admin or `system` password included, has the same
  status and the same text ("Invalid credentials… sign in with Cloud18"): never an oracle of the
  local password or of which accounts are admin accounts. The real reason is in the security log
  only (`api_auth_failure`). The local
  password is never tried first for them (no session, no asynchronous SSO upgrade); their Cloud18
  login (password grant, second factor per `cloud18-login-acr`) and Sign in with Cloud18 stay. A
  wrong password stays 401. API tokens and realm tokens are unchanged.
- `/api/auth/cloud18` answers `cloud18Only` when the rule holds (`Cloud18LoginRequired`). The login page then goes straight to the Cloud18 sign-in (the realm's page offers Sign
  up; an open realm session comes straight back, signed in). The admin zone is `/login?admin`: the
  user and password form (the local admin), with a way back to Cloud18. A failed Cloud18 return
  shows its reason, a Sign in with Cloud18 button and the Admin zone link, never a redirect loop.
  `/api/auth/cloud18/start` never ends on a bare error page: when it cannot start it sends back to
  `/login?cloud18_error=failed`. A local session's logout lands on `/login?admin`; a Cloud18
  session's logout ends the realm session. On a Cloud18-only instance `/login` does not run the
  autologin (`/api/autologin`); `/login?admin` still does.
  With the realm unreachable the browser stops on the realm's address: the admin zone is reached
  by its address, `/login?admin`.

## Recovery: the configuration file

With Keycloak or the realm down, whoever holds the host edits `/etc/replication-manager/config.toml`
(for example empties `cloud18-login-acr`, sets `mcp-server-auth = "any"`, which also reopens the
login to every local account) and restarts. Those keys are
immutable: only the file changes them, never the API. The local admin login keeps working through
the API meanwhile.

## Tests

`config/terminal_auth_test.go` (`TestTerminalAuthAllows`, `TestCloud18LoginOnly`),
`server/api_login_cloud18_only_test.go` (`TestLoginCloud18OnlyRefusesLocalAccounts`), `server/api_global_settings_admin_test.go`
(`TestGlobalSettingsLocalAdminOnly` with the admin-required header, `TestImmutableGlobalSettingRefused`,
`TestClusterSettingToolsRefuseGlobalKeys`), `TestMCPAuthAllows`.
