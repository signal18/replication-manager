app.service('AppService', ['$http', '$localStorage', function ($http, $localStorage) {

    var getApiDomain = function(){
        return 'api';
    };

    var setAuthenticated = function (user, token) {
        // store username and token in local storage to keep user logged in between page refreshes
        $localStorage.currentUser = { username: user, token: token };
    };

    var hasAuthHeaders = function () {
        return ($localStorage.currentUser.token);
    };

    var logout = function() {
        $localStorage.currentUser = {}
    };

    return {
        getApiDomain: getApiDomain,
        setAuthenticated: setAuthenticated,
        hasAuthHeaders: hasAuthHeaders,
        logout: logout
    };
}]);