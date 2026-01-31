# Server: Env Config and Runtime Changes

## Scope
This document covers server-side changes made to configuration loading, environment overrides, cluster discovery, runtime defaults, and mailer behavior in this branch.

## Environment Configuration and Precedence
- Env prefixes are standardized to `REPLICATION_MANAGER_DEFAULT_*` for the default scope and `REPLICATION_MANAGER_<CLUSTER>_*` for cluster scopes.
- Unprefixed env variables are not used for overrides.
- Precedence is enforced as: config file < env vars < CLI flags.
- Env/CLI overrides are applied both to the live config object and to immutable flag maps to ensure secret derivation uses the override values.

## Cluster Discovery from Env
- Cluster discovery now scans env vars prefixed with `REPLICATION_MANAGER_<SCOPE>_` and matches known config keys to derive the cluster scope.
- The discovered scope name becomes the cluster name (lowercased).
- Display titles remain optional; discovery is not tied to a `title` value.

## Immutable Map Propagation for Secrets
- Env and CLI overrides are propagated into immutable config maps used by `DecryptSecretsFromConfig`.
- This ensures credentials like `db-servers-credential` set via env are used for actual database connections.

## Non-Root Working Directory Fallback
- When no explicit `monitoring-datadir` is set and the process runs as non-root, the working directory defaults to:
  - `$HOME/.local/replication-manager/data`
- The cluster config path is derived from this working directory, and the directory is created if missing.

## Mailer Initialization Behavior
- `mail-to` is treated as the opt-in signal for init email.
- When `mail-to` is empty, init mail is skipped with an INFO log.
- When `mail-to` is set but required SMTP fields are missing, init mail is skipped with an ERROR log.
- `Run()` no longer initializes the mailer unless `mail-to` is set.

## Cloud18 Config Guard
- `ReadCloud18Config` now guards against nil viper input and missing files to avoid nil dereferences.

## Branch Changes (Prose Summary)
This branch standardizes env overrides, enforces precedence, and improves env-only behavior by ensuring secrets consume env/CLI overrides, enabling cluster discovery from env scopes, and setting a safe non-root working directory. It also tightens mailer init behavior and hardens Cloud18 config handling. Tests were added/updated to validate precedence, env discovery, and runtime fallbacks.
