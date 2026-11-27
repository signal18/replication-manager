# On-premise SSH settings: settings API and GUI

Issue #2452. The on-premise orchestrator (and the proxmox one, which runs its host scripts in
the guests over the same SSH) reaches the database and proxy hosts over SSH. Its settings could
only be set in a cluster file. They now go through the per-cluster settings API and the GUI,
like every other cluster setting (T6, T16).

## Settings and how the API takes them

All go through the existing endpoints, no new route:
`/api/clusters/{cluster}/settings/actions/set/{setting}/{value}` and
`/api/clusters/{cluster}/settings/actions/switch/{setting}`. The ACL is the existing
`/settings/actions/set` and `/settings/actions/switch` rules (cluster settings grant).

| Setting | Value sent | Setter | Check |
| --- | --- | --- | --- |
| `onpremise-ssh` | switch, or `true`/`false` on set | `switchClusterSettings` / `applyIsActive` | |
| `onpremise-ssh-port` | in clear | `SetOnPremiseSSHSetting` | 1 to 65535 |
| `onpremise-ssh-credential` | base64 of `user:password` | `SetOnPremiseSSHCredential` | a user; the password may be empty (key authentication) |
| `onpremise-ssh-private-key` | base64 of a path or of the key | `SetOnPremiseSSHPrivateKey` | see below |
| `onpremise-ssh-start-db-script`, `-start-proxy-script`, `-stop-proxy-script`, `-db-job-script`, `-upgrade-db-script` | base64 of a path | `SetOnPremiseSSHSetting` | absolute path of an existing file; empty: the built-in command |

The setters are in `cluster/cluster_set_onpremise.go`, the API cases in
`server/api_cluster.go` `setClusterSetting`, next to the proxmox ones.

## The credential

`onpremise-ssh-credential` was already a secret (`secret:"true"`, its json name the mechanical
camelCase `onpremiseSshCredential`, so `MaskSecretsJSON` masks it in every API read, and
`GetEncryptedValueFromMemory` encrypts it with the instance key on save, F9). The setter goes
through the credential chain like the other credentials (`CredentialChain().Set`, then
`MarkSecretVersionStoreDirty`): a version in every active store, refused (409) when no store
keeps versions. It is added to `credentialSettings` with `passwordOptional`, so a value that is
not `user:password` is refused with 400 before any change, while `user:` (key authentication,
the flag's default `root:`) is accepted. `GetApiChangeLogFormat` logs it as `****` (F10).

## The private key: a path, or the key received as content

The setting is, and stays, the path of the key on the replication-manager host: every reader
(`OnPremiseGetSSHKey`, the SSH dial of databases and proxies, the snapshot backends through the
on-premise orchestrator, the `WARN0169` check) opens a file. Keeping a path means no reader
changes and existing configurations keep working.

So that an operator without shell access to the replication-manager host can still give a key,
the API also takes the key itself: a value beginning with `-----BEGIN` is parsed
(`ssh.ParseRawPrivateKey`; a key with a passphrase is refused, nothing could unlock it), written
with `misc.WriteFileDurable` to `<cluster working dir>/.ssh/onpremise_ssh_key`, mode 0600
(forced again after the write, an existing file keeps its mode otherwise), and the setting set to
that path. The key content is never in the config, in the config git (which stages
`<cluster>/*.toml` and named json files only, and commits only tracked files), in the API JSON
or in a log line; parse errors never quote it.

Any other value must be the absolute path of an existing file, parsed the same way (a file that
is not a key, or a key with a passphrase, is refused at once instead of failing at the next SSH
dial); empty goes back to the default `~/.ssh/id_rsa` of the replication-manager user. Each change
re-arms the key check (`HaveSSHKeyChecked = false`). When the setting leaves the file written from
key content (cleared, or another path), that file is removed: no key stays on disk that no
setting names. Before a new key overwrites an existing file, the file is chmod 0600 first, so the
key is never readable by another user even for a moment; the `.ssh` directory is created 0700.

## Trust model of the script settings

The five scripts are files of the replication-manager host, read and run over SSH on the database
and proxy hosts. Whoever holds the cluster settings grant can point them at any file the
replication-manager user can read and have it run on those hosts: the same power that grant
already gives through the other script settings (`prov-db-host-upgrade-script`, the backup and
alert scripts, which run on the replication-manager host itself) and that the cluster file gave.
The API adds no new power, so the scripts are not restricted to a directory; the check is an
absolute path of an existing regular file (`os.Stat` follows a symlink to its target).

Not covered: the key file lives on the instance that received it. A peer that takes the cluster
over from the config git gets the path, not the file: give the key to each instance, or use a
path present on every host. Carrying the key itself through the encrypted secret stores would
need a separate secret setting; left for later.

## /etc wins (T16)

`immutableClusterSetting` refuses (409, `errImmutableGlobalSetting`) a set or a switch of an
on-premise SSH setting that the immutable configuration sets (the file under /etc, the command
line, the environment). The general cluster-setting path only warns and overwrites until the
next restart; these settings are refused instead, as T16 says. The credential is not in that
list: it follows the credential chain, which allows a rotation of a value pinned in /etc when a
versioned store holds the new one (T16's credential exception).

## GUI

`share/dashboard_react/src/Pages/Configs/components/OrchestratorDbVM.jsx`, the "Orchestrator VM"
block of the configurator, shown for the onpremise and proxmox orchestrators: the switch, the
port, the credential (`SetCredentialsModal`, write-only, the password optional for this type),
the key (`SSHPrivateKeyModal`, write-only: a path or a pasted key; the current path is shown,
never a key) and the scripts. The proxy scripts sit in the Proxies tab, the others in the
Databases tab. A setting pinned in /etc shows the lock of `clusterSetting` rows.

## Tests

- `cluster/cluster_set_onpremise_test.go`: the credential masked in the API JSON, saved
  encrypted and read back; the key content written 0600, the setting holding the path, the key in
  neither the JSON nor the saved file, the refusals (passphrase, not a key, relative path,
  missing file); port and scripts.
- `server/api_onpremise_ssh_settings_test.go`: the API cases, the 400 on a malformed credential,
  the change log masked, the 409 on a setting pinned in the immutable configuration.
