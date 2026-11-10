# Cluster roles and ACL (Cloud18)

Status: **design draft (2026-11-03, decisions 2026-11-05), not implemented.** The "Today" sections describe the
code as it stands; the rest is the target.

## The principle: repman rules, the others follow

Who holds which role on a cluster is decided in repman, on the cluster, and nowhere else. The
back office (BO), the CRM, the member zone of the website and Grafana never decide a role: they
read what repman says, through the BO.

## The cluster key: `<uri>.<cluster>`

A repman is identified by its registration URI, `domain.subdomain.zone` (`cloud18-domain`,
`cloud18-sub-domain`, `cloud18-sub-domain-zone`, see the registration page of the user doc). Two
repman can each have a cluster with the same name, so a cluster is identified everywhere outside
repman by **`<domain>.<subdomain>.<zone>.<cluster>`**. A user who holds roles on several clusters
sees the list of these keys.

## The roles

The role names are those of `config/grants.go`; what each role may do is in
`cluster/cluster_acl_rules.go`.

| Role | Held by | How many per cluster |
|---|---|---|
| `sysops`, `dbops` | the provider's people (the repman's own team) | several |
| `extsysops`, `extdbops` | delegated technical roles, see the lifecycle | several |
| `sponsor` | the client who took the cluster | several |
| payer | the one who is invoiced | **exactly one** |
| `visitor` | read-only | several |

`pending-*`, `quote-*` and `unsubscribed-*` are the states of the external roles during a sale
(see `doc/implementation/peer/MARKETPLACE.md`).

The holders of `extsysops` or `extdbops` see the cluster's graphs, its vault and its other
technical parts.

### The payer

The payer is a single attribution: one holder per cluster at any time. By default it is the
sponsor. The current payer hands it over to another member of the cluster. **Override:** the
provider's `sysops` on the cluster can always take it back, and if its holder disappears (account
deleted, leaves the cluster) it returns to the provisioning sysops, so the payer is never empty.
Every move of the payer is logged (who, from whom, to whom, when).

**Today:** there is no payer role. Billing (`cluster/cluster_billing_feed.go`,
`BillingIdentity`) sends the identities holding the `sponsor` role, and the separate payer was
handled outside repman. **Target:** the payer is a role on the cluster in repman, like the others.
Its name follows the existing role names (T19), to choose when it is built.

## Lifecycle of a cluster for sale

| Step | `extsysops`, `extdbops` | payer |
|---|---|---|
| 1. Provisioning of a cluster for sale | on the sysops who provisions it | the sysops who provisions it |
| 2. A client takes the cluster | given to the sponsor too (they stay on the provisioning sysops) | moves to the sponsor |
| 3. Later | the sponsor gives and removes them for anyone, except the repman's registration account, which always keeps them | the current payer hands it to one other member; the provider's sysops can take it back |

## How the roles reach the other tools

1. **Repman** holds the roles of each cluster (its users and their roles, as today:
   `ApiCredentialsAclAllow`, `user:pass:cluster:roles`).
2. **The BO** reads them from what it already harvests, at its own pace, and builds the list
   `<uri>.<cluster>` × user × role.
3. **The BO syncs the Cloud18 identity provider** (Keycloak, realm `cloud18`): one group per
   cluster and role, `/clusters/<uri>.<cluster>/<role>`, members added and removed by the BO.
4. **Each tool reads the token's `groups` claim at sign-in** and shows the clusters it lists. No
   tool calls the BO or the CRM to know a user's rights.

The Signal18 team (admin groups of the identity provider) sees every cluster in every tool.

Decided 2026-11-05 (details in the BO's design): a role change takes effect at the BO's pace; a
repman the BO cannot read keeps its last known roles, emptied after 7 days without a successful read;
each run compares and fixes the differences; the BO accepts from each repman only roles on its own
clusters (groups under its own `<uri>`), anything else is refused and logged.

The technical accounts a repman is registered with (its `cloud18-gitlab-user`) are the repman's
own sysops; they get no access to the tools through these groups.

## In each tool

| Tool | How |
|---|---|
| Grafana | one space (organization) per `<uri>.<cluster>`, created and filled by the BO; a user on several clusters switches between them. The open-source Grafana cannot hide one cluster's data from another user inside one organization, hence one per cluster. |
| CRM | invoices the cluster's current payer, as repman says through the BO |
| Member zone | shows the list of the user's `<uri>.<cluster>` and, per cluster, what the role allows |

## Open

- What each role sees in each tool (Grafana, CRM, member zone).
- The name of the payer role in repman.
- From the review of #2303: dots in cluster names inside the group path, a renamed URI or cluster,
  the claim size for a user on many clusters, whether the `pending-*`, `quote-*`, `unsubscribed-*`
  states are pushed too.

## Local accounts and Cloud18 identities (#2359)

An account with a stored password is **local**: it signs in with that password only, and a Cloud18
session of the same name is refused (`IsLocalOnlyAccount`), so an identity elsewhere never rides a
local account. Two exceptions accept the Cloud18 sign-in despite a stored password:

- the account the instance is registered with (`cloud18-gitlab-user`);
- on a Cloud18 instance, any **email** account (`isCloud18Identity`): an email is a Cloud18 identity.
  Before the SSO-only add, `users/add` gave an account added without a password a random password
  nobody knows, which refused the person's Cloud18 sign-in, and the peers' realm tokens for it, on
  every cluster. Plain-name accounts (`admin`, `dba`) stay local; off Cloud18 nothing changes; no
  configuration is rewritten.

This relies on the email of a Cloud18 session being proven: every Cloud18 path goes through
`VerifyCloud18Token`, which refuses a token without a verified email, and the realm verifies emails,
allows no duplicate email and no username change, and trusts only identity providers that verify them.
