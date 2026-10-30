# Gateway route headers: X-Forwarded-Proto (#2252)

## Rule

The backend that `buildGroupedHostRouteFragment` (`cluster/prov_opensvc_app.go`) writes for an
app's `host` routes sets:

```
http-request set-header X-Forwarded-Proto https
```

A `host` route is always served over https: the gateway terminates TLS on 443 and routes on the
Host header. `set-header`, never `add-header`, so a value sent by the client is replaced, not
kept next to ours. Port routes (`buildRouteFragment`, http or tcp on a source port) are not
TLS-terminated by the gateway and carry no such header.

## Why

An app behind a reverse proxy learns its own scheme from `X-Forwarded-Proto`. Without it, an app
that rebuilds its URL from the request believes it is served over plain http.

Found with Mattermost: it builds its OAuth `redirect_uri` from the configured SiteURL when it sends
the user to the identity provider (`https://…/signup/gitlab/complete`), but from the request
(`X-Forwarded-Proto` + `Host`, `web/oauth.go completeOAuth`) when it exchanges the code. The
gateway sent no `X-Forwarded-Proto` to the app, so the exchange carried `http://…`, the provider
answered `invalid_grant`, and no GitLab sign-in succeeded. A capture between the gateway and the
app showed `host` and `x-forwarded-for`, no `x-forwarded-proto`.

## Gateway head

The gateway's head template had `http-request add-header X-Forwarded-Proto https if { ssl_fc }` in
`backend acme_challenge_backend` (the default backend) instead of `frontend https`: only the
default backend got it. The head is not written by repman; it is fixed in the gateway template, and
on a running gateway in `<ns>/cfg/haproxy` key `haproxy.cfg.head` followed by `task#mergecfg`. The
route backend does not depend on it: every app gets the header whatever the head holds.

## Test

`TestRouteFragments_XForwardedProto` (`cluster/prov_opensvc_app_route_test.go`): the host route
fragment sets the header exactly once and never adds it; http and tcp port route fragments carry
none.
