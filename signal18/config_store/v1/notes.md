## Install notes

Requires some setup, download protoc: https://github.com/protocolbuffers/protobuf/releases and copy the bin to `/usr/local/bin` and put the include in `/usr/local/include`.

Then we need some generators for the Protobuffer compiler to generate Go output

```
go install google.golang.org/protobuf/cmd/protoc-gen-go
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc
```

Then we can compile it.

NB: the paths=source_relative for go and grpc are important else it'll end up with the entire package name again inside the path directory!

```
protoc -I=/usr/local/include/google/protobuf -I signal18/config_store/v1/ --go_opt=paths=source_relative --go_out=./config_store --go-grpc_opt=paths=source_relative --go-grpc_out=./config_store signal18/config_store/v1/config.proto
```