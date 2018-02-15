app.controller('LoginController', ['$scope', '$http', '$localStorage', 'AppService',
    function($scope, $http, $localStorage, AppService) {

        $scope.login = function(user){
            //This section should be moved to a service.
            $http.post(AppService.getApiDomain() + '/login', {"username": user.username, "password": user.password })
                .success(function (response) {
                    // login successful if there's a token in the response
                    if (response.token) {
                        AppService.setAuthenticated(user.username, response.token);
                        //TODO: redirect to dashboard.
                    } else {
                        $scope.message = "Invalid username or password.";
                    }
                });

        };
}]);