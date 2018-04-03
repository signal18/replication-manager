FROM golang:1.9-alpine3.7

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

RUN mkdir -p \
        /etc/replication-manager \
        /usr/share/replication-manager/dashboard \
        /var/lib/replication-manager

RUN \
    apk --no-cache --update add make git musl-dev && \
    rm -rf /var/cache/apk/*

COPY . .

RUN make osc cli

COPY dashboard /usr/share/replication-manager/dashboard/

RUN mv build/binaries/replication-manager-osc /go/bin/replication-manager \
    && mv build/binaries/replication-manager-cli /go/bin/

WORKDIR /go/bin

COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/dashboard /usr/share/replication-manager/dashboard
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-osc /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

CMD ["replication-manager","monitor","--http-server"]
EXPOSE 10001
