# Config persistence: the save/load gate asymmetry

## Symptom

A per-cluster setting changed at runtime (via GUI/API) — e.g. a custom
`backup-mysqldump-options` with `--skip-ssl` — **works during the session but
reverts to the old value after a repman restart.** The value is genuinely on
disk: it is written to the datadir `<cluster>/<cluster>.toml` under
`[saved-<cluster>]`, committed, and pushed to the git config repo (confirmed by
reading the file on the repo's `master` branch). So the **save works**; the loss
happens on **restart**.

## Root cause

Two different gates decide "does monitoring-save-config apply", and they resolve
its default **differently**:

| Path | Gate | Value when the key is ABSENT from the config file |
|------|------|---------------------------------------------------|
| **Save** (`SaveCallBack` → `SaveConfigFile`) | `conf.ConfRewrite` — the `monitoring-save-config` **pflag**, default **true** (`server.go:388`) | **true** → writes `<cluster>/<cluster>.toml` |
| **Load at startup** (`InitConfig`) | `firstRead.GetBool("default.monitoring-save-config")` — reads the config **file** (`server.go:1777`) | **false** → skips the whole datadir-overlay read block |

So for a user who never explicitly sets `monitoring-save-config` in their config
(the common case — the flag default is `true`, so nobody needs to):

- **Saves happen** (`ConfRewrite` = flag default `true`) → the change reaches
  `[saved-<cluster>]` on disk and in git.
- **Startup skips loading** the datadir overlays (`GetBool` returns `false` for
  an absent key) → the `[saved-<cluster>]` section is never merged → the value
  silently reverts to the static/`[DEFAULT]` value on restart.

This also explains why a **runtime** reload (`ReconstructLiveClusterConfig`,
the API `settings/actions/reload`) preserves the value — that path is **not**
gated on `monitoring-save-config`; it always reads
`<cluster>/<cluster>.toml` when present. Only the full **process restart**
(`InitConfig`) loses it. This is the OSC build; `monitoring-save-config`
defaults to `true` there (shared `AddFlags`, no build-tag variance).

## Fix

`monitoringSaveConfigEnabled(firstRead, flagDefault)` resolves the gate to the
flag default (`conf.ConfRewrite`, true) when the key is **absent**, and only
uses the file value when it is **explicitly set**:

```go
func monitoringSaveConfigEnabled(firstRead *viper.Viper, flagDefault bool) bool {
	if firstRead.IsSet("default.monitoring-save-config") {
		return firstRead.GetBool("default.monitoring-save-config")
	}
	return flagDefault
}
```

`InitConfig` now uses it (`server.go:1777`), so the **load gate matches the save
gate**: if repman saved it, repman reads it back. Explicit `= false` in the
config still disables persistence; env and command-line overrides are unchanged.

## Observability

`Cluster.LastConfigSaveToDisk` (exposed in the API as `lastConfigSaveToDisk`,
groups `web`) is stamped whenever `SaveConfigFile` actually writes the datadir
`<cluster>.toml`. It lets an operator (and the regression tests) distinguish
"changed in memory only" from "written to the file that startup reads back" —
a passing timestamp with a value that still reverts pinpoints the load gate; a
timestamp that never advances pinpoints the save.

## Tests

- `server_reload_test.go`:
  - `TestReconstructLiveClusterConfig_PreservesSavedBackupOption` — a persisted
    `[saved-<cluster>]` backup option survives the reconstruct/reload path.
  - `TestMonitoringSaveConfigEnabled_DefaultsToFlagWhenAbsent` — the gate
    defaults to the flag value when absent, honors explicit true/false. This is
    the direct reproduction of the asymmetry.
- `regtest/test_config_persist_backup_option.go` — `testConfigPersistBackupOption`:
  against a live cluster, change `backup-mysqldump-options`, persist, and assert
  both that `LastConfigSaveToDisk` advanced and that the value reached the datadir
  `<cluster>.toml` (the save side, on real infrastructure).
