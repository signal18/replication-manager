#!/bin/bash
echo "# Getting branch info"
git status -bs
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


echo "# Building packages replication-manager-cli"

rm -rf build/usr/share
rm -rf build/usr/etc
rm -rf build/var
cp replication-manager-cli build/usr/bin/
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-client .
fpm --package replication-manager-client-$version-$head.tar -C build -s dir -t tar -n replication-manager-client .
gzip replication-manager-client-$version-$head.tar
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-client .

echo "# Preparing server directy to build dir"
mkdir -p build/usr/share/replication-manager/dashboard
mkdir -p build/etc/replication-manager
mkdir -p build/etc/systemd/system
mkdir -p build/etc/init.d
mkdir -p build/var/lib/replication-manager
echo "# Copying files to build dir"
cp -r dashboard/* build/usr/share/replication-manager/dashboard/
cp -r share/* build/usr/share/replication-manager/

# do not package commercial collector docker images
rm -rf build/usr/share/replication-manager/opensvc/*.tar.gz

echo "# Building packages replication-manager-min"
cp etc/* build/etc/replication-manager/
rm -f build/etc/replication-manager/config.toml.sample.opensvc.*
cp replication-manager-min build/usr/bin/
cp service/replication-manager-min.service build/etc/systemd/system/replication-manager.service
cp service/replication-manager-min.init.el6 build/etc/init.d/replication-manager
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-min .
fpm --package replication-manager-min-$version-$head.tar -C build -s dir -t tar -n replication-manager-min .
gzip replication-manager-osc-$version-$head.tar
cp service/replication-manager-min.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-min .
rm -f build/usr/bin/replication-manager-min

echo "# Building packages replication-manager-osc"
cp etc/* build/etc/replication-manager/
rm -f build/etc/replication-manager/config.toml.sample.opensvc.*
cp replication-manager-osc build/usr/bin/
cp service/replication-manager-osc.service build/etc/systemd/system/replication-manager.service
cp service/replication-manager-osc.init.el6 build/etc/init.d/replication-manager
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-osc .
fpm --package replication-manager-osc-$version-$head.tar -C build -s dir -t tar -n replication-manager-osc .
gzip replication-manager-osc-$version-$head.tar
cp service/replication-manager-osc.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-osc .
rm -f build/usr/bin/replication-manager-osc

echo "# Building packages replication-manager-tst"
cp etc/* build/etc/replication-manager/
rm -f build/etc/replication-manager/config.toml.sample.opensvc.*
cp replication-manager-tst build/usr/bin/
cp service/replication-manager-tst.service build/etc/systemd/system/replication-manager.service
cp service/replication-manager-tst.init.el6 build/etc/init.d/replication-manager
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-tst .
fpm --package replication-manager-tst-$version-$head.tar -C build -s dir -t tar -n replication-manager-tst .
gzip replication-manager-tst-$version-$head.tar
cp service/replication-manager-tst.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-tst .
rm -f build/usr/bin/replication-manager-tst

echo "# Building packages replication-manager-pro"
cp etc/* build/etc/replication-manager/
cp service/replication-manager-pro.service build/etc/systemd/system/replication-manager.service
cp service/replication-manager-pro.init.el6 build/etc/init.d/replication-manager
cp test/opensvc build/usr/share/replication-manager/tests
cp replication-manager-pro build/usr/bin/
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-pro .
fpm --package replication-manager-pro-$version-$head.tar -C build -s dir -t tar -n replication-manager-pro .
gzip replication-manager-pro-$version-$head.tar
cp service/replication-manager-pro.init.deb7 build/etc/init.d/replication-manager
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-pro .
rm -f build/usr/bin/replication-manager-pro

echo "# Building packages abitrator"
rm -rf build/etc
rm -rf build/usr/share
cp service/replication-manager-arb.service build/etc/systemd/system
cp service/replication-manager-arb.init.el6 build/etc/init.d/replication-manager-arb
cp replication-manager-arb build/usr/bin/
fpm --rpm-os linux --epoch $epoch --iteration $head -v $version -C build -s dir -t rpm -n replication-manager-arbitrator .
fpm --package replication-manager-arbitrator-$version-$head.tar -C build -s dir -t tar -n replication-manager-arbitrator .
gzip replication-manager-arbitrator-$version-$head.tar
cp service/replication-manager-arb.init.deb7 build/etc/init.d/replication-manager-arbitrator
fpm --epoch $epoch --iteration $head -v $version -C build -s dir -t deb -n replication-manager-arbitrator .
rm -f build/usr/bin/replication-manager-arb
