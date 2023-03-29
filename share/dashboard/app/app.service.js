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
        hasAuthHeaders: hasAuthHeaders,
        getUser: getUser,
        logout: logout
    };
}]);
