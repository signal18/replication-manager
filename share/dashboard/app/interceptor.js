app.factory('BearerAuthInterceptor', ['$q', '$location', '$localStorage', function ($q, $location, $localStorage) {
    return {
        request: function(config) {
            config.headers = config.headers || {};
            if ($localStorage.currentUser && $localStorage.currentUser.token) {
                config.headers.Authorization = 'Bearer ' + $localStorage.currentUser.token;
            }
            return config || $q.when(config);
        },
        response: function(response) {
            if (response.status === 401 || response.status === 404 || response.status === 503 ) {
                $location.path('login');
            }
            return response || $q.when(response);
        },
        responseError: function (response) {
            console.log(response);
            if (response.status === 401 || response.status === 404 || response.status === 503) {
                $localStorage.currentUser = '';
                $location.path('login');
            }
            return response || $q.when(response);
        }
    };
}]);

// Register the previously created AuthInterceptor.
app.config(function ($httpProvider) {
    $httpProvider.interceptors.push('BearerAuthInterceptor');
});
