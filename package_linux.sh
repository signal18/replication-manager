#!/bin/bash

# exit on error
set -e

nobuild=0
if [ "$1" != "" ]; then
  if [ "$1" = "--no-build" ]; then
    nobuild=1
  fi
fi

echo "# Getting branch info"
git status -bs

builddir="$(pwd)"/build
mkdir -p "$builddir"/binaries "$builddir"/package "$builddir"/tar "$builddir"/release

version=$(git describe --tag --abbrev=4 | sed 's/^v//')
head=$(git rev-parse --short HEAD)
epoch=$(date +%s)
release=1
description="Replication Manager for MariaDB and MySQL"
maintainer="info@signal18.io"
license="GPLv3"
architecture=${architecture:-amd64}

if [ $nobuild -eq 0 ]; then
  echo "# Building for $architecture"
  ARCH=$architecture make
fi

export version

echo "# Cleaning up previous builds"
rm -rf "$builddir"/package/* "$builddir"/tar/* "$builddir"/release/*
mkdir -p "$builddir"/package/usr/bin

echo "# Building packages"
for type in deb rpm; do
  for config in client osc prov arbitrator; do
    nfpm pkg --packager $type --config nfpm/$config.yaml --target build/release/
  done
done

echo "# Build complete"
rm -rf "$builddir"/package/
