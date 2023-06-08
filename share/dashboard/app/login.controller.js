app.controller('LoginController', ['$scope', '$http', '$localStorage', '$location', 'AppService', '$window', '$http',
    function($scope, $http, $localStorage, $location, AppService, $http) {

        $scope.login = function(user){
            var $http = angular.injector(['ng']).get('$http');
            var requestData = {
                // Add your request data here
                username: user.username,
                password: user.password
              };
        
              $http({
                method: 'POST',
                url: AppService.getApiDomain() + '/login',
                data: requestData
              })
            //$http.post(, {"username": user.username, "password": user.password })
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
        /*$scope.gitLogin = function() {
  
            var $http = angular.injector(['ng']).get('$http');
            $http({
                method: 'GET',
                url: '/api/auth'
              }).then(function(response) {
                // Redirect the user to the GitLab authorization page

                console.log(response);
                $window.location.href = response.data.authorizationUrl;
              })
              .catch(function(error) {
                console.error('Failed to authenticate with GitLab:', error);
              });
          };*/
    }
]);