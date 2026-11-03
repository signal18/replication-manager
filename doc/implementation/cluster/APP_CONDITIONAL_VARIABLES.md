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

update-opensvc-config updates the service definition right after the push (below), so the
removed value's scoped keyword goes with its key. Where the definition is not updated (a
failed update), the node's scoped keyword still maps the deleted key; if the container
restarts then, the agent ignores a mapping to a missing key (om3: "ignore env from ... has
no key matching"; v2: "ignored cfg environment mapping ... does not exist"; both tested) and
the node gets the default value.

## On OpenSVC v2

The keys and the scoped keywords are the same on v2 (tested on agent 2.1-2073). v2 cannot
replace a service definition in place (update-opensvc-template is v3 only), so
update-opensvc-config sets the keywords on the live object instead
(`openSVCUpdateAppEnvironmentV2`):

- It renders `container#app` as the template does and sends one `set` of
  `configs_environment`, `secrets_environment` and their `@<agent>` keywords, then one
  `unset` of the `@<node>` keywords of every cluster node without a value
  (`appEnvironmentKeywordsV2`). Unsetting a keyword the object does not have succeeds.
- Both go to `/service_action` with o-node `ANY`: one node holding the object runs it and
  the agent copies the definition to the other nodes (seen on the second node in under 1 s).
- An app whose object does not exist (`ObjectExistsV2`) only gets its maps, as on v3.
- `SetServiceConfigKeysCheckedV2` / `UnsetServiceConfigKeysCheckedV2` read the agent's
  answer (`checkServiceActionV2Body`). Answers that applied nothing still carry status 0 on
  2.1-2073: `{"nodes": {}}` (the o-node holds no such object) and
  `{"info": "empty node selection"}` (unknown o-node); both are errors, as are a top-level
  error or status and a node whose `status` or `data.ret` is not 0. The agent writes an
  unknown keyword with only a warning in `data.err`, so keyword names are not checked there.
- Tested through the API on two agents: a stale `@<node>` keyword removed; a value added
  for an agent that had none reaches its container after a restart of that instance, without
  a reprovisioning; a removed value leaves the default; an app never provisioned gets its
  maps only.

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
   removes stale ones, then updates the service definition of a provisioned app without
   restarting it: on OpenSVC v3 the whole definition (`OpenSVCUpdateAppTemplate`, as
   update-opensvc-template does), on v2 its environment keywords (above). An app whose
   service does not exist yet only gets its maps (the
   provisioning writes the definition); a refresh failure is returned as an error after the
   maps were pushed.
2. A recreate of the container applies the values: `POST .../actions/restart` (which also
   refreshes the definition first), or any restart of the instance (`om ... restart`, a
   crash). The container (`rm=true`) is recreated with the new environment. A stop/start of
   the existing container keeps its old environment.

## Regtests (OpenSVC v3)

- `testAppConditionalVariablesOpenSVC`: one flex app on two agents, an env and a secret
  variable with a value per agent; checks the cfg/sec keys, the scoped keywords and each
  node's container environment, then removes one agent's values and applies them as above.
- `testAppConditionalProxiesOpenSVC`, the issue's "Observed" case: a flex app on two agents
  reaches its database through `{{proxies.0.host}}` by default and, by a per-agent value of
  `DB_HOST`, through the proxy placed on its own agent. The app answers each HTTP request with
  one query through its proxy: 200 with the client address the primary saw (the proxy the
  connection came through), 503 when the query fails. Checked: each instance answers through
  its own proxy; with one agent's app instance and proxy stopped, the other instance answers
  200 through its proxy for 30 s, both ways; with one proxy only stopped, the instance of that
  agent answers 503 (no fallback proxy) while the other holds, and it answers through its
  proxy again once the proxy is started.
  - Needs two proxies placed on two app agents (`prov-proxy-agents`), else it SKIPs.
  - The checks call each instance's address, not the app name: the orchestrator lists a
    restarted instance in the app name only about 45 s after it is up.
  - The app has a fixed name, `rtcondprx`, with `app-db-auto-create` and `app-db-owned`: its
    schema and user, which repman never drops, are reused by the next run.
  - `{{proxies.0.host}}` is resolved before the test takes the app lock: the substitution
    view takes that lock itself.
