FROM golang:1.16-buster as builder

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

RUN apt-get update && apt-get -y install mydumper
RUN apt-get -y install ca-certificates restic mariadb-client haproxy libmariadb-dev fuse sysbench curl
RUN curl -LO https://github.com/sysown/proxysql/releases/download/v2.2.0/proxysql_2.2.0-debian10_amd64.deb && dpkg -i proxysql_2.2.0-debian10_amd64.deb
CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
