FROM golang:1.6-alpine
RUN apk add --update git && rm -rf /var/cache/apk/*
RUN mkdir -p /go/src/replication-manager
WORKDIR /go/src/replication-manager
COPY . /go/src/replication-manager
RUN go get .
CMD ["replication-manager"] 
