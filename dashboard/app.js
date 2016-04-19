var app = angular.module('dashboard', ['ngResource']);

app.factory('Servers', function($resource) {
  return $resource('http://localhost:10001/servers');
});

app.controller('DashboardController', ['$scope', 'Servers', function ($scope, Servers) {
  Servers.query({}, function(data) {
    $scope.servers = data;
  });
}]);
