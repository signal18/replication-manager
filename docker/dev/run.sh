#!/bin/bash
set -e
arg="$1"
if [ "$arg" == "v2" ] || [ "$arg" == "V2" ]; then
make cli pro osc WITH_REACT=OFF
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-osc monitor --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share &
pid="$!"
elif [ "$arg" == "skip" ] || [ "$arg" == "SKIP" ]; then
make cli pro WITH_REACT=OFF
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-pro monitor --user=repman --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share > /var/log/replication-manager/replication-manager.log 2>&1 &
pid="$!"
elif [ "$arg" == "run" ] || [ "$arg" == "RUN" ]; then
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-pro monitor --user=repman --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share > /var/log/replication-manager/replication-manager.log 2>&1 &
pid="$!"

elif [ "$arg" == "osc" ] || [ "$arg" == "OSC" ]; then
make cli osc
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-osc monitor --plugin-signing-public-key="" --user=repman --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share > /var/log/replication-manager/replication-manager.log 2>&1 &
pid="$!"
elif [ "$arg" == "run-osc" ] || [ "$arg" == "RUN-OSC" ]; then
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-osc monitor --plugin-signing-public-key="" --user=repman --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share > /var/log/replication-manager/replication-manager.log 2>&1 &
pid="$!"
else
make cli pro
cp share/opensvc/moduleset_mariadb.svc.mrm.db.json /usr/share/replication-manager/opensvc/
./wait.sh replication-manager-pro;
./wait.sh replication-manager-osc;
./replication-manager-pro monitor --plugin-signing-public-key="" --user=repman --http-root=/go/src/github.com/signal18/replication-manager/share/dashboard --monitoring-sharedir=/go/src/github.com/signal18/replication-manager/share > /var/log/replication-manager/replication-manager.log 2>&1 &
pid="$!"
fi
echo "$pid"
echo "$pid" > /tmp/repman.pid
