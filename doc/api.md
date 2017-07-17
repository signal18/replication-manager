The rest API is using JWT TLS and is served by default on port 3000 by the  replication-manager monitor

One can customize and credential via setting your own user password in config file  

```
api-port ="3000"
api-user = "admin:mariadb"
```

At startup of the monitor some x509 certificates are loaded from the replication-manager share directory to ensure TLS https secure communication.

Replace those files with your own certificate to make sure your deployment is secured.

```
# Key considerations for algorithm "RSA" ≥ 2048-bit
openssl genrsa -out server.key 2048

# Key considerations for algorithm "ECDSA" ≥ secp384r1
# List ECDSA the supported curves (openssl ecparam -list_curves)
openssl ecparam -genkey -name secp384r1 -out server.key
openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650
```

At startup replication-manager monitor will generate in memory extra self signed RSA certificate to ensure token encryption exchange for JWT   

# Calling API via client

API can be call via command line client to simplify curl syntax with JWT token

```
./replication-manager api  --url="https://127.0.0.1:3000/api/clusters/ux_dck_zpool_loop/servers/actions/add/192.168.1.73/3306"   --cluster="ux_dck_zpool_loop"
```

# API unprotected endpoints

/api/login

INPUT:
```
{"username":"admin", "password":"mariadb"}
```
OUTPUT:
```
{"token":"hash"}
```

/api/clusters

OUPUT:
```
{"clusters":["ux_pkg_lvm_loop","ux_dck_zpool_loop","ux_dck_lvm_loop","ux_dck_nopool_loop","ux_pkg_nopool_loop","osx_pkg_nopool_loop","osx_dck_nopool_loop"]}
/api/status todo
```

/api/status

OUPUT:
```
{"alive": true}      
./replication-manager api  --url="https://127.0.0.1:3000/api/status"  
```

# API protected endpoints

/api/clusters/{clusterName}/actions/switchover

/api/clusters/{clusterName}/actions/failover

/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}

/api/clusters/{clusterName}/actions/replication/cleanup

/api/clusters/{clusterName}/actions/services todo

/api/clusters/{clusterName}/actions/start-traffic todo

/api/clusters/{clusterName}/actions/stop-traffic todo

List agents services resources

/api/clusters/{clusterName}/actions/services/bootstrap

/api/clusters/{clusterName}/servers/actions/add/{host}/{port}

/api/clusters/{clusterName}/servers/{serverName}/actions/start

/api/clusters/{clusterName}/servers/{serverName}/actions/stop

/api/clusters/{clusterName}/servers/{serverName}/actions/backup todo

/api/clusters/{clusterName}/servers/{serverName}/actions/maintenance todo

/api/clusters/{clusterName}/topology/servers

/api/clusters/{clusterName}/topology/master

/api/clusters/{clusterName}/topology/slaves

/api/clusters/{clusterName}/topology/proxies

/api/clusters/{clusterName}/topology/logs

/api/clusters/{clusterName}/topology/alerts

/api/clusters/{clusterName}/topology/crashes

/api/clusters/{clusterName}/tests

/api/clusters/{clusterName}/tests/actions/run/{testName}

/api/clusters/{clusterName}/settings

/api/clusters/{clusterName}/settings/reload

/api/clusters/{clusterName}/settings/switch/interactive

/api/clusters/{clusterName}/settings/switch/readonly

/api/clusters/{clusterName}/settings/switch/verbosity

/api/clusters/{clusterName}/settings/switch/autorejoin

/api/clusters/{clusterName}/settings/switch/rejoinflashback

/api/clusters/{clusterName}/settings/switch/rejoinmysqldump

/api/clusters/{clusterName}/settings/switch/failoversync

/api/clusters/{clusterName}/settings/switch/swithoversync

/api/clusters/{clusterName}/settings/reset/failovercontrol
