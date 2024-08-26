app.directive('flatpickr', function($timeout) {
    return {
        restrict: 'A',
        require: 'ngModel',
        link: function(scope, element, attrs, ngModel) {
            var flatpickrInstance;
            var lastMinDate;
            var lastMaxDate;

            function initFlatpickr() {
                var options = {
                    time_24hr : true,
                    static: attrs.flatpickrStatic === 'true',
                    enableTime: attrs.flatpickrEnableTime === 'true',
                    dateFormat: attrs.flatpickrDateFormat || "Y-m-d H:i:S",
                    position: attrs.flatpickrPosition || 'auto',
                    utc: attrs.flatpickrUtc === 'true',
                    onChange: function(selectedDates, dateStr, instance) {
                        scope.$apply(function() {
                            ngModel.$setViewValue(dateStr);
                        });
                    },
                    onOpen: function() {
                        if (!options.static) {
                            adjustDatepickerPosition();
                        }
                    }
                };

                flatpickrInstance = flatpickr(element[0], options);

                // Watch for changes in minDate attribute and update minDate
                scope.$watch(attrs.flatpickrMinDate, debounce(function(newValue) {
                    if (newValue) {
                        const minDate = parseDate(newValue, attrs.flatpickrMinDateType || 'datetime');
                        if (minDate && (!lastMinDate || minDate.getTime() !== lastMinDate.getTime())) {
                            flatpickrInstance.set('minDate', minDate);
                            lastMinDate = minDate;
                        }
                    }
                }, 300));

                // Watch for changes in maxDate attribute and update maxDate
                scope.$watch(attrs.flatpickrMaxDate, debounce(function(newValue) {
                    let maxDate;
                    if (newValue === 'now') {
                        maxDate = parseDate();
                    } else if (newValue) {
                        maxDate = parseDate(newValue, attrs.flatpickrMaxDateType || 'datetime');
                    }

                    if (maxDate && (!lastMaxDate || maxDate.getTime() !== lastMaxDate.getTime())) {
                        flatpickrInstance.set('maxDate', maxDate);
                        lastMaxDate = maxDate;
                    }
                }, 300));
            }

            function parseDate(value, type) {
                let ms;
                switch (type) {
                    case 'unix':
                        ms = Math.floor(parseInt(value) * 1000 + (new Date().getTimezoneOffset()*60000))
                        return new Date(ms); // Unix timestamp in seconds
                    case 'unix-ms':
                        ms = Math.floor(parseInt(value) + (new Date().getTimezoneOffset()*60000))
                        return new Date(parseInt(ms)); // Unix timestamp in milliseconds
                    case 'datetime':
                    default:
                        let tmp = new Date(value)
                        return new Date(Math.floor(tmp.getTime() + (tmp.getTimezoneOffset()*60000))); // ISO 8601 or Date string
                }
            }

            function adjustDatepickerPosition() {
                const calendar = document.querySelector('.flatpickr-calendar');
                if (calendar) {
                    const calendarRect = calendar.getBoundingClientRect();
                    const bottomSpace = window.innerHeight - calendarRect.bottom;

                    if (bottomSpace < 0) {
                        calendar.style.top = (calendar.offsetTop + bottomSpace) + 'px';
                    }
                }
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

            $timeout(initFlatpickr, 0);
        }
    };
});
