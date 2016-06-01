FROM golang:1.6-onbuild

RUN apt-get update -q \
	&& apt-get install -y --no-install-recommends mysql-client \
	&& rm -rf /var/lib/apt/lists/*
