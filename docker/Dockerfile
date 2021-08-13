FROM golang:1.16-buster as builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make pro cli

FROM debian:buster-slim

RUN mkdir -p \
        /etc/replication-manager \
        /etc/replication-manager/cluster.d \
        /var/lib/replication-manager

COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/config.toml.docker /etc/replication-manager/config.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/haproxy/config.toml /etc/replication-manager/cluster.d/localmasterslavehaproxy.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/etc/local/masterslave/proxysql/config.toml /etc/replication-manager/cluster.d/localmasterslaveproxysql.toml
COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/dashboard /usr/share/replication-manager/dashboard
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-osc /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

RUN apt-get -y install ca-certificates restic mariadb-client haproxy

CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
