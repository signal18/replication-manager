#!/bin/bash
# start_licence_builder.sh — dev image command for the Cloud18 licence-builder (#2309).
#
# Double usage of the one monitor: run.sh builds + starts the regular dev monitor in the
# background (as any dev instance does), then license-builder runs beside it. The builder
# tests each package it builds by calling license-pull on that same monitor
# (LICENSE_BUILDER_MONITOR_URL). That monitor must have git-url-pull empty, or license-pull
# refuses (see doc/implementation/licensebuilder/LICENSE_BUILDER.md).
#
# Run as the app command (non-login shell), so the WORKDIR symlinks the dev image's bashrc
# would create are made here first; then run.sh (build + start monitor), then the builder.
set -e
WORKDIR=/go/src/github.com/signal18/replication-manager
cd "$WORKDIR"
# The dev image ships Go with GOTOOLCHAIN=local, which refuses the newer toolchain some
# releases (and develop) require in go.mod. Exporting a concrete toolchain here covers both
# run.sh's monitor build and the builder's own per-version `make plugins` (it inherits the
# environment). A newer toolchain builds older go.mod directives, so this is a safe floor;
# bump it with the repo's Go line. The toolchain is already in the image's module cache.
# The image sets GOTOOLCHAIN=local (non-empty), so override that explicitly; respect only a
# deliberate non-"local" value an operator may have set.
if [ -z "$GOTOOLCHAIN" ] || [ "$GOTOOLCHAIN" = "local" ]; then
	export GOTOOLCHAIN=go1.26.10
fi
for s in run.sh stop.sh restart.sh wait.sh; do
	[ -e "$s" ] || ln -s "docker/dev/$s" "$s"
done
# run.sh and the exec below launch ./replication-manager-pro, which `make cli pro` builds to
# build/binaries/; a fresh clone has no WORKDIR symlink for it (dev instances carry one from an
# earlier build). Create it now — it dangles until run.sh's make builds the target, then resolves.
ln -sf build/binaries/replication-manager-pro replication-manager-pro
# The beside-monitor orchestrates nothing. Force the local orchestrator in its config so it does
# not try to load the OpenSVC cluster certificate (/etc/replication-manager/s18.p12) and fatal at
# start. This is a scope:server setting read from the config file (the env var is not honored for
# it). git-url-pull stays empty so license-pull is accepted.
CFG=/etc/replication-manager/config.toml
grep -q '^prov-orchestrator' "$CFG" 2>/dev/null || echo 'prov-orchestrator = "local"' >> "$CFG"
./run.sh "$@"
# The build writes share/plugins as root; license-pull later re-writes each package's .sig there
# as the monitor's --user (repman). Make the directory writable so the extractor's unlink+recreate
# succeeds for that less-privileged user.
chmod -R a+rwX share/plugins 2>/dev/null || true
exec ./replication-manager-pro license-builder
