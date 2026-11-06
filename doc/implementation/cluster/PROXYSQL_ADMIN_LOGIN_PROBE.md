# ProxySQL admin login: external probe on a local-only refusal (#2331)

## Problem
`proxysql-user` defaults to `admin`. ProxySQL accepts `admin` from the local host only; a
replication-manager that reaches ProxySQL over the network is refused with
`Error 1040: User 'admin' can only connect locally`. The ProxySQL template creates a second
login, `external`, with the same password, which is accepted from outside.

## Behaviour
`ProxySQLProxy.Connect()` (`cluster/prx_proxysql.go`) probes `external` with the same password
when both hold:

1. the login in use is `admin`;
2. ProxySQL answered the login with MySQL error 1040.

If the probe connects, `external` becomes the login: the proxy uses it and the `proxysql-user`
secret is written through the credential chain (`setAddedProxysqlCredential`), so it survives a
restart. If the probe fails, the original error is returned and nothing changes.

Any other error, or any other configured user, never probes.

## Notes
- `router/proxysql/proxysql.go` wraps the connection error with `%w` instead of `%s` so the
  MySQL error number can be read (`errors.As` on `*mysql.MySQLError`); the message is unchanged.
- The password rotation is unchanged: it writes one `user:password` pair into
  `admin-admin_credentials`.

## Tests
`cluster/prx_proxysql_admin_probe_test.go`, against a fake ProxySQL admin: switch on 1040,
stay on `admin` when `external` is refused too, no probe on 1045 or for a user other than `admin`.
