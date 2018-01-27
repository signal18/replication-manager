![replication-manager](https://github.com/signal18/replication-manager/raw/2.0/dashboard/static/logo.png)

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

The container runs with http-server enabled by default and exposed on port 10001. It does not provide a default configuration file, since Replication Manager doesn't work well if you don't provide your own configuration. Therefore, you should at least mount a minimal config file. Please refer to our docs or to the source repository for working examples.

Example usage, deploying a container with a config file in the working directory:
```
docker run -d -p 10001:10001 -v $(pwd)/config.toml:/etc/replication-manager/config.toml --name repman signal18/replication-manager:2.0
```

The container also includes the replication-manager client. You can run commands non-interactively such as:
```
docker exec -ti repman replication-manager-cli switchover
```

#### Production Deployments

As Replication Manager is a network application, it is wise to deploy it in existing Docker installations with user-defined networks, using orchestrators such as Compose, Kubernetes or Swarm.

The source repository provides a [working example](https://github.com/signal18/replication-manager/blob/2.0/test/docker/replication/docker-compose.yml) for Compose.

#### [Documentation](https://docs.signal18.io)

#### License

__replication-manager__ is released under the GPLv3 license. ([complete licence text](https://github.com/signal18/replication-manager/blob/master/LICENSE))

It includes third-party libraries released under their own licences. Please refer to the `vendor` directory for more information.

It also includes derivative work from the `go-carbon` library by Roman Lomonosov, released under the MIT licence and found under the `graphite` directory. The original library can be found here: https://github.com/lomik/go-carbon

#### Copyright and Support

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL 18 SARL](https://signal18.io/products).
