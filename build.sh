#!/usr/bin/env sh
BINARY=replication-manager

# These are the values we want to pass for VERSION and BUILD
# git tag 1.0.1
# git commit -am "One more change after the tags"
VERSION=`git describe --tags`
BUILD=`date +%FT%T%z`

go build -ldflags "-w -s -X main.Version=${VERSION} -X main.Build=${BUILD}" ${LDFLAGS} -o ${BINARY}
