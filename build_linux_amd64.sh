#!/usr/bin/env sh
# These are the values we want to pass for VERSION and BUILD
# git tag 1.0.1
# git commit -am "One more change after the tags"
VERSION=$(git describe --abbrev=0 --tags)
FULLVERSION=$(git describe --tags)
BUILD=$(date +%FT%T%z)

BINARY=replication-manager-osc
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-pro
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-cli
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo clients" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-arb
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo arbitrator" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-min
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitration=OFF -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithMail=ON -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}

#BINARY=mrm-test
#env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitration=OFF -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithMail=ON -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}

#BINARY=mrm-cli
#env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo clients" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitration=OFF -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithMail=ON -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}
