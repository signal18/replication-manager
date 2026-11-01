# Preserved/delta variables: reject empty or malformed names (#2278)

## The failure

repman keeps "preserved" and "delta" MariaDB variables and writes them to the server's
`01_preserved.cnf` / `02_delta.cnf`. Each line is built as `"loose_" + <name>`. If a
variable's name is **empty** — e.g. a `loose_=true` line read back from an existing
config, whose `loose_` prefix `NormalizeConfigVarName` strips to nothing — the writer
emits a bare:

```
loose_=true
```

The MariaDB entrypoint validates the config before starting the server
(`mariadbd --verbose --help`). It rejects `--loose-=true` as a **fatal ambiguous option**
("variable prefix ... is not unique"), so **mariadbd never starts** and writes no server
error log; the instance only shows as "start failed", which is hard to diagnose because
the fatal line is in the entrypoint's config-check output, not the server log.

A `loose_`-prefixed **unknown** option is ignored (a warning); a `loose_` with an **empty
name** is *ambiguous*, which is fatal — that is the distinction.

In the field this was amplified by `prov-db-compliance-auto-agree` auto-preserving the
malformed entry, so it regenerated on every provision (same auto-agree family as #1834).

## The guard

`config.IsValidConfigVarName(name)` accepts a name only when, after stripping a
`loose_`/`loose-` prefix, it is non-empty and made of `[A-Za-z0-9_.-]` (dot covers
plugin-qualified options).

- **Root cause** — `VariablesMap.loadFromSection` (config package) skips an invalid name
  so the junk never enters the map, the delta/dropped-vars state or the UI, and does not
  re-warn on every provision.
- **Defence in depth** — `WriteDeltaVariables` and `WritePreservedVariables`
  (`cluster/srv_cnf.go`) also skip an invalid key (WARN) before building a line, and the
  `dropped_variables.json` persistence skips it too, so a bare `loose_=` can never be
  written even if an invalid entry reaches the map by another path.

Valid variable names are unaffected. Tests: `config.TestIsValidConfigVarName`,
`config.TestLoadFromConfigFileDropsEmptyName`, `cluster.TestWritePreservedVariablesSkipsEmptyKey`.
