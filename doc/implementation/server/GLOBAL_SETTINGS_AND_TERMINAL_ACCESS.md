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

## Recovery: the configuration file

With Keycloak or the realm down, whoever holds the host edits `/etc/replication-manager/config.toml`
(for example empties `cloud18-login-acr`, sets `mcp-server-auth`) and restarts. Those keys are
immutable: only the file changes them, never the API. The local admin login keeps working through
the API meanwhile.

## Tests

`config/terminal_auth_test.go` (`TestTerminalAuthAllows`), `server/api_global_settings_admin_test.go`
(`TestGlobalSettingsLocalAdminOnly` with the admin-required header, `TestImmutableGlobalSettingRefused`,
`TestClusterSettingToolsRefuseGlobalKeys`), `TestMCPAuthAllows`.
