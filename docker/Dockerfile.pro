FROM golang:1.14-alpine as builder

RUN apk --no-cache --update add make git gcc musl-dev

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make pro cli


FROM alpine:3

RUN mkdir -p \
        /etc/replication-manager \
        /var/lib/replication-manager

RUN mkdir /lib64 && ln -s /lib/libc.musl-x86_64.so.1 /lib64/ld-linux-x86-64.so.2

COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/dashboard /usr/share/replication-manager/dashboard
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-pro /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli

RUN apk --no-cache --update add ca-certificates restic mariadb-client mariadb haproxy

CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
