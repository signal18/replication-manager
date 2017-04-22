## Building from source

* Download a Go release (Go 1.6 minimum): https://golang.org/dl/ or use your system's release if >= 1.6
* Create directories where Go installs binaries: `mkdir -p ~/go ~/go/bin ~/go/src`
* Point GOPATH environment variable to the root of this directory: `export GOPATH=~/go`
* Add the go binaries directory to your path: `export PATH=$PATH:~/go/bin`
* Install _glide_ (https://github.com/Masterminds/glide), _git_ and _hg_
* Clone the source: `go get github.com/tanji/replication-manager`
* Enter the source directory: `cd ~/go/src/github.com/tanji/replication-manager/`
* Install dependencies with _glide_: `glide install`
* Install _replication-manager_: `go install`
