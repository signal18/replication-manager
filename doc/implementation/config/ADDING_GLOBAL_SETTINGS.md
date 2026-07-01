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

### 3. Constant (if adding a log module)

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
- [ ] If log module: constant, tag, eligibility, index entries in `config/config.go`
- [ ] `GlobalSettings.jsx`: UI control with `setGlobalSetting` or `switchGlobalSetting`
- [ ] Verify the JSON key (`json:"..."` tag) matches the `config?.` access in JSX
