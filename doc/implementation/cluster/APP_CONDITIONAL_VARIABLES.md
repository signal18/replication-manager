# App per-agent variable values on OpenSVC (#2282)

An app deployment variable can carry per-agent values (`Conditional`, a list of
`{agent, value}`), set in the GUI (Deployment → Variables) or through
`deployment/variables/index/{i}/conditional/modify`. This note describes how those values
reach the app container on OpenSVC.

## Keys

`OpenSVCCreateAppVariableMaps` (`cluster/prov_opensvc_app.go`) writes, in the app's cfg
object (env variables) or sec object (secret variables):

- `<NAME>`: the default value
- `<NAME>@<agent>`: the value for that agent, one key per entry of `Conditional`

## Scoped keywords

The app container imports the whole object (`configs_environment = <app>/*`,
`secrets_environment = <app>/*`). OpenSVC applies `@<node>` scoping to service keywords,
not to the keys of a cfg/sec object. A `<NAME>@<agent>` key therefore reaches the container
only as a variable literally named `<NAME>@<agent>`.

`OpenSVCGetAppContainerSection` adds, for each agent that has at least one per-agent value,
a keyword scoped to that agent on `container#app`:

```
configs_environment@<agent> = <default configs_environment> <NAME>=<app>/<NAME>@<agent> ...
secrets_environment@<agent> = <default secrets_environment> <NAME>=<app>/<NAME>@<agent> ...
```

- A scoped keyword replaces the default keyword on that node, so it repeats the default
  entries (including `env/MYSQL_ROOT_PASSWORD` for `prov-app-db-credentials` apps) before
  the mappings.
- A side (cfg or sec) gets a scoped keyword only if one of its variables has a value for
  that agent.
- Names or agents that cannot appear in a space-separated keyword (empty, whitespace, `=`,
  `@`, and `/` for an agent) are skipped.
- `environment@<agent>` is not used: it would put secret values in clear in the service
  configuration, and a cfg import of the same name wins over it.
- Only `container#app` is scoped. The git-clone and S3 init containers and the jobs sidecar
  keep the default import.

## Removing a per-agent value

The API replaces the variable's `Conditional` list, so a removed agent leaves the repman
configuration. On the next map push, `OpenSVCCreateAppVariableMaps` lists the cfg and sec
keys (`ListConfigKeys`, `ListSecretKeys`) and deletes each `<NAME>@<agent>` key whose
`<NAME>` is a variable of that type and whose agent is no longer in its `Conditional`
(`staleConditionalKeys`). A key that is itself a variable name is never deleted. A listing
failure or a delete failure is a warning and leaves the key in place: the key is inert once
no keyword maps it, so it never fails the push nor a provisioning.

On OpenSVC v3 update-opensvc-config refreshes the service definition right after the push
(below), so the removed value's scoped keyword goes with its key. Where the definition is not
refreshed (v2, or a failed refresh), the node's scoped keyword still maps the deleted key; if
the container restarts then, the agent ignores a mapping to a missing key (om3: "ignore env
from ... has no key matching"; tested) and the node gets the default value.

## On OpenSVC v2

The keys and the scoped keywords are the same on v2 (tested on agent 2.1-2073). The service
definition cannot be refreshed in place on v2 (update-opensvc-template is v3 only), so:

- Removing a per-agent value: the map push deletes its key; the node's scoped keyword still
  maps it, the agent ignores a mapping to a missing key ("ignored cfg environment mapping ...
  does not exist") and the node gets the default value after its restart.
- Adding a value for an agent that had none needs the new scoped keyword, which on v2 only a
  reprovisioning writes (not tested).

## After an upgrade

The scoped keywords are part of the rendered template, so an app that already had per-agent
values renders a template that differs from the one it was provisioned with, and its
template hash differs from the provisioned one ("needs reprov"). The values are applied by
update-opensvc-config and a recreate of the container (below). The provisioned hash is read
from the template file that only a provisioning rewrites, so applying the values does not
clear that flag.

The template refresh PUTs the whole service configuration, so a scoped keyword that is no
longer generated disappears with it.

## Applying a change

As #2282 states it: update-opensvc-config plus a recreate of the container.

1. `POST /api/clusters/{c}/apps/{app}/actions/update-opensvc-config`
   (`OpenSVCUpdateAppConfig`; GUI: app menu, Push Config/Secret Maps) pushes the keys,
   removes stale ones, then, on OpenSVC v3, refreshes the service definition of a
   provisioned app (`OpenSVCUpdateAppTemplate`, as update-opensvc-template does) without
   restarting it. An app whose service does not exist yet only gets its maps (the
   provisioning writes the definition); a refresh failure is returned as an error after the
   maps were pushed.
2. A recreate of the container applies the values: `POST .../actions/restart` (which also
   refreshes the definition first), or any restart of the instance (`om ... restart`, a
   crash). The container (`rm=true`) is recreated with the new environment. A stop/start of
   the existing container keeps its old environment.
