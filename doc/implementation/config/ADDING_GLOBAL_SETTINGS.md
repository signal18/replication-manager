# Adding a Global (Server-Scoped) Setting

How to add a new setting that applies at the server level rather than
per-cluster. Server-scoped settings appear in the Global Settings page
and use the `/api/clusters/settings/actions/` endpoints.

## Backend

### 1. Config struct field (`config/config.go`)

Add the `scope:"server"` struct tag so the setting is treated as
immutable per-cluster and only configurable at server level:

```go
MyNewSetting bool `scope:"server" mapstructure:"my-new-setting" toml:"my-new-setting" json:"myNewSetting"`
```

Without `scope:"server"` the setting is per-cluster and appears in
the cluster Settings page instead.

### 2. CLI flag (`server/server.go` — `AddFlags()`)

Register the flag so it can be set via CLI, environment variable, or
TOML config:

```go
flags.BoolVar(&conf.MyNewSetting, "my-new-setting", false, "Description of the setting")
```

### 3. Runtime apply case (`server/api_global_settings.go`)

Global settings are applied at runtime by `setRepmanSetting` (values)
and `switchRepmanSetting` (boolean toggles) in
`server/api_global_settings.go`. Add a `case "my-new-setting":` that
assigns the value to `repman.Conf` and updates any live subsystem
(e.g. the mail cases also call `repman.Mailer.Update*`). Both functions
end with `repman.ConfigManager.SaveConfig(repman, false)`, which
persists the change to `<datadir>/default.toml` under `[saved-default]`
(when `monitoring-save-config` is on) and reloads it in
`server.InitConfig()` at startup — no extra code needed for
persistence.

Case labels must match the `mapstructure` tag exactly — a stray
trailing space in the label (historical bug with
`"arbitration-external "`) makes the setting silently unreachable
("setting not found").

### 4. If the setting is a SECRET

The encrypted save/reload machinery is driven by hardcoded key lists;
a new secret must be registered in each:

1. `config/config.go` — add the key to the map literal in
   `DecryptSecretsFromConfig()` (the canonical secret list). This makes
   the value decrypt into `conf.Secrets[key]` at startup.
2. `server/server.go` — add a case in `GetEncryptedValueFromMemory()`
   so `SaveDynamic()`/`SaveImmutable()` write the value encrypted
   (`hash_` prefix) to the datadir TOML files.
3. `cluster/cluster.go` — same case in the cluster twin
   `GetEncryptedValueFromMemory()` if the key also exists per cluster.
4. In the `setRepmanSetting` case: the GUI sends secrets
   base64-encoded (`btoa`), so decode and update the `Secrets` map
   (pattern of `mail-smtp-password` / `arbitration-external-secret`):

```go
case "my-new-secret":
    val, err := base64.StdEncoding.DecodeString(value)
    if err != nil {
        return errors.New("unable to decode")
    }
    repman.Conf.MyNewSecret = string(val)
    var new_secret config.Secret
    new_secret.Value = repman.Conf.MyNewSecret
    new_secret.OldValue = repman.Conf.GetDecryptedValue("my-new-secret")
    repman.Conf.Secrets["my-new-secret"] = new_secret
```

5. Consumers must read the secret via
   `conf.GetDecryptedValue("my-new-secret")` (never the raw struct
   field, which may hold the `hash_...` ciphertext when loaded from an
   encrypted config file).
6. In the GUI use `<TextForm type='password' ...>` and wrap the value
   in `btoa()` before dispatch.

### 5. Constant (if adding a log module)

When adding a new log module, four places in `config/config.go` need
entries:

| Location | Example |
|----------|---------|
| `ConstLogMod*` int constant | `ConstLogModArbitration = 32` |
| `ConstLogName*` string constant | `ConstLogNameArbitration = "log-arbitration"` |
| `IsEligibleForPrinting()` switch | `case module == ConstLogModArbitration:` |
| `GetTagsForLog()` switch | `case ConstLogModArbitration: return "arbitration"` |
| `GetIndexFromModuleName()` switch | `case ConstLogNameArbitration: return ConstLogModArbitration` |

Log modules with a bool + level pair:

```go
LogArbitration      bool `mapstructure:"log-arbitration" toml:"log-arbitration" json:"logArbitration"`
LogArbitrationLevel int  `mapstructure:"log-level-arbitration" toml:"log-level-arbitration" json:"logArbitrationLevel"`
```

The bool enables the module; the level controls verbosity (0=off,
1=error, 2=warn, 3=info, 4=debug).

## Frontend

### Global settings page

File: `share/dashboard_react/src/Pages/ClustersGlobalSettings/GlobalSettings.jsx`

The component receives `config` from the global monitor state.
Use `setGlobalSetting` / `switchGlobalSetting` from `globalClustersSlice`:

```jsx
// Boolean toggle
{
  key: 'My New Setting',
  value: (
    <RMSwitch
      confirmTitle={'Confirm switch global settings for My New Setting?'}
      onChange={(_v, setRefresh) => dispatch(switchGlobalSetting({ setting: 'my-new-setting', setRefresh }))}
      isChecked={config?.myNewSetting}
    />
  )
}

// Log level slider
{
  key: 'Log My Module',
  value: (
    <LogSlider
      value={config?.logMyModuleLevel}
      confirmTitle={`Confirm change 'log-level-my-module' to: `}
      onChange={(val) => dispatch(setGlobalSetting({ setting: 'log-level-my-module', value: val }))}
    />
  )
}
```

### Per-cluster settings page (for comparison)

File: `share/dashboard_react/src/Pages/Settings/LogsSettings.jsx` (or other `*Settings.jsx`)

Uses `selectedCluster?.config` and `setSetting` / `switchSetting` from
`settingsSlice` — these hit per-cluster endpoints.

## API endpoints

| Scope | Action | Endpoint |
|-------|--------|----------|
| Global | Switch bool | `GET /api/clusters/settings/actions/switch/{setting}` |
| Global | Set value | `GET /api/clusters/settings/actions/set/{setting}/{value}` |
| Global | Clear | `GET /api/clusters/settings/actions/clear/{setting}` |
| Cluster | Switch bool | `GET /api/clusters/{name}/settings/actions/switch/{setting}` |
| Cluster | Set value | `GET /api/clusters/{name}/settings/actions/set/{setting}/{value}` |

## Checklist

- [ ] `config/config.go`: struct field with `scope:"server"`
- [ ] `server/server.go`: `AddFlags()` entry
- [ ] `server/api_global_settings.go`: `setRepmanSetting` and/or `switchRepmanSetting` case
- [ ] If secret: `DecryptSecretsFromConfig()` key, `GetEncryptedValueFromMemory()` case(s), base64 + `Secrets` map in the API case, `btoa()` + `type='password'` in the GUI
- [ ] If log module: constant, tag, eligibility, index entries in `config/config.go`
- [ ] `GlobalSettings.jsx` (or a dedicated card like `ArbitrationSettings.jsx`): UI control with `setGlobalSetting` or `switchGlobalSetting`
- [ ] Verify the JSON key (`json:"..."` tag) matches the `config?.` access in JSX
