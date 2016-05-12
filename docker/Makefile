org ?= mariadb
image ?= replication-manager
version ?= latest
name = $(org)/$(image):$(version)

runImage: replication-manager-alpine
	docker build --pull --file Dockerfile.run --tag $(name) ..
	rm -f replication-manager-alpine

replication-manager-alpine: builderImage
	docker run --rm -v $(CURDIR)/../:/go/src/replication-manager replication-manager:builder go build -v -o replication-manager-alpine

builderImage:
	docker build --pull --file Dockerfile.build --tag replication-manager:builder ..

pushToHub: runImage
	docker push $(name)

.PHONY: runImage builderImage pushToHub
