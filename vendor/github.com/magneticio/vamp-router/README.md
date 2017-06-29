# Vamp-router

*NOTE: Since version 0.8.0, Vamp-router is not used in common Vamp setup.*

Vamp-router is inspired by [bamboo](https://github.com/QubitProducts/bamboo) and [consul-haproxy](https://github.com/hashicorp/consul-haproxy). It is not a straight fork or clone of either of these, but parts are borrowed. 

Vamp-router's features are:

-   Update the config through REST or through Zookeeper
-   Set complex routes & filters for canary releasing and A/B-testing
-   Get statistics on frontends, backends and servers
-   Stream statistics over SSE or Kafka
-   Set ACL's with short codes
-   Set HTTP & TCP Spike limiting *(experimental)*

*Important:* : Currently, Vamp-router does NOT check validity of the HAproxy command, ACLs and configs submitted to it. Submitting a config where a frontend references a non-existing backend will be accepted by the REST api but crash HAproxy.

*Important:* : Vamp-router should be run on a "proper" Linux box or container. It will work on Mac OSX for developing, building and testing, but reloading will drop connections due to OSX's TCP stack.

## Installing: the easy Docker way

Start up an instance with all defaults and bind it to the local network interface

    $ docker run --net=host magneticio/vamp-router:latest

    ██╗   ██╗ █████╗ ███╗   ███╗██████╗
    ██║   ██║██╔══██╗████╗ ████║██╔══██╗
    ██║   ██║███████║██╔████╔██║██████╔╝
    ╚██╗ ██╔╝██╔══██║██║╚██╔╝██║██╔═══╝
     ╚████╔╝ ██║  ██║██║ ╚═╝ ██║██║
      ╚═══╝  ╚═╝  ╚═╝╚═╝     ╚═╝╚═╝
                           router
                           version 0.7.10
                           by magnetic.io
                                          
    18:39:05.413 main NOTI ==>  Attempting to load config at //.vamp_lb/haproxy_new.cfg
    18:39:05.413 main NOTI ==>  Did not find a config, loading example config...
    18:39:05.418 main NOTI ==>  Created new pidfile...
    18:39:05.424 main NOTI ==>  Initializing metric streams...
    18:39:05.424 main NOTI ==>  Initializing REST API...
        
The default ports are:

    10001      REST Api (for config, stats etc)  
    1988       built-in Haproxy stats
    
## Changing ports

You could change the REST api port by adding the `-port` flag

    $ docker run --net=host magneticio/vamp-router:latest -port=1234

Or by exporting an environment variable `VAMP_LB_PORT`.
     
     $ export VAMP_LB_PORT=12345
     $ docker run --net=host magneticio/vamp-router:latest


    
## Routes

A Route is structured set of Haproxy frontends, backends and servers. The Route provides a convenient
and higher level way of creating and managing this structure. You could create this structure by
hand with separate API calls, but this is faster and easier in 9 out of 10 cases.

The structure of a route is as follows:

                              -> [srv a] -> sock -> [fe a: be a] -> [*srv] -> host:port
                            /
    ->[fe (fltr)(qts) : be]-
                            \
                              -> [srv b] -> sock -> [fe b: be b] -> [*srv] -> host:port

    fe = frontend
    be = backend
    srv = server
    fltr = filter
    qts = quotas

The above example has two services, *A* and *B*, but a route can have many services. The start of the
route (the first frontend) has filters and quotas that influence the way traffic flows in a route,
i.e. to which services the traffic goes. All items in a route map to actual Haproxy types from the `vamp-router/haproxy` Go package.

### Routes actions

Routes live under the `/routes` endpoint which provides the following actions:

    GET     /routes  
    POST    /routes  

    GET     /routes/:route  
    PUT     /routes/:route  
    DELETE  /routes/:route  

    GET     /routes/:route/services  
    POST    /routes/:route/services  
    GET     /routes/:route/services/:service  
    PUT     /routes/:route/services/:service  
    DELETE  /routes/:route/services/:service  

    GET     /routes/:route/services/:service/servers  
    GET     /routes/:route/services/:service/servers/:server  
    PUT     /routes/:route/services/:service/servers/:server  
    POST    /routes/:route/services/:service/servers  
    DELETE  /routes/:route/services/:service/servers/:server 


For example, create a route by posting this json object to `routes`. All necessary backends, frontends, servers and sockets will be created "under water". Read the comments for specific details

    $ http POST localhost:10001/v1/routes
    
    {
      "name": "test_route_2",                               # a unique name
      "port": 9026,                                         # the port to bind to
      "protocol": "http",
      "filters": [                                          # some filter with a destination service
        {
          "name": "uses_internet_explorer",
          "condition": "hdr_sub(user-agent) MSIE",
          "destination": "service_b"
        }
      ],
      "httpQuota": {
        "sampleWindow": "1s",
        "rate": 10000,
        "expiryTime": "10s"
      },
      "tcpQuota": {
        "sampleWindow": "3s",
        "rate": 10000,
        "expiryTime": "10s"
      },
      "services": [                                           # one or multiple services
        {
          "name": "service_a",                                # a unique name within this set of services
          "weight": 30,                                     # weight of the service
          "servers": [
            {
              "name": "paas.55f73f0d-6087-4964-a70e",       # some name for your server. Should be unique
              "host": "192.168.2.2",                        # the endpoint for your application
              "port": 8081
            }
          ]
        },
        {
          "name": "service_b",
          "weight": 70,
          "servers": [
            {
              "name": "paas.fb76ea52-098f-4e2a-abbe",
              "host": "192.168.2.2",
              "port": 8082
            }
          ]
        }
      ]
    } 


Updating the weight of the services can be done by using a `PUT` request to the `services` resource of a route:

    $ http PUT http://localhost:10001/v1/routes/test_route_2/services/service_a


    {
      "name": "service_a",                                
      "weight": 40,                                     # a new weight
      "servers": [
        {
          "name": "paas.55f73f0d-6087-4964-a70e",       
          "host": "192.168.2.2",                        
          "port": 8081
        }
      ]
    }

### Route filters

Filters on routes provide some convenient higher abstractions and "shortcodes" for setting up (groups 
of) conditions on how to route the traffic flowing into a route.

Let's look at a typical filter:

    {
      "name": "uses_internet_explorer",
      "condition": "user-agent = Android",
      "destination": "service_b"
    }

This piece of json does three things:

1) it give the filter a name, which is compulsory.
2) is uses a short code `user-agent = Android` to match all User-Agent headers that have the word 
`Android` in them.
3) it send the traffic that matches the condition to service `service_b`


Short codes are human readable condition that are translated to the more opaque HAproxy ACL's.
The following are all equivalent:

    hdr_sub(user-agent) 
    user-agent=Android
    User-Agent=Android
    user-agent = Android
    user.agent = Android

Currently available are:

    User-Agent = *string*
    Host = *string*
    Cookie *cookie name* Contains *string*
    Has Cookie *cookie name*
    Misses Cookie *cookie name*
    Header *header name* Contains *string*
    Has Header *header name*
    Misses Header *header name*

You can also use negations on any filter with an equality operator, like:

    User-Agent != *string*
    Host != *string*

#### Route filters vs. ACL's

If no short code is found, the filter's condition is just treated as an ACL. This means you can always
just use HAproxy ACL's in routes as well as in frontends.

The example below will route all Internet Explorer users to a different backend. You can update this on the fly
without loosing sessions or causing errors due to Haproxy's smart restart mechanisms.

    {
        "frontends" : [
            {
                "name" : "test_fe_1",                               # declare a frontend
                ...                                                 # some stuff left out for brevity
                "acls" : [
                    {
                        "name" : "uses_msie",                       # set an ACL by giving it a name and some pattern. 
                        "backend" : "testbe2",                      # set the backend to send traffic to
                        "pattern" : "hdr_sub(user-agent) MSIE"      # This pattern matches all HTTP requests  that have
                    }                                               # "MSIE" in their User-Agent header                 

                ]
            }
        ]
    }    


    
### Rate / Spike limiting 

You can set limits on specific connection rates for HTTP and TCP traffic. This comes in handy if you want to protect
yourself from abusive users or other spikes. The rates are calculated over a specific time range. The example below
tracks the TCP connection rate over 30 seconds. If more than 200 new connections are made in this time period, the 
client receives an 503 error and goes into a "cooldown" period for 60 seconds (`expiryTime`)

    {
        "frontends" : [
            {
                "name" : "test_fe_1",
                ... 
                "httpSpikeLimit" : {
                    "sampleTime" : "30s",
                    "expiryTime" : "60s",
                    "rate" : 50
                },
                "tcpSpikeLimit" : {
                    "sampleTime" : "30s",
                    "expiryTime" : "60s",
                    "rate" : 200
            }
    }

Note: the time format used, i.e. `30s`, is the default Haproxy time format. More details [here](http://cbonte.github.io/haproxy-dconv/configuration-1.5.html#2.2)

## Frontends

The frontend is the basic listening port or unix socket. Here's an example of a basic HTTP frontend:

    http GET localhost:10001/v1/frontends

    {
        "name" : "test_fe_1",
        "bindPort" : 8000,
        "bindIp" : "0.0.0.0",
        "defaultBackend" : "testbe1",
        "mode" : "http",
        "options" : {
            "httpClose" :  true
    }

You can also setup the frontend to listen on Unix sockets. _Note_: you have to explicitly declare the protocol
coming over the socket. On this example we declare the Haproxy specific `proxy` protocol.

    {
        "name" : "test_fe_1",
        "mode" : "http",
        "defaultBackend" : "testbe2",
        "unixSock" : "/tmp/vamp_testbe2_1.sock",
        "sockProtocol" : "accept-proxy"
    }


## Setting Backends and servers

More info to follow. _Note_: You can point servers to standard IP + port pairs or to Unix sockets.
Here are some examples:

    {  "backends" : [
    
            {
                "name" : "testbe1",
                "mode" : "http",
                "servers" : [
                    {
                        "name" : "test_be1_1",
                        "host" : "192.168.59.103",
                        "port" : 8081,
                        "weight" : 100,
                        "maxconn" : 1000,
                        "check" : false,
                        "checkInterval" : 10
                        },
                    {
                        "name" : "test_be1_2",
                        "host" : "192.168.59.103",
                        "port" : 8082,
                        "weight" : 100,
                        "maxconn" : 1000,
                        "check" : false,
                        "checkInterval" : 10
                    }
                ],
                "proxyMode" : false
            }
        ]
    }
    
    
And with proxy mode set to true:

    { 
        "backends" : 
            [
                {
                    "name" : "testbe2",
                    "mode" : "http",
                    "servers" : [
                        {
                            "name" : "test_be2_1",
                            "unixSock" : "/tmp/vamp_testbe2_1.sock",
                            "weight" : 100
                        }
                    ],
                    "proxyMode" : true,
                    "options" : {}
                }
            ]
    }

### Updating the full configuration via REST

Post a configuration. You can use the example file `resources/config_example.json`

    $ http POST http://192.168.59.103:10001/v1/config < resources/config_example.json 
    HTTP/1.1 200 OK
     
### Updating the full configuration using Zookeeper

When you provide vamp-router with a valid Zookeeper connection string using the `-zooConString` flag, vamp-router will watch for changes to the key: `/magnetic/vamplb`. You can set your own namespace using the `-zooConKey` flag. To this node you need to publish a full configuration in JSON format. Starting up a localproxy using Zookeeper
looks like this:  

    -zooConString=10.161.63.88:2181,10.189.106.106:2181,10.5.99.23:2181    

## Getting statistics

Statistics are published in three different ways: straight from the REST interface, or as stream using SSE or Kafka topics.

### Stats via REST
     
Grab some stats from the `/stats` endpoint. Notice the IP address. This is [boot2docker](https://github.com/boot2docker/boot2docker)'s address on my Macbook. I'm using [httpie](https://github.com/jakubroztocil/httpie) instead of curl.

    $ http http://192.168.59.103:10001/v1/stats
    HTTP/1.1 200 OK
    
    [
        {
            "act": "", 
            "bck": "", 
            "bin": "3572", 
            "bout": "145426", 
            "check_code": "", 
            "check_duration": "", 
            "check_status": "", 
            "chkdown": "", 
            "chkfail": "", 
            "cli_abrt": "", 
            ...
            
Valid endpoints are `stats/frontends`, `stats/backends` and `stats/servers`. The `/stats` endpoint gives you all of them
in one go.

### Stats streaming via SSE

All statistics are also streamed as Server Sent Events (SSE). Just do a GET on `/stats/stream` and the server will respond
with a continuous stream of all stats, using the following format:

    event: metric
    data: {"tags":["test_fe_1","frontend","rate"],"value":0,"timestamp":"2015-02-24T18:45:07Z"}

    event: metric
    data: {"tags":["test_fe_1","frontend","rate_lim"],"value":0,"timestamp":"2015-02-24T18:45:07Z"}

    event: metric
    data: {"tags":["test_fe_1","frontend","rate_max"],"value":0,"timestamp":"2015-02-24T18:45:07Z"}


### Stats streaming via Kafka

Statistics are also published as Kafka topics. Configure a Kafka endpoint using the `-kakfaHost` and `-kafkaPort` flags.
Stats are published as the following topic:

- router.all

The messages on that topic are json strings:

    {
        "tags": [
            "test_fe_1",
            "frontend",
            "rate"
        ],
        "value": 0,
        "timestamp": "2015-02-24T18:45:07Z"
    },
    {
        "tags": [
            "test_fe_1",
            "frontend",
            "rate_lim"
        ],
        "value": 0,
        "timestamp": "2015-02-24T18:45:07Z"
    },
    {
        "tags": [
            "test_fe_1",
            "frontend",
            "rate_max"
        ],
        "value": 0,
        "timestamp": "2015-02-24T18:45:07Z"
    }

__Note:__ currently, not all Haproxy metric types are sent. At this moment, the list is hardcoded as a `wantedMetrics` slice:
    
    wantedMetrics  := []string{ "Scur", "Qcur","Smax","Slim","Weight","Qtime","Ctime","Rtime","Ttime","Req_rate","Req_rate_max","Req_tot","Rate","Rate_lim","Rate_max" }

For an explanation of the metric types, please read [this](http://cbonte.github.io/haproxy-dconv/configuration-1.5.html#9.1)            

 
### Startup Flags & Options

Run `--help` for all options and their defaults:

```
Usage of ./vamp-router:
  -binary="/usr/local/sbin/haproxy": Path to the HAproxy binary
  -configPath="": Location of configuration files, defaults to configuration/
  -customWorkDir="": Custom working directory for sockets and pid files, default to data/
  -headless=false: Run without any logging output to the console
  -kafkaHost="": The hostname or ip address of the Kafka host
  -kafkaPort=9092: The port of the Kafka host
  -logPath="/var/log/vamp-router/vamp-router.log": Location of the log file
  -port=10001: Port/IP to use for the REST interface. Overrides $PORT0 env variable
  -zooConKey="magneticio/vamplb": Zookeeper root key
  -zooConString="": A zookeeper ensemble connection string
```  

### Installing: the harder custom build way

Install HAproxy 1.5 or greater in whatever way you like. Just make sure the `haproxy` executable is in your `PATH`. For Ubuntu, use:


    $ add-apt-repository ppa:vbernat/haproxy-1.5 -y  
    $ apt-get update -y  
    $ apt-get install -y haproxy  


Clone this repo 

    git clone https://github.com/magneticio/vamp-router 

CD into the directory just and build the program and run it. 
 
    $ go install
    $ vamp-router

If you're on Mac OSX or Windows and want to compile for Linux (which is probably the OS 
you're using to run HAproxy), you need to cross compile. 
For this, go to your Go `src` directory, i.e.

    $ cd /usr/local/Cellar/go/1.4.1

Compile the compiler with the correct arguments for OS and ARC

    $ GOOS=linux GOARCH=386 CGO_ENABLED=0 ./make.bash --no-clean

Compile the application

    $ GOOS=windows GOARCH=386 go build 

## Integration testing (experimental)

Integration tests require a functioning local Docker installation and Haproxy. Run the integration test suite as follows:

    $ go test -tags integration -v --customWorkDir=/tmp/vamp_integration_test --headless=true

The `--customWorkDir` flag makes sure you will not overwrite or delete any previous settings as the test runner will delete
this direcory at the end. The `--headless` flag will ensure only the test code outputs to the console.

    
