FROM golang:1.22-bullseye as builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make pro cli

FROM debian:bullseye-slim

RUN mkdir -p \
        /etc/replication-manager \
        /etc/replication-manager/cluster.d \
        /var/lib/replication-manager


COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/config.toml.docker /etc/replication-manager/config.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/haproxy/config.toml /etc/replication-manager/cluster.d/localmasterslavehaproxy.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/proxysql/config.toml /etc/replication-manager/cluster.d/localmasterslaveproxysql.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-pro /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

RUN apt-get update && apt-get -y install mydumper ca-certificates restic mariadb-client mariadb-server mariadb-plugin-spider haproxy libmariadb-dev fuse sysbench curl
RUN curl -LO https://github.com/sysown/proxysql/releases/download/v2.5.2/proxysql_2.5.2-debian11_amd64.deb && dpkg -i proxysql_2.5.2-debian11_amd64.deb
RUN apt-get install -y adduser libfontconfig1 && curl -LO https://dl.grafana.com/oss/release/grafana_8.1.1_amd64.deb && dpkg -i grafana_8.1.1_amd64.deb
CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
