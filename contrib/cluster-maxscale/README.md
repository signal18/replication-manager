# docker-compose to test maxscale with replication-manager

## Getting started

```sh
docker-compose up -d
```

* Some services may (must) have fail
```sh
docker-compose ps
```

* Bootstrap cluster
```sh
docker-compose run --rm replication-manager bootstrap --hosts=mariadb1,mariadb2,mariadb3 --user=root --rpluser=repl:pass --verbose
```
Cluster is now ready

* Restart _maxscale_
```sh
docker-compose restart maxscale
```
Maxscale is now ready to receive requests
