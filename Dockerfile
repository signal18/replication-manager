FROM golang:1.6-alpine
RUN apk add --update git && rm -rf /var/cache/apk/*
RUN mkdir -p /go/src/github.com/mariadb-corporation/replication-manager
WORKDIR /go/src/github.com/mariadb-corporation/replication-manager
COPY . /go/src/github.com/mariadb-corporation/replication-manager/
RUN go build .
RUN mkdir -p /etc/replication-manager && mkdir -p /usr/share/replication-manager/dashboard
COPY config.toml /etc/replication-manager/
COPY dashboard/* /usr/share/replication-manager/dashboard/
RUN rm -rf /go/src
WORKDIR /go/bin
ENTRYPOINT ["replication-manager"]
CMD ["monitor", "--daemon", "--http-server"]
EXPOSE 10001
