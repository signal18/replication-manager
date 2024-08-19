app.directive('flatpickr', function($timeout) {
    return {
        restrict: 'A',
        require: 'ngModel',
        link: function(scope, element, attrs, ngModel) {
            var flatpickrInstance;
            var lastMinDate;

            function initFlatpickr() {
                flatpickrInstance = flatpickr(element[0], {
                    static: attrs.flatpickrStatic === 'true',
                    enableTime: attrs.flatpickrEnableTime === 'true',
                    dateFormat: attrs.flatpickrDateFormat || "Y-m-d H:i:S",
                    onChange: function(selectedDates, dateStr, instance) {
                        scope.$apply(function() {
                            ngModel.$setViewValue(dateStr);
                        });
                    }
                });

                // Watch for changes in ng-model value and update Flatpickr
                ngModel.$render = function() {
                    var date = ngModel.$viewValue ? new Date(ngModel.$viewValue) : null;
                    if (date) {
                        flatpickrInstance.setDate(date, false); // Avoid triggering onChange
                    } else {
                        flatpickrInstance.clear();
                    }
                };

                // Watch for changes in minDate attribute and update minDate
                scope.$watch(attrs.flatpickrMinDate, debounce(function(newValue) {
                    if (newValue) {
                        const minDate = new Date(newValue * 1000); // Unix timestamp to Date
                        if (!lastMinDate || minDate.getTime() !== lastMinDate.getTime()) {
                            flatpickrInstance.set('minDate', minDate);
                            lastMinDate = minDate;
                        }
                    }
                }, 300));
            }

            function debounce(func, wait) {
                var timeout;
                return function() {
                    var context = this, args = arguments;
                    $timeout.cancel(timeout);
                    timeout = $timeout(function() {
                        func.apply(context, args);
                    }, wait);
                };
            }

            // Use $timeout to ensure the DOM is updated before initializing
            $timeout(initFlatpickr, 0);
        }
    };
});
