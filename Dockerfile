FROM golang:1.6-alpine
RUN apk add --update git && rm -rf /var/cache/apk/*
RUN mkdir -p /go/src/replication-manager
WORKDIR /go/src/replication-manager
COPY . /go/src/replication-manager
RUN go install .
RUN rm -rf /go/src/replication-manager
RUN mkdir /etc/replication-manager
COPY config.toml /etc/replication-manager/config.toml
WORKDIR /go/bin
CMD ["replication-manager", "monitor", "--daemon"] 
