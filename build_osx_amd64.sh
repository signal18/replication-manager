#!/usr/bin/env sh
BINARY=replication-manager

# These are the values we want to pass for VERSION and BUILD
# git tag 1.0.1
# git commit -am "One more change after the tags"
VERSION=$(git describe --abbrev=0 --tags)
FULLVERSION=$(git describe --tags)
BUILD=$(date +%FT%T%z)

env GOOS=darwin GOARCH=amd64  go build -a -v --tags netgo --ldflags "-w -s -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON" ${LDFLAGS} -o ${BINARY}
