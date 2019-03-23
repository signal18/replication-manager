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
