#!/bin/bash
sysbench \
--test=/home/tanj/Dev/source/sysbench/sysbench/tests/db/oltp.lua \
--mysql-host=127.0.0.1 \
--mysql-port=4006 \
--mysql-user=root \
--mysql-password=admin \
--mysql-db=test \
--mysql-table-engine=innodb \
--mysql-ignore-errors=all \
--oltp-test-mode=complex \
--oltp-read-only=off \
--oltp-reconnect=on \
--oltp-tables-count=16 \
--oltp-table-size=100000 \
--max-requests=100000000 \
--num-threads=3 \
--max-time=60 \
--report-checkpoint=30 \
--report-interval=1 \
$1
