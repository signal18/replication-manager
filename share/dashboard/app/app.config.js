app.config(function($mdThemingProvider, $mdAriaProvider) {
  $mdThemingProvider.theme('default').primaryPalette('cyan');
  $mdAriaProvider.disableWarnings();
});

app.config(['$qProvider', function ($qProvider) {
    $qProvider.errorOnUnhandledRejections(false);
}]);


