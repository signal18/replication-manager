app.factory('Cluster', function($resource) {
    return $resource('api/clusters/:clusterName',{clusterName:'@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        });
});

app.factory('Servers', function($resource) {
    return $resource('api/clusters/:clusterName/topology/servers',{clusterName:'@clusters'});
});

app.factory('Proxies', function($resource) {
    return $resource('api/clusters/:clusterName/topology/proxies',{clusterName:'@clusters'});
});

app.factory('Slaves', function($resource) {
  return $resource('api/clusters/:clusterName/topology/slaves',{clusterName:'@clusters'});
});

app.factory('Alerts', function($resource) {
    return $resource(
        'api/clusters/:clusterName/topology/alerts',{clusterName:'@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Master', function($resource) {
    return $resource(
        'api/clusters/:clusterName/topology/master',{clusterName:'@clusters'},
        {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Monitor', function($resource) {
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


app.factory('Test', function($resource) {
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

app.controller('DashboardController', ['$scope', '$routeParams', '$interval', '$http', '$location', '$mdSidenav', 'Servers', 'Monitor', 'Alerts', 'Master', 'Proxies', 'Slaves', 'Cluster', 'AppService',
    function($scope, $routeParams, $interval, $http, $location, $mdSidenav, Servers, Monitor, Alerts, Master, Proxies, Slaves, Cluster, AppService) {

   //Selected cluster is choose from the drop-down-list
   $scope.selectedClusterName = undefined;

   var getClusterUrl = function(){
       return AppService.getClusterUrl($scope.selectedClusterName);
   };

   $scope.isLoggedIn = AppService.hasAuthHeaders();
   if (!$scope.isLoggedIn){
       $location.path('login');
   }

   $scope.logout = function(){
     AppService.logout();
     $location.path('login');
   };

    var timeFrame = $routeParams.timeFrame;
    if (timeFrame == "") {
        timeFrame = "10m";
    }

    var refreshInterval = 2000;

    var callServices = function(){
        if (!AppService.hasAuthHeaders()) return;
        Monitor.query( {}, function(data) {
            if (data){
                $scope.settings = data;
                $scope.logs = data.logs.buffer;
                $scope.agents = data.agents;
            }
        }, function() {
            $scope.reserror = true;
        });

        if ($scope.selectedClusterName){
            Cluster.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.selectedCluster = data;
                $scope.reserror = false;
            }, function() {
                $scope.reserror = true;
            });

            Servers.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.servers = data;
                $scope.reserror = false;
            }, function() {
                $scope.reserror = true;
            });

            Alerts.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.alerts = data;
            }, function() {
                $scope.reserror = true;
            });

            Master.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.master = data;
            }, function() {
                $scope.reserror = true;
            });

            Proxies.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.proxies = data;
            }, function() {
                $scope.reserror = true;
            });

            Slaves.query({clusterName:$scope.selectedClusterName}, function(data) {
                $scope.slaves = data;
            }, function() {
                $scope.reserror = true;
            });
        }
    };

    $interval(function() {
        callServices();
    }, refreshInterval);

    $scope.selectedUserIndex = undefined;

    $scope.switch = function(fail) {
        if (fail == false) {
            var r = confirm("Confirm switchover");
            if (r == true) {
                var response = $http.get(getClusterUrl()+'/actions/switchover');
                response.success(function(data, status, headers, config) {
                    console.log("Ok.");
                });
                response.error(function(data, status, headers, config) {
                    console.log("Error.");
                });
            }
        } else {
            var r2 = confirm("Confirm failover");
            if (r2 == true) {
                var response2 = $http.get(getClusterUrl()+'/actions/failover');
                response2.success(function(data, status, headers, config) {
                    console.log("Ok.");
                });
                response2.error(function(data, status, headers, config) {
                    console.log("Error.");
                });
            }
        }
    };

    $scope.maintenance = function(server) {
        var r = confirm("Confirm maintenance for server-id: " + server);
        if (r == true) {
            var response = $http.get(getClusterUrl()+'/servers/'+server+'/actions/maintenance'  );
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };
    $scope.start = function(server) {
        var r = confirm("Confirm start for server-id: " + server);
        if (r == true) {
            var response = $http.get(getClusterUrl()+'/servers/'+server+'/actions/start' );
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };
    $scope.stop = function(server) {
        var r = confirm("Confirm stop for server-id: " + server);
        if (r == true) {
            var response = $http.get(getClusterUrl()+'/servers/'+server+'/actions/stop' );
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };




    $scope.toggletraffic = function() {
        var r = confirm("Confirm toggle traffic");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/settings/actions/switch/database-hearbeat');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.resetfail = function() {
        var r = confirm("Reset Failover counter?");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/actions/reset-failover-counter');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };


    $scope.setactive = function() {
        var r = confirm("Confirm Active Status?");
        if (r == true) {
            var response = $http.get('/api/setactive');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }

    };

    $scope.bootstrap = function() {
        var r = confirm("Bootstrap operation will destroy your existing replication setup. \n Are you really sure?");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/services/actions/bootstrap');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.unprovision = function() {
        var r = confirm("Unprovision operation will destroy your existing data. \n Are you really sure?");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/services/actions/unprovision');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.rolling = function() {
        var response = $http.get(getClusterUrl() + '/actions/rolling');
        response.success(function(data, status, headers, config) {
            console.log("Ok.");
        });

        response.error(function(data, status, headers, config) {
            console.log("Error.");
        });
    };


    $scope.gtidstring = function(arr) {
        var output = [];
        if (arr != null) {
            for (i = 0; i < arr.length; i++) {
                var gtid = "";
                gtid = arr[i].domainId + '-' + arr[i].serverId + '-' + arr[i].seqNo;
                output.push(gtid);
            }
            return output.join(",");
        }
        return '';
    };

    $scope.test = function() {
        var r = confirm("Confirm test run, this could cause replication to break!");
        if (r == true) {
            var response = $http.get('/api/tests');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };


    $scope.sysbench = function() {
        var r = confirm("Confirm sysbench run !");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/actions/sysbench');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.runonetest = function() {
        var r = confirm("Confirm run one test !");
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/tests/actions/run/' + $scope.tests);
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
            $scope.tests = "";
        }
    };

    $scope.optimizeAll = function() {

        var response = $http.get(getClusterUrl() + '/actions/optimize');
        response.success(function(data, status, headers, config) {
            console.log("Ok.");
        });
        response.error(function(data, status, headers, config) {
            console.log("Error.");
        });
    };

    $scope.backupphysical = function(server) {
      var r = confirm("Confirm master physical backup");
        var response = $http.get(getClusterUrl() + '/actions/master-physical-backup');
        response.success(function(data, status, headers, config) {
            console.log("Ok.");
        });
        response.error(function(data, status, headers, config) {
            console.log("Error.");
        });
    };

    $scope.optimize = function(server) {
        var r = confirm("Confirm optimize for server-id: " + server);
        if (r == true) {
            var response = $http.get(getClusterUrl() + '/servers/' + server + '/actions/optimize');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.switchsettings = function(setting) {

            var response = $http.get(getClusterUrl() + '/settings/actions/switch/' + setting );
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });

    };


    $scope.$watch('settings.maxdelay', function (newVal, oldVal) {
      if (typeof newVal != 'undefined') {
      var response = $http.get(getClusterUrl() + '/settings/actions/set/failover-max-slave-delay/' + newVal );
      response.success(function(data, status, headers, config) {
          console.log("Ok.");
      });
      response.error(function(data, status, headers, config) {
          console.log("Error.");
      });
      }
    });

    $scope.setsettings = function(setting,value) {

            var response = $http.get(getClusterUrl() + '/settings/actions/set/' + setting +'/'+value);
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });

    };

    $scope.selectUserIndex = function(index) {
      var r = confirm("Confirm select Index  " + index);
      if ($scope.selectedUserIndex !== index) {
        $scope.selectedUserIndex = index;
      }
      else {
        $scope.selectedUserIndex = undefined;
      }
    };

    $scope.toggleLeft = buildToggler('left');
    $scope.toggleRight = buildToggler('right');

    function buildToggler(componentId) {
      return function() {
        $mdSidenav(componentId).toggle();
      };
    }

}]);
