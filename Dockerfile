FROM golang:1.6-alpine
RUN echo "@testing http://nl.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories
RUN apk add --update git glide@testing && rm -rf /var/cache/apk/*
RUN mkdir -p /go/src/replication-manager
WORKDIR /go/src/replication-manager
COPY . /go/src/replication-manager
RUN glide install
RUN go install .
RUN rm -rf /go/src/replication-manager
RUN mkdir /etc/replication-manager
COPY config.toml /etc/replication-manager/config.toml
WORKDIR /go/bin
ENTRYPOINT ["replication-manager"]
CMD ["monitor", "--daemon"]
