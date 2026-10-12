# MariaDB ColumnStore database image for replication-manager ColumnStore child clusters (#2002).
#
# The official mariadb image of the OLTP parent's line (a replica is never older than its
# primary), plus ColumnStore from the MariaDB repository the image already configures
# (11.8.9 ships ColumnStore 25.10.6), plus CMAPI. MariaDB's repository stopped carrying
# CMAPI after 11.8.8, which pairs ColumnStore 25.10.4 with CMAPI 23.10.0: that package
# is taken from the 11.8.8 pool, URL and checksum pinned (SHA256 of MariaDB's index).
ARG MARIADB_VERSION=11.8.9
FROM mariadb:${MARIADB_VERSION}

ARG CMAPI_URL=https://archive.mariadb.org/mariadb-11.8.8/repo/ubuntu/pool/main/m/mariadb-columnstore-cmapi/mariadb-columnstore-cmapi_23.10.0_amd64.deb
ARG CMAPI_SHA256=a4207e711460275db499fba727a3d876e7b2a64eadd8678d5de3124d098a3d90

RUN set -eux; \
    apt-get update; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends mariadb-plugin-columnstore curl; \
    curl -fsSL -o /tmp/cmapi.deb "$CMAPI_URL"; \
    echo "$CMAPI_SHA256  /tmp/cmapi.deb" | sha256sum -c -; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends /tmp/cmapi.deb; \
    rm -f /tmp/cmapi.deb; \
    rm -rf /var/lib/apt/lists/*

# 3306 SQL, 8600-8630 8700 8800 ColumnStore inter-node, 8640 CMAPI
EXPOSE 3306 8600-8630 8640 8700 8800
