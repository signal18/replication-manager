FROM golang:1.14-alpine as builder

RUN apk --no-cache --update add make git gcc musl-dev mariadb-client

RUN mkdir -p /go/src/github.com/signal18/replication-manager
WORKDIR /go/src/github.com/signal18/replication-manager

COPY . .

RUN make pro cli

FROM alpine:3 AS builder2

RUN mkdir -p /build

WORKDIR /build

RUN apk add --virtual .build-deps git build-base automake autoconf libtool mariadb-dev --update \
  && git clone https://github.com/akopytov/sysbench.git \
  && cd /build/sysbench \
  && ./autogen.sh \
  && ./configure --disable-shared \
  && make install \
  && apk del .build-deps

WORKDIR /build

RUN apk add -t build-depends build-base automake bzip2 patch git cmake openssl-dev zlib-dev libc6-compat libexecinfo-dev && \
      git clone https://github.com/sysown/proxysql.git && \
      cd /build/proxysql && \
      git checkout v1.4.12 && \
      make clean && \
      make build_deps && \
      NOJEMALLOC=1 make

WORKDIR /build

RUN export LIB_PACKAGES='glib mysql-client pcre' && \
    export BUILD_PACKAGES='glib-dev mariadb-dev zlib-dev pcre-dev libressl-dev cmake build-base' && \
    apk add --no-cache --update $LIB_PACKAGES $BUILD_PACKAGES && \
    git clone -b no-flush https://github.com/tanji/mydumper &&  \
    cd mydumper && \
    cmake . &&  \
    make && \
    apk del $BUILD_PACKAGES

FROM alpine:3

RUN mkdir -p \
        /etc/replication-manager \
        /var/lib/replication-manager

RUN mkdir /lib64 && ln -s /lib/libc.musl-x86_64.so.1 /lib64/ld-linux-x86-64.so.2

COPY --from=builder /go/src/github.com/signal18/replication-manager/share /usr/share/replication-manager/
COPY --from=builder /go/src/github.com/signal18/replication-manager/dashboard /usr/share/replication-manager/dashboard
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-pro /usr/bin/replication-manager
COPY --from=builder /go/src/github.com/signal18/replication-manager/build/binaries/replication-manager-cli /usr/bin/replication-manager-cli
COPY --from=builder2 /build/mydumper/mydumper /usr/bin/mydumper
COPY --from=builder2 /build/mydumper/myloader /usr/bin/myloader
COPY --from=builder2 /build/proxysql/src/proxysql /usr/bin/proxysql
COPY --from=builder2 /usr/local/share/sysbench /usr/local/share/sysbench
COPY --from=builder2 /usr/local/bin/sysbench /usr/bin/sysbench

RUN apk --no-cache --update add ca-certificates restic mariadb-client mariadb haproxy mariadb-dev fuse glib pcre openssl-dev zlib-dev libc6-compat libexecinfo-dev


CMD ["replication-manager", "monitor", "--http-server"]
EXPOSE 10001
