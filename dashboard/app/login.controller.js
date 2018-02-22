app.controller('LoginController', ['$scope', '$http', '$localStorage', '$location', 'AppService',
    function($scope, $http, $localStorage, $location, AppService) {

        $scope.login = function(user){
            $http.post(AppService.getApiDomain() + '/login', {"username": user.username, "password": user.password })
                .then(function(success) {
                    var data = success.data;
                    if (data.token) {
                        AppService.setAuthenticated(user.username, data.token);
                        $location.path('dashboard');
                    } else {
                        $scope.message = "Invalid username or password.";
                    }
            });
        };
}]);