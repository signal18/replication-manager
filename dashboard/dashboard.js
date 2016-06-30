var app = angular.module('dashboard', ['ngResource']);

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

app.factory('Master', function($resource) {
  return $resource(
    '/master',
    '',
    { 'query':  {method:'GET', isArray:false} }
  );
});


app.controller('DashboardController', ['$scope', '$interval', '$http', 'Servers', 'Log', 'Settings', 'Master', function ($scope, $interval, $http, Servers, Log, Settings, Master) {
  $interval(function(){
  Servers.query({}, function(data) {
    $scope.servers = data;
  });
  Log.query({}, function(data) {
    $scope.log = data;
  });
  Settings.query({}, function(data) {
    $scope.settings = data;
  });
  Master.query({}, function(data) {
    $scope.master = data;
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
}]);
