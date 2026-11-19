# Cluster secrets: the read endpoint

`GET /api/clusters/{clusterName}/secrets` (`server/api_cluster_secrets.go`,
`cluster/cluster_secrets.go`) returns the credentials replication-manager generated or holds for
the cluster, in clear, to their owner. It is the one place a self-service user reads the
credentials of a cluster provisioned for them.

## Rule: a pure read

The handler and `GetClusterSecrets` change nothing.

- They do not rotate, generate, save, encrypt or set a credential. A client may use one of these
  passwords in its own code, and a rotation it did not ask for breaks its production.
- A value that cannot be read (not set, does not decrypt) is left out. It is never replaced or
  regenerated.
- `TestClusterSecretsArePureRead` parses the two files and fails on any call whose name rotates,
  saves, generates, provisions, encrypts, applies, writes, updates, sets, adds, drops, deletes,
  revokes, mails or switches. The response header `Set` is the one exception.

## Contents

- `clusterSecretKeys` in `cluster_secrets.go` lists them. A proxy's key is listed only when that
  proxy is on. "user:password" settings are split at the first `:`.
- Then the database user of each app (`AppDbUser`, `AppDbPass`), read under the app's lock.
- The instance's own secrets are never in the list: API users, git and registry tokens, Vault,
  OpenSVC, SSH, mail and alert tokens, cloud keys. `TestClusterSecretKeysExcludeInstanceSecrets`
  checks it.
- `instanceAdmin` is the `admin` entry of `api-credentials`, a server secret. It is returned only
  when the caller passes `holdsServerCredentials`, the rule that already guards a change of a
  server secret: `cluster-server-credentials` on every cluster.

## Access

- The grant is `cluster-show-secrets`, in the cluster family. Its holders:
  - the holders of `cluster-rotate-passwords`: sysops, extsysops, and the Cloud18 owner through
    `CreateAdminUserForm`'s `cluster` family;
  - the self-service creator of a cluster, on that cluster only. `selfServiceSponsorGrants` gives
    it the `cluster-show` family, so the buyer reads the credentials of the cluster they
    provisioned: that is the onboarding need.

  dbops, extdbops and a sponsor given the role's defaults do not get it
  (`TestShowSecretsGrantHolders`, `TestClusterAddAuthorize`).
- ACL rule `/secrets` in `clusterACLRules`. No shorter pattern matches the URL.
- A Cloud18 realm token reaches the handler as the SSO session of its person
  (`cloud18TokenSession`). That is how the member area of the Signal18 website reads the
  secrets with the member's own sign-in.
- Off-switch: `api-cluster-secrets` (server scope, default true). When it is off, the handler
  answers 403 and the GUI row is hidden.

## Audit

- Every answer is a security event `cluster_secrets_read`, giving the caller, the cluster and
  the keys returned.
- A refusal is a security event `cluster_secrets_refused`.
- The values are never logged (F10). The answer carries `Cache-Control: no-store`.

## GUI

- `Pages/Settings/ClusterSecrets.jsx` is the "Secrets" row of the cluster's Credentials
  settings.
- Reveal calls the endpoint and Hide drops the answer.
- The values live in the component's state only: never the redux store, never browser storage.
