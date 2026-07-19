#!/bin/bash

WORKDIR=/go/src/github.com/signal18/replication-manager
DEVDIR=$WORKDIR/docker/dev

for script in run.sh stop.sh restart.sh wait.sh; do
    [ ! -e "$WORKDIR/$script" ] && ln -s "$DEVDIR/$script" "$WORKDIR/$script"
done

# run.sh launches the monitor from the workdir root (./replication-manager-pro /
# -osc), but 'make' builds the binaries into build/binaries/. Older dev images had
# root-level symlinks baked in; fresh images don't, so run.sh's backgrounded launch
# silently exec-failed ("no such file") and repman never came up — no panic, just a
# vanished PID. Recreate the symlinks here (force: they may dangle until 'make' runs,
# then resolve once the binary exists).
for bin in replication-manager-pro replication-manager-osc replication-manager-cli; do
    ln -sf "$WORKDIR/build/binaries/$bin" "$WORKDIR/$bin"
done

exec "$@"
