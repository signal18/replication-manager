app.factory('Cluster', function ($resource) {
    return $resource('api/clusters/:clusterName', {clusterName: '@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        });
});

app.factory('Clusters', function ($resource) {
    return $resource('api/clusters',
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        });
});

app.factory('Servers', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/servers', {clusterName: '@clusters'});
});

app.factory('Proxies', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/proxies', {clusterName: '@clusters'});
});

app.factory('Slaves', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/slaves', {clusterName: '@clusters'});
});

app.factory('Processlist', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/processlist', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('Tables', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/tables', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('Status', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/status-delta', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('PFSStatements', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/pfs-statements', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('SlowQueries', function ($resource) {
  return $resource('api/clusters/:clusterName/servers/:serverName/slow-queries', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('Variables', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/variables', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('StatusInnoDB', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/status-innodb', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('ServiceOpenSVC', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/service-opensvc', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('Alerts', function ($resource) {
    return $resource(
        'api/clusters/:clusterName/topology/alerts', {clusterName: '@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Master', function ($resource) {
    return $resource(
        'api/clusters/:clusterName/topology/master', {clusterName: '@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Monitor', function ($resource) {
    return $resource(
        '/api/monitor',
        '', {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});


app.factory('Test', function ($resource) {
    return $resource(
        'api/tests',
        '', {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});
