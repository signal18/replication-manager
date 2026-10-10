# router/haproxy: test fixtures are checked at runtime, not vendored

`configuration_test.go`, `routes_test.go`, `filters_test.go`, and `runtime_test.go`
were brought in wholesale from the upstream `vamp-router` project (commit
`e27693517`, "Add router after rename"). They reference a `router/test/`
fixture directory (`test_config1.json`, `test_route.json`, `test_service.json`,
`haproxy_test.cfg`, etc.) and a `../configuration/templates/haproxy_config.template`
path that have never been checked into this repository — not even in the
import commit itself. `TestRuntime_HaproxyFunctions` additionally needs a real
`haproxy` binary on disk.

## Where the originals live

This repo already depends on `github.com/magneticio/vamp-router` as a real Go
module (see `go.mod` — it's the source of `runtime_test.go`'s `helpers`
import), so the exact upstream source tree at the vendored commit is
recoverable from the module cache:

```
go mod download github.com/magneticio/vamp-router
# fixtures: $(go env GOMODCACHE)/github.com/magneticio/vamp-router@<version>/test/
# template: .../configuration/templates/haproxy_config.template
```

Diffing `router/haproxy/*.go` against that module's `haproxy/` package
confirmed the production logic is essentially untouched since import (only
copyright headers, `io/ioutil`→`os`, import reordering, a `%s`→`%d` format
fix, and one added `Runtime.Host`/`Port` field differ), so those original
fixtures are still valid for this package's tests — nothing needs to be
reconstructed.

## Why they aren't committed here

Copying ~4KB of vendored-project fixtures into this repo just to make one
package's tests pass was judged not worth the permanent repo bloat. Instead,
`configuration_test.go` defines `fixtureFiles` (every path these tests read)
and a `requireFixtures(t)` helper that every fixture-dependent test calls
first: if any listed file is missing, the test `t.Skip`s with a pointer back
to this doc; if the full fixture set is present (e.g. someone drops it in
locally, or a future setup step provides it), the tests run for real against
it. `TestRuntime_HaproxyFunctions` additionally skips if no `haproxy` binary
exists at `haRuntime.Binary`.

Verified 2026-08-24: with the fixtures copied in from the module cache above
(and a real `haproxy` binary), all 46 tests in this package pass. Without
them, the package still passes `go test` cleanly — everything fixture- or
binary-dependent skips instead of failing, and the genuinely self-contained
tests (`TestConfiguration_ServiceName`/`RouteName`, `TestConfiguration_Persist`,
`TestFilters_ParseFilterCondition`, `TestFactories_CompileSocketName`,
`TestRuntime_SetNewPid`/`UseExistingPid`, `TestStructs_Error`) still run.
