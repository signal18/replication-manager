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
(`calibrate-iops`, `calibrate-smt-gain`). Cluster settings are not concerned: they keep their grants.

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
(`config.TerminalAuthAllows`, security log `terminal_denied`). The global terminal (the shell on the
repman host) is for the local `admin` account only: the `cloud18-gitlab-user` exception is removed.
`terminal-session-enabled` stays the off switch.

## Tests

`config/terminal_auth_test.go` (`TestTerminalAuthAllows`), `server/api_global_settings_admin_test.go`
(`TestGlobalSettingsLocalAdminOnly`, `TestImmutableGlobalSettingRefused`).
