var routeProvider, app = angular.module('dashboard', ['ngResource', 'ngMaterial', 'ngRoute', 'ng-token-auth', 'ngStorage'])
    .config(function($routeProvider) {
        routeProvider = $routeProvider;
        $routeProvider
            .when('/dashboard', {
                templateUrl: 'app/dashboard.html',
                controller: 'DashboardController'
            })
            .when('/login', {
                templateUrl: 'app/login.html',
                controller: 'DashboardController'
            })
            .otherwise({
                redirectTo: '/login'
            });
    });