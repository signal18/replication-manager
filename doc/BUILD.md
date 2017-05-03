## Building from source

* Download a Go release (Go 1.6 minimum): https://golang.org/dl/ or use your system's release if >= 1.6
* Create a build directory: `mkdir -p ~/go`
* Point GOPATH environment variable to this directory: `export GOPATH=~/go`
* Clone the source: `go get github.com/tanji/replication-manager`
* Compile and install: `go install github.com/tanji/replication-manager`
* Add the go binaries directory to your path: `export PATH=$PATH:~/go/bin`
