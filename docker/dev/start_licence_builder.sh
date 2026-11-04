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
./run.sh "$@"
exec ./replication-manager-pro license-builder
