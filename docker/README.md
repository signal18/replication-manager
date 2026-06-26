![replication-manager](https://github.com/signal18/replication-manager/raw/develop/share/dashboard/static/logo.png)

__replication-manager__ is a high availability solution to manage MariaDB 10.x and MySQL & Percona Server 5.7 replication topologies.  

The main features are:
 * Replication monitoring
 * Topology detection
 * Slave to master promotion (switchover)
 * Master election on failure detection (failover)
 * Replication best practice enforcement
 * Target to up to zero loss in most failure scenarios
 * Multiple cluster management
 * Proxy integration (ProxySQL, MaxScale, HAProxy, Spider)

#### Quick start

The container runs with http-server enabled by default and exposed on port 10001. You can mount your own configuration file, or layer cluster-specific settings on top of the bundled default config using `REPLICATION_MANAGER_*` environment variables (env vars take precedence over file values).

Example usage, deploying a container with a config file in the working directory:
```
docker run -d -p 10001:10001 -v $(pwd)/config.toml:/etc/replication-manager/config.toml --name repman signal18/replication-manager:latest
```

Example usage, overriding cluster settings via environment variables on top of the bundled config:
```
docker run -d -p 10001:10001 \
  -e REPLICATION_MANAGER_DEFAULT_HTTP_SERVER=true \
  -e REPLICATION_MANAGER_DEFAULT_HTTP_BIND_ADDRESS=0.0.0.0 \
  -e REPLICATION_MANAGER_DEFAULT_HTTP_PORT=10001 \
  -e REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_HOSTS=db1,db2 \
  -e REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_CREDENTIAL=root:admin \
  -e REPLICATION_MANAGER_CLUSTER1_REPLICATION_CREDENTIAL=root:admin \
  --name repman signal18/replication-manager:2.0
```

#### Auto-creating a missing config.toml (opt-in)

By default the container behaves exactly as before. If you mount a directory over `/etc/replication-manager` and that directory is **empty**, you can ask the container to seed a working default `config.toml` by setting:

```
REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true
```

Example:
```
docker run -d -p 10001:10001 \
  -e REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true \
  -v /my/empty/config-dir:/etc/replication-manager \
  --name repman signal18/replication-manager:latest
```

**Guardrails — the feature only acts when ALL of the following are true:**
1. `REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true` is set explicitly.
2. `/etc/replication-manager/config.toml` does not already exist.
3. The bundled fallback template exists in the image (`/usr/share/replication-manager/config.toml.default`).
4. `/etc/replication-manager/` is writable at container start time.

If any condition is false, the container starts normally with no file operations performed. **Existing configs are never overwritten or modified** — this includes regular files, directories, symlinks, and broken symlinks at the config path. Conditions 1 and 2 are silent no-ops (normal operation); conditions 3 and 4 log a message to stderr before continuing.

The seeded template is the Docker-specific default (`etc/local/config.toml.docker`), which pre-fills binary paths for the tools bundled in the image so the container starts without additional configuration.

**Rootless images (`*-rootless` tags):** the entrypoint runs as UID 10001 (`repman`). For auto-create to succeed, the mounted directory must be writable by that user:
```
sudo chown 10001:10001 /my/empty/config-dir
docker run -d -p 10001:10001 \
  -e REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true \
  -v /my/empty/config-dir:/etc/replication-manager \
  --name repman signal18/replication-manager:latest-rootless
```
If the directory is not writable, auto-create is skipped with a log message and the container starts normally.

The container also includes the replication-manager client. You can run commands non-interactively such as:
```
docker exec -ti repman replication-manager-cli switchover
```

#### Production Deployments

As Replication Manager is a network application, it is wise to deploy it in existing Docker installations with user-defined networks, using orchestrators such as Compose, Kubernetes or Swarm.

The source repository provides a [working example](https://github.com/signal18/replication-manager/blob/develop/share/tests/docker/replication/docker-compose.yml) for Compose.

#### [Documentation](https://docs.signal18.io)

#### License

__replication-manager__ is released under the GPLv3 license. ([complete licence text](https://github.com/signal18/replication-manager/blob/master/LICENSE))

It includes third-party libraries released under their own licences. Please refer to the `vendor` directory for more information.

It also includes derivative work from the `go-carbon` library by Roman Lomonosov, released under the MIT licence and found under the `graphite` directory. The original library can be found here: https://github.com/lomik/go-carbon

#### Copyright and Support

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL 18 SARL](https://signal18.io/products).
