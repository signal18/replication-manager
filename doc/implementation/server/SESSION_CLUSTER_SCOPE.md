# Cluster scope of local logins and of the API tokens they create

Code: `server/api_session_scope.go`, `server/api_token_owner.go`, `server/api_token.go`,
`server/api.go` (`loginHandler`, `IsValidClusterACL`), `cluster/cluster_acl.go`,
`cluster/cluster_add.go`.

## Why

One account name can hold a different password on each cluster: a cluster's own list
(`api-credentials-external`) is per cluster, and a service account shared by many clusters gets
one password per cluster. A local login used to accept a password if it was valid on *any*
cluster and then gave a session named after the account, instance-wide. The cluster ACL
re-verifies the session's password on each cluster, but every check that resolves the identity
by name only did not: direct grant checks (`requestACLUser`, `UserHasGlobalGrant`), the MCP
global check, the peer login as the instance, and above all API token creation, whose token then
held the account's grants on every cluster where the name exists.

## Sessions

`loginHandler`'s `tryLocalAuth` collects every cluster where the password is the account's
password. The session's `CustomUserInfo` then carries:

| claim      | value                                   |
|------------|-----------------------------------------|
| `scope`    | `clusters`                              |
| `clusters` | the clusters the password authenticated |
| `scope_at` | when the scope was decided (unix ms)    |

except when the account and password are an entry of the instance's list (`api-credentials` of
the server configuration, `isInstanceCredential`) **and** that password is the account's password
on every cluster where the account exists: that session carries no scope and covers every cluster,
as before. A cluster section that sets another password for an instance account makes its login
scoped to the clusters the password does open.

Autologin and the dashboard session (no login form) are scoped the same way
(`passwordSessionInfo`), and neither opens a session as the instance's Cloud18 identity.

An SSO password login whose GitLab profile gives no email takes the login name as its identity
when it is an email address (the realm just authenticated it, `ssoLoginEmail`); otherwise the
login is refused, and the background SSO upgrade of a local session fails (the local session,
scoped or not, stays as it is). An SSO login never yields a Local session named after the
account: that session would be instance-wide.

A session without `scope` is instance-wide. This is safe because the RSA key that signs sessions
is generated at each start: every session a running instance accepts was issued by this code, and
the issuers that set no scope are the instance-wide ones (SSO, the instance's list, the service,
autologin and dashboard logins).

Enforcement, all through `sessionClusters` / `sessionAllowsCluster`:

- `IsValidClusterACL`: refused on a cluster outside the scope, before any other check;
- `loginACLUser` (so `requestACLUser`, `requestACLUserOnCluster`, `UserHasGlobalGrant`);
- MCP: `Principal.SessionScoped` / `SessionClusters`, checked by `AuthorizeMCP`,
  `AuthorizeMCPGlobal` and the peer identity (`isAdminUserOn`);
- the peer login proxy acting as the instance (`isAdminUserOn`);
- the terminal: a cluster terminal needs the cluster in the scope; the host shell needs an
  instance-wide session;
- `GetUserInfoMap` exposes `SessionScope` / `SessionClusters` for handlers reading the claims map;
- the endpoints that checked the account name on the cluster go through `requestACLUser`
  (server, server attribute, proxy, app, replica by index);
- `whoami` lists the grants and roles of the scope's clusters only.

### Instance-wide actions

They need a credential covering the instance (`requestUnrestricted`: no session scope, and not
an API token created from a scoped login), whatever grant the account holds on the clusters of a
scoped login:

- the global settings (`isGlobalSettingsAdmin`, also when routed through a cluster's settings);
- the import of clusters from git, the global mail test, the peers' cluster list (empty);
- the global intervention start and end, the server active/standby switch;
- the rotation of an account of the instance's list (its own name included: the per-cluster
  password it logged in with is not that account's instance password).

### Global grants

A grant checked "on at least one cluster" (`UserHasGlobalGrant`: `global-admin-show`,
`cluster-create`, `prov-cluster`) is read on the scope's clusters only. A scoped login holding such
a grant on one of its clusters still exercises it with its global effect (an instance-wide view
behind `global-admin-show`, a cluster creation): that is the grant its cluster gives it. Give a
global grant only to an account whose every cluster password is trusted, or to an instance
account.

### Clusters created or deleted after the login

The scope is the list of clusters the password opened at login. A cluster created after the login
is not in it: log in again. A deleted cluster leaves the scope of every session issued before
its deletion (`markClusterDeleted`, `clusterDeletedSince` against `scope_at`), so a cluster
created again under the same name is not reached by those sessions; the record of deletions is
pruned past a session's lifetime (F4). The API tokens bound to a login on the deleted cluster
(`ownerClusters`) are revoked at the deletion (`revokeAPITokensBoundTo`).

## API tokens

The token record (and its HS256 claims) gains:

- `ownerScope`: `instance` (login covering the instance, SSO, or repman itself for app tokens) or
  `clusters`;
- `ownerClusters`: for `clusters`, the session's clusters.

A scoped caller lists, displays and revokes as owner only the tokens created from a login
restricted to clusters all within its own (`tokenVisibleToScope`): never an instance token, a
legacy token, or a token of another cluster's login, and never their strings. An unscoped caller
sees all its tokens.

`handlerMuxAPITokens` passes the session scope to `createAPITokenScoped`: the token-create grant
must be held on a cluster of the scope, a named cluster outside it is refused, and "every
cluster" (`*` or none) means every cluster of the scope, never `*`. At authentication,
`parseVerifiedAPIToken` returns the record bound to `ownerClusters`
(`APIToken.boundToOwnerClusters`), so a record whose `clusters` say more still acts on the
owner's clusters only, and is never global.

### Tokens issued before the field (no `ownerScope`)

Nothing records which password their login used. `apiTokenLegacyOwnerOK` keeps them working
(the owner's current grants on every cluster of their scope, as before) only when the login that
issued them necessarily covered all those clusters:

- an SSO owner (`ownerAuthType` SSO), or an owner with no local password on any cluster it
  exists on: releases of late September 2026 stored no `ownerAuthType`, and a passwordless
  account can only have logged in through SSO (`apiTokenOwnerIsSSO`);
- an app token repman issued itself (`createdFrom` "app ...");
- a local owner whose password is the same on every cluster it exists on. An account of the
  instance's list is one, unless a cluster section sets another password for it.

A local owner holding different passwords on different clusters is refused, logged once per
token in the security log (the set of logged ids is pruned with the token store at each save,
F4): the owner logs in with the cluster's credentials and creates a new
token. This is a behaviour change for such accounts, deliberately: their old tokens cannot be
told apart from a token created with one cluster's password.

The legacy rule is wider than "instance's list or SSO": it also keeps a local account with a
single password on all its clusters, since any login of it was valid on each of them. A password
rotated since the token was created is not visible to this check.

### Clusters added after a token

A token whose owner's login covered the instance (`ownerScope` `instance`: an SSO session, an
instance-account session, repman itself) with the `*` scope covers every cluster where its owner
exists, including clusters added after the token was created: nobody recreates a token when a
cluster is added. Only a token of a password-scoped session stays bound to the clusters of its
login. A cluster deletion revokes only the tokens bound to that cluster (`ownerClusters`), never
a `*` token.

### Owners removed or blocked

A removed owner loses its tokens (`userExists`, `GetACLUser`): the user lists decide. repman asks
no external service about an owner's account state, and the token lifetime is not capped for it:
the cause (a login covering clusters its password does not open, a local password for the Cloud18
identity) is what this change fixes. A ban is a removal from the lists or the password rotation,
which revokes the owner's tokens (`revokeUserAPITokens`).

## The instance's Cloud18 identity

`cloud18-gitlab-user` signs in with Cloud18 only. It used to be added to every cluster's list
with a generated password (`addUser`), which made a local password login under that name
possible with any cluster's copy. Now:

- `addUser` stores it without password, whatever is given;
- `LoadAPIUsers` drops a password stored for it (in memory), and `addUser` / `DropUser` strip
  it from the stored list at their next rewrite (`stripCloud18IdentityPassword`, through the
  credential chain);
- `isValidACL` and `GetAPIUser` refuse a local password for it (`IsCloud18Identity`), and
  `loginHandler` never tries a local login under that name.
