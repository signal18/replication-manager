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
for s in run.sh stop.sh restart.sh wait.sh; do
	[ -e "$s" ] || ln -s "docker/dev/$s" "$s"
done
./run.sh "$@"
exec ./replication-manager-pro license-builder
