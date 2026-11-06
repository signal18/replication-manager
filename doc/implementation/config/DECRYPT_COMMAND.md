# The `decrypt` command

`replication-manager decrypt` shows an operator the configuration's secrets in clear: database,
replication, proxy, backup, API and Cloud18 credentials, which the configuration stores encrypted
(`hash_`, the instance key `monitoring-key-path`).

## What it prints

1. **The files the configuration load read**, in the order it read them: the main config file, as
   the daemon resolves it (`monitoring-config-file`, the embed and tarball lookups), every `.toml` of
   the include directory, `default.toml`, each cluster's saved file
   (`<working dir>/<cluster>/<cluster>.toml`) and `.pull/cloud18.toml`. `InitConfig` records each
   file it reads in `ReplicationManager.ConfigPathList`, so the command never keeps its own copy of
   the lookup. Each file is printed as written, every `hash_` value in clear, alone or inside a list
   (`user:hash_…,user:hash_…`, through `GetDecryptedEmbedded`).
2. **Each cluster's effective encrypted values** (`RevealEffective`): what the cluster runs with,
   environment variables and command-line flags included, by config key name.

The output goes to the command's standard output only. Nothing is written to disk.

## Output safety

The files are written by the daemon's own account (`default.toml` and the saved cluster files),
which may be an unprivileged user when systemd starts repman as root with `--user=repman`. The
command prints them as root, so their content must never drive the operator's terminal:
- every control character but the tab is escaped (`\xNN`, `revealSafe`);
- a token that does not decrypt to printable text (another key, a corrupted value) is shown as
  `<not decryptable with this key>`, never as bytes (`revealLine`). It is a heuristic: AES-CFB has
  no integrity check, so a wrong key decrypts to arbitrary bytes without error, and a short value
  can come out printable by chance. Tabs and UTF-8 text are kept.

## Who may run it

- **Root**, on a deployed system: the key is in `/etc/replication-manager`.
- **The user it runs as**, with the self-contained binary (embed build): its key is in that user's
  `~/.config/replication-manager`.

Anyone else is refused before the configuration is loaded (loading creates directories). The
command loads the configuration read-only (`ReplicationManager.ReadOnlyCommand`): `InitConfig`
then creates no key, no directory (`initFS`, the working directory) and removes no legacy file
(`removeLegacyP12Pem`). It only looks for the key where the daemon does
(`FindExistingKey`: `monitoring-key-path`, then `<confdir-extra>` or `~/.config/replication-manager`). Loading the configuration otherwise creates one
when none is found where the user looks, and every value would then "decrypt" with the wrong key.
A missing or unreadable key stops the command with the reason.

## Start-up clean-up

At every start, and when the command runs, `config.RemoveRevealFiles` removes old output files of
the command (`*.reveal`, in its old output format only) from `config.RevealOutputDir`: never an
empty, unreadable or other file, never a symlink's target. Each removal is a security log line
naming the file, never its content. It is a security clean-up, with no off-switch (T14).

## Tests

`config/reveal_test.go`: the file printed with its secrets in clear and nothing written; the
effective values; control characters escaped and undecryptable tokens marked; only the old output
files removed.
