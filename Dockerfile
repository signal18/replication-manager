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

RUN make osc && make cli

COPY dashboard /usr/share/replication-manager/dashboard/

RUN mv build/binaries/replication-manager-osc /go/bin/replication-manager \
    && mv build/binaries/replication-manager-cli /go/bin/

WORKDIR /go/bin

RUN rm -rf /go/src /go/pkg && apk --no-cache del make git musl-dev
RUN mkdir /lib64 && ln -s /lib/libc.musl-x86_64.so.1 /lib64/ld-linux-x86-64.so.2

CMD ["replication-manager","monitor","--http-server"]
EXPOSE 10001
