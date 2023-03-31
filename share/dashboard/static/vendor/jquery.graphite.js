// graphite.js

(function ($) {
    $.fn.graphite = function (options) {
        if (options === "update") {
            $.fn.graphite.update(this, arguments[1]);
            return this;
        }

        // Initialize plugin //
        options = options || {};
        var settings = $.extend({}, $.fn.graphite.defaults, options);

        return this.each(function () {
            var $this = $(this);

            $this.data("graphOptions", settings);
            $.fn.graphite.render($this, settings);
        });

    };

    $.fn.graphite.geturl = function(rawOptions) {
        var src = rawOptions.url + "?";

        // use random parameter to force image refresh
        var options = $.extend({}, rawOptions);

        options["_t"] = options["_t"] || Math.random();

        $.each(options, function (key, value) {
            if (key === "target") {
                $.each(value, function (index, value) {
                    src += "&target=" + value;
                });
            } else if (value !== null && key !== "url") {
                src += "&" + key + "=" + value;
            }
        });

        return src.replace(/\?&/, "?");
    };

    $.fn.graphite.render = function($img, options) {
        $img.attr("src", $.fn.graphite.geturl(options));
        $img.attr("height", options.height);
        $img.attr("width", options.width);
    };

    $.fn.graphite.update = function($img, options) {
        options = options || {};
        $img.each(function () {
            var $this = $(this);
            var settings = $.extend({}, $this.data("graphOptions"), options);
            $this.data("graphOptions", settings);
            $.fn.graphite.render($this, settings);
        });
    };

    // Default settings. 
    // Override with the options argument for per-case setup
    // or set $.fn.graphite.defaults.<value> for global changes
    $.fn.graphite.defaults = {
        from: "-1hour",
        height: "300",
        until: "now",
        url: "/render/",
        width: "940"
    };

}(jQuery));
