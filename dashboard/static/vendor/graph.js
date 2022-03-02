var graphite_url = window.location.protocol+"//"+window.location.hostname+":10002";  // enter your graphite url, e.g. http://your.graphite.com

var dashboards =
[
  { "name": "repman",  // give your dashboard a name (required!)
    "refresh": 10000,  // each dashboard has its own refresh interval (in ms)
    // add an (optional) dashboard description. description can be written in markdown / html.
    "description": ""
                ,
    "metrics":  // metrics is an array of charts on the dashboard
    [
      {
        "alias": "Avg QPS",  // display name for this metric
        "target": "perSecond(mysql.*.mysql_global_status_queries)",  // enter your graphite barebone target expression here
        "renderer": "area",
        "interpolation": "linear",
        "description": "",  // enter your metric description here
        "summary": "avg",  // available options: [sum|min|max|avg|last|<function>]
        "summary_formatter": d3.format(",f") // customize your summary format function. see d3 formatting docs for more options
        // also supported are tick_formatter to customize y axis ticks, and totals_formatter to format the values in the legend
      },
      {
        "alias": "Max Replication Delay ",
        "targets": "sumSeries(mysql.*.mysql_slave_status_seconds_behind_master)",  // targets array is also supported
                  // see below for more advanced usage
        "description": "",
        "renderer": "area",   // use any rickshaw-supported renderer ( bar,line)
        "interpolation": "cardinal",
        "summary": "max",
        "unstack": true  // other parameters like unstack, interpolation, stroke, min, height are also available (see rickshaw documentation for more info)

      },
      {
        "alias": "Network",
        "target": "perSecond(mysql.*.mysql_global_status_bytes_*)",
        "alias": "Network In&Out",
        "targets": "perSecond(mysql.*.mysql_global_status_bytes_*)",  // targets array is also supported
                  // see below for more advanced usage
        "description": "",
        "renderer": "area",   // use any rickshaw-supported renderer ( bar,line)
        "interpolation": "cardinal",
        "summary": "avg",
        "unstack": false  //l values are normally ignored, but you can convert null to a specific value (usually zero)
      },
      {
        "alias": "Last Threads",  // display name for this metric
        "target": "sumSeries(mysql.*.mysql_global_status_threads_running)",  // enter your graphite barebone target expression here
        "description": "",  // enter your metric description here
        "summary": "last",  // available options: [sum|min|max|avg|last|<function>]
        // also supported are tick_formatter to customize y axis ticks, and totals_formatter to format the values in the legend
        "renderer": "line",
        "unstack": false,
        "summary_formatter": d3.format(",f")
      },
    ]
  },

];

var scheme = [
              '#423d4f',
              '#4a6860',
              '#848f39',
              '#a2b73c',
              '#ddcb53',
              '#c5a32f',
              '#7d5836',
              '#963b20',
              '#7c2626',
              ].reverse();

function relative_period() { return (typeof period == 'undefined') ? 1 : parseInt(period / 7) + 1; }
function entire_period() { return (typeof period == 'undefined') ? 1 : period; }
function at_least_a_day() { return entire_period() >= 1440 ? entire_period() : 1440; }

function stroke(color) { return color.brighter().brighter() }
function format_pct(n) { return d3.format(",f")(n) + "%" }
