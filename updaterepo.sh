#!/bin/bash
echo '{"repos": [' > share/repo/repos.json

echo '{"name": "mariadb", "image": "mariadb", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/mariadb/tags >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "mariadb", "image": "mariadb/columnstore", "tags":' >> share/repo/repos.json
curl -s  https://registry.hub.docker.com/v1/repositories/mariadb/columnstore/tags  >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "mysql", "image": "mysql", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/mysql/tags >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "percona", "image": "percona", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/percona/tags >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "proxysql", "image": "proxysql/proxysql", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/proxysql/proxysql/tags >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "maxscale", "image": "mariadb/maxscale", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/mariadb/maxscale/tags >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "haproxy", "image": "haproxytech/haproxy-alpine", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/haproxy/tags  >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "sphinx", "image": "leodido/sphinxsearch", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/leodido/sphinxsearch/tags  >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "postgres", "image": "postgres", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v1/repositories/postgres/tags  >> share/repo/repos.json
echo '}' >> share/repo/repos.json

echo ']}' >> share/repo/repos.json
