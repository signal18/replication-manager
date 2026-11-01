module github.com/signal18/replication-manager/cluster/archiveplugin/plugins/pbs

go 1.26.10

require (
	github.com/alphadose/haxmap v1.4.1
	github.com/dchest/siphash v1.2.3
	github.com/klauspost/compress v1.18.7
	github.com/signal18/replication-manager v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.57.0
	golang.org/x/net v0.60.0
)

require (
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace github.com/signal18/replication-manager => ../../../..
