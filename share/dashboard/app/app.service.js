app.service('AppService', ['$http', '$localStorage', function ($http, $localStorage) {

    var getApiDomain = function(){
        return 'api';
    };

    var getClusterUrl = function(clusterName){
        return '/'+this.getApiDomain()+'/clusters/' + clusterName;
    };

    var setAuthenticated = function (user, token) {
        // store username and token in local storage to keep user logged in between page refreshes
        $localStorage.currentUser = { username: user, token: token };
    };

    var setAuthToken = function (token) {
        // store username and token in local storage to keep user logged in between page refreshes
        if (!$localStorage.currentUser) {
            $localStorage.currentUser = { username: "", token: token };
        } else {
            $localStorage.currentUser.token = token
        }
    };

    var setAuthUser = function (user) {
        // store username based on valid token
        $localStorage.currentUser.username = user
    };

    var hasAuthHeaders = function () {
        return ($localStorage.currentUser && $localStorage.currentUser.token);
    };

    var getUser = function () {
        return $localStorage.currentUser.username;
    };

    var logout = function() {
        $localStorage.currentUser = {}
    };

    return {
        getApiDomain: getApiDomain,
        getClusterUrl: getClusterUrl,
        setAuthenticated: setAuthenticated,
        setAuthToken: setAuthToken,
        setAuthUser: setAuthUser,
        hasAuthHeaders: hasAuthHeaders,
        getUser: getUser,
        logout: logout
    };
}]);
