#!/bin/bash
set -e
arg="$1"

WORKDIR=/go/src/github.com/signal18/replication-manager
SHAREDIR=$WORKDIR/share
PUBKEY=$SHAREDIR/plugins/plugin-signing.pub
DATADIR=/var/lib/replication-manager
COMMON_ARGS="--user=repman --http-root=$SHAREDIR/dashboard --monitoring-sharedir=$SHAREDIR --plugin-signing-public-key=$PUBKEY"

# The monitor's console (stdout+stderr: Go crash traces, output of libraries that print
# directly) goes to CONSOLE_LOG. repman's own log is log-file, rotated by repman itself;
# this file is not, so each start begins a fresh one and keeps the previous run's as .1
# (the run that may have crashed). Never appended forever: it reached GBs on a long-lived
# instance (law F3). Called after wait.sh, once the previous monitor is gone.
CONSOLE_LOG=/var/log/replication-manager/replication-manager.log
new_console_log() {
	if [ -f "$CONSOLE_LOG" ]; then
		mv -f "$CONSOLE_LOG" "$CONSOLE_LOG.1"
	fi
}

# Symlink shared plugin dir to locally built plugins so all clusters use the dev build
SHARED_PLUGINS=$DATADIR/plugins
if [ ! -L "$SHARED_PLUGINS" ]; then
	rm -rf "$SHARED_PLUGINS"
	ln -s "$WORKDIR/build/plugins" "$SHARED_PLUGINS"
fi

if [ "$arg" == "v2" ] || [ "$arg" == "V2" ]; then
make cli pro osc WITH_REACT=OFF
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-osc monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"
elif [ "$arg" == "skip" ] || [ "$arg" == "SKIP" ]; then
make cli pro WITH_REACT=OFF
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-pro monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"
elif [ "$arg" == "run" ] || [ "$arg" == "RUN" ]; then
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-pro monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"

elif [ "$arg" == "osc" ] || [ "$arg" == "OSC" ]; then
make cli osc
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-osc monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"
elif [ "$arg" == "run-osc" ] || [ "$arg" == "RUN-OSC" ]; then
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-osc monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"
else
make cli pro
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
new_console_log
./replication-manager-pro monitor $COMMON_ARGS > "$CONSOLE_LOG" 2>&1 &
pid="$!"
fi
echo "$pid"
echo "$pid" > /tmp/repman.pid
