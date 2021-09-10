#!/bin/bash
set -e
cd docker/replication
docker-compose up -d db1 db2 db3 proxysql
cd -
