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
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends mariadb-plugin-columnstore curl iproute2; \
    curl -fsSL -o /tmp/cmapi.deb "$CMAPI_URL"; \
    echo "$CMAPI_SHA256  /tmp/cmapi.deb" | sha256sum -c -; \
    # CMAPI's postinst registers a systemd service (systemctl), absent in a container:
    # a no-op /usr/bin/systemctl for that install only (dpkg runs maintainer scripts
    # with its own PATH, without /usr/local/bin); the container starts CMAPI itself
    test ! -e /usr/bin/systemctl; \
    printf '#!/bin/sh\nexit 0\n' > /usr/bin/systemctl; \
    chmod +x /usr/bin/systemctl; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends /tmp/cmapi.deb; \
    rm -f /usr/bin/systemctl /tmp/cmapi.deb; \
    rm -rf /var/lib/apt/lists/*

# the package's /var/lib/columnstore (local/module, the dbroot layout): a volume mounted
# there hides it, the entrypoint copies what the volume lacks (columnstore-entrypoint.sh)
# the same for /etc/columnstore (Columnstore.xml, CMAPI's configuration)
RUN cp -a /var/lib/columnstore /usr/share/columnstore/var-lib-columnstore && \
    cp -a /etc/columnstore /usr/share/columnstore/etc-columnstore && \
    # CMAPI runs as mysql (columnstore-entrypoint.sh) and writes its self-signed
    # certificate next to its code at first start
    chown -R mysql:mysql /usr/share/columnstore/cmapi/cmapi_server

# ColumnStore's processes before the server, without systemd (columnstore-entrypoint.sh)
COPY columnstore-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/columnstore-entrypoint.sh

# 3306 SQL, 8600-8630 8700 8800 ColumnStore inter-node, 8640 CMAPI
EXPOSE 3306 8600-8630 8640 8700 8800

ENTRYPOINT ["columnstore-entrypoint.sh"]
CMD ["mariadbd"]
