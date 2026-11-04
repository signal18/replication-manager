# license-pull: load a -pull package from a file (#2309)

A replication-manager without internet access cannot pull its `-pull` repo. `license-pull`
loads, from a package file in the datadir, what that pull would bring.

## The package

A `tar.gz` with a `manifest.json` (`cluster/logplugin.PackageManifest`) listing each file, its
destination and its kind:

| Kind | Placed under | Read by |
|---|---|---|
| `plugin` | `<datadir>/.pull/plugins/` | `syncPluginsFromPull` |
| `plugin-sig` | `<ShareDir>/plugins/` | the signature check of `syncPluginsFromPull` |
| `config` | `<datadir>/.pull/` (`cloud18.toml`, `<cluster>/inject.toml`) | `CheckCloud18Config`, `CheckInjectConfig` |
| `pubkey` | `<datadir>/.pull/` | `plugin-signing-public-key`, when a setting points to it |

The manifest also carries `wire`, the log plugin protocol the plugins were built for
(`wire.WireVersion`, the `wire-v<N>` of the plugin publishing), and `platform` (`GOOS/GOARCH`).
`license-pull` refuses a package whose wire or platform is not this replication-manager's, before
anything is placed (GWARN023 names both values).

Any other kind is skipped. A destination that is absolute or goes out of its base (`..`) refuses
the package before anything is written (`safeJoin`). Each file is bounded to 256 MB (F3).

## The steps: one function for the pull and license-pull

`applyPullDir(pullDir, withCloud18Config)` (`server/server_git.go`) holds what used to follow the
clone in `PullCloud18Configs`: `cloud18.toml`, the peer, partner and marketplace files when
`cloud18` is on, the plugins and their data. The git pull calls it with `false`: `cloud18.toml` is
applied only when `cloud18` is on, as before. `license-pull` calls it with `true`: an air-gapped
replication-manager has `cloud18` off, and its `cloud18.toml` is applied all the same. A
cluster's `inject.toml` needs no call: `CheckInjectConfig` reads it at the next cluster save, as
after a pull.

## Decisions

- **Same code as a pull.** `license-pull` only places the files; verifying and loading the plugins
  stay `syncPluginsFromPull`'s job, so a package and a pull can never be checked differently.
- **Refused on a replication-manager that pulls** (`git-url-pull` set): it gets the package through
  its repo, and two writers of `.pull/` would race the git manager.
- **Path inside the datadir.** A relative path is read under the datadir; the resolved file
  (symlinks followed) must be in it.
- **Checksum.** The checksum of the last package loaded (`ComputeFileChecksum`, the one
  `cloud18.toml` uses) is kept in memory: the same package again answers `unchanged` and changes
  nothing. A refused package keeps no checksum, so the next call loads again.
- **Loaded or refused per plugin.** A plugin is loaded when the pull copied it to the shared plugin
  dir, which follows its signature check; the answer lists both.
- **State, not log (F7).** A refused package, or a plugin of it refused by the signature check,
  opens GWARN023 (global, WARNING) until the next `license-pull`. `ProduceLicensePullState` sets it
  each tick from the kept refusal; it takes the lock with `TryLock` and never waits for a
  `license-pull` in progress (F2).
- **One license-pull at a time** (`licensePullMu`).

## API, ACL, GUI

- `POST /api/clusters/settings/actions/license-pull`, body `{"path": "..."}`, answer
  `LicensePullResult` (`package`, `checksum`, `unchanged`, `loaded`, `refused`); 400 with the reason
  when refused.
- ACL: `GrantGlobalSettings` / `GrantGlobalAdminConf`, as `cloud18-identity`.
- GUI: Global settings, Cloud18, row **Licence package** under Connect (path field + Load), shown
  when `git-url-pull` is empty.

## Tests

`server/server_license_pull_test.go` `TestLicensePull`: loaded, unchanged, forged plugin refused
and kept, unsafe destination, package outside the datadir, `git-url-pull` set.
