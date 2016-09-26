FROM alpine:3.4

# set env from golang container
ENV \
    GOPATH="/go" \
    PATH="/go/bin:/usr/local/go/bin:$PATH"

RUN mkdir -p /go/src/github.com/tanji/replication-manager
WORKDIR /go/src/github.com/tanji/replication-manager
COPY . /go/src/github.com/tanji/replication-manager/

RUN mkdir -p \
        /go/bin \
        /etc/replication-manager \
        /usr/share/replication-manager/dashboard

RUN \
    apk --no-cache --update add git go && \
    go install github.com/tanji/replication-manager && \
    apk --no-cache del git go && \
    rm -rf /go/src /go/pkg && \
    rm -rf /var/cache/apk/*

COPY etc/config.toml.sample /etc/replication-manager/
COPY dashboard/* /usr/share/replication-manager/dashboard/

WORKDIR /go/bin
ENTRYPOINT ["replication-manager"]
CMD ["monitor", "--daemon", "--http-server"]
EXPOSE 10001
