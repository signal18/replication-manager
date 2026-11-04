# Alert email recipients: grant `alert-mail-receiver` (#2313)

## Before

The alert emails (state alerts, `cluster_chk.go`; password rotation, `cluster_sec.go`) were sent
only when `mail-to` was set, to `mail-to` plus, on Cloud18, every operational address of the
cluster: the owner (`cloud18-gitlab-user`), the DbOps, the External DbOps and SysOps, the sponsor.
A role meant every alert; a user could be added to the alerts only through `mail-to`.

## Now

`GetAlertRecipients` (`cluster/cluster_mail.go`), for an alert (`All`):

1. `mail-to`;
2. every user of the cluster holding `alert-mail-receiver` whose name is an address (contains `@`:
   `admin`, `system` never are);
3. on Cloud18, the owner and ops addresses of the config that are **not** users of the cluster:
   there is no user to carry a grant for them, they keep receiving as before.

The alert is sent when that list is not empty, so a grant holder receives without `mail-to`.
The role flags (`DbOps`, `SysOps`, `Sponsor`, …) still add that role's address, for a message meant
for that role; no caller uses them for an alert.

## The grant

- `alert-mail-receiver`, family `alert` (`config/grants.go`): `GetGrantAlert`, `HasAllAlertGrants`,
  the compact form `alert`. The GUI user grant editor groups grants by prefix, so the family shows
  as its own group with no GUI change.
- Explicit: no other family implies it, so nobody starts receiving at the upgrade. The default ACL
  of `admin` (`api-credentials-acl-allow`) names `alert` so that admin can give it (the GUI shows a
  delegator only the grants it holds); `admin` is not an address, it receives nothing.
- Role defaults (`GetDefaultAllowDiscardACL`): sysops, dbops and extsysops through `*`; sponsor and
  extdbops name `alert`. The fixed grant lists of the Cloud18 owner (`CreateAdminUserForm`) and of
  the self-service sponsor (`selfServiceSponsorGrants`) name it too: an owner, DbOps, External Ops
  or sponsor created from now on holds it, as they received the alerts before.

## Upgrade and removal

`LoadAPIUsers` grants it to a user who got the alerts by its role before (`receivedAlertsByRole`:
on Cloud18, the owner, DbOps, External DbOps/SysOps addresses, the sponsor role) while none of the
user's ACL lists decides it (`aclsDecideGrant`: an allow or discard entry naming the grant or a
prefix of it). The next `SaveAcls` writes `alert` in the user's allow list: decided.

A user added or updated without the grant gets a discard entry `user:alert-mail-receiver`
(`alertMailDiscard`, in `addUser` and `UpdateUser`): `UpdateUser` drops the user's discard entries
then reloads, so without it the upgrade would give the grant back at once. `SaveAcls` then writes
the family in the discard list (`GetCompactGrants`).

## Not in this change

Slack, Teams and Pushover send to one webhook or token of the cluster: a per-user receiver needs
the user's identifier on that channel.

## Tests

`cluster/cluster_alert_receiver_test.go`: recipients by grant with and without `mail-to`, Cloud18
config addresses, the upgrade, a removal kept across reloads, a user added without the grant, the
role defaults.
