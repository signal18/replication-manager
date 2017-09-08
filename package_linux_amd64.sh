#!/bin/bash
echo "# Getting branch info"
git status -bs
version=$(git describe --tag --abbrev=4)
head=$(git rev-parse --short HEAD)
epoch=$(date +%s)
echo "# Building"
./build_linux_amd64.sh
echo "# Cleaning up previous builds"
rm -rf build
rm *.tar.gz
rm *.deb
rm *.rpm
mkdir -p build/usr/bin


echo "# Building packages replication-manager-cli"

rm -rf build/usr/share
rm -rf build/usr/etc
rm -rf build/var
cp replication-manager-cli build/usr/bin/
fpm --rpm-os linux --epoch $epoch -v $version -C build -s dir -t rpm -n replication-manager-client .
fpm --package replication-manager-client-$version.tar -C build -s dir -t tar -n replication-manager-client .
gzip replication-manager-client-$version.tar
fpm --epoch $epoch -v $version -C build -s dir -t deb -n replication-manager-client .

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


for flavor in min osc tst pro arb
do
    echo "# Building packages replication-manager-$flavor"
    cp etc/* build/etc/replication-manager/
    rm -f build/etc/replication-manager/config.toml.sample.opensvc.*
    cp replication-manager-$flavor build/usr/bin/
    cp service/replication-manager-$flavor.service build/etc/systemd/system/replication-manager.service
    cp service/replication-manager-$flavor.init.el6 build/etc/init.d/replication-manager
    fpm --rpm-os linux --epoch $epoch -v $version -C build -s dir -t rpm -n replication-manager-$flavor .
    cp service/replication-manager-$flavor.init.deb7 build/etc/init.d/replication-manager
    fpm --epoch $epoch -v $version -C build -s dir -t deb -n replication-manager-$flavor .
    rm -f build/usr/bin/replication-manager-$flavor

    echo "# Building tarball replication-manager-$flavor"
    cp etc/* buildtar/etc/
    rm -f build/etc/config.toml.sample.opensvc.*
    cp replication-manager-$flavor buildtar/bin/
    cp service/replication-manager-$flavor.service buildtar/etc/replication-manager.service
    cp service/replication-manager-$flavor.init.el6 buildtar/etc/replication-manager.init
    fpm --package replication-manager-$flavor-$version.tar -C buildtar -s dir -t tar -n replication-manager-$flavor .
    gzip replication-manager-$flavor-$version.tar
done


