# GitLab personal access token: reuse, not mint at every start (#2153)

A Cloud18 instance pushes its config to GitLab with a personal access token
(`git-acces-token`). `InitGitConfig` runs at every start and at every Cloud18
settings change.

## The problem

`InitGitConfig` minted a new token each time, and GitLab revokes the previous
one but keeps it as a row. The saved token was never reused because
`InitGitConfig` marked `git-acces-token` immutable: `SaveDynamic` drops the
immutable keys from `default.toml`, so the token never came back at the next
start. Revoked tokens piled up in GitLab, one per start per instance.

## The policy

1. At start, the saved token (`default.toml`, encrypted) is checked with
   `githelper.PATValid` (`GET /api/v4/personal_access_tokens/self`).
2. A new token is minted only when GitLab definitely refuses the saved one:
   no token, HTTP 401 or 403 (an expired token answers 401), or a 200 saying
   it is inactive or revoked.
3. Any other failure (network, timeout, 5xx, 404 on a GitLab without the
   endpoint, an unreadable reply) keeps the saved token and logs a WARN:
   minting during a GitLab outage would bring the pile-up back. A token that
   is really broken behind such an error is replaced at the next start or by
   the daily rotation.
4. The token is held as a dynamic secret (`keepGitAccessToken`): written
   encrypted to `default.toml` by `SaveDynamic`, read back at start. An
   immutable mark left by an older version is removed, so an instance heals
   at its first start on this code (it mints one last token, then reuses it).
5. The daily rotation (server ticker) still replaces the token once a day, so
   it never expires; it saves the new one, and keeps the current one when
   GitLab returns none.

## Consequence

The encrypted token is in `default.toml`, so it is pushed to the instance's
config repository like the other encrypted secrets.

## Test

`utils/githelper/pat_valid_test.go` covers the replies (active, revoked,
inactive, 401, 403, 500, 404, unreadable, timeout, no token). On a dev
instance: two restarts after the first start on the fix leave the instance's
token count in GitLab unchanged.
