app.controller('LoginController', ['$scope', '$http', '$localStorage', '$location', 'AppService',
    function($scope, $http, $localStorage, $location, AppService) {

        $scope.login = function(user){
            //This section should be moved to a service.
            $http.post(AppService.getApiDomain() + '/login', {"username": user.username, "password": user.password })
                .success(function (response) {
                    // login successful if there's a token in the response
                    if (response.token) {
                        AppService.setAuthenticated(user.username, response.token);
                        $location.path('dashboard');
                    } else {
                        $scope.message = "Invalid username or password.";
                    }
                });

        };
}]);