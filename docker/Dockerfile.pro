FROM golang:1.23-bookworm AS builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN apt-get update && apt-get -y install nodejs npm
RUN make pro cli

FROM debian:bookworm-slim

RUN mkdir -p \
        /etc/replication-manager \
        /etc/replication-manager/cluster.d \
        /var/lib/replication-manager

RUN apt-get update && apt-get -y  install apt-transport-https curl \
 && mkdir -p /etc/apt/keyrings && curl -o /etc/apt/keyrings/mariadb-keyring.pgp 'https://mariadb.org/mariadb_release_signing_key.pgp'

COPY docker/mariadb.sources /etc/apt/sources.list.d/mariadb.sources

COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/config.toml.docker /etc/replication-manager/config.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/haproxy/config.toml /etc/replication-manager/cluster.d/localmasterslavehaproxy.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/proxysql/config.toml /etc/replication-manager/cluster.d/localmasterslaveproxysql.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-pro /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

ENV PROXYSQL_VERSION=2.7.3
ENV MARIADB_VERSION=11.4
ENV MYDUMPER_VERSION=0.17.1-1

RUN curl -LsS https://r.mariadb.com/downloads/mariadb_repo_setup | bash -s -- --mariadb-server-version="mariadb-$MARIADB_VERSION"

# Move to top for better cache
RUN apt-get update && apt-get -y install ca-certificates restic mariadb-client mariadb-server mariadb-backup mariadb-plugin-spider haproxy wget openssh-client libmariadb-dev fuse sysbench curl vim libatomic1 libglib2.0 libpcre3 adduser libfontconfig1
RUN curl -LO https://github.com/sysown/proxysql/releases/download/v$PROXYSQL_VERSION/proxysql_$PROXYSQL_VERSION-debian12_amd64.deb && dpkg -i proxysql_$PROXYSQL_VERSION-debian12_amd64.deb
RUN curl -LO https://github.com/mydumper/mydumper/releases/download/v$MYDUMPER_VERSION/mydumper_$MYDUMPER_VERSION.bookworm_amd64.deb && dpkg -i mydumper_$MYDUMPER_VERSION.bookworm_amd64.deb
RUN curl -LO https://dl.grafana.com/oss/release/grafana_8.1.1_amd64.deb && dpkg -i grafana_8.1.1_amd64.deb && rm -f grafana_8.1.1_amd64.deb
RUN rm -rf /var/lib/mysql/*

CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
