var routeProvider,app = angular.module('dashboard', ['ngResource','ngMaterial','ngRoute']).config(function($routeProvider){
        routeProvider = $routeProvider;
        $routeProvider
            .when('/:timeFrame', {templateUrl: '/', controller: 'DashboardController'})

        .otherwise({redirectTo: '/login'});

});


app.factory('Servers', function($resource) {
  return $resource('/servers');
});


app.factory('Log', function($resource) {
  return $resource('/log');
});

app.factory('Settings', function($resource) {
  return $resource(
    '/settings',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});

app.factory('Alerts', function($resource) {
  return $resource(
  '/alerts' ,
  '',
  { 'query':  {method:'GET', isArray:false} }
);
});

app.factory('Master', function($resource) {
  return $resource(
    '/master',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});

app.factory('Test', function($resource) {
  return $resource(
    '/tests',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});

app.factory('Sysbench', function($resource) {
  return $resource(
    '/sysbench',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});

app.factory('SetCluster', function($resource) {
  return $resource(
    '/setcluster',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});

app.factory('Bootstrap', function($resource) {
  return $resource(
    '/bootstrap',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});


app.controller('DashboardController', ['$scope', '$routeParams','$interval', '$http', 'Servers', 'Log', 'Settings','Alerts', 'Master', function ($scope,$routeParams,$interval, $http,Servers, Log, Settings,Alerts, Master) {

   var timeFrame = $routeParams.timeFrame;
   if (timeFrame=="") {
     timeFrame="10m"
    }

  $interval(function(){
  Servers.query({}, function(data) {
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
  Settings.query({}, function(data) {
    $scope.settings = data;
  }, function(error) {
    $scope.reserror = true;
  });
  Alerts.query({}, function(data) {
    $scope.alerts = data;
  }, function(error) {
    $scope.reserror = true;
  });
  Master.query({}, function(data) {
    $scope.master = data;
  }, function(error) {
    $scope.reserror = true;

  });
}, 1000);
$scope.switch = function(fail) {
  if (fail == false) {
    var r = confirm("Confirm switchover");
    if (r == true) {
      var response = $http.get('/switchover');
      response.success(function(data, status, headers, config) {
          console.log("Ok.");
      });
      response.error(function(data, status, headers, config) {
          console.log("Error.");
      });
    }
  }
  else {
    var r = confirm("Confirm failover");
    if (r == true) {
      var response = $http.get('/failover');
      response.success(function(data, status, headers, config) {
          console.log("Ok.");
      });
      response.error(function(data, status, headers, config) {
          console.log("Error.");
      });
    }
  }
};

$scope.maintenance = function(server) {
  var r = confirm("Confirm maintenance for server-id: "+server);
  if (r == true) {
    var response = $http.get('/maintenance?server='+server);
    response.success(function(data, status, headers, config) {
        console.log("Ok.");
    });
    response.error(function(data, status, headers, config) {
        console.log("Error.");
    });
  }
};
$scope.start = function(server) {
  var r = confirm("Confirm start for server-id: "+server);
  if (r == true) {
    var response = $http.get('/start?server='+server);
    response.success(function(data, status, headers, config) {
        console.log("Ok.");
    });
    response.error(function(data, status, headers, config) {
        console.log("Error.");
    });
  }
};
$scope.stop = function(server) {
  var r = confirm("Confirm stop for server-id: "+server);
  if (r == true) {
    var response = $http.get('/stop?server='+server);
    response.success(function(data, status, headers, config) {
        console.log("Ok.");
    });
    response.error(function(data, status, headers, config) {
        console.log("Error.");
    });
  }
};


$scope.inttoggle = function() {
var r = confirm("Confirm Mode Change");
if (r == true) {
  var response = $http.get('/interactive');
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
  var response = $http.get('/resetfail');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });

  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.rplchecks = function() {
var r = confirm("Confirm Change Replication Checks?");
if (r == true) {
  var response = $http.get('/rplchecks');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });

  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.failsync = function() {
var r = confirm("Confirm Change Sync Checks?");
if (r == true) {
  var response = $http.get('/failsync');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });

  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }

};

$scope.verbose = function() {
var r = confirm("Confirm Verbosity Change?");
if (r == true) {
  var response = $http.get('/setverbosity');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });

  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }

};

$scope.switchsync = function() {
var r = confirm("Confirm Change Switchover Sync Checks?");
if (r == true) {
  var response = $http.get('/switchsync');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.setrejoin = function() {
var r = confirm("Confirm Change Auto Rejoin ?");
if (r == true) {
  var response = $http.get('/setrejoin');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};


$scope.setrejoinbackupbinlog = function() {
var r = confirm("Confirm Change Auto backup binlogs ?");
if (r == true) {
  var response = $http.get('/setrejoinbackupbinlog');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};


$scope.setrejoinsemisync = function() {
var r = confirm("Confirm Change Auto Rejoin Semisync SYNC  ?");
if (r == true) {
  var response = $http.get('/setrejoinsemisync');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.setrejoinflashback = function() {
var r = confirm("Confirm Change Auto Rejoin FlashBack ?");
if (r == true) {
  var response = $http.get('/setrejoinflashback');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.setrejoindump = function() {
var r = confirm("Confirm Change Auto Rejoin FlashBack ?");
if (r == true) {
  var response = $http.get('/setrejoindump');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });
  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.settest = function() {
var r = confirm("Confirm Change Test Prod Mode?");
if (r == true) {
  var response = $http.get('/settest');
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
  var response = $http.get('/bootstrap');
  response.success(function(data, status, headers, config) {
      console.log("Ok.");
    });

  response.error(function(data, status, headers, config) {
      console.log("Error.");
    });
  }
};

$scope.gtidstring = function(arr) {
  var output = [];
  if (arr != null) {
    for (i = 0; i < arr.length; i++) {
      var gtid = "";
      gtid = arr[i]["DomainID"] + '-' + arr[i]["ServerID"] + '-' + arr[i]["SeqNo"];
      output.push(gtid)
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
      var response = $http.get('/sysbench');
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
      var response = $http.get('/runonetest?test='+$scope.tests );
           response.success(function(data, status, headers, config) {
            console.log("Ok.");
          });
          response.error(function(data, status, headers, config) {
            console.log("Error.");
          });
          $scope.tests=""
        }
      };

    $scope.setcluster = function() {

      var response = $http.get('/setcluster?cluster='+$scope.clusters );
           response.success(function(data, status, headers, config) {
            console.log("Ok.");
          });
          response.error(function(data, status, headers, config) {
            console.log("Error.");
          });


      };
  }]);
