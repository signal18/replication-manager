app.controller('DashboardController',
function (
  $scope,
  $routeParams,
  $timeout,
  $http,
  $location,
  $mdSidenav,
  $mdDialog,
  Servers,
  Clusters,
  Monitor,
  Alerts,
  Master,
  Proxies,
  Slaves,
  Cluster,
  AppService,
  Processlist,
  Tables,
  VTables ,
  Status,
  Variables,
  StatusInnoDB ,
  ServiceOpenSVC,
  PFSStatements,
  PFSStatementsSlowLog,
  SlowQueries,
  ExplainPlanPFS,
  ExplainPlanSlowLog,
  MetaDataLocks,
  QueryResponseTime,
  Backups,
  Certificates,
  QueryRules

 ) {

  $scope.selectedClusterName = undefined;
  $scope.selectedPlan= undefined;
  $scope.selectedOrchestrator= undefined;
  $scope.plans= undefined;
  $scope.selectedServer = undefined;
  $scope.selectedQuery = undefined;
  $scope.menuOpened = false;
  $scope.serverListTabular = false;
  $scope.selectedTab = undefined;
  $scope.selectedAcls = [];
  $scope.selectedUserIndex = undefined;
  $scope.newUserAcls = undefined;
  $scope.refreshInterval = 2000;
  $scope.digestmode = "pfs";

  $scope.missingDBTags = undefined;
  $scope.missingProxyTags = undefined;
  var promise = undefined;

  $scope.user = undefined ;

  $scope.monitors = [
    { id: 'mariadb', name: 'MariaDB' },
    { id: 'mysql', name: 'MySQL' },
    { id: 'percona', name: 'Percona' },
    { id: 'proxysql', name: 'ProxySQL' },
    { id: 'haproxy', name: 'HaProxy' },
    { id: 'shardproxy', name: 'ShardProxy' },
    { id: 'maxscale', name: 'MaxScale' },
    { id: 'sphinx', name: 'SphinxProxy' },
    { id: 'extvip', name: 'VIP' },  ];
    $scope.selectedMonitor = { id: 'mariadb', name: 'MariaDB' };

//$scope.selectedPlan = undefined;



    var getClusterUrl = function () {
      return AppService.getClusterUrl($scope.selectedClusterName);
    };

    $scope.isLoggedIn = AppService.hasAuthHeaders();
    if (!$scope.isLoggedIn) {
      $location.path('login');
    } else {
      $scope.user = AppService.getUser();
    }

    $scope.logout = function () {
      AppService.logout();
      $location.path('login');
    };

    var timeFrame = $routeParams.timeFrame;
    if (timeFrame == "") {
      timeFrame = "10m";
    }

    $scope.toogleRefresh = function()  {
      if ($scope.menuOpened) {
        $scope.menuOpened = false;
        //   $scope.openedAt = "";
      } else {
        $scope.menuOpened = true;
        //   $scope.openedAt = new Date().toLocaleString();
      }
    };


    $scope.callServices = function () {
      if (!AppService.hasAuthHeaders()) return;
      if ($scope.menuOpened) return;

      //  $scope.selectedPlan = "";
      // get list of clusters
      if ($scope.selectedClusterName==undefined && $scope.selectedServer==undefined ) {
        Clusters.query({}, function (data) {
          if (data) {
            $scope.clusters = data;
            if ($scope.clusters.length === 1 && $scope.settings.config.monitoringSaveConfig==false ) {
              $scope.selectedClusterName = $scope.clusters[0].name;
            }
            //else {
            //  $scope.refreshInterval = 2000;
          //  }
          }
        }, function () {
          $scope.reserror = true;
        });
        Monitor.query({}, function (data) {
          if (data) {
            $scope.settings = data;
            $scope.plans =	$scope.settings.servicePlans;
            $scope.orchestrators =	$scope.settings.serviceOrchestrators;
            $scope.selectedPlan = $scope.plans[12];
            $scope.selectedOrchestrator= $scope.orchestrators[3];
            $scope.selectedPlanName =  $scope.selectedPlan.plan;
            if ($scope.newUserAcls == undefined)  {
            //  alert(data.config.httpRefreshInterval);
                $scope.refreshInterval = 	$scope.settings.config.httpRefreshInterval;
            }
            $scope.newUserAcls = JSON.parse(JSON.stringify($scope.settings.serviceAcl));
            if ((data.logs) && (data.logs.buffer)) $scope.logs = data.logs.buffer;

          }
        }, function () {
          $scope.reserror = true;
        });

      }
      if ($scope.selectedClusterName ) {
        Servers.query({clusterName: $scope.selectedClusterName}, function (data) {
          if (!$scope.menuOpened) {
            if (data) {
              $scope.servers = data;
              function myfilter(array, test){
                var passedTest =[];
                for (var i = 0; i < array.length; i++) {
                  if(test( array[i]))
                  passedTest.push(array[i]);
                }
                return passedTest;
              }
              $scope.slaves=myfilter(data,function(currentServer){return ( currentServer.isSlave);});
              $scope.reserror = false;
            }
          }
        }, function () {
          $scope.reserror = true;
        });
      } // fetch server most of  the time
      if ($scope.selectedClusterName && $scope.selectedServer==undefined ) {
        Cluster.query({clusterName: $scope.selectedClusterName}, function (data) {
          $scope.selectedCluster = data;
          function isInTags(array,array2, test){
            var passedTest =[];
            for (var i = 0; i < array.length; i++) {
              if(test( array[i].name,array2))
              passedTest.push(array[i]);
            }
            return passedTest;
          }
          $scope.agents = data.agents;
          $scope.missingDBTags=isInTags(data.configTags,data.dbServersTags,function(currentTag,dbTags){ return (dbTags.indexOf(currentTag)== -1);});
          $scope.missingProxyTags=isInTags(data.configPrxTags,data.proxyServersTags,function(currentTag,proxyTags){ return (proxyTags.indexOf(currentTag)== -1);});


          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });



        if ( $scope.selectedTab=='Shards' ) {
          VTables.query({clusterName: $scope.selectedClusterName}, function (data) {
            $scope.vtables = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }


        Alerts.query({clusterName: $scope.selectedClusterName}, function (data) {
          $scope.alerts = data;
        }, function () {
          $scope.reserror = true;
        });


        // console.log($scope.selectedServer);
        // console.log($scope.selectedTab);


        Master.query({clusterName: $scope.selectedClusterName}, function (data) {
          $scope.master = data;
        }, function () {
          $scope.reserror = true;
        });

        if ($scope.selectedTab=='Proxies') {
          Proxies.query({clusterName: $scope.selectedClusterName}, function (data) {
            if (!$scope.menuOpened) {
              $scope.proxies = data;
              $scope.reserror = false;
            }

          }, function () {
            $scope.reserror = true;
          });
        }
        if ($scope.selectedTab=='Backups') {
          Backups.query({clusterName: $scope.selectedClusterName}, function (data) {
            if (!$scope.menuOpened) {
              $scope.backups = data;
              $scope.reserror = false;
            }

          }, function () {
            $scope.reserror = true;
          });
        }
        if ($scope.selectedTab=='Certificates') {
          Certificates.query({clusterName: $scope.selectedClusterName}, function (data) {
            if (!$scope.menuOpened) {
              $scope.certificates = data;
              $scope.reserror = false;
            }
          }, function () {
            $scope.reserror = true;
          });
        }
        if ($scope.selectedTab=='QueryRules') {
          QueryRules.query({clusterName: $scope.selectedClusterName}, function (data) {
            if (!$scope.menuOpened) {
              $scope.queryrules = data;
              $scope.reserror = false;
            }
          }, function () {
            $scope.reserror = true;
          });
        }
      }
      if ($scope.selectedClusterName && $scope.selectedServer   ){
        if ($scope.selectedTab=='Processlist') {
          Processlist.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.processlist = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }

        if ($scope.selectedTab=='PFSQueries') {
          if ($scope.digestmode == 'pfs') {
            PFSStatements.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
              $scope.pfsstatements = data;
              $scope.reserror = false;
            }, function () {
              $scope.reserror = true;
            });
          } else {
            PFSStatementsSlowLog.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
              $scope.pfsstatements = data;
              $scope.reserror = false;
            }, function () {
              $scope.reserror = true;
            });
          }
        }

        if ($scope.selectedTab=='LogSlow') {
          SlowQueries.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.slowqueries = data;
            $scope.reserror = false;

          }, function () {
            $scope.reserror = true;
          });
        }
        if ( $scope.selectedTab=='Tables') {
          Tables.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.tables = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }

        if ( $scope.selectedTab=='Status') {
          Status.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.status = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }

        if ( $scope.selectedTab=='Variables') {
          Variables.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.variables = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }
        if ( $scope.selectedTab=='MetaDataLocks') {
          MetaDataLocks.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.metadatalocks = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }
        if ( $scope.selectedTab=='QueryResponseTime') {
          QueryResponseTime.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.queryresponsetime = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }
        if ( $scope.selectedTab=='StatusInnoDB') {
          StatusInnoDB.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.statusinnodb = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }

        if ( $scope.selectedTab=='ServiceOpenSVC') {
          ServiceOpenSVC.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer}, function (data) {
            $scope.serviceopensvc = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }

      } // End Selected Server

      $scope.bsTableStatus = {
        options: {
          data: $scope.status,
          rowStyle: function (row, index) {
            return { classes: 'none' }
          },
          cache: false,
          striped: true,
          pagination: false,
          pageSize: 20,
          pageList: [5, 10, 25, 50, 10],
          search: true,
          showColumns: false,
          showRefresh: false,
          clickToSelect: false,
          showToggle: false,
          maintainSelected: true,
          columns: [ {
            field: 'variableName',
            title: 'Name',
            align: 'left',
            valign: 'bottom',
            sortable: true
          }, {
            field: 'value',
            title: 'Value',
            align: 'right',
            valign: 'bottom',
            sortable: true
          }]
        }
      };
      $scope.bsTableSlow = {
        options: {
          data: $scope.slowqueries,
          rowStyle: function (row, index) {
            return { classes: 'none' }
          },
          paginationLoop: false,
          cache: false,
          striped: true,
          pagination: true,
          pageSize: 20,
          pageList: [5, 10, 25, 50, 100],
          search: true,
          showColumns: false,
          showRefresh: false,
          clickToSelect: false,
          showToggle: false,
          maintainSelected: false,
          columns: [
            {
              field: 'id',
              title: '#',
              width: "4%",
              formatter: function (value, row, index) {
                return index + 1
              }
            },
            {
              field: 'lastSeen',
              title: 'Time',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "15%"
            }, {
              field: 'schemaName',
              title: 'Schema',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "10%"
            }, {
              field: 'query',
              title: 'Query',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "40%"
            }, {
              field: 'execTimeTotal',
              title: 'Time',
              align: 'left',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'rowsScanned',
              title: 'Rows Examined',
              align: 'left',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'rowsSent',
              title: 'Rows Sent',
              align: 'true',
              valign: 'bottom',
              sortable: true
            }
          ]
        }
      };
      $scope.bsMetaDataLocks = {
        options: {
          data: $scope.metadatalocks,
          rowStyle: function (row, index) {
            return { classes: 'none' }
          },
          paginationLoop: false,
          cache: false,
          striped: true,
          pagination: true,
          pageSize: 20,
          pageList: [5, 10, 25, 50, 100],
          search: true,
          showColumns: false,
          showRefresh: false,
          clickToSelect: false,
          showToggle: false,
          maintainSelected: false,
          columns: [
            {
              field: 'threadId',
              title: '#',
              width: "4%",
              formatter: function (value, row, index) {
                return index + 1
              }
            },
            {
              field: 'lastSeen',
              title: 'Time',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "15%"
            }, {
              field: 'lockMode.String',
              title: 'Schema',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "20%"
            }, {
              field: 'lockDuration.String',
              title: 'Duration',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "15%"
            }, {
              field: 'lockType.String',
              title: 'Lock Type',
              align: 'left',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'lockSchema.String',
              title: 'Schema',
              align: 'left',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'lockName.String',
              title: 'Table',
              align: 'true',
              valign: 'bottom',
              sortable: true
            }
          ]
        }
      };
      $scope.bsQueryResponseTime = {
        options: {
          data: $scope.queryresponsetime,
          rowStyle: function (row, index) {
            return { classes: 'none' }
          },
          paginationLoop: false,
          cache: false,
          striped: true,
          pagination: true,
          pageSize: 20,
          pageList: [5, 10, 25, 50, 100],
          search: true,
          showColumns: false,
          showRefresh: false,
          clickToSelect: false,
          showToggle: false,
          maintainSelected: false,
          columns: [
            {
              field: 'time',
              title: '#',
              width: "4%",
              formatter: function (value, row, index) {
                return index + 1
              }
            },
            {
              field: 'time',
              title: 'Time',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "30%"
            }, {
              field: 'count',
              title: 'Count',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "20%"
            }, {
              field: 'total',
              title: 'Total',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "15%"
            }
          ]
        }
      };


      $scope.bsTableProcessList = {
        options: {
          data: $scope.processlist,
          rowStyle: function (row, index) {
            return { classes: 'none' }
          },
          cache: false,
          striped: true,
          pagination: true,
          pageSize: 20,
          search: false,
          showColumns: false,
          showRefresh: false,
          clickToSelect: false,
          showToggle: false,
          maintainSelected: false,
          columns: [
            {
              field: 'id',
              title: 'Id',
              align: 'left',
              valign: 'bottom',
              width: "4%"
            }, {
              field: 'user',
              title: 'User',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "8%"
            }, {
              field: 'host',
              title: 'Host',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "8%"
            },
            {
              field: 'db.String',
              title: 'Db',
              align: 'left',
              valign: 'bottom',
              sortable: true
            },
            {
              field: 'command',
              title: 'Command',
              align: 'left',
              valign: 'bottom',
              sortable: true,
              width: "10%"
            }, {
              field: 'time.Float64',
              title: 'Time',
              align: 'left',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'state.String',
              title: 'State',
              align: 'tlef',
              valign: 'bottom',
              sortable: true
            }, {
              field: 'info.String',
              title: 'Info',
              align: 'true',
              valign: 'bottom',
              sortable: true,
              width: "40%"
            }
          ]
        }
      };
      $scope.$digest()
    };
    //end callServices

    $scope.startPromise = function()  {

      promise = $timeout(function() {
        $scope.callServices();
        $scope.startPromise();
      }, $scope.refreshInterval);
    }

    $scope.start = function() {
      // Don't start if already defined
      if ( angular.isDefined( $scope.promise) ) return;
      $scope.startPromise();
    };

    $scope.calculateInterval = function(number) {
      $scope.refreshInterval += Number(number);
      //change the interval
      $timeout.cancel( $scope.promise);
      $scope.startPromise();
    };

    $scope.checkIfAllowedInterval = function(number){
      if (number > 2000 && number < 600000) {
        $scope.refreshInterval = number;
      }else{
        $scope.refreshInterval = 2000;
      }
    };

    $scope.cancel = function () {
      $timeout.cancel($scope.promise);
        $scope.promise = undefined;
    };

    $scope.$on('$destroy', function() {
      $scope.cancel();
    });


    var httpGetWithoutResponse = function (url) {
      $http.get(url)
      .then(
        function () {
          console.log("Ok.");
        },
        function () {
          console.log("Error.");
        });
      };

      var createCluster = function (cluster,plan,orchestrator,headcluster) {
          alert(headcluster);
          if (headcluster) {

          $http.get('/api/clusters/' + headcluster  + '/actions/add/' +cluster)
          .then(
          function () {
            console.log('cluster created..' + orchestrator);
            createClusterSetOrchetrator(cluster,plan,orchestrator);
          },
          function () {
            console.log("Error cluster create.");
          });
        } else {
          $http.get('/api/clusters/actions/add/' +cluster)
          .then(
          function () {
            console.log('cluster created..' + orchestrator);
            createClusterSetOrchetrator(cluster,plan,orchestrator);
          },
          function () {
            console.log("Error cluster create.");
          });
        }
        };

        var createClusterSetOrchetrator = function (cluster,plan,orchestrator) {
            $http.get('/api/clusters/'+ cluster + '/settings/actions/set/prov-orchestrator/'+orchestrator)
            .then(
            function () {
              console.log('Set orchetrator done..');
              createClusterSetPlan(cluster,plan);
            },
            function () {
              console.log("Error in set orchetrator.");
            });
          };

    var createClusterSetPlan = function (cluster,plan) {
        console.log('Setting plan..' + plan);
        httpGetWithoutResponse('/api/clusters/'+ cluster + '/settings/actions/set/prov-service-plan/'+plan);
    };

      $scope.isEqualLongQueryTime = function (a, b) {
        if (Number(a)==Number(b)) {
          return true;
        }
        return false;
      };


      $scope.switch = function (fail) {
        if (fail) {
          if (confirm("Confirm failover")) httpGetWithoutResponse(getClusterUrl() + '/actions/failover');
        } else {
          if (confirm("Confirm switchover")) httpGetWithoutResponse(getClusterUrl() + '/actions/switchover');
        }
      };

      $scope.rolling = function (fail) {
          if (confirm("Confirm rolling restart")) httpGetWithoutResponse(getClusterUrl() + '/actions/rolling');
      };

      $scope.rotationkeys = function () {
        if (confirm("Confirm rotation certificates")) httpGetWithoutResponse(getClusterUrl() + '/actions/rotatekeys');
      };

      $scope.clbootstrap = function (topo) {
        if (confirm("Bootstrap operation will destroy your existing replication setup. \n Are you really sure?")) httpGetWithoutResponse(getClusterUrl() + '/actions/replication/bootstrap/' + topo);
      };

      $scope.dbmaintenance = function (server) {
        if (confirm("Confirm maintenance for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/maintenance');
      };
      $scope.dbstart = function (server) {
        if (confirm("Confirm start for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/start');
      };
      $scope.dbstop = function (server) {
        if (confirm("Confirm stop for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/stop');
      };
      $scope.dbprovision = function (server) {
        if (confirm("Confirm provision server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/provision');
      };
      $scope.dbunprovision = function (server) {
        if (confirm("Confirm unprovision for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/unprovision');
      };
      $scope.prxprovision = function (id) {
        if (confirm("Confirm provision proxy id: " + id)) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/provision');
      };
      $scope.prxunprovision = function (id) {
        if (confirm("Confirm unprovision proxy id: " + id)) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/unprovision');
      };
      $scope.prxstop = function (id) {
        if (confirm("Confirm stop proxy id: " + id)) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/stop');
      };
      $scope.prxstart= function (id) {
        if (confirm("Confirm start proxy id: " + id)) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/start');
      };
      $scope.dbreseedxtrabackup = function (server) {
        if (confirm("Confirm reseed with xtrabackup for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/physicalbackup');
      };
      $scope.dbreseedmysqldump = function (server) {
        if (confirm("Confirm reseed with mysqldump for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/logicalbackup');
      };
      $scope.dbreseedmysqldumpmaster = function (server) {
        if (confirm("Confirm reseed with mysqldump for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/logicalmaster');
      };
      $scope.dbxtrabackup = function (server) {
        if (confirm("Confirm sending xtrabackup for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/backup-physical');
      };
      $scope.dbdump = function (server) {
        if (confirm("Confirm sending mysqldump for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/backup-logical');
      };
      $scope.dbskipreplicationevent = function (server) {
        if (confirm("Confirm skip replication event for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/skip-replication-event');
      };
      $scope.dbtoogleinnodbmonitor = function (server) {
        if (confirm("Confirm toogle innodb monitor server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-innodb-monitor');
      };
      $scope.dbtooglemetadalocks= function (server) {
        if (confirm("Confirm toogle metadata lock plugin server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-meta-data-locks');
      };
      $scope.dbtooglequeryresponsetime= function (server) {
        if (confirm("Confirm toogle query response time plugin server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-query-response-time');
      };
      $scope.dbtoogleslowquerycapture = function (server) {
        if (confirm("Confirm toogle slow query capture server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query-capture');
      };


      $scope.dbtoogleslowquery = function (server) {
        if (confirm("Confirm toogle slow query log capture: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query');
      };
      $scope.dbtooglepfsslowquery = function (server) {
        if (confirm("Confirm toogle slow query PFS capture: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-pfs-slow-query');
      };
      $scope.dbresetpfsslow = function (server) {
        if (confirm("Confirm toogle slow query PFS capture: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reset-pfs-queries');
      };
      $scope.dbtoogleslowquerytable = function (server) {
        if (confirm("Confirm toogle slow query mode between TABLE and FILE server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query-table');
      };


      $scope.dbtooglepfsslow = function (server) {
        confirm("Confirm toogle digest mode between PFS and SLOW server-id: " + server) ;
        if ($scope.digestmode=="slow") {
          $scope.digestmode="pfs";
        }  else {
          $scope.digestmode="slow";
        }
      };

      $scope.dbtooglereadonly = function (server) {
        if (confirm("Confirm toogle read only on server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-read-only');
      };
      $scope.dbstartslave = function (server) {
        if (confirm("Confirm start slave on server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/start-slave');
      };
      $scope.dbstopslave = function (server) {
        if (confirm("Confirm start slave on server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/stop-slave');
      };
      $scope.dbresetmaster = function (server) {
        if (confirm("Confirm reset master this may break replication when done on master, server-id : " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reset-master');
      };

      $scope.dboptimize = function (server) {
        if (confirm("Confirm optimize for server-id: " + server)) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/optimize');
      };

      $scope.toggletraffic = function () {
        if (confirm("Confirm toggle traffic")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/switch/database-hearbeat');
      };


      $scope.resetfail = function () {
        if (confirm("Reset Failover counter?")) httpGetWithoutResponse(getClusterUrl() + '/actions/reset-failover-counter');
      };

      $scope.setactive = function () {
        if (confirm("Confirm Active Status?")) httpGetWithoutResponse(getClusterUrl() + '/api/setactive');
      };

      $scope.provision = function () {
        if (confirm("Provision Cluster. \n Are you really sure?")) httpGetWithoutResponse(getClusterUrl() + '/services/actions/provision');
      };

      $scope.unprovision = function () {
        if (confirm("Unprovision operation will destroy your existing data. \n Are you really sure?")) httpGetWithoutResponse(getClusterUrl() + '/services/actions/unprovision');
      };

      $scope.rolling = function () {
        httpGetWithoutResponse(getClusterUrl() + '/actions/rolling');
      };


      $scope.gtidstring = function (arr) {
        var output = [];
        if (arr != null) {
          for (i = 0; i < arr.length; i++) {
            var gtid = "";
            gtid = arr[i].domainId + '-' + arr[i].serverId + '-' + arr[i].seqNo;
            output.push(gtid);
          }
          return output.join(", ");
        }
        return '';
      };

      $scope.test = function () {
        if (confirm("Confirm test run, this could cause replication to break!")) httpGetWithoutResponse('/api/tests');
      };


      $scope.sysbench = function () {
        if (confirm("Confirm sysbench run !")) httpGetWithoutResponse(getClusterUrl() + '/actions/sysbench');
      };

      $scope.runonetest = function () {
        if (confirm("Confirm run one test !"+$scope.tests)) {
          httpGetWithoutResponse(getClusterUrl() + '/tests/actions/run/' + $scope.tests);
          $scope.tests = "";
        }
      };


      $scope.optimizeAll = function () {
        httpGetWithoutResponse(getClusterUrl() + '/actions/optimize');
      };

      $scope.backupphysical = function (server) {
        if (confirm("Confirm master physical backup")) httpGetWithoutResponse(getClusterUrl() + '/actions/master-physical-backup');
      };


      $scope.cladddbtag = function (tag) {
        if (confirm("Confirm add tag "+tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/add-db-tag/'+tag);
      };
      $scope.cldropdbtag = function (tag) {
        if (confirm("Confirm drop tag "+tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/drop-db-tag/'+tag);
      };

      $scope.claddproxytag = function (tag) {
        if (confirm("Confirm add tag "+tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/add-proxy-tag/'+tag);
      };
      $scope.cldropproxytag = function (tag) {
        if (confirm("Confirm drop tag "+tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/drop-proxy-tag/'+tag);
      };


      $scope.clsetdbcore = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-cpu-cores/'+value.toString());
      };
      $scope.clsetdbdisk = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-size/'+value.toString());
      };
      $scope.clsetdbio = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString(),add)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-iops/'+value.toString());
      };
      $scope.clsetdbmem = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-memory/'+value.toString());
      };

      $scope.saveDBImage = function (image) {
        if (confirm("Confirm change database OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-image/'+image);
      };
      $scope.saveProxySQLImage = function (image) {
        if (confirm("Confirm change ProxySQL OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-proxysql-img/'+image);
      };
      $scope.saveProxySQLImage = function (image) {
        if (confirm("Confirm change HaProxy OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-haproxy-img/'+image);
      };
      $scope.saveShardproxyImage = function (image) {
        if (confirm("Confirm change Shard Proxy OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-shardproxy-img/'+image);
      };
      $scope.saveMaxscaleImage = function (image) {
        if (confirm("Confirm change Maxscale Proxy OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-maxscale-img/'+image);
      };
      $scope.saveSphinxImage = function (image) {
        if (confirm("Confirm change Sphinx OCI image: "+image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/setprov-sphinx-docker-img/'+image);
      };

      $scope.saveDBDisk = function (selectedDBDiskTyoe,selectedDBDiskFS,selectedDBDiskPool,selectedDBDiskDevice) {
        if (confirm("Confirm change DB disk: "+selectedDBDiskTyoe+ "/"+selectedDBDiskFS+"/"+selectedDBDiskPool+"/"+selectedDBDiskDevice)) {
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-type/'+selectedDBDiskTyoe);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-fs/'+selectedDBDiskFS);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-pool/'+selectedDBDiskPool);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-device/'+selectedDBDiskDevice);
        }
      };

      $scope.saveProxyDisk = function (selectedProxyDiskType,selectedProxyDiskFS,selectedProxyDiskPool,selectedProxyDiskDevice) {
        if (confirm("Confirm change Proxy disk: "+selectedProxyDiskType+ "/"+selectedProxyDiskFS+"/"+selectedProxyDiskPool+"/"+selectedProxyDiskDevice)) {
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-type/'+selectedProxyDiskType);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-fs/'+selectedProxyDiskFS);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-pool/'+selectedProxyDiskPool);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-device/'+selectedProxyDiskDevice);
        }
      };

      $scope.saveDBServiceType = function (selectedDBVM,selectedProxyVM) {
        if (confirm("Confirm change VM type: "+selectedDBVM+ "/"+selectedProxyVM)) {
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-service-type/'+selectedDBVM);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-service-type/'+selectedProxyVM);
        }
      };

      $scope.saveBackupType = function (selectedLogicalBackup,selectedPhysicalBackup) {
        if (confirm("Confirm backup types: "+selectedLogicalBackup+ "/" + selectedPhysicalBackup)) {
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-logical-type/'+selectedLogicalBackup);
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-physical-type/'+selectedPhysicalBackup);
        }
      };

      $scope.clsetproxycore = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-cpu-cores/'+value.toString());
      };
      $scope.clsetproxydisk = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-size/'+value.toString());
      };

      $scope.clsetproxymem = function (base,add) {
        value= Number(base)+add;
        if (confirm("Confirm add tag "+value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-memory/'+value.toString());
      };

      $scope.switchsettings = function (setting) {
        httpGetWithoutResponse(getClusterUrl() + '/settings/actions/switch/' + setting);
      };

      $scope.reshardtable = function (schema,table) {
        httpGetWithoutResponse(getClusterUrl() + '/schema/'+schema+'/'+table+'/actions/reshard-table');
      };

      $scope.checksumtable = function (schema,table) {
        httpGetWithoutResponse(getClusterUrl() + '/schema/'+schema+'/'+table+'/actions/checksum-table');
      };

      $scope.checksumalltables = function (schema,table) {
        httpGetWithoutResponse(getClusterUrl() + '/actions/checksum-all-tables');
      };
      $scope.$watch('settings.maxdelay', function (newVal, oldVal) {
        if (typeof newVal != 'undefined') {
          httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/failover-max-slave-delay/' + newVal);
        }
      });

      $scope.setsettings = function (setting, value) {
        httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/' + setting + '/' + value);
      };

      $scope.openCluster = function (cluster) {
    //    $scope.refreshInterval = 2000;
        $scope.selectedClusterName = cluster;
      };

      $scope.back = function () {
        if (typeof $scope.selectedServer != 'undefined'){
          $scope.selectedServer = undefined;
        }   else   {
          $scope.selectedClusterName = undefined;
        }
        $scope.menuOpened = false;

        $scope.selectedCluster = undefined;
        $mdSidenav('right').close();
      };

      $scope.openClusterDialog = function () {
        $mdDialog.show({
          contentElement: '#myClusterDialog',
          scope: $scope,
          preserveScope: true,
          clickOutsideToClose: false,
          escapeToClose: false
        });
        $scope.menuOpened = true;
        $scope.openedAt = new Date().toLocaleString();
      };

      $scope.closeClusterDialog = function () {

        $mdDialog.hide({contentElement: '#myClusterDialog'});
        $scope.menuOpened = false;
        $scope.servers = {};
        $scope.slaves = {};
        $scope.master = {};
        $scope.alerts = {};
        $scope.logs = {};
        $scope.proxies = {};
        $mdSidenav('right').close();
      };

      $scope.newClusterDialog = function () {
       $scope.menuOpened = true;
        $mdDialog.show({
          contentElement: '#myNewClusterDialog',
         preserveScope: true,
          parent: angular.element(document.body),
          //      clickOutsideToClose: false,
          //    escapeToClose: false,
        });
      };
      $scope.closeNewClusterDialog = function (plan,orchestrator) {
        $mdDialog.hide({contentElement: '#myNewClusterDialog',});
        if (confirm("Confirm Creating Cluster " + $scope.dlgAddClusterName + " "  +  plan+" for " + orchestrator )) {
          createCluster( $scope.dlgAddClusterName,plan,orchestrator,$scope.selectedClusterName);

          $scope.selectedClusterName = $scope.dlgAddClusterName;
          $scope.servers={};
          $scope.slaves={};
          $scope.master={};
          $scope.alerts={};
          $scope.logs={};
          $scope.proxies={};
        //  $scope.callServices();
        //  $scope.setClusterCredentialDialog();
        }
        $mdSidenav('right').close();
        $scope.menuOpened = false;
      };
      $scope.cancelNewClusterDialog = function () {
        $mdDialog.hide({contentElement: '#myNewClusterDialog',});
        $mdSidenav('right').close();
        $scope.menuOpened = false;
      };


      $scope.newUserDialog = function () {
       $scope.menuOpened = true;
        $mdDialog.show({
          contentElement: '#myNewUserDialog',
         preserveScope: true,
          parent: angular.element(document.body),
        });
      };
      $scope.closeNewUserDialog = function () {
        $mdDialog.hide({contentElement: '#myNewUserDialog',});
        if (confirm("Confirm Creating Cluster " + $scope.dlgAddUserName  )) {
            angular.forEach($scope.newUserAcls, function (value, index) {
          //   console.log(value);
             alert(value.grant +':'+value.enable);
            });
        };

        $mdSidenav('right').close();
        $scope.menuOpened = false;
      };



      $scope.cancelNewUserDialog = function () {
        $mdDialog.hide({contentElement: '#myNewUserDialog',});
        $mdSidenav('right').close();
        $scope.menuOpened = false;
      };


      $scope.newServerDialog = function () {
        $mdDialog.show({
          contentElement: '#myNewServerDialog',
          parent: angular.element(document.body),
        });
      };
      $scope.closeNewServerDialog = function () {
        $mdDialog.hide({contentElement: '#myNewServerDialog',});
        if (confirm("Confirm adding new server " + $scope.dlgServerName + ":" + $scope.dlgServerPort+ "  "+ $scope.selectedMonitor.id)) httpGetWithoutResponse(getClusterUrl() + '/actions/addserver/' + $scope.dlgServerName + '/' + $scope.dlgServerPort+"/"+$scope.selectedMonitor.id);
      };
      $scope.cancelNewServerDialog = function () {
        $mdDialog.hide({contentElement: '#myNewServerDialog',});
      };

      $scope.setClusterCredentialDialog = function () {
        $mdDialog.show({
          contentElement: '#myClusterCredentialDialog',
          parent: angular.element(document.body),
          clickOutsideToClose: false,
          escapeToClose: false,
        });
      };
      $scope.closeClusterCredentialDialog = function () {
        $mdDialog.hide({contentElement: '#myClusterCredentialDialog',});
        if (confirm("Confirm set user/password")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/db-servers-credential/' + $scope.dlgClusterUser + ':' + $scope.dlgClusterPassword);
      };
      $scope.cancelClusterCredentialDialog = function () {
        $mdDialog.hide({contentElement: '#myClusterCredentialDialog',});
      };

      $scope.setRplCredentialDialog = function () {
        $mdDialog.show({
          contentElement: '#myRplCredentialDialog',
          parent: angular.element(document.body),
          clickOutsideToClose: false,
          escapeToClose: false,
        });
      };
      $scope.closeRplCredentialDialog = function () {
        $mdDialog.hide({contentElement: '#myRplCredentialDialog',});
        if (confirm("Confirm set user/password")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/replication-credential/' + $scope.dlgRplUser + ':' + $scope.dlgRplPassword);
      };
      $scope.cancelRplCredentialDialog = function () {
        $mdDialog.hide({contentElement: '#myRplCredentialDialog',});
      };

      $scope.openDebugClusterDialog = function () {
        $mdDialog.show({
          contentElement: '#myClusterDebugDialog',
          parent: angular.element(document.body),
        });
        $scope.menuOpened = true;
      };
      $scope.closeDebugClusterDialog = function () {
        $mdDialog.hide({contentElement: '#myClusterDebugDialog',});
        $scope.menuOpened = false;
      };

      $scope.openDebugServerDialog = function () {
        $mdDialog.show({
          contentElement: '#myServerDebugDialog',
          parent: angular.element(document.body),
        });
      };
      $scope.closeDebugServerDialog = function () {
        $mdDialog.hide({contentElement: '#myServerDebugDialog',});
      };

      $scope.openDebugProxiesDialog = function () {
        $mdDialog.show({
          contentElement: '#myProxiesDebugDialog',
          parent: angular.element(document.body),
        });
      };
      $scope.closeDebugProxiesDialog = function () {
        $mdDialog.hide({contentElement: '#myProxiesDebugDialog',});
      };

      $scope.selectUserIndex = function (index) {
        var r = confirm("Confirm select Index  " + index);
        if ($scope.selectedUserIndex !== index) {
          $scope.selectedUserIndex = index;
        }
        else {
          $scope.selectedUserIndex = undefined;
        }
      };

      $scope.onTabSelected  = function (tab) {

        $scope.selectedTab=tab;
      };

      $scope.onTabClicked  = function (tab) {
        $scope.selectedTab=tab;
      };

      $scope.openServer  = function (id) {
        $scope.selectedServer=id;
        $scope.onTabSelected('Processlist');
      };

      $scope.longQueryTime =  "0";


      $scope.updateLongQueryTime = function (time,name)  {
        if (confirm("Confirm change Long Query Time" +   time  + " on server "+  name  )) httpGetWithoutResponse(getClusterUrl() + '/servers/' + name +'/actions/set-long-query-time/'+time);
      };

      $scope.explainPlan = undefined;

      $scope.queryExplainPFS = function (digest) {
        $scope.selectedQuery=digest;
        ExplainPlanPFS.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer, queryDigest: $scope.selectedQuery}, function (data) {
          $scope.explainPlan = data;
          $scope.reserror = false;

        }, function () {
          $scope.reserror = true;
        });


      };

      $scope.queryExplainSlowLog = function (digest) {
        $scope.selectedQuery=digest;
        ExplainPlanSlowLog.query({clusterName: $scope.selectedClusterName,serverName: $scope.selectedServer, queryDigest: $scope.selectedQuery}, function (data) {
          $scope.explainPlan = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      };

      $scope.closeExplain = function () {
        $scope.selectedQuery=undefined;
      };

      var httpGetExplainPlan = function (url) {
        $http.get(url)
        .subscribe(res => {
          $scope.explainPlan= res._body;
        });

      };
      $scope.toggleLeft = buildToggler('left');
      $scope.toggleRight = buildToggler('right');

      function buildToggler(componentId) {
        return function () {
          $mdSidenav(componentId).toggle();

        };
      }
      $scope.toogleTabular = function()  {
        $scope.serverListTabular = !$scope.serverListTabular;
      };

      $scope.$on('$mdMenuOpen', function (event, menu) {
        console.log('Opening menu refresh server will stop...', event, menu);
        $scope.menuOpened = true;
        $scope.openedAt = new Date().toLocaleString();
      });

      $scope.$on('$mdMenuClose', function (event, menu) {
        console.log('Closing menu refresh servers will resume...', event, menu);
        $scope.menuOpened = false;
        $scope.openedAt = "";
      });
      $scope.getTablePct  = function (table,index) {
        return ((table+index) /($scope.selectedCluster.dbTableSize + $scope.selectedCluster.dbTableSize + 1)*100).toFixed(2);
      };


      $scope.start();

    });
