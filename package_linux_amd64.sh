#!/bin/bash

nobuild=0
if [ "$1" != "" ]; then
  if [ "$1" = "--no-build" ]; then
    nobuild=1
  fi
fi

echo "# Getting branch info"
git status -bs

version=$(git describe --tag --abbrev=4)
head=$(git rev-parse --short HEAD)
epoch=$(date +%s)
description="Replication Manager for MariaDB and MySQL"
maintainer="info@signal18.io"
license="GPLv3"

if [ $nobuild -eq 0 ]; then
  echo "# Building"
  ./build_linux_amd64.sh
fi

echo "# Cleaning up previous builds"
rm -rf build
rm -rf buildtar
rm *.tar.gz
rm *.deb
rm *.rpm
mkdir -p build/usr/bin

cflags=(-m "$maintainer" --license "$license" -v $version)

mkdir -p build/usr/share/replication-manager/dashboard
mkdir -p build/etc/replication-manager
mkdir -p build/etc/systemd/system
mkdir -p build/etc/init.d
mkdir -p build/var/lib/replication-manager
mkdir -p buildtar/bin
mkdir -p buildtar/etc
mkdir -p buildtar/share
mkdir -p buildtar/data

echo "# Copying files to build dir"
cp -r dashboard/* build/usr/share/replication-manager/dashboard/
cp -r share/* build/usr/share/replication-manager/

echo "# Building replication-manager packages"
cp etc/* build/etc/replication-manager/
cp replication-manager build/usr/bin/
cp service/replication-manager.service build/etc/systemd/system/replication-manager.service
cp service/replication-manager.init.el6 build/etc/init.d/replication-manager
cp service/replication-manager-arbitrator.init.el6 build/etc/init.d/replication-manager-arbitrator
fpm ${cflags[@]} --rpm-os linux -C build -s dir -t rpm -n replication-manager --epoch $epoch --description "$description" .
cp service/replication-manager.init.deb7 build/etc/init.d/replication-manager
cp service/replication-manager-arb.init.deb7 build/etc/init.d/replication-manager-arbitrator
fpm ${cflags[@]} -C build -s dir -t deb -n replication-manager --description "$description" .
rm -f build/usr/bin/replication-manager

echo "# Building replication-manager tarball"
cp etc/* buildtar/etc/
cp replication-manager buildtar/bin/
cp service/replication-manager.service buildtar/share/replication-manager.service
cp service/replication-manager.init.el6 buildtar/share/replication-manager.init
fpm --package replication-manager-$version.tar --prefix replication-manager -C buildtar -s dir -t tar -n replication-manager .
gzip replication-manager-$version.tar
rm -rf buildtar/bin/replication-manager

echo "# Build complete"
