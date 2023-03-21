#!/bin/bash
echo '{"repos": [' > share/repo/repos.json

echo '{"name": "mariadb", "image": "mariadb", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/library/mariadb/tags/?page_size=1000 >> share/repo/repos.json
echo '},' >> share/repo/repos.json

# this repo dont exist
#echo '{"name": "mariadb", "image": "mariadb/columnstore", "tags":' >> share/repo/repos.json
#curl -s  https://registry.hub.docker.com/v2/repositories/mariadb/columnstore/tags/?page_size=1000  >> share/repo/repos.json
#echo '},' >> share/repo/repos.json

echo '{"name": "mysql", "image": "mysql", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/library/mysql/tags/?page_size=1000 >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "percona", "image": "percona", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/library/percona/tags/?page_size=1000 >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "proxysql", "image": "proxysql/proxysql", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/proxysql/proxysql/tags/?page_size=1000 >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "maxscale", "image": "mariadb/maxscale", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/mariadb/maxscale/tags/?page_size=1000 >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "haproxy", "image": "haproxytech/haproxy-alpine", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/library/haproxy/tags/?page_size=1000  >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "sphinx", "image": "leodido/sphinxsearch", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/leodido/sphinxsearch/tags/?page_size=1000  >> share/repo/repos.json
echo '},' >> share/repo/repos.json

echo '{"name": "postgres", "image": "postgres", "tags":' >> share/repo/repos.json
curl -s https://registry.hub.docker.com/v2/repositories/library/postgres/tags/?page_size=1000  >> share/repo/repos.json
echo '}' >> share/repo/repos.json

echo ']}' >> share/repo/repos.json
