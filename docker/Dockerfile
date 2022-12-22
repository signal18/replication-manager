FROM nexus.adsrv.wtf/click/golang:1.19.2-202210270909 as builder

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make osc cli arb

FROM ubuntu:jammy-20221130

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ="EST"

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
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-arb /usr/bin/replication-manager-arb

RUN apt-get update -q && \
    apt-get install -yq \
    ca-certificates \
    restic \
    mariadb-client \
    haproxy \
    tzdata && \
    apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* /var/cache/apt/*

CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
