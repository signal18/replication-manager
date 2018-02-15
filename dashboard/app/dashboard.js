app.factory('Servers', function($resource) {
    return $resource('/clusters/:clusterName/topology/servers',{clusterName:'@clusters'});
});

app.factory('Proxies', function($resource) {
    return $resource('/clusters/:clusterName/topology/proxies',{clusterName:'@clusters'});
});

app.factory('Slaves', function($resource) {
  return $resource('/clusters/:clusterName/topology/slaves',{clusterName:'@clusters'});
});

app.factory('Log', function($resource) {
    return $resource('/log');
});

app.factory('Agents', function($resource) {
    return $resource('/agents');
});

app.factory('Settings', function($resource) {
    return $resource(
        '/settings',
        '', {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Alerts', function($resource) {
    return $resource(
        '/clusters/:clusterName/topology/alerts',{clusterName:'@clusters'},
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
        '/clusters/:clusterName/topology/master',{clusterName:'@clusters'},
       {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.factory('Test', function($resource) {
    return $resource(
        '/tests',
        '', {
            'query': {
                method: 'GET',
                isArray: false
            }
        }
    );
});

app.controller('DashboardController', ['$scope', '$routeParams', '$interval', '$http', 'Servers', 'Log', 'Settings', 'Alerts', 'Master', 'Agents', 'Proxies', 'Slaves', 'AppService',
    function($scope, $routeParams, $interval, $http, Servers, Log, Settings, Alerts, Master, Agents, Proxies, Slaves, AppService) {

    var timeFrame = $routeParams.timeFrame;
    if (timeFrame == "") {
        timeFrame = "10m";
    }

        var refreshInterval = 2000;

        $interval(function() {
            Settings.query( {}, function(data) {
                $scope.settings = data;
            }, function(error) {
                $scope.reserror = true;
            });
            Servers.query({clusterName:$scope.clusters}, function(data) {
                $scope.servers = data;
                $scope.reserror = false;
            }, function(error) {
                $scope.reserror = true;

            });
            Log.query({}, function(data) {
                $scope.log = data;
            }, function(error) {
                $scope.reserror = true;

            });
            Agents.query({}, function(data) {
                $scope.agents = data;
            }, function(error) {
                $scope.reserror = true;
            });

            Alerts.query({clusterName:$scope.clusters}, function(data) {
                $scope.alerts = data;
            }, function(error) {
                $scope.reserror = true;
            });
            Master.query({clusterName:$scope.clusters}, function(data) {
                $scope.master = data;
            }, function(error) {
                $scope.reserror = true;
            });
            Proxies.query({clusterName:$scope.clusters}, function(data) {
                $scope.proxies = data;
            }, function(error) {
                $scope.reserror = true;
            });

            Slaves.query({clusterName:$scope.clusters}, function(data) {
                $scope.slaves = data;
            }, function(error) {
                $scope.reserror = true;
            });
        }, refreshInterval);

    $scope.selectedUserIndex = undefined;

    $scope.switch = function(fail) {
        if (fail == false) {
            var r = confirm("Confirm switchover");
            if (r == true) {
                var response = $http.get('/clusters/'+$scope.clusters+'/actions/switchover');
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
                var response2 = $http.get('/clusters/'+$scope.clusters+'/actions/failover');
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
            var response = $http.get('/clusters/'+$scope.clusters+'/servers/'+server+'/actions/maintenance'  );
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
            var response = $http.get('/clusters/'+$scope.clusters+'/servers/'+server+'/actions/start' );
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
            var response = $http.get('/clusters/'+$scope.clusters+'/servers/'+server+'/actions/stop' );
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
            var response = $http.get('/clusters/' + $scope.clusters + '/settings/actions/switch/database-hearbeat');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/actions/reset-failover-counter');
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
            var response = $http.get('/setactive');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/services/actions/bootstrap');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/services/actions/unprovision');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });

            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.rolling = function() {
        var response = $http.get('/clusters/' + $scope.clusters + '/actions/rolling');
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
                gtid = arr[i].DomainID + '-' + arr[i].ServerID + '-' + arr[i].SeqNo;
                output.push(gtid);
            }
            return output.join(",");
        }
        return '';
    };

    $scope.test = function() {
        var r = confirm("Confirm test run, this could cause replication to break!");
        if (r == true) {
            var response = $http.get('/tests');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/actions/sysbench');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/tests/actions/run/' + $scope.tests);
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
            $scope.tests = "";
        }
    };

    $scope.setcluster = function() {

        var response = $http.get('/setcluster?cluster=' + $scope.clusters);
        response.success(function(data, status, headers, config) {
            console.log("Ok.");
        });
        response.error(function(data, status, headers, config) {
            console.log("Error.");
        });
    };

    $scope.optimize = function() {

        var response = $http.get('/clusters/' + $scope.clusters + '/actions/optimize');
        response.success(function(data, status, headers, config) {
            console.log("Ok.");
        });
        response.error(function(data, status, headers, config) {
            console.log("Error.");
        });
    };

    $scope.backupphysical = function(server) {
      var r = confirm("Confirm master physical backup");
        var response = $http.get('/clusters/' + $scope.clusters + '/actions/backupphysical');
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
            var response = $http.get('/clusters/' + $scope.clusters + '/servers/' + server + '/optimize');
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });
        }
    };

    $scope.switchsettings = function(setting) {

            var response = $http.get('/clusters/' + $scope.clusters + '/settings/actions/switch/' + setting );
            response.success(function(data, status, headers, config) {
                console.log("Ok.");
            });
            response.error(function(data, status, headers, config) {
                console.log("Error.");
            });

    };


    $scope.$watch('settings.maxdelay', function (newVal, oldVal) {
      if (typeof newVal != 'undefined') {
      var response = $http.get('/clusters/' + $scope.clusters + '/settings/actions/set/failover-max-slave-delay/' + newVal );
      response.success(function(data, status, headers, config) {
          console.log("Ok.");
      });
      response.error(function(data, status, headers, config) {
          console.log("Error.");
      });
      }
    });

    $scope.setsettings = function(setting,value) {

            var response = $http.get('/clusters/' + $scope.clusters + '/settings/actions/set/' + setting +'/'+value);
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

}]);
