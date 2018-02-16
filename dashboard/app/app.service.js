app.service('AppService', ['$http', '$localStorage', function ($http, $localStorage) {

    var getApiDomain = function(){
        return 'api';
    };

    var setAuthenticated = function (user, token) {
        // store username and token in local storage to keep user logged in between page refreshes
        $localStorage.currentUser = { username: user, token: token };

        // add jwt token to auth header for all requests made by the $http service
        $http.defaults.headers.common.Authorization = 'Bearer ' + token;
    };

    var hasAuthHeaders = function () {
        return ($http.defaults.headers.common.Authorization);
    };

    return {
        getApiDomain: getApiDomain,
        setAuthenticated: setAuthenticated,
        hasAuthHeaders: hasAuthHeaders
    };
}]);