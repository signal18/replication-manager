# `utils/k8sexec`: bounded command execution in a container

`k8sexec.Run(ctx, restConfig, Target{Namespace, Pod, Container}, argv, Options)` runs one command in one named
container through the Kubernetes API (`pods/exec`, client-go `remotecommand`, SPDY) and returns stdout, stderr
and the exit status. It is a Go helper for replication-manager's own code: no HTTP route, no stdin, no TTY, no
shell of its own (the caller passes an argument vector).

- Bounded: timeout 30 s by default, 5 min at most; 64 KiB per stream by default, 1 MiB at most (the rest is
  dropped and flagged, the command still finishes).
- Errors carry a `Kind`: `DENIED` (RBAC or authentication), `NOT_FOUND` (no such Pod), `UNAVAILABLE` (no such
  container, or not running), `TIMEOUT`, `CANCELED`, `EXIT` (the command ran; the `Result` is complete),
  `INFRA`, `INVALID` (refused before anything was sent). The refusals of the API server reach the client only as
  text on an upgrade failure; the classification reads that text and is covered by tests against a server that
  returns each refusal.
- The identity needs RBAC `create` on `pods/exec`. The product's Role does not grant it and must not be extended
  for this: the Kubernetes regtests document the permission and check it first.
- The Pod is named, never selected: a caller that follows a replaced Pod reads the current one before each call.
- Dependencies: `github.com/moby/spdystream` and `github.com/mxk/go-flowrate` (indirect, already in the module
  graph through client-go).

Used by `testK8SDatabaseConfigParity` (#2395). A product Web Terminal for Kubernetes Pods is a separate feature
(an interactive stream with TTY and resize), not provided here.
