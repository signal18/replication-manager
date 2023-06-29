app.controller('LoginController', ['$scope', '$http', '$localStorage', '$location', 'AppService', '$window','Monitor',
    function($scope, $http, $localStorage, $location, AppService, Monitor) {

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
        $scope.gitLogin = function() {
          //to get data for OAuth 
          $http.post('/api/monitor', {}).then(function(success) {
            var data = success.data
            if (data){
              $scope.settings = data;
              $scope.apiOAuthClientID =	$scope.settings.config.apiOAuthClientID;
              $scope.apiOAuthProvider = $scope.settings.config.apiOAuthProvider;
              $scope.apiOAuthSecretID = $scope.settings.config.apiOAuthSecretID;
            }
          }).then(function(){
          var authURL = $scope.apiOAuthProvider+'/oauth/authorize?' + $.param({
            authority: $scope.apiOAuthProvider,
            client_id: $scope.apiOAuthClientID,
            secret_id: $scope.apiOAuthSecretID,
            redirect_uri: 'https://'+$location.host()+':'+$location.port()+'/api/auth/callback',
            response_type: 'code',
            scope: 'openid profile email api'
          });
          // Redirect the user to the oAuth URL
          window.location.href = authURL;

        })};
    }
]);

