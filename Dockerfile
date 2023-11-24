FROM golang:1.20-bullseye as builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make osc cli

FROM debian:buster-slim

RUN apt-get update && apt-get -y --no-install-recommends install ca-certificates mariadb-client \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

RUN useradd repman --user-group \
    && mkdir -p /etc/replication-manager/cluster.d /var/lib/replication-manager \
    && chown repman:repman /etc/replication-manager \
    && chown repman:repman /var/lib/replication-manager

COPY --from=builder --chown=repman:repman /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder --chown=repman:repman /go/src/github.com/signal18/replication-manager/etc/local/config.toml.docker /etc/replication-manager/config.toml
COPY --from=builder --chown=repman:repman /go/src/github.com/signal18/replication-manager/etc/cluster.d/cluster1.toml.sample /etc/replication-manager/cluster.d/cluster1.toml
COPY --from=builder --chown=repman:repman /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-osc /usr/bin/replication-manager
COPY --from=builder --chown=repman:repman /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

USER repman
CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
