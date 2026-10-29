#!/bin/sh
# Builds sysbench from upstream at a pinned commit into $DESTDIR (default /sysbench), for the
# repman images to copy its /usr tree. The Debian package (1.0.20) cannot connect with TLS
# without a client certificate, read from fixed file names in its working directory: a
# server that trusts no client certificate (MariaDB's automatic TLS) refuses it, and with
# require_secure_transport every benchmark fails. Upstream (1.1) takes --mysql-ssl=on with
# optional --mysql-ssl-ca/cert/key. Built against libmariadb (no SSL mode option: on/off), with
# the MySQL and PostgreSQL drivers, LuaJIT bundled.
set -eu
SYSBENCH_COMMIT=${SYSBENCH_COMMIT:-3ceba0b1e115f8c50d1d045a4574d8ed643bd497}
DESTDIR=${DESTDIR:-/sysbench}

apt-get update
apt-get -y install --no-install-recommends ca-certificates curl make automake libtool pkg-config \
  gcc g++ libaio-dev libmariadb-dev libmariadb-dev-compat libpq-dev python3
mkdir -p /src && cd /src
curl -fsSL "https://github.com/akopytov/sysbench/archive/$SYSBENCH_COMMIT.tar.gz" | tar xz --strip-components=1
./autogen.sh
./configure --prefix=/usr --with-mysql --with-pgsql
make -j"$(nproc)"
make install DESTDIR="$DESTDIR"
"$DESTDIR/usr/bin/sysbench" --version
