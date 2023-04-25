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

builddir="$(pwd)"/build
mkdir -p "$builddir"/binaries "$builddir"/package "$builddir"/tar "$builddir"/release

version=$(git describe --tag --abbrev=4 | sed 's/^v//')
head=$(git rev-parse --short HEAD)
epoch=$(date +%s)
description="Replication Manager for MariaDB and MySQL"
maintainer="info@signal18.io"
license="GPLv3"
architecture=${architecture:-amd64}

if [ $nobuild -eq 0 ]; then
  echo "# Building for $architecture"
  ARCH=$architecture make
fi

echo "# Cleaning up previous builds"
rm -rf "$builddir"/package/* "$builddir"/tar/* "$builddir"/release/*
mkdir -p "$builddir"/package/usr/bin

echo "# Building packages replication-manager-cli"

cflags=(-a "$architecture" -m "$maintainer" --license "$license" -v $version)

cp "$builddir"/binaries/replication-manager-cli "$builddir"/package/usr/bin/
fpm ${cflags[@]} --rpm-os linux -C "$builddir"/package -s dir -t rpm -n replication-manager-client --description "$description - client package" -p "$builddir/release"
fpm ${cflags[@]} -C "$builddir"/package -s dir -t deb -n replication-manager-client --description "$description - client package" -p "$builddir/release"
fpm --package replication-manager-client-$version.tar -C "$builddir"/package -s dir -t tar -n replication-manager-client -p "$builddir"/release/replication-manager-client-$version.tar.gz


mkdir -p "$builddir"/package/usr/share/replication-manager/dashboard
mkdir -p "$builddir"/package/etc/replication-manager
mkdir -p "$builddir"/package/etc/replication-manager/cluster.d
mkdir -p "$builddir"/package/etc/systemd/system
mkdir -p "$builddir"/package/etc/init.d
mkdir -p "$builddir"/package/var/lib/replication-manager
mkdir -p "$builddir"/tar/bin
mkdir -p "$builddir"/tar/etc
mkdir -p "$builddir"/tar/etc/cluster.d
mkdir -p "$builddir"/tar/share
mkdir -p "$builddir"/tar/data

echo "# Copying files to build dir"
cp -r dashboard/* "$builddir"/package/usr/share/replication-manager/dashboard/
cp -r share/* "$builddir"/package/usr/share/replication-manager/

# do not package commercial collector docker images
rm -rf "$builddir"/package/usr/share/replication-manager/opensvc/*.tar.gz


for flavor in osc tst pro osc-cgo
do
    echo "# Building packages replication-manager-$flavor"
    case $flavor in
        osc)
            extra_desc="Open source version"
            ;;
        osc-cgo)
            extra_desc="Open source glibc version "
            ;;
        pro)
            extra_desc="Professional version"
            ;;
        tst)
            extra_desc="Testing version"
            ;;
    esac
    cp -r etc/* "$builddir"/package/etc/replication-manager/
    if [ "$flavor" != "pro" ]; then
      rm -f "$builddir"/package/etc/replication-manager/config.toml.sample.opensvc.*
    fi

    cp "$builddir"/binaries/replication-manager-$flavor "$builddir"/package/usr/bin/
    cp service/replication-manager-$flavor.service "$builddir"/package/etc/systemd/system/replication-manager.service
    cp service/replication-manager-$flavor.init.el6 "$builddir"/package/etc/init.d/replication-manager
    fpm ${cflags[@]} --rpm-os linux -C "$builddir"/package -s dir -t rpm --config-files /etc/replication-manager/cluster.d/cluster1.toml --config-files /etc/replication-manager/config.toml.sample -n replication-manager-$flavor --epoch $epoch --description "$description - $extra_desc" -p "$builddir/release"
    cp service/replication-manager-$flavor.init.deb7 "$builddir"/package/etc/init.d/replication-manager
    fpm ${cflags[@]} -C "$builddir"/package -s dir -t deb --config-files /etc/replication-manager/cluster.d/cluster1.toml.sample --config-files /etc/replication-manager/config.toml -n replication-manager-$flavor --description "$description - $extra_desc" -p "$builddir/release"
    rm -f "$builddir"/package/usr/bin/replication-manager-$flavor

    echo "# Building tarball replication-manager-$flavor"
    cp -r etc/* "$builddir"/tar/etc/
    if [ "$flavor" != "pro" ]; then
      rm -f "$builddir"/tar/etc/config.toml.sample.opensvc.*
    fi
    cp "$builddir"/binaries/replication-manager-$flavor-basedir "$builddir"/tar/bin/replication-manager-$flavor
    cp service/replication-manager-$flavor-basedir.service "$builddir"/tar/share/replication-manager.service
    cp service/replication-manager-$flavor-basedir.init.el6 "$builddir"/tar/share/replication-manager.init
    fpm --package replication-manager-$flavor-$version.tar --prefix replication-manager-$flavor -C "$builddir"/tar -s dir -t tar -n replication-manager-$flavor -p "$builddir"/release/replication-manager-$flavor-$version.tar.gz .
    rm -rf "$builddir"/tar/bin/replication-manager-$flavor
done

echo "# Building arbitrator packages"
rm -rf "$builddir"/package/etc
rm -rf "$builddir"/package/usr/share
mkdir -p "$builddir"/package/etc/replication-manager
mkdir -p "$builddir"/package/etc/systemd/system
mkdir -p "$builddir"/package/etc/init.d
mkdir -p "$builddir"/package/var/lib/replication-manager
cp service/replication-manager-arb.service "$builddir"/package/etc/systemd/system
cp "$builddir"/binaries/replication-manager-arb "$builddir"/package/usr/bin/
# RPM
cp service/replication-manager-arb.init.el6 "$builddir"/package/etc/init.d/replication-manager-arb
fpm ${cflags[@]} --rpm-os linux -C "$builddir"/package -s dir -t rpm -n replication-manager-arbitrator --epoch $epoch  --description "$description - arbitrator package" -p "$builddir/release"
# Debian
cp service/replication-manager-arb.init.deb7 "$builddir"/package/etc/init.d/replication-manager-arbitrator
fpm ${cflags[@]} -C "$builddir"/package -s dir -t deb -n replication-manager-arbitrator --description "$description - arbitrator package" -p "$builddir/release"
# tar
rm -rf "$builddir"/tar/*
mkdir -p "$builddir"/tar/etc
mkdir -p "$builddir"/tar/data
mkdir -p "$builddir"/tar/bin
mv "$builddir"/package/usr/bin/replication-manager-arb "$builddir"/tar/bin
fpm --package replication-manager-arbitrator-$version.tar -C "$builddir"/tar -s dir -t tar -n replication-manager-arbitrator -p "$builddir"/release/replication-manager-arbitrator-$version.tar.gz
echo "# Build complete"
rm -rf "$builddir"/tar/
rm -rf "$builddir"/package/
