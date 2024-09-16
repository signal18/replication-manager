FROM golang:1.23-bookworm AS builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .
RUN apt-get update
RUN apt-get -y install nodejs npm
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

RUN apt-get update && apt-get -y install mydumper ca-certificates restic mariadb-server=1:11* mariadb-client mariadb-plugin-spider haproxy libmariadb-dev fuse sysbench curl
RUN curl -LO https://github.com/sysown/proxysql/releases/download/v2.5.2/proxysql_2.5.2-debian11_amd64.deb && dpkg -i proxysql_2.5.2-debian11_amd64.deb && rm -f proxysql_2.5.2-debian11_amd64.deb \
  && apt-get install -y adduser libfontconfig1 && curl -LO https://dl.grafana.com/oss/release/grafana_8.1.1_amd64.deb && dpkg -i grafana_8.1.1_amd64.deb && rm -f grafana_8.1.1_amd64.deb \ 
  && rm -rf /var/lib/mysql/*

CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
