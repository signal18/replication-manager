#!/bin/bash
echo "# Getting branch info"
git status -bs
echo "# Press Return or Space to start build, all other keys to quit"
# read -s -n 1 key
if [[ $key != "" ]]; then exit; fi
version=$(git describe --tag)
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
mkdir -p build/usr/share/replication-manager/dashboard
mkdir -p build/etc/replication-manager
mkdir -p build/etc/systemd/system
mkdir -p build/etc/init.d
mkdir -p build/var/lib/replication-manager
echo "# Copying files to build dir"
cp replication-manager build/usr/bin/
cp etc/* build/etc/replication-manager/
cp -r dashboard/* build/usr/share/replication-manager/dashboard/
cp -r share/* build/usr/share/replication-manager/

# do not package commercial collector docker images
rm -rf build/usr/share/replication-manager/opensvc/*.tar.gz
echo "# Building packages replication-manager"
cp service/replication-manager.service build/etc/systemd/system
cp service/replication-manager.init.el6 build/etc/init.d/replication-manager
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager .
fpm --package replication-manager-$version-$head.tar -C build -s dir -t tar -n replication-manager .
gzip replication-manager-$version-$head.tar
cp service/replication-manager.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager .

echo "# Building packages replication-manager-pro"
cp service/replication-manager.service build/etc/systemd/system
cp service/replication-manager.init.el6 build/etc/init.d/replication-manager
rm -f build/usr/bin/replication-manager
cp replication-manager-pro build/usr/bin/
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-pro .
fpm --package replication-manager-pro-$version-$head.tar -C build -s dir -t tar -n replication-manager-pro .
gzip replication-manager-pro-$version-$head.tar
cp service/replication-manager.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-pro .

echo "# Building packages replication-manager-cli"

rm -rf build/usr/share
rm -rf build/usr/etc
rm -rf build/var
rm -f build/usr/bin/replication-manager-pro
cp replication-manager-cli build/usr/bin/
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-client .
fpm --package replication-manager-client-$version-$head.tar -C build -s dir -t tar -n replication-manager-client .
gzip replication-manager-client-$version-$head.tar
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-client .
