# Where a credential change is kept

A credential set from the GUI, the API or the CLI is written to a credential
store: the place replication-manager reads it back from. Today the store is
the configuration, and `monitoring-save-config` decides its scope:

| `monitoring-save-config` | Source shown | A change |
|---|---|---|
| `true` (dynamic mode) | `dynamic-config` | is saved to `<cluster>.toml` in the data directory and is read back at the next start |
| `false` (static mode) | `runtime-config` | lives in memory: it survives a tag change or a configuration reread, and is gone at the next restart; the configuration files stay the source |

In static mode the database and replication credentials take no change: losing
them at the next restart would lock replication-manager out of the databases.

A credential set in a configuration file replication-manager never writes
(`config.toml`, `cluster.d/*.toml`) is shown as `config-file` and takes no
change, in either mode: the file would win at the next restart. Change it in
the file. A credential nothing sets is shown as `default`.

The Database Credentials section of the cluster settings shows each
credential's source and, when a change is refused, the reason.

## What changed

Until now a credential set at runtime could be replaced by the value of the
configuration as soon as replication-manager reread it, for example when a
database or proxy tag was added or removed (#2017). A change is now written to
its store first, and every reread takes the value from the stores, so the last
value set is the one in use.

The same rule applies to the database, replication and proxy credentials, the
backup verification credential, the restic password, the mail and cloud18
credentials and the application credentials.

Credentials kept in Vault are unchanged by this release.
