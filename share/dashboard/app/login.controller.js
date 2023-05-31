app.controller('LoginController', ['$scope', '$http', '$localStorage', '$location', 'AppService',
    function($scope, $http, $localStorage, $location, AppService) {

        $scope.login = function(user){
            $http.post(AppService.getApiDomain() + '/login', {"username": user.username, "password": user.password })
                .then(function(success) {
                    var data = success.data;
                    if (data.token) {
                        AppService.setAuthenticated(user.username, data.token);
                        $location.path('dashboard');
                    } else if (success.status === 429) {
                        $scope.message = "3 authentication errors for the user " + user.username + ", please try again in 3 minutes";
                    } else{
                        $scope.message = "Invalid username or password.";
                    }
            });
        };
}]);