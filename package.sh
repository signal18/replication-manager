#!/bin/bash
git checkout 0.7
head=$(git rev-parse --short HEAD)
epoch=$(date +%s)
./build.sh
rm -rf build
rm *.tar.gz
rm *.deb
rm *.rpm
mkdir -p build/usr/bin
mkdir -p build/usr/share/replication-manager/dashboard
mkdir -p build/etc/replication-manager
mkdir -p build/etc/systemd/system
mkdir -p build/etc/init.d
cp replication-manager build/usr/bin/
cp etc/config.toml.sample build/etc/replication-manager/config.toml.sample
cp -r dashboard/* build/usr/share/replication-manager/dashboard/
cp service/replication-manager.service build/etc/systemd/system
cp service/replication-manager.init.el6 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v 0.7.0 -C build -s dir -t rpm -n replication-manager .
fpm --package replication-manager-0.7.0-$head.tar -C build -s dir -t tar -n replication-manager .
gzip replication-manager-0.7.0-$head.tar
cp service/replication-manager.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v 0.7.0 -C build -s dir -t deb -n replication-manager .
