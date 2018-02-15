app.controller('LoginController', ['$scope', '$http', '$localStorage',
    function($scope, $http, $localStorage) {

        $scope.login = function(user){

            $http.post('/api/login', {"username": user.username, "password": user.password })
                .success(function (response) {
                    // login successful if there's a token in the response
                    if (response.token) {
                        // store username and token in local storage to keep user logged in between page refreshes
                        $localStorage.currentUser = { username: user.username, token: response.token };

                        // add jwt token to auth header for all requests made by the $http service
                        $http.defaults.headers.common.Authorization = 'Bearer ' + response.token;

                        //TODO: redirect to dashboard.
                    } else {
                        $scope.message = "Invalid username or password.";
                    }
                });

        };
}]);