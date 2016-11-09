FROM ubuntu:latest

MAINTAINER tim@magnetic.io

# This Dockerfile does the basic install of vamp-router and Haproxy. Please see:
# https://github.com/magneticio/vamp-router
#
# HAproxy is currently version 1.5.3 build from source on Ubuntu with the following options
# apt-get install build-essential
# apt-get install libpcre3-dev
# make TARGET=linux26 ARCH=i386 USE_PCRE=1 USE_LINUX_SPLICE=1 USE_LINUX_TPROXY=1
#
#

ADD ./target/linux_i386/vamp-router /vamp-router

ADD ./configuration /configuration

ADD ./examples /examples

ADD ./target/linux_i386/haproxy /usr/sbin/haproxy

EXPOSE 80

EXPOSE 10001

EXPOSE 1988

ENTRYPOINT ["/vamp-router"]
