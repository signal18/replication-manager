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
    return $resource('api/clusters');
});

app.factory('Servers', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/servers', {clusterName: '@clusters'});
});

app.factory('Proxies', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/proxies', {clusterName: '@clusters'});
});

app.factory('Backups', function ($resource) {
    return $resource('api/clusters/:clusterName/backups', {clusterName: '@clusters'});
});

app.factory('Certificates', function ($resource) {
    return $resource('api/clusters/:clusterName/certificates', {clusterName: '@clusters'});
});

app.factory('GraphiteFilterList', function ($resource) {
    return $resource('api/clusters/:clusterName/graphite-filterlist', {clusterName: '@clusters'});
});

app.factory('QueryRules', function ($resource) {
    return $resource('api/clusters/:clusterName/queryrules', {clusterName: '@clusters'});
});

app.factory('Slaves', function ($resource) {
    return $resource('api/clusters/:clusterName/topology/slaves', {clusterName: '@clusters'});
});

app.factory('Processlist', function ($resource) {
    return $resource('api/clusters/:clusterName/top', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('Tables', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/tables', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('VTables', function ($resource) {
    return $resource('api/clusters/:clusterName/schema', {clusterName: '@clusters'});
});

app.factory('Status', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/status-delta', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('PFSStatements', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/digest-statements-pfs', {clusterName: '@clusters',serverName: '@server'});
});

app.factory('PFSStatementsSlowLog', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/digest-statements-slow', {clusterName: '@clusters',serverName: '@server'});
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

app.factory('MetaDataLocks', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/meta-data-locks', {clusterName: '@clusters',serverName: '@server',queryDigest: '@digest'});
});

app.factory('QueryResponseTime', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/query-response-time', {clusterName: '@clusters',serverName: '@server',queryDigest: '@digest'});
});

app.factory('ExplainPlanPFS', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/queries/:queryDigest/actions/explain-pfs', {clusterName: '@clusters',serverName: '@server',queryDigest: '@digest'});
});

app.factory('ExplainPlanSlowLog', function ($resource) {
    return $resource('api/clusters/:clusterName/servers/:serverName/queries/:queryDigest/actions/explain-slowlog', {clusterName: '@clusters',serverName: '@server',queryDigest: '@digest'});
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

app.factory('Jobs', ['$resource', '$q', function($resource, $q) {
    var resource = $resource('api/clusters/:clusterName/jobs', {
      clusterName: '@clusterName'
    }, {
      get: {
        method: 'GET',
        isArray: false
      }
    });

    var pendingRequests = [];

    var service = {
      get: function(clusterName) {
        var deferred = $q.defer();
        var request = resource.get({ clusterName: clusterName }, function(data) {
          deferred.resolve(data);
        }, function(error) {
          deferred.reject(error);
        });
        pendingRequests.push(request);
        deferred.promise.finally(function() {
          var index = pendingRequests.indexOf(request);
          if (index !== -1) {
            pendingRequests.splice(index, 1);
          }
        });
        return deferred.promise;
      },
      cancelPendingRequests: function() {
        pendingRequests.forEach(function(request) {
          request.$cancel();
        });
        pendingRequests = [];
      }
    };

    return service;
  }]);
