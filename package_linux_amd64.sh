#!/bin/bash

# exit on error
set -e

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
rm -rf build buildtar
rm -f *.tar.gz *.deb *.rpm
mkdir -p build/usr/bin

echo "# Building packages replication-manager-cli"

cflags=(-m "$maintainer" --license "$license" -v $version)

rm -rf build/usr/share build/usr/etc build/var
cp replication-manager-cli build/usr/bin/
fpm ${cflags[@]} --rpm-os linux -C build -s dir -t rpm -n replication-manager-client --description "$description - client package" .
fpm ${cflags[@]} -C build -s dir -t deb -n replication-manager-client --description "$description - client package" .
fpm --package replication-manager-client-$version.tar -C build -s dir -t tar -n replication-manager-client .
gzip replication-manager-client-$version.tar

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

# do not package commercial collector docker images
rm -rf build/usr/share/replication-manager/opensvc/*.tar.gz


for flavor in min osc tst pro
do
    echo "# Building packages replication-manager-$flavor"
    case $flavor in
        min)
            extra_desc="Minimal version"
            ;;
        osc)
            extra_desc="Open source version"
            ;;
        pro)
            extra_desc="Professional version"
            ;;
        tst)
            extra_desc="Testing version"
            ;;
    esac
    cp -r etc/* build/etc/replication-manager/
    if [ "$flavor" != "pro" ]; then
      rm -f build/etc/replication-manager/config.toml.sample.opensvc.*
    else
      cp -rp test/opensvc build/usr/share/replication-manager/tests
    fi
    cp replication-manager-$flavor build/usr/bin/
    cp service/replication-manager-$flavor.service build/etc/systemd/system/replication-manager.service
    cp service/replication-manager-$flavor.init.el6 build/etc/init.d/replication-manager
    fpm ${cflags[@]} --rpm-os linux -C build -s dir -t rpm -n replication-manager-$flavor --epoch $epoch --description "$description - $extra_desc" .
    cp service/replication-manager-$flavor.init.deb7 build/etc/init.d/replication-manager
    fpm ${cflags[@]} -C build -s dir -t deb -n replication-manager-$flavor --description "$description - $extra_desc" .
    rm -f build/usr/bin/replication-manager-$flavor

    echo "# Building tarball replication-manager-$flavor"
    cp -r etc/* buildtar/etc/
    if [ "$flavor" != "pro" ]; then
      rm -f buildtar/etc/config.toml.sample.opensvc.*
    else
      cp -rp test/opensvc buildtar/share/tests
    fi
    cp replication-manager-$flavor-basedir buildtar/bin/replication-manager-$flavor
    cp service/replication-manager-$flavor-basedir.service buildtar/share/replication-manager.service
    cp service/replication-manager-$flavor-basedir.init.el6 buildtar/share/replication-manager.init
    fpm --package replication-manager-$flavor-$version.tar --prefix replication-manager-$flavor -C buildtar -s dir -t tar -n replication-manager-$flavor .
    gzip replication-manager-$flavor-$version.tar
    rm -rf buildtar/bin/replication-manager-$flavor
done

echo "# Building arbitrator packages"
rm -rf build/etc
rm -rf build/usr/share
mkdir -p build/etc/replication-manager
mkdir -p build/etc/systemd/system
mkdir -p build/etc/init.d
cp service/replication-manager-arb.service build/etc/systemd/system
cp service/replication-manager-arb.init.el6 build/etc/init.d/replication-manager-arb
cp replication-manager-arb build/usr/bin/
fpm ${cflags[@]} --rpm-os linux -C build -s dir -t rpm -n replication-manager-arbitrator --epoch $epoch  --description "$description - arbitrator package" .
fpm --package replication-manager-arbitrator-$version.tar -C build -s dir -t tar -n replication-manager-arbitrator .
gzip replication-manager-arbitrator-$version.tar
cp service/replication-manager-arb.init.deb7 build/etc/init.d/replication-manager-arbitrator
fpm ${cflags[@]} -C build -s dir -t deb -n replication-manager-arbitrator --description "$description - arbitrator package" .
rm -f build/usr/bin/replication-manager-arb

echo "# Build complete"
