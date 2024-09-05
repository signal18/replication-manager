app.controller('DashboardController', function (
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
  Jobs,
  Tables,
  VTables,
  Status,
  Variables,
  StatusInnoDB,
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
  QueryRules,
  GraphiteFilterList
) {
  $scope.now = new Date()
  $scope.yearNow = $scope.now.getFullYear();
  $scope.selectedClusterName = undefined;
  $scope.selectedPlan = undefined;
  $scope.selectedOrchestrator = undefined;
  $scope.plans = undefined;
  $scope.selectedServer = undefined;
  $scope.selectedQuery = undefined;
  $scope.menuOpened = false;
  $scope.serverListTabular = false;
  $scope.selectedTab = undefined;
  $scope.selectedSubTab = undefined;
  $scope.selectedAcls = [];
  $scope.selectedUserIndex = undefined;
  $scope.newUserAcls = undefined;
  $scope.flatpickrInstance = undefined;
  $scope.refreshInterval = 4000;
  $scope.digestmode = "pfs";
  $scope.gfilter = {
    whitelist: "",
    blacklist: ""
  };
  $scope.gfilterUpdate = true
  $scope.grafanaConfigs = []
  $scope.showGC = false

  $scope.missingDBTags = [];
  $scope.missingProxyTags = [];
  $scope.promise = undefined;

  $scope.defaultRestoreForm = function () {
    $scope.restoreForm = {
      selectedServer: undefined,
      selectedHost: undefined,
      selectedPort: undefined,
      selectedBackup: undefined,
      pitr: undefined,
      restoreTime: undefined,
      canPITR: false,
    };
  }

  $scope.canPITR = function (selectedBackup) {
    $scope.restoreForm.pitr = false
    $scope.resetRestoreTime() // Reset restore time for show PITR
    let backup = $scope.selectedCluster.backupList[selectedBackup];
    if (backup) {
      const srv = $scope.servers.find(sv => sv.url === backup.source);
      if (srv && srv.binaryLogFiles[backup.binLogFileName]) {
        $scope.restoreForm.canPITR = true
        return
      }
    }
    $scope.restoreForm.canPITR = false
  }

  $scope.defaultRestoreForm();
  $scope.resetRestoreTime = function () {
    $scope.restoreForm.restoreTime = undefined
  }

  $scope.showTable = false
  $scope.showLog = true
  $scope.showLogTask = true
  $scope.showJobs = {}

  $scope.togglePanel = function (panel, srv) {
    if (panel == "log") {
      $scope.showLog = !$scope.showLog
    }
    if (panel == "task") {
      $scope.showLogTask = !$scope.showLogTask
    }
    if (panel == "jobs") {
      if ($scope.showJobs[srv] === undefined) {
        $scope.showJobs[srv] = true
      } else {
        $scope.showJobs[srv] = !$scope.showJobs[srv]
      }
    }
  }

  $scope.roCaptureTrigger = true
  $scope.selectedMonitoringCaptureTrigger = ""

  $scope.toggleCaptureTrigger = function () {
    $scope.roCaptureTrigger = !$scope.roCaptureTrigger
  }
  $scope.SetCaptureTrigger = function (val) {
    if ($scope.roCaptureTrigger) {
      $scope.selectedMonitoringCaptureTrigger = val
      angular.element(document.querySelector('#selectedMonitoringCaptureTrigger')).get(0).value = val
    }
  }

  $scope.roIgnoreErrors = true
  $scope.selectedMonitoringIgnoreErrors = ""

  $scope.toggleIgnoreErrors = function () {
    $scope.roIgnoreErrors = !$scope.roIgnoreErrors
  }
  $scope.SetIgnoreErrors = function (val) {
    if ($scope.roIgnoreErrors) {
      $scope.selectedMonitoringIgnoreErrors = val
      angular.element(document.querySelector('#selectedMonitoringIgnoreErrors')).get(0).value = val
    }
  }

  $scope.mariadbGtid = false
  $scope.mysqlGtid = false

  $scope.user = undefined;
  $scope.isReseeding = {};

  $scope.wait = undefined;
  $scope.settingsMenu = {
    general: true,
    monitoring: false,
    replication: false,
    rejoin: false,
    backups: false,
    proxies: false,
    schedulers: false,
    logs: false,
    graphs: false,
    global: false,
  };

  $scope.entries = {
    header: [],
    servers: []
  }

  $scope.monitors = [
    { id: 'mariadb', name: 'MariaDB' },
    { id: 'mysql', name: 'MySQL' },
    { id: 'percona', name: 'Percona' },
    { id: 'proxysql', name: 'ProxySQL' },
    { id: 'haproxy', name: 'HaProxy' },
    { id: 'shardproxy', name: 'ShardProxy' },
    { id: 'maxscale', name: 'MaxScale' },
    { id: 'sphinx', name: 'SphinxProxy' },
    { id: 'extvip', name: 'VIP' },];

  $scope.schedulersecondes = [
    { id: undefined, name: '' },
    { id: '*', name: 'ALL' },
    { id: '0', name: '00' },
    { id: '1', name: '01' },
    { id: '2', name: '02' },
    { id: '3', name: '03' },
    { id: '4', name: '04' },
    { id: '5', name: '05' },
    { id: '6', name: '06' },
    { id: '7', name: '07' },
    { id: '8', name: '08' },
    { id: '9', name: '09' },
    { id: '10', name: '10' },
    { id: '11', name: '11' },
    { id: '12', name: '12' },
    { id: '13', name: '13' },
    { id: '14', name: '14' },
    { id: '15', name: '15' },
    { id: '16', name: '16' },
    { id: '17', name: '17' },
    { id: '18', name: '18' },
    { id: '19', name: '19' },
    { id: '20', name: '20' },
    { id: '21', name: '21' },
    { id: '22', name: '22' },
    { id: '23', name: '23' },
    { id: '24', name: '24' },
    { id: '25', name: '25' },
    { id: '26', name: '26' },
    { id: '27', name: '27' },
    { id: '28', name: '28' },
    { id: '29', name: '29' },
    { id: '30', name: '30' },
    { id: '31', name: '31' },
    { id: '32', name: '32' },
    { id: '33', name: '33' },
    { id: '34', name: '34' },
    { id: '35', name: '35' },
    { id: '36', name: '36' },
    { id: '37', name: '37' },
    { id: '38', name: '38' },
    { id: '39', name: '39' },
    { id: '40', name: '40' },
    { id: '41', name: '41' },
    { id: '42', name: '42' },
    { id: '43', name: '43' },
    { id: '44', name: '44' },
    { id: '45', name: '45' },
    { id: '46', name: '46' },
    { id: '47', name: '47' },
    { id: '48', name: '48' },
    { id: '49', name: '49' },
    { id: '50', name: '50' },
    { id: '51', name: '51' },
    { id: '52', name: '52' },
    { id: '53', name: '53' },
    { id: '54', name: '54' },
    { id: '55', name: '55' },
    { id: '56', name: '56' },
    { id: '57', name: '57' },
    { id: '58', name: '58' },
    { id: '59', name: '59' },
  ];
  $scope.schedulerminutes = $scope.schedulersecondes;
  $scope.schedulerhours = [
    { id: undefined, name: '' },
    { id: '*', name: 'ALL' },
    { id: '0', name: '00' },
    { id: '1', name: '01' },
    { id: '2', name: '02' },
    { id: '3', name: '03' },
    { id: '4', name: '04' },
    { id: '5', name: '05' },
    { id: '6', name: '06' },
    { id: '7', name: '07' },
    { id: '8', name: '08' },
    { id: '9', name: '09' },
    { id: '10', name: '10' },
    { id: '11', name: '11' },
    { id: '12', name: '12' },
    { id: '13', name: '13' },
    { id: '14', name: '14' },
    { id: '15', name: '15' },
    { id: '16', name: '16' },
    { id: '17', name: '17' },
    { id: '18', name: '18' },
    { id: '19', name: '19' },
    { id: '20', name: '20' },
    { id: '21', name: '21' },
    { id: '22', name: '22' },
    { id: '23', name: '23' },
  ];
  $scope.schedulerdays = [
    { id: undefined, name: '' },
    { id: '*', name: 'ALL' },
    { id: '1', name: '01' },
    { id: '2', name: '02' },
    { id: '3', name: '03' },
    { id: '4', name: '04' },
    { id: '5', name: '05' },
    { id: '6', name: '06' },
    { id: '7', name: '07' },
    { id: '8', name: '08' },
    { id: '9', name: '09' },
    { id: '10', name: '10' },
    { id: '11', name: '11' },
    { id: '12', name: '12' },
    { id: '13', name: '13' },
    { id: '14', name: '14' },
    { id: '15', name: '15' },
    { id: '16', name: '16' },
    { id: '17', name: '17' },
    { id: '18', name: '18' },
    { id: '19', name: '19' },
    { id: '20', name: '20' },
    { id: '21', name: '21' },
    { id: '22', name: '22' },
    { id: '23', name: '23' },
    { id: '24', name: '24' },
    { id: '25', name: '25' },
    { id: '26', name: '26' },
    { id: '27', name: '27' },
    { id: '28', name: '28' },
    { id: '29', name: '29' },
    { id: '30', name: '30' },
    { id: '31', name: '31' },
  ];
  $scope.schedulermonths = [
    { id: undefined, name: '' },
    { id: '*', name: 'ALL' },
    { id: '1', name: 'JAN' },
    { id: '2', name: 'FEB' },
    { id: '3', name: 'MAR' },
    { id: '4', name: 'APR' },
    { id: '5', name: 'MAY' },
    { id: '6', name: 'JUN' },
    { id: '7', name: 'JUL' },
    { id: '8', name: 'AUG' },
    { id: '9', name: 'SEP' },
    { id: '10', name: 'OCT' },
    { id: '11', name: 'NOV' },
    { id: '12', name: 'DEC' },
  ];
  $scope.schedulerweeks = [
    { id: undefined, name: '' },
    { id: '*', name: 'ALL' },
    { id: '0', name: 'SUN' },
    { id: '1', name: 'MON' },
    { id: '2', name: 'TUE' },
    { id: '3', name: 'WED' },
    { id: '4', name: 'THU' },
    { id: '5', name: 'FRI' },
    { id: '6', name: 'SAT' },
  ];

  $scope.sortObjectValues = function (values, sortBy, direction) {
    if (!values) return [];

    let array = Object.values(values);
    if (direction.toLowerCase() == 'desc') {
      array.sort((a, b) => b[sortBy] - a[sortBy]); // Descending order
    } else {
      array.sort((a, b) => a[sortBy] - b[sortBy]); // Ascending order
    }
    return array;
  }

  $scope.formatDateToUTC = function (date) {
    // Check if the input is a valid Date object
    if (!(date instanceof Date) || isNaN(date)) {
      throw new Error('Invalid Date');
    }

    // Convert date to ISO string and extract the relevant parts
    var isoString = date.toISOString();
    var formattedDate = isoString.slice(0, 19).replace('T', ' ') + ' UTC';

    return formattedDate;
  }

  $scope.parseUTCDate = function (dateString) {
    // Ensure the input is a string
    if (typeof dateString !== 'string') {
      throw new Error('Input must be a string');
    }

    // Convert 'yyyy-MM-dd HH:mm:ss' to 'yyyy-MM-ddTHH:mm:ssZ'
    var isoString = dateString.replace(' ', 'T') + 'Z';

    // Create a Date object from the ISO string
    return new Date(isoString);
  }

  var getClusterUrl = function () {
    return AppService.getClusterUrl($scope.selectedClusterName);
  };

  var git_user = { username: "", password: "" };
  var token;
  var git_data = $location.search();

  var timeFrame = $routeParams.timeFrame;

  $scope.formatBytes = function (bytes, decimals = 2) {
    if (bytes === 0) return '0 Bytes';

    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));

    return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + ' ' + sizes[i];
  }

  $scope.bufferList = [1024, 2048, 4096, 8192, 16384, 32768, 65536, 1048576]

  if (git_data["user"] && git_data["token"] && !AppService.hasAuthHeaders()) {
    git_user.username = git_data["user"];
    token = git_data["token"];
    git_user.password = git_data["pass"]
    AppService.setAuthenticated(git_user.username, token);
    timeFrame = "";
    $location.url($location.path());
  }

  $scope.isLoggedIn = AppService.hasAuthHeaders();
  if (!$scope.isLoggedIn) {
    $location.path('login');
    return null;

  } else {
    $scope.user = AppService.getUser();
  }

  $scope.logout = function () {
    AppService.logout();
    $location.path('login');
  };


  if (timeFrame == "") {
    timeFrame = "10m";
    console.log('timeframe:', timeFrame);
  }

  $scope.toogleRefresh = function () {
    if ($scope.menuOpened) {
      $scope.menuOpened = false;
      //   $scope.openedAt = "";
    } else {
      $scope.menuOpened = true;
      //   $scope.openedAt = new Date().toLocaleString();
    }
  };
  $scope.addTokenStyle = {}
  $scope.roApiTokenTimeout = true
  $scope.selectedApiTokenTimeout = 48

  $scope.canCancel = function (t) {
    return t.state === 0 || (t.start < Math.floor((Date.now() - 300000) / 1000) && !t.end && t.state < 3)
  }

  $scope.getBackupMethod = function (method) {
    switch (method) {
      case 1: return 'Logical';
      case 2: return 'Physical';
      default: return 'Unknown';
    }
  };

  $scope.getBackupStrategy = function (strategy) {
    switch (strategy) {
      case 1: return 'Full';
      case 2: return 'Incremental';
      case 3: return 'Differential';
      default: return 'Unknown';
    }
  };

  $scope.SetApiTokenTimeout = function (val) {
    if ($scope.roApiTokenTimeout) {
      $scope.selectedApiTokenTimeout = Number(val)
      angular.element(document.querySelector('#selectedApiTokenTimeout')).get(0).value = Number(val)
    }
  }

  $scope.addTokenExpireHour = function (val) {
    if (!$scope.roApiTokenTimeout) {
      $scope.selectedApiTokenTimeout += Number(val);
      angular.element(document.querySelector('#selectedApiTokenTimeout')).get(0).value = Number($scope.selectedApiTokenTimeout)
    }
  };

  $scope.toggleApiTokenTimeout = function () {
    $scope.roApiTokenTimeout = !$scope.roApiTokenTimeout
    $scope.SetApiTokenTimeout($scope.selectedCluster.config.apiTokenTimeout)
    if ($scope.roApiTokenTimeout) {
      $scope.addTokenStyle = {}
    } else {
      $scope.addTokenStyle = { "color": "#468cc5" }
    }
  }

  $scope.setTask = function (val) {
    $scope.selectedTask = val
  }

  $scope.callServices = function () {


    $scope.isLoggedIn = AppService.hasAuthHeaders();

    if (!AppService.hasAuthHeaders() || $scope.menuOpened == true) {
      $timeout.cancel($scope.promise);
      return null;
    }
    //  $scope.selectedPlan = "";
    // get list of clusters
    //  if ($scope.selectedClusterName === undefined && $scope.selectedServer === undefined ) {
    if (!$scope.selectedClusterName && !$scope.selectedServer) {
      Clusters.query({}, function (data) {
        if (data) {
          $scope.clusters = data;

          if ($scope.clusters.length === 1 && $scope.settings.config.monitoringSaveConfig == false && $scope.clusters[0].name == "Default") {
            $scope.selectedClusterName = $scope.clusters[0].name;
          }
        }
      }, function () {
        $scope.reserror = true;
      });
      Monitor.query({}, function (data) {
        if (data) {
          $scope.settings = data;
          $scope.plans = $scope.settings.servicePlans;
          $scope.orchestrators = $scope.settings.serviceOrchestrators;
          $scope.selectedPlan = $scope.plans[12];
          $scope.selectedOrchestrator = $scope.orchestrators[3];
          $scope.selectedPlanName = $scope.selectedPlan.plan;
          //     if ($scope.newUserAcls === undefined)  {
          //  alert(data.config.httpRefreshInterval);
          //
          if (!$scope.refreshInterval) {
            $scope.refreshInterval = $scope.settings.config.httpRefreshInterval;
          }
          $scope.newUserAcls = JSON.parse(JSON.stringify($scope.settings.serviceAcl));
          if ((data.logs) && (data.logs.buffer)) $scope.logs = data.logs.buffer;

        }
      }, function () {
        $scope.reserror = true;
      });

    }
    // end !$scope.selectedServer & $scope.selectedClusterName
    if ($scope.selectedClusterName) {
      console.log($scope.selectedClusterName);
      Servers.query({ clusterName: $scope.selectedClusterName }, function (data) {
        if (!$scope.menuOpened) {
          if (data) {
            $scope.servers = data;
            function myfilter(array, test) {
              var passedTest = [];
              for (var i = 0; i < array.length; i++) {
                if (test(array[i]))
                  passedTest.push(array[i]);
              }
              return passedTest;
            }
            $scope.slaves = myfilter(data, function (currentServer) { return (currentServer.isSlave); });
            $scope.mariadbGtid = Array.isArray(data) ? data.some(function (currentServer) { return (currentServer.haveMariadbGtid); }) : false;
            $scope.mysqlGtid = Array.isArray(data) ? data.some(function (currentServer) { return (currentServer.haveMysqlGtid); }) : false;
            $scope.reserror = false;
          }
        }
      }, function () {
        $scope.reserror = true;
      });
    } // fetch server most of  the time
    if ($scope.selectedClusterName && !$scope.selectedServer) {
      Cluster.query({ clusterName: $scope.selectedClusterName }, function (data) {
        $scope.selectedCluster = data;
        function isInTags(array, array2, test) {
          var passedTest = [];
          for (var i = 0; i < array.length; i++) {
            if (test(array[i].name, array2))
              passedTest.push(array[i]);
          }
          return passedTest;
        }
        $scope.agents = data.agents;
        $scope.missingDBTags = isInTags(data.configurator.configTags, data.configurator.dbServersTags, function (currentTag, dbTags) { return (dbTags.indexOf(currentTag) == -1); });
        $scope.missingProxyTags = isInTags(data.configurator.configPrxTags, data.configurator.proxyServersTags, function (currentTag, proxyTags) { return (proxyTags.indexOf(currentTag) == -1); });
        $scope.SetIgnoreErrors(data.config.monitoringIgnoreErrors);
        $scope.SetCaptureTrigger(data.config.monitoringCaptureTrigger);
        $scope.SetApiTokenTimeout(data.config.apiTokenTimeout);

        $scope.reserror = false;
      }, function () {
        $scope.reserror = true;
      });

      Jobs.get($scope.selectedClusterName)
        .then(function (jobEntries) {
          $scope.entries.header = jobEntries.header;
          $scope.entries.servers = jobEntries.servers;
        })
        .catch(function (error) {
          console.error('Error getting job entries:', error);
        });



      if ($scope.selectedTab == 'Shards') {
        VTables.query({ clusterName: $scope.selectedClusterName }, function (data) {
          $scope.vtables = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }


      Alerts.query({ clusterName: $scope.selectedClusterName }, function (data) {
        $scope.alerts = data;
      }, function () {
        $scope.reserror = true;
      });


      // console.log($scope.selectedServer);
      // console.log($scope.selectedTab);


      Master.query({ clusterName: $scope.selectedClusterName }, function (data) {
        $scope.master = data;
      }, function () {
        $scope.reserror = true;
      });

      if ($scope.selectedTab == 'Proxies') {
        Proxies.query({ clusterName: $scope.selectedClusterName }, function (data) {
          if (!$scope.menuOpened) {
            $scope.proxies = data;
            $scope.reserror = false;
          }

        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'Backups') {
        Backups.query({ clusterName: $scope.selectedClusterName }, function (data) {
          if (!$scope.menuOpened) {
            $scope.backups = data;
            $scope.reserror = false;
          }

        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'Certificates') {
        Certificates.query({ clusterName: $scope.selectedClusterName }, function (data) {
          if (!$scope.menuOpened) {
            $scope.certificates = data;
            $scope.reserror = false;
          }
        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'QueryRules') {
        QueryRules.query({ clusterName: $scope.selectedClusterName }, function (data) {
          if (!$scope.menuOpened) {
            $scope.queryrules = data;
            $scope.reserror = false;
          }
        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'Settings') {
        //Only change if not configured yet
        if ($scope.grafanaConfigs.length == 0) {
          $http.get('/api/configs/grafana').then(function (res) {
            $scope.grafanaConfigs = res.data
          })
        }

        if ($scope.settingsMenu.graphs) {
          //Only change if updated
          if ($scope.gfilterUpdate) {
            GraphiteFilterList.get({ clusterName: $scope.selectedClusterName }, function (data) {
              if (!$scope.menuOpened) {
                $scope.gfilter = data;
                $scope.gfilterUpdate = false
                $scope.reserror = false;
              }
            }, function () {
              $scope.reserror = true;
            });
          }
        }
      }
    }
    if ($scope.selectedClusterName && $scope.selectedServer) {
      if ($scope.selectedTab == 'Processlist') {
        Processlist.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.processlist = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }

      if ($scope.selectedTab == 'PFSQueries') {
        if ($scope.digestmode == 'pfs') {
          PFSStatements.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
            $scope.pfsstatements = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        } else {
          PFSStatementsSlowLog.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
            $scope.pfsstatements = data;
            $scope.reserror = false;
          }, function () {
            $scope.reserror = true;
          });
        }
      }

      if ($scope.selectedTab == 'LogSlow') {
        SlowQueries.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.slowqueries = data;
          $scope.reserror = false;

        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'Tables') {
        Tables.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.tables = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }

      if ($scope.selectedTab == 'Status') {
        Status.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.status = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }

      if ($scope.selectedTab == 'Variables') {
        Variables.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.variables = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'MetaDataLocks') {
        MetaDataLocks.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.metadatalocks = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'QueryResponseTime') {
        QueryResponseTime.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.queryresponsetime = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }
      if ($scope.selectedTab == 'StatusInnoDB') {
        StatusInnoDB.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
          $scope.statusinnodb = data;
          $scope.reserror = false;
        }, function () {
          $scope.reserror = true;
        });
      }

      if ($scope.selectedTab == 'ServiceOpenSVC') {
        ServiceOpenSVC.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer }, function (data) {
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
        columns: [{
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
    //      $scope.$digest()
    return null;
  };
  //end callServices

  /*  $scope.startPromise = function()  {
      // https://github.com/angular/angular.js/issues/1522 $timeout replaced window.setTimeout
      promise =  window.setTimeout(function() {
        $scope.callServices();
        $scope.startPromise();
      }, $scope.refreshInterval);
    }
    */



  $scope.startPromise = function () {
    $timeout.cancel($scope.promise);
    //        console.log(  $scope.refreshInterval);
    if ($scope.isLoggedIn) {
      promise = $timeout(function () {
        $scope.callServices();
        $scope.startPromise();
      }, $scope.refreshInterval);
    }
  };

  $scope.toogleTable = function () {
    $scope.showTable = !$scope.showTable;
  };



  $scope.start = function () {
    //    console.log("start promise");
    // Don't start if already defined
    if ($scope.promise) return;
    $scope.startPromise();
  };

  $scope.$on('$destroy', function () {
    $timeout.cancel($scope.promise);
  });


  $scope.calculateInterval = function (number) {
    $scope.refreshInterval += Number(number);
  };

  $scope.checkIfAllowedInterval = function (number) {
    if (number > 2000 && number < 600000) {
      $scope.refreshInterval = number;
    } else {
      $scope.refreshInterval = 2000;
    }
  };

  /*    $scope.cancel = function () {
        $timeout.cancel($scope.promise);
          $scope.promise = undefined;
      };

  */


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

  var createCluster = function (cluster, plan, orchestrator, headcluster) {
    alert(cluster);
    if (headcluster) {

      $http.get('/api/clusters/' + headcluster + '/actions/add/' + cluster)
        .then(
          function () {
            console.log('cluster created..' + orchestrator);
            createClusterSetOrchetrator(cluster, plan, orchestrator);
          },
          function () {
            console.log("Error cluster create.");
          });
    } else {
      $http.get('/api/clusters/actions/add/' + cluster)
        .then(
          function () {
            console.log('cluster created..' + orchestrator);
            createClusterSetOrchetrator(cluster, plan, orchestrator);
          },
          function () {
            console.log("Error cluster create.");
          });
    }
  };

  var createClusterSetOrchetrator = function (cluster, plan, orchestrator) {
    $http.get('/api/clusters/' + cluster + '/settings/actions/set/prov-orchestrator/' + orchestrator)
      .then(
        function () {
          console.log('Set orchetrator done..');
          createClusterSetPlan(cluster, plan);
        },
        function () {
          console.log("Error in set orchetrator.");
        });
  };
  var deleteCluster = function (cluster) {
    alert(cluster)
    console.log("cluster " + cluster + " deleted..");
    $http.get('/api/clusters/actions/delete/' + cluster)
  };
  var createClusterSetPlan = function (cluster, plan) {
    console.log('Setting plan..' + plan);
    httpGetWithoutResponse('/api/clusters/' + cluster + '/settings/actions/set/prov-service-plan/' + plan);
  };

  $scope.isEqualLongQueryTime = function (a, b) {
    if (Number(a) == Number(b)) {
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
  $scope.cancelrollingrestart = function (fail) {
    if (confirm("Confirm cancel rolling restart")) httpGetWithoutResponse(getClusterUrl() + '/actions/cancel-rolling-restart');
  };
  $scope.cancelrollingreprov = function (fail) {
    if (confirm("Confirm cancel rolling reprovision")) httpGetWithoutResponse(getClusterUrl() + '/actions/cancel-rolling-reprov');
  };
  $scope.certificatesrotate = function () {
    if (confirm("Confirm rotation certificates")) httpGetWithoutResponse(getClusterUrl() + '/actions/certificates-rotate');
  };
  $scope.certificatesreload = function () {
    if (confirm("Confirm reload certificates")) httpGetWithoutResponse(getClusterUrl() + '/actions/certificates-reload');
  };
  $scope.clbootstrap = function (topo) {
    if (confirm("Bootstrap operation will destroy your existing replication setup. \n Are you really sure?")) httpGetWithoutResponse(getClusterUrl() + '/actions/replication/bootstrap/' + topo);
  };

  $scope.dbmaintenance = function (server, host, port) {
    if (confirm("Confirm maintenance for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/maintenance');
  };
  $scope.dbjobs = function (server, host, port) {
    if (confirm("Confirm running remote jobs for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/run-jobs');
  };
  $scope.dbpromote = function (server, host, port) {
    if (confirm("Confirm promotion for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/switchover');
  };
  $scope.dbsetprefered = function (server, host, port) {
    if (confirm("Confirm set as prefered for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/set-prefered');
  };
  $scope.dbsetunrated = function (server, host, port) {
    if (confirm("Confirm set as unrated for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/set-unrated');
  };
  $scope.dbsetignored = function (server, host, port) {
    if (confirm("Confirm set as ignored for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/set-ignored');
  };
  $scope.dbstart = function (server, host, port) {
    if (confirm("Confirm start for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/start');
  };
  $scope.dbstop = function (server, host, port) {
    if (confirm("Confirm stop for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/stop');
  };
  $scope.dbprovision = function (server, host, port) {
    if (confirm("Confirm provision server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/provision');
  };
  $scope.dbunprovision = function (server, host, port) {
    if (confirm("Confirm unprovision for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/unprovision');
  };
  $scope.prxprovision = function (id, host, port) {
    if (confirm("Confirm provision proxy: " + host + ":" + port + " (" + id + ")")) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/provision');
  };
  $scope.prxunprovision = function (id, host, port) {
    if (confirm("Confirm unprovision proxy: " + host + ":" + port + " (" + id + ")")) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/unprovision');
  };
  $scope.prxstop = function (id, host, port) {
    if (confirm("Confirm stop proxy: " + host + ":" + port + " (" + id + ")")) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/stop');
  };
  $scope.prxstart = function (id, host, port) {
    if (confirm("Confirm start proxy: " + host + ":" + port + " (" + id + ")")) httpGetWithoutResponse(getClusterUrl() + '/proxies/' + id + '/actions/start');
  };
  // Will immediately show cancel button
  $scope.dbreseedphysicalbackup = function (server, host, port) {
    if (confirm("Confirm reseed with physical backup (" + $scope.selectedCluster.config.backupPhysicalType + " " + ($scope.selectedCluster.config.compressBackups ? 'compressed' : '') + ") for server: " + host + ":" + port + " (" + server + ")")) { $scope.isReseeding[server] = true; httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/physicalbackup') };
  };
  $scope.dbreseedphysicalmaster = function (server, host, port) {
    if (confirm("Confirm reseed from master (" + $scope.selectedCluster.config.backupPhysicalType + " " + ($scope.selectedCluster.config.compressBackups ? 'compressed' : '') + ") for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/physicalmaster');
  };
  // scope reseed is only for fast show button toggle
  $scope.dbreseedcancel = function (server, host, port) {
    if (confirm("Confirm cancel all reseed for server: " + host + ":" + port + " (" + server + ")")) { $scope.isReseeding[server] = false; httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed-cancel') };
  };
  $scope.flushlogs = function (server, host, port) {
    if (confirm("Confirm flush logs for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/flush-logs');
  };
  $scope.dbreseedlogical = function (server, host, port) {
    if (confirm("Confirm reseed with logical backup (" + $scope.selectedCluster.config.backupLogicalType + ") for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/logicalbackup');
  };
  $scope.dbreseedmysqldumpmaster = function (server, host, port) {
    if (confirm("Confirm reseed with mysqldump for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reseed/logicalmaster');
  };
  $scope.dbphysicalbackup = function (server, host, port) {
    if (confirm("Confirm sending physical backup (" + $scope.selectedCluster.config.backupPhysicalType + " " + ($scope.selectedCluster.config.compressBackups ? 'compressed' : '') + ") for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/backup-physical');
  };
  $scope.dbdump = function (server, host, port) {
    if (confirm("Confirm sending logical backup (" + $scope.selectedCluster.config.backupLogicalType + ") for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/backup-logical');
  };
  $scope.dbskipreplicationevent = function (server, host, port) {
    if (confirm("Confirm skip replication event for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/skip-replication-event');
  };
  $scope.dbtoogleinnodbmonitor = function (server, host, port) {
    if (confirm("Confirm toogle innodb monitor server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-innodb-monitor');
  };
  $scope.dbtooglemetadalocks = function (server, host, port) {
    if (confirm("Confirm toogle metadata lock plugin server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-meta-data-locks');
  };
  $scope.dbtooglequeryresponsetime = function (server, host, port) {
    if (confirm("Confirm toogle query response time plugin server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-query-response-time');
  };
  $scope.dbtoogleslowquerycapture = function (server, host, port) {
    if (confirm("Confirm toogle slow query capture server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query-capture');
  };


  $scope.dbtoogleslowquery = function (server, host, port) {
    if (confirm("Confirm toogle slow query log capture server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query');
  };
  $scope.dbtooglepfsslowquery = function (server, host, port) {
    if (confirm("Confirm toogle slow query PFS capture server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-pfs-slow-query');
  };
  $scope.dbresetpfsslow = function (server, host, port) {
    if (confirm("Confirm toogle slow query PFS capture server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reset-pfs-queries');
  };
  $scope.dbtoogleslowquerytable = function (server, host, port) {
    if (confirm("Confirm toogle slow query mode between TABLE and FILE server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-slow-query-table');
  };


  $scope.dbtooglepfsslow = function (server, host, port) {
    confirm("Confirm toogle digest mode between PFS and SLOW server: " + host + ":" + port + " (" + server + ")");
    if ($scope.digestmode == "slow") {
      $scope.digestmode = "pfs";
    } else {
      $scope.digestmode = "slow";
    }
  };

  $scope.dbtooglereadonly = function (server, host, port) {
    if (confirm("Confirm toogle read only on server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/toogle-read-only');
  };
  $scope.dbstartslave = function (server, host, port) {
    if (confirm("Confirm start slave on server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/start-slave');
  };

  $scope.dbstopslave = function (server, host, port) {
    if (confirm("Confirm stop slave on server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/stop-slave');
  };

  $scope.dbresetmaster = function (server, host, port) {
    if (confirm("Confirm reset master this may break replication when done on master, server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reset-master');
  };

  $scope.dbresetslaveall = function (server, host, port) {
    if (confirm("Confirm reset slave this will break replication on server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/reset-slave-all');
  };

  $scope.dboptimize = function (server, host, port) {
    if (confirm("Confirm optimize for server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/optimize');
  };

  $scope.toggletraffic = function () {
    if (confirm("Confirm toggle traffic")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/switch/database-heartbeat');
  };
  $scope.configDiscover = function () {
    if (confirm("Confirm database discover config")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/discover');
  };
  $scope.configApplyDynamic = function () {
    if (confirm("Confirm database apply config")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/apply-dynamic-config');
  };
  $scope.resetfail = function () {
    if (confirm("Reset Failover counter?")) httpGetWithoutResponse(getClusterUrl() + '/actions/reset-failover-control');
  };
  $scope.resetsla = function () {
    if (confirm("Reset SLA counters?")) httpGetWithoutResponse(getClusterUrl() + '/actions/reset-sla');
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

  $scope.dropmonitor = function (host, port, type = "") {
    let url = getClusterUrl() + '/actions/dropserver/' + host + '/' + port
    if (type != "") {
      url = url + "/" + type
    }
    if (confirm("Confirm remove " + type + "monitor on server " + host + ":" + port)) httpGetWithoutResponse(url);
  }

  $scope.clusterRotateCredentials = function () {
    if (confirm("Rotate database and replication monitoring user credentials. \n Are you really sure?")) httpGetWithoutResponse(getClusterUrl() + '/actions/rotate-passwords');
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


  $scope.sysbench = function (threads) {
    if (confirm("Confirm sysbench run !" + threads)) httpGetWithoutResponse(getClusterUrl() + '/actions/sysbench?threads=' + threads);
  };

  $scope.runonetest = function (test) {
    if (confirm("Confirm run one test !" + test)) {
      httpGetWithoutResponse(getClusterUrl() + '/tests/actions/run/' + test);
      $scope.tests = "";
    }
  };

  $scope.optimizeAll = function () {
    httpGetWithoutResponse(getClusterUrl() + '/actions/optimize');
  };

  $scope.backupphysical = function () {
    if (confirm("Confirm master physical (" + $scope.selectedCluster.config.backupPhysicalType + " " + ($scope.selectedCluster.config.compressBackups ? 'compressed' : '') + ") backup")) httpGetWithoutResponse(getClusterUrl() + '/actions/master-physical-backup');
  };

  $scope.cladddbtag = function (tag) {
    if (confirm("Confirm add tag " + tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/add-db-tag/' + tag);
  };
  $scope.cldropdbtag = function (tag) {
    if (confirm("Confirm drop tag " + tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/drop-db-tag/' + tag);
  };

  $scope.claddproxytag = function (tag) {
    if (confirm("Confirm add tag " + tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/add-proxy-tag/' + tag);
  };
  $scope.cldropproxytag = function (tag) {
    if (confirm("Confirm drop tag " + tag)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/drop-proxy-tag/' + tag);
  };
  $scope.configreload = function () {
    if (confirm("Confirm reload config")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/reload');
  };

  $scope.cancelJob = function (server, host, port, task) {
    if (confirm("Warning! This action will forcefully cancel the job. Ensure the job is not currently running. Confirm to proceed with the cancellation of '" + task + "' on server " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/job-cancel/' + task);
  }

  $scope.clsetdbcore = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-cpu-cores/' + value.toString());
  };

  $scope.saveLogicalBackupCron = function (selectedDbServersLogicalBackupHour, selectedDbServersLogicalBackupMin, selectedDbServersLogicalBackupSec, selectedDbServersLogicalBackupDay, selectedDbServersLogicalBackupMonth, selectedDbServersLogicalBackupWeek, selectedDbServersLogicalBackupHourTo, selectedDbServersLogicalBackupMinTo, selectedDbServersLogicalBackupSecTo, selectedDbServersLogicalBackupDayTo, selectedDbServersLogicalBackupMonthTo, selectedDbServersLogicalBackupWeekTo, selectedDbServersLogicalBackupHourPer, selectedDbServersLogicalBackupMinPer, selectedDbServersLogicalBackupSecPer) {
    value = selectedDbServersLogicalBackupSec;
    if (selectedDbServersLogicalBackupSecTo) value += '-' + selectedDbServersLogicalBackupSecTo;
    if (selectedDbServersLogicalBackupSecPer) value += '/' + selectedDbServersLogicalBackupSecPer;

    value += ' ' + selectedDbServersLogicalBackupMin;
    if (selectedDbServersLogicalBackupMinTo) value += '-' + selectedDbServersLogicalBackupMinTo;
    if (selectedDbServersLogicalBackupMinPer) value += '/' + selectedDbServersLogicalBackupMinPer;

    value += ' ' + selectedDbServersLogicalBackupHour;
    if (selectedDbServersLogicalBackupHourTo) value += '-' + selectedDbServersLogicalBackupHourTo;
    if (selectedDbServersLogicalBackupHourPer) value += '/' + selectedDbServersLogicalBackupHourPer;

    value += ' ' + selectedDbServersLogicalBackupDay;
    if (selectedDbServersLogicalBackupDayTo) value += '-' + selectedDbServersLogicalBackupDayTo;
    value += ' ' + selectedDbServersLogicalBackupMonth;
    if (selectedDbServersLogicalBackupMonthTo) value += '-' + selectedDbServersLogicalBackupMonthTo;

    value += ' ' + selectedDbServersLogicalBackupWeek;
    if (selectedDbServersLogicalBackupWeekTo) value += '-' + selectedDbServersLogicalBackupWeekTo;

    if (confirm("Confirm save logical backup scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-logical-backup-cron/' + encodeURIComponent(value));
  };

  $scope.savePhysicalBackupCron = function (selectedDbServersPhysicalBackupHour, selectedDbServersPhysicalBackupMin, selectedDbServersPhysicalBackupSec, selectedDbServersPhysicalBackupDay, selectedDbServersPhysicalBackupMonth, selectedDbServersPhysicalBackupWeek, selectedDbServersPhysicalBackupHourTo, selectedDbServersPhysicalBackupMinTo, selectedDbServersPhysicalBackupSecTo, selectedDbServersPhysicalBackupDayTo, selectedDbServersPhysicalBackupMonthTo, selectedDbServersPhysicalBackupWeekTo, selectedDbServersPhysicalBackupHourPer, selectedDbServersPhysicalBackupMinPer, selectedDbServersPhysicalBackupSecPer) {
    value = selectedDbServersPhysicalBackupSec;
    if (selectedDbServersPhysicalBackupSecTo) value += '-' + selectedDbServersPhysicalBackupSecTo;
    if (selectedDbServersPhysicalBackupSecPer) value += '/' + selectedDbServersPhysicalBackupSecPer;

    value += ' ' + selectedDbServersPhysicalBackupMin;
    if (selectedDbServersPhysicalBackupMinTo) value += '-' + selectedDbServersPhysicalBackupMinTo;
    if (selectedDbServersPhysicalBackupMinPer) value += '/' + selectedDbServersPhysicalBackupMinPer;

    value += ' ' + selectedDbServersPhysicalBackupHour;
    if (selectedDbServersPhysicalBackupHourTo) value += '-' + selectedDbServersPhysicalBackupHourTo;
    if (selectedDbServersPhysicalBackupHourPer) value += '/' + selectedDbServersPhysicalBackupHourPer;

    value += ' ' + selectedDbServersPhysicalBackupDay;
    if (selectedDbServersPhysicalBackupDayTo) value += '-' + selectedDbServersPhysicalBackupDayTo;
    value += ' ' + selectedDbServersPhysicalBackupMonth;
    if (selectedDbServersPhysicalBackupMonthTo) value += '-' + selectedDbServersPhysicalBackupMonthTo;

    value += ' ' + selectedDbServersPhysicalBackupWeek;
    if (selectedDbServersPhysicalBackupWeekTo) value += '-' + selectedDbServersPhysicalBackupWeekTo;

    if (confirm("Confirm save Physical backup scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-physical-backup-cron/' + encodeURIComponent(value));
  };

  $scope.saveAlertDisableCron = function (selectedDbServersAlertDisableHour, selectedDbServersAlertDisableMin, selectedDbServersAlertDisableSec, selectedDbServersAlertDisableDay, selectedDbServersAlertDisableMonth, selectedDbServersAlertDisableWeek, selectedDbServersAlertDisableHourTo, selectedDbServersAlertDisableMinTo, selectedDbServersAlertDisableSecTo, selectedDbServersAlertDisableDayTo, selectedDbServersAlertDisableMonthTo, selectedDbServersAlertDisableWeekTo, selectedDbServersAlertDisableHourPer, selectedDbServersAlertDisableMinPer, selectedDbServersAlertDisableSecPer) {
    value = selectedDbServersAlertDisableSec;
    if (selectedDbServersAlertDisableSecTo) value += '-' + selectedDbServersAlertDisableSecTo;
    if (selectedDbServersAlertDisableSecPer) value += '/' + selectedDbServersAlertDisableSecPer;

    value += ' ' + selectedDbServersAlertDisableMin;
    if (selectedDbServersAlertDisableMinTo) value += '-' + selectedDbServersAlertDisableMinTo;
    if (selectedDbServersAlertDisableMinPer) value += '/' + selectedDbServersAlertDisableMinPer;

    value += ' ' + selectedDbServersAlertDisableHour;
    if (selectedDbServersAlertDisableHourTo) value += '-' + selectedDbServersAlertDisableHourTo;
    if (selectedDbServersAlertDisableHourPer) value += '/' + selectedDbServersAlertDisableHourPer;

    value += ' ' + selectedDbServersAlertDisableDay;
    if (selectedDbServersAlertDisableDayTo) value += '-' + selectedDbServersAlertDisableDayTo;
    value += ' ' + selectedDbServersAlertDisableMonth;
    if (selectedDbServersAlertDisableMonthTo) value += '-' + selectedDbServersAlertDisableMonthTo;

    value += ' ' + selectedDbServersAlertDisableWeek;
    if (selectedDbServersAlertDisableWeekTo) value += '-' + selectedDbServersAlertDisableWeekTo;

    if (confirm("Confirm save alert disable scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-alert-disable-cron/' + encodeURIComponent(value));
  };

  $scope.saveAnalyzeCron = function (selectedDbServersAnalyzeHour, selectedDbServersAnalyzeMin, selectedDbServersAnalyzeSec, selectedDbServersAnalyzeDay, selectedDbServersAnalyzeMonth, selectedDbServersAnalyzeWeek, selectedDbServersAnalyzeHourTo, selectedDbServersAnalyzeMinTo, selectedDbServersAnalyzeSecTo, selectedDbServersAnalyzeDayTo, selectedDbServersAnalyzeMonthTo, selectedDbServersAnalyzeWeekTo, selectedDbServersAnalyzeHourPer, selectedDbServersAnalyzeMinPer, selectedDbServersAnalyzeSecPer) {
    value = selectedDbServersAnalyzeSec;
    if (selectedDbServersAnalyzeSecTo) value += '-' + selectedDbServersAnalyzeSecTo;
    if (selectedDbServersAnalyzeSecPer) value += '/' + selectedDbServersAnalyzeSecPer;

    value += ' ' + selectedDbServersAnalyzeMin;
    if (selectedDbServersAnalyzeMinTo) value += '-' + selectedDbServersAnalyzeMinTo;
    if (selectedDbServersAnalyzeMinPer) value += '/' + selectedDbServersAnalyzeMinPer;

    value += ' ' + selectedDbServersAnalyzeHour;
    if (selectedDbServersAnalyzeHourTo) value += '-' + selectedDbServersAnalyzeHourTo;
    if (selectedDbServersAnalyzeHourPer) value += '/' + selectedDbServersAnalyzeHourPer;

    value += ' ' + selectedDbServersAnalyzeDay;
    if (selectedDbServersAnalyzeDayTo) value += '-' + selectedDbServersAnalyzeDayTo;
    value += ' ' + selectedDbServersAnalyzeMonth;
    if (selectedDbServersAnalyzeMonthTo) value += '-' + selectedDbServersAnalyzeMonthTo;

    value += ' ' + selectedDbServersAnalyzeWeek;
    if (selectedDbServersAnalyzeWeekTo) value += '-' + selectedDbServersAnalyzeWeekTo;

    if (confirm("Confirm save Analyze backup scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-analyze-cron/' + encodeURIComponent(value));
  };

  $scope.saveOptimizeCron = function (selectedDbServersOptimizeHour, selectedDbServersOptimizeMin, selectedDbServersOptimizeSec, selectedDbServersOptimizeDay, selectedDbServersOptimizeMonth, selectedDbServersOptimizeWeek, selectedDbServersOptimizeHourTo, selectedDbServersOptimizeMinTo, selectedDbServersOptimizeSecTo, selectedDbServersOptimizeDayTo, selectedDbServersOptimizeMonthTo, selectedDbServersOptimizeWeekTo, selectedDbServersOptimizeHourPer, selectedDbServersOptimizeMinPer, selectedDbServersOptimizeSecPer) {
    value = selectedDbServersOptimizeSec;
    if (selectedDbServersOptimizeSecTo) value += '-' + selectedDbServersOptimizeSecTo;
    if (selectedDbServersOptimizeSecPer) value += '/' + selectedDbServersOptimizeSecPer;

    value += ' ' + selectedDbServersOptimizeMin;
    if (selectedDbServersOptimizeMinTo) value += '-' + selectedDbServersOptimizeMinTo;
    if (selectedDbServersOptimizeMinPer) value += '/' + selectedDbServersOptimizeMinPer;

    value += ' ' + selectedDbServersOptimizeHour;
    if (selectedDbServersOptimizeHourTo) value += '-' + selectedDbServersOptimizeHourTo;
    if (selectedDbServersOptimizeHourPer) value += '/' + selectedDbServersOptimizeHourPer;

    value += ' ' + selectedDbServersOptimizeDay;
    if (selectedDbServersOptimizeDayTo) value += '-' + selectedDbServersOptimizeDayTo;
    value += ' ' + selectedDbServersOptimizeMonth;
    if (selectedDbServersOptimizeMonthTo) value += '-' + selectedDbServersOptimizeMonthTo;

    value += ' ' + selectedDbServersOptimizeWeek;
    if (selectedDbServersOptimizeWeekTo) value += '-' + selectedDbServersOptimizeWeekTo;

    if (confirm("Confirm save Optimize backup scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-optimize-cron/' + encodeURIComponent(value));
  };

  $scope.saveLogsCron = function (selectedDbServersLogsHour, selectedDbServersLogsMin, selectedDbServersLogsSec, selectedDbServersLogsDay, selectedDbServersLogsMonth, selectedDbServersLogsWeek, selectedDbServersLogsHourTo, selectedDbServersLogsMinTo, selectedDbServersLogsSecTo, selectedDbServersLogsDayTo, selectedDbServersLogsMonthTo, selectedDbServersLogsWeekTo, selectedDbServersLogsHourPer, selectedDbServersLogsMinPer, selectedDbServersLogsSecPer) {
    value = selectedDbServersLogsSec;
    if (selectedDbServersLogsSecTo) value += '-' + selectedDbServersLogsSecTo;
    if (selectedDbServersLogsSecPer) value += '/' + selectedDbServersLogsSecPer;

    value += ' ' + selectedDbServersLogsMin;
    if (selectedDbServersLogsMinTo) value += '-' + selectedDbServersLogsMinTo;
    if (selectedDbServersLogsMinPer) value += '/' + selectedDbServersLogsMinPer;

    value += ' ' + selectedDbServersLogsHour;
    if (selectedDbServersLogsHourTo) value += '-' + selectedDbServersLogsHourTo;
    if (selectedDbServersLogsHourPer) value += '/' + selectedDbServersLogsHourPer;

    value += ' ' + selectedDbServersLogsDay;
    if (selectedDbServersLogsDayTo) value += '-' + selectedDbServersLogsDayTo;
    value += ' ' + selectedDbServersLogsMonth;
    if (selectedDbServersLogsMonthTo) value += '-' + selectedDbServersLogsMonthTo;

    value += ' ' + selectedDbServersLogsWeek;
    if (selectedDbServersLogsWeekTo) value += '-' + selectedDbServersLogsWeekTo;

    if (confirm("Confirm save logs scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-logs-cron/' + encodeURIComponent(value));
  };

  $scope.saveLogsTableRotateCron = function (selectedDbServersLogsTableRotateHour, selectedDbServersLogsTableRotateMin, selectedDbServersLogsTableRotateSec, selectedDbServersLogsTableRotateDay, selectedDbServersLogsTableRotateMonth, selectedDbServersLogsTableRotateWeek, selectedDbServersLogsTableRotateHourTo, selectedDbServersLogsTableRotateMinTo, selectedDbServersLogsTableRotateSecTo, selectedDbServersLogsTableRotateDayTo, selectedDbServersLogsTableRotateMonthTo, selectedDbServersLogsTableRotateWeekTo, selectedDbServersLogsTableRotateHourPer, selectedDbServersLogsTableRotateMinPer, selectedDbServersLogsTableRotateSecPer) {
    value = selectedDbServersLogsTableRotateSec;
    if (selectedDbServersLogsTableRotateSecTo) value += '-' + selectedDbServersLogsTableRotateSecTo;
    if (selectedDbServersLogsTableRotateSecPer) value += '/' + selectedDbServersLogsTableRotateSecPer;

    value += ' ' + selectedDbServersLogsTableRotateMin;
    if (selectedDbServersLogsTableRotateMinTo) value += '-' + selectedDbServersLogsTableRotateMinTo;
    if (selectedDbServersLogsTableRotateMinPer) value += '/' + selectedDbServersLogsTableRotateMinPer;

    value += ' ' + selectedDbServersLogsTableRotateHour;
    if (selectedDbServersLogsTableRotateHourTo) value += '-' + selectedDbServersLogsTableRotateHourTo;
    if (selectedDbServersLogsTableRotateHourPer) value += '/' + selectedDbServersLogsTableRotateHourPer;

    value += ' ' + selectedDbServersLogsTableRotateDay;
    if (selectedDbServersLogsTableRotateDayTo) value += '-' + selectedDbServersLogsTableRotateDayTo;
    value += ' ' + selectedDbServersLogsTableRotateMonth;
    if (selectedDbServersLogsTableRotateMonthTo) value += '-' + selectedDbServersLogsTableRotateMonthTo;

    value += ' ' + selectedDbServersLogsTableRotateWeek;
    if (selectedDbServersLogsTableRotateWeekTo) value += '-' + selectedDbServersLogsTableRotateWeekTo;

    if (confirm("Confirm save LogsTableRotate scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-db-servers-logs-table-rotate-cron/' + encodeURIComponent(value));
  };

  $scope.saveRollingRestartCron = function (selectedRollingRestartHour, selectedRollingRestartMin, selectedRollingRestartSec, selectedRollingRestartDay, selectedRollingRestartMonth, selectedRollingRestartWeek, selectedRollingRestartHourTo, selectedRollingRestartMinTo, selectedRollingRestartSecTo, selectedRollingRestartDayTo, selectedRollingRestartMonthTo, selectedRollingRestartWeekTo, selectedRollingRestartHourPer, selectedRollingRestartMinPer, selectedRollingRestartSecPer) {
    value = selectedRollingRestartSec;
    if (selectedRollingRestartSecTo) value += '-' + selectedRollingRestartSecTo;
    if (selectedRollingRestartSecPer) value += '/' + selectedRollingRestartSecPer;

    value += ' ' + selectedRollingRestartMin;
    if (selectedRollingRestartMinTo) value += '-' + selectedRollingRestartMinTo;
    if (selectedRollingRestartMinPer) value += '/' + selectedRollingRestartMinPer;

    value += ' ' + selectedRollingRestartHour;
    if (selectedRollingRestartHourTo) value += '-' + selectedRollingRestartHourTo;
    if (selectedRollingRestartHourPer) value += '/' + selectedRollingRestartHourPer;

    value += ' ' + selectedRollingRestartDay;
    if (selectedRollingRestartDayTo) value += '-' + selectedRollingRestartDayTo;
    value += ' ' + selectedRollingRestartMonth;
    if (selectedRollingRestartMonthTo) value += '-' + selectedRollingRestartMonthTo;

    value += ' ' + selectedRollingRestartWeek;
    if (selectedRollingRestartWeekTo) value += '-' + selectedRollingRestartWeekTo;

    if (confirm("Confirm save RollingRestart scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-rolling-restart-cron/' + encodeURIComponent(value));
  };

  $scope.saveRollingReprovCron = function (selectedRollingReprovHour, selectedRollingReprovMin, selectedRollingReprovSec, selectedRollingReprovDay, selectedRollingReprovMonth, selectedRollingReprovWeek, selectedRollingReprovHourTo, selectedRollingReprovMinTo, selectedRollingReprovSecTo, selectedRollingReprovDayTo, selectedRollingReprovMonthTo, selectedRollingReprovWeekTo, selectedRollingReprovHourPer, selectedRollingReprovMinPer, selectedRollingReprovSecPer) {
    value = selectedRollingReprovSec;
    if (selectedRollingReprovSecTo) value += '-' + selectedRollingReprovSecTo;
    if (selectedRollingReprovSecPer) value += '/' + selectedRollingReprovSecPer;

    value += ' ' + selectedRollingReprovMin;
    if (selectedRollingReprovMinTo) value += '-' + selectedRollingReprovMinTo;
    if (selectedRollingReprovMinPer) value += '/' + selectedRollingReprovMinPer;

    value += ' ' + selectedRollingReprovHour;
    if (selectedRollingReprovHourTo) value += '-' + selectedRollingReprovHourTo;
    if (selectedRollingReprovHourPer) value += '/' + selectedRollingReprovHourPer;

    value += ' ' + selectedRollingReprovDay;
    if (selectedRollingReprovDayTo) value += '-' + selectedRollingReprovDayTo;
    value += ' ' + selectedRollingReprovMonth;
    if (selectedRollingReprovMonthTo) value += '-' + selectedRollingReprovMonthTo;

    value += ' ' + selectedRollingReprovWeek;
    if (selectedRollingReprovWeekTo) value += '-' + selectedRollingReprovWeekTo;

    if (confirm("Confirm save RollingReprov scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-rolling-reprov-cron/' + encodeURIComponent(value));
  };

  $scope.saveJobsSshCron = function (selectedJobsSshHour, selectedJobsSshMin, selectedJobsSshSec, selectedJobsSshDay, selectedJobsSshMonth, selectedJobsSshWeek, selectedJobsSshHourTo, selectedJobsSshMinTo, selectedJobsSshSecTo, selectedJobsSshDayTo, selectedJobsSshMonthTo, selectedJobsSshWeekTo, selectedJobsSshHourPer, selectedJobsSshMinPer, selectedJobsSshSecPer) {
    value = selectedJobsSshSec;
    if (selectedJobsSshSecTo) value += '-' + selectedJobsSshSecTo;
    if (selectedJobsSshSecPer) value += '/' + selectedJobsSshSecPer;

    value += ' ' + selectedJobsSshMin;
    if (selectedJobsSshMinTo) value += '-' + selectedJobsSshMinTo;
    if (selectedJobsSshMinPer) value += '/' + selectedJobsSshMinPer;

    value += ' ' + selectedJobsSshHour;
    if (selectedJobsSshHourTo) value += '-' + selectedJobsSshHourTo;
    if (selectedJobsSshHourPer) value += '/' + selectedJobsSshHourPer;

    value += ' ' + selectedJobsSshDay;
    if (selectedJobsSshDayTo) value += '-' + selectedJobsSshDayTo;
    value += ' ' + selectedJobsSshMonth;
    if (selectedJobsSshMonthTo) value += '-' + selectedJobsSshMonthTo;

    value += ' ' + selectedJobsSshWeek;
    if (selectedJobsSshWeekTo) value += '-' + selectedJobsSshWeekTo;

    if (confirm("Confirm save JobsSsh scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-jobs-ssh-cron/' + encodeURIComponent(value));
  };

  $scope.saveSlaRotateCron = function (selectedSlaRotateHour, selectedSlaRotateMin, selectedSlaRotateSec, selectedSlaRotateDay, selectedSlaRotateMonth, selectedSlaRotateWeek, selectedSlaRotateHourTo, selectedSlaRotateMinTo, selectedSlaRotateSecTo, selectedSlaRotateDayTo, selectedSlaRotateMonthTo, selectedSlaRotateWeekTo, selectedSlaRotateHourPer, selectedSlaRotateMinPer, selectedSlaRotateSecPer) {
    value = selectedSlaRotateSec;
    if (selectedSlaRotateSecTo) value += '-' + selectedSlaRotateSecTo;
    if (selectedSlaRotateSecPer) value += '/' + selectedSlaRotateSecPer;

    value += ' ' + selectedSlaRotateMin;
    if (selectedSlaRotateMinTo) value += '-' + selectedSlaRotateMinTo;
    if (selectedSlaRotateMinPer) value += '/' + selectedSlaRotateMinPer;

    value += ' ' + selectedSlaRotateHour;
    if (selectedSlaRotateHourTo) value += '-' + selectedSlaRotateHourTo;
    if (selectedSlaRotateHourPer) value += '/' + selectedSlaRotateHourPer;

    value += ' ' + selectedSlaRotateDay;
    if (selectedSlaRotateDayTo) value += '-' + selectedSlaRotateDayTo;
    value += ' ' + selectedSlaRotateMonth;
    if (selectedSlaRotateMonthTo) value += '-' + selectedSlaRotateMonthTo;

    value += ' ' + selectedSlaRotateWeek;
    if (selectedSlaRotateWeekTo) value += '-' + selectedSlaRotateWeekTo;

    if (confirm("Confirm save SlaRotate scheduler  " + value)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set-cron/scheduler-sla-rotate-cron/' + encodeURIComponent(value));
  };

  $scope.clsetdbdisk = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-size/' + value.toString());
  };
  $scope.clsetdbio = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString(), add)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-iops/' + value.toString());
  };
  $scope.clsetdbmem = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm memory change " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-memory/' + value.toString());
  };
  $scope.clsetdbconns = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm connections change " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-max-connections/' + value.toString());
  };
  $scope.clsetdbexpirelogdays = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm expire binlog days change " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-expire-log-days/' + value.toString());
  };
  $scope.saveDBImage = function (image) {
    if (confirm("Confirm change database OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-image/' + image);
  };
  $scope.saveProxySQLImage = function (image) {
    if (confirm("Confirm change ProxySQL OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-proxysql-img/' + image);
  };
  $scope.saveProxySQLImage = function (image) {
    if (confirm("Confirm change HaProxy OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-haproxy-img/' + image);
  };
  $scope.saveShardproxyImage = function (image) {
    if (confirm("Confirm change Shard Proxy OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-shardproxy-img/' + image);
  };
  $scope.saveMaxscaleImage = function (image) {
    if (confirm("Confirm change Maxscale Proxy OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-docker-maxscale-img/' + image);
  };
  $scope.saveSphinxImage = function (image) {
    if (confirm("Confirm change Sphinx OCI image: " + image)) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/setprov-sphinx-docker-img/' + image);
  };

  $scope.saveGraphiteWhitelist = function (wl) {
    if (confirm("Confirm update graphite whitelist?"))
      $http.post(getClusterUrl() + '/settings/actions/set-graphite-filterlist/whitelist', { "whitelist": wl })
        .then(function () {
          console.log("Graphite whitelist updated successfully")
        }, function (err) {
          alert("Failed to update graphite whitelist. Err: " + err)
        });
  };

  $scope.saveGraphiteBlacklist = function (bl) {
    if (confirm("Confirm update graphite blacklist?"))
      $http.post(getClusterUrl() + '/settings/actions/set-graphite-filterlist/blacklist', { "blacklist": bl })
        .then(function () {
          console.log("Graphite blacklist updated successfully")
        }, function (err) {
          alert("Failed to update graphite blacklist. Err: " + err)
        });
  };

  $scope.reloadFilterlist = function () {
    if (confirm("This will reload graphite filterlist from file to runtime. Confirm?")) $http.get(getClusterUrl() + '/settings/actions/reload-graphite-filterlist').then(function (res) {
      $scope.gfilterUpdate = true
    },
      function (err) {
        alert("Failed to reload filterlist to runtime. Err:" + err)
      });
  }

  $scope.resetFilterlist = function (type) {
    if (confirm("This will reset graphite filterlist to " + type + " template. Confirm?")) {
      $http.get(getClusterUrl() + '/settings/actions/reset-graphite-filterlist/' + type).then(function (res) {
        $scope.gfilterUpdate = true
      },
        function (err) {
          alert("Failed to reset filterlist using " + type + " template. Err:" + err)
        });
    }
  }

  $scope.saveTopoTarget = function (topo) {
    if (confirm("This will set preferred topology to " + topo + ". Confirm?")) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/topology-target/' + topo);
    }
  }

  $scope.saveDBDisk = function (selectedDBDiskTyoe, selectedDBDiskFS, selectedDBDiskPool, selectedDBDiskDevice) {
    if (confirm("Confirm change DB disk: " + selectedDBDiskTyoe + "/" + selectedDBDiskFS + "/" + selectedDBDiskPool + "/" + selectedDBDiskDevice)) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-type/' + selectedDBDiskTyoe);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-fs/' + selectedDBDiskFS);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-pool/' + selectedDBDiskPool);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-disk-device/' + selectedDBDiskDevice);
    }
  };

  $scope.saveProxyDisk = function (selectedProxyDiskType, selectedProxyDiskFS, selectedProxyDiskPool, selectedProxyDiskDevice) {
    if (confirm("Confirm change Proxy disk: " + selectedProxyDiskType + "/" + selectedProxyDiskFS + "/" + selectedProxyDiskPool + "/" + selectedProxyDiskDevice)) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-type/' + selectedProxyDiskType);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-fs/' + selectedProxyDiskFS);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-pool/' + selectedProxyDiskPool);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-device/' + selectedProxyDiskDevice);
    }
  };

  $scope.saveDBServiceType = function (selectedDBVM, selectedProxyVM) {
    if (confirm("Confirm change VM type: " + selectedDBVM + "/" + selectedProxyVM)) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-db-service-type/' + selectedDBVM);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-service-type/' + selectedProxyVM);
    }
  };

  $scope.saveBackupType = function (selectedLogicalBackup, selectedPhysicalBackup, selectedBinlogBackup, selectedBinlogBackupScript) {
    if (confirm("Confirm backup types: " + selectedLogicalBackup + "/" + selectedPhysicalBackup + "/" + selectedBinlogBackup)) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-logical-type/' + selectedLogicalBackup);
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-physical-type/' + selectedPhysicalBackup);
      if (selectedBinlogBackup == "script") {
        alert("Saved Physical and Logical Backup Type")
        if (!selectedBinlogBackupScript) {
          alert("Backup binlog script not confirmed, cancel setting backup mode to script")
        } else {
          if (confirm("Confirm backup script: " + selectedBinlogBackupScript)) {
            httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-binlog-type/' + selectedBinlogBackup);
            httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-binlog-script/' + selectedBinlogBackupScript);
          } else {
            alert("Backup script not confirmed, cancel setting backup mode to script")
          }
        }
      } else {
        httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-binlog-type/' + selectedBinlogBackup);
      }
    }
  };

  $scope.saveBinlogParseMode = function (selectedBinlogParseMode) {
    if (confirm("Confirm binlog parse mode: " + selectedBinlogParseMode)) {
      httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/binlog-parse-mode/' + selectedBinlogParseMode);
    }
  };

  $scope.clsetproxycore = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-cpu-cores/' + value.toString());
  };
  $scope.clsetproxydisk = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-disk-size/' + value.toString());
  };

  $scope.clsetproxymem = function (base, add) {
    value = Number(base) + add;
    if (confirm("Confirm add tag " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/prov-proxy-memory/' + value.toString());
  };

  $scope.switchsettings = function (setting) {
    if (confirm("Confirm switch settings for " + setting.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/switch/' + setting);
  };

  $scope.reshardtable = function (schema, table) {
    httpGetWithoutResponse(getClusterUrl() + '/schema/' + schema + '/' + table + '/actions/reshard-table');
  };

  $scope.checksumtable = function (schema, table) {
    httpGetWithoutResponse(getClusterUrl() + '/schema/' + schema + '/' + table + '/actions/checksum-table');
  };

  $scope.checksumalltables = function (schema, table) {
    httpGetWithoutResponse(getClusterUrl() + '/actions/checksum-all-tables');
  };
  $scope.changemaxdelay = function (delay) {
    if (confirm("Confirm change delay  " + delay.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/failover-max-slave-delay/' + delay);
  };
  $scope.changefailoverlimit = function (limit) {
    if (confirm("Confirm change failover-limit  " + limit.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/failover-limit/' + limit);
  };
  $scope.changebackupbinlogskeep = function (delay) {
    if (confirm("Confirm change keep binlogs files " + delay.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/backup-binlogs-keep/' + delay);
  };
  $scope.changeproxiesmaxconnections = function (delay) {
    if (confirm("Confirm change backends max connections  " + delay.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/proxy-servers-backend-max-replication-lag/' + delay);
  };
  $scope.changeproxiesmaxdelay = function (delay) {
    if (confirm("Confirm change delay  " + delay.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/proxy-servers-backend-max-replication-lag/' + delay);
  };
  $scope.changeswitchoverwaitroutechange = function (delay) {
    if (confirm("Confirm change wait change route detection " + delay.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/switchover-wait-route-change/' + delay);
  };
  $scope.changedelaystatrotate = function (rotate) {
    if (confirm("Confirm change delay stat rotate value to " + rotate.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/delay-stat-rotate/' + rotate);
  };
  $scope.changeprintdelaystatinterval = function (intv) {
    if (confirm("Confirm change delay stat rotate value to " + intv.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/print-delay-stat-interval/' + intv);
  };

  $scope.setsettings = function (setting, value) {
    if (confirm("Confirm change '" + setting.toString() + "' to: " + value.toString())) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/' + setting + '/' + value);
  };

  $scope.setsettingsnullable = function (setting, value) {
    if (value.length == 0) {
      value = "{undefined}"
    }

    return $scope.setsettings(setting, value)
  };

  $scope.sendRestoreForm = function (form) {
    let unixts = Math.floor(form.restoreTime.getTime() / 1000)

    $http.post(getClusterUrl() + '/servers/' + form.selectedServer + '/actions/pitr', { Backup: form.selectedBackup, UseBinlog: form.pitr, RestoreTime: unixts })
      .then(function () {
        console.log("Restore request sent successfully")
      }, function (err) {
        alert("Failed to send restore request. Err: " + err)
      });
  };


  $scope.saveApiTokenTimeout = function (to) {
    $scope.setsettings("api-token-timeout", to)
    $scope.toggleApiTokenTimeout()
  };

  $scope.openCluster = function (clusterName) {
    $timeout.cancel($scope.promise);
    $scope.selectedTab = 'Dashboard';
    $scope.selectedClusterName = clusterName;
    //  $scope.start();
  };

  $scope.back = function () {
    if (typeof $scope.selectedServer != 'undefined') {
      $scope.selectedServer = undefined;
    } else {
      $scope.selectedClusterName = undefined;
    }
    $scope.menuOpened = false;

    //    $scope.selectedCluster = undefined;
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

    $mdDialog.hide({ contentElement: '#myClusterDialog' });
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
  $scope.closeNewClusterDialog = function (plan, orchestrator) {
    $mdDialog.hide({ contentElement: '#myNewClusterDialog', });
    if (confirm("Confirm Creating Cluster " + $scope.dlgAddClusterName + " " + plan + " for " + orchestrator)) {
      createCluster($scope.dlgAddClusterName, plan, orchestrator, $scope.selectedClusterName);

      $scope.selectedClusterName = $scope.dlgAddClusterName;
      $scope.servers = {};
      $scope.slaves = {};
      $scope.master = {};
      $scope.alerts = {};
      $scope.logs = {};
      $scope.proxies = {};
      //  $scope.callServices();
      //  $scope.setClusterCredentialDialog();
    }
    $mdSidenav('right').close();
    $scope.menuOpened = false;
  };
  $scope.closeDeleteClusterDialog = function (cluster) {
    $mdDialog.hide({ contentElement: '#myDeleteClusterDialog', });
    if (confirm("Confirm Deleting Cluster : " + cluster)) {
      deleteCluster(cluster);

      //$scope.selectedClusterName = $scope.dlgAddClusterName;
      $scope.servers = {};
      $scope.slaves = {};
      $scope.master = {};
      $scope.alerts = {};
      $scope.logs = {};
      $scope.proxies = {};
      //  $scope.callServices();
      //  $scope.setClusterCredentialDialog();
    }

    $mdSidenav('right').close();
    $scope.menuOpened = false;


  };
  $scope.deleteClusterDialog = function () {
    $scope.menuOpened = true;
    $mdDialog.show({
      scope: $scope,
      contentElement: '#myDeleteClusterDialog',
      preserveScope: true,
      parent: angular.element(document.body),
      //      clickOutsideToClose: false,
      //    escapeToClose: false,
    });
  };

  $scope.cancelNewClusterDialog = function () {
    $mdDialog.hide({ contentElement: '#myNewClusterDialog', });
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
    $mdDialog.hide({ contentElement: '#myNewUserDialog', });
    if (confirm("Confirm Creating Cluster " + $scope.dlgAddUserName)) {
      angular.forEach($scope.newUserAcls, function (value, index) {
        //   console.log(value);
        alert(value.grant + ':' + value.enable);
      });
    };

    $mdSidenav('right').close();
    $scope.menuOpened = false;
  };



  $scope.cancelNewUserDialog = function () {
    $mdDialog.hide({ contentElement: '#myNewUserDialog', });
    $mdSidenav('right').close();
    $scope.menuOpened = false;
  };


  $scope.newServerDialog = function () {
    $mdDialog.show({
      contentElement: '#myNewServerDialog',
      parent: angular.element(document.body),
    });
  };
  $scope.closeNewServerDialog = function (dlgServerName, dlgServerPort, selectedMonitor) {
    $mdDialog.hide({ contentElement: '#myNewServerDialog', });
    if (confirm("Confirm adding new server " + dlgServerName + ":" + dlgServerPort + "  " + selectedMonitor)) httpGetWithoutResponse(getClusterUrl() + '/actions/addserver/' + dlgServerName + '/' + dlgServerPort + "/" + selectedMonitor);
  };
  $scope.cancelNewServerDialog = function () {
    $mdDialog.hide({ contentElement: '#myNewServerDialog', });
  };

  $scope.setClusterCredentialDialog = function () {
    $mdDialog.show({
      contentElement: '#myClusterCredentialDialog',
      parent: angular.element(document.body),
      clickOutsideToClose: false,
      preserveScope: true,
      escapeToClose: false,
    });
  };

  $scope.closeClusterCredentialDialog = function (user, pass) {
    $mdDialog.hide({ contentElement: '#myClusterCredentialDialog', });
    if (confirm("Confirm set user/password")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/db-servers-credential/' + user + ':' + pass);
  };
  $scope.cancelClusterCredentialDialog = function () {
    $mdDialog.hide({ contentElement: '#myClusterCredentialDialog', });
  };

  $scope.setRplCredentialDialog = function () {
    $mdDialog.show({
      contentElement: '#myRplCredentialDialog',
      parent: angular.element(document.body),
      clickOutsideToClose: false,
      escapeToClose: false,
    });
  };
  $scope.closeRplCredentialDialog = function (user, pass) {
    $mdDialog.hide({ contentElement: '#myRplCredentialDialog', });
    if (confirm("Confirm set user/password")) httpGetWithoutResponse(getClusterUrl() + '/settings/actions/set/replication-credential/' + user + ':' + pass);
  };
  $scope.cancelRplCredentialDialog = function () {
    $mdDialog.hide({ contentElement: '#myRplCredentialDialog', });
  };

  $scope.openDebugClusterDialog = function () {
    $mdDialog.show({
      contentElement: '#myClusterDebugDialog',
      parent: angular.element(document.body),
    });
    $scope.menuOpened = true;
  };
  $scope.closeDebugClusterDialog = function () {
    $mdDialog.hide({ contentElement: '#myClusterDebugDialog', });
    $scope.menuOpened = false;
  };

  $scope.openDebugServerDialog = function () {
    $mdDialog.show({
      contentElement: '#myServerDebugDialog',
      parent: angular.element(document.body),
    });
  };
  $scope.closeDebugServerDialog = function () {
    $mdDialog.hide({ contentElement: '#myServerDebugDialog', });
  };

  $scope.openDebugProxiesDialog = function () {
    $mdDialog.show({
      contentElement: '#myProxiesDebugDialog',
      parent: angular.element(document.body),
    });
  };
  $scope.closeDebugProxiesDialog = function () {
    $mdDialog.hide({ contentElement: '#myProxiesDebugDialog', });
  };

  $scope.openRestoreDialog = function (server, host, port) {
    $scope.restoreForm.selectedServer = server
    $scope.restoreForm.selectedHost = host
    $scope.restoreForm.selectedPort = port
    $mdDialog.show({
      contentElement: '#myRestoreDialog',
      parent: angular.element(document.body),
    });
  };
  $scope.closeRestoreDialog = function (restoreForm) {
    $mdDialog.hide({ contentElement: '#myRestoreDialog', });
    backup = $scope.selectedCluster.backupList[restoreForm.selectedBackup]
    restoreForm.restoreTime = restoreForm.restoreTime ? $scope.parseUTCDate(restoreForm.restoreTime) : new Date(backup.startTime)

    let msg = "Confirm restore server " + restoreForm.selectedHost + ":" + restoreForm.selectedPort + " with " + backup.backupTool
    if (restoreForm.pitr) {
      msg = msg + " and binary logs"
    }
    msg = msg + " (" + $scope.formatDateToUTC(restoreForm.restoreTime) + ") ?\n"
    if ($scope.master.id != restoreForm.selectedServer) {
      msg = msg + "This will make this slave as standalone."
    }
    if (confirm(msg)) $scope.sendRestoreForm(restoreForm);
    $scope.defaultRestoreForm()
  };
  $scope.cancelRestoreDialog = function () {
    $mdDialog.hide({ contentElement: '#myRestoreDialog', });
    $scope.defaultRestoreForm()
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

  $scope.onTabSelected = function (tab) {
    $scope.selectedTab = tab;
    $scope.gfilterUpdate = true;
    if (tab == "Backups") {
      $scope.selectedSubTab = tab;
    }
  };

  $scope.onSubTabSelected = function (sub) {
    $scope.selectedSubTab = sub;
  };

  $scope.onTabClicked = function (tab) {
    $scope.selectedTab = tab;
    $scope.gfilterUpdate = true;
    if (tab == "Backups") {
      $scope.selectedSubTab = tab;
    }
  };

  $scope.onSubTabClicked = function (sub) {
    $scope.selectedSubTab = sub;
  };

  $scope.openServer = function (id) {
    $scope.selectedServer = id;
    $scope.onTabSelected('Processlist');
  };

  $scope.setSettingsMenu = function (menu) {
    $scope.settingsMenu = {
      general: false,
      monitoring: false,
      replication: false,
      rejoin: false,
      backups: false,
      proxies: false,
      schedulers: false,
      cloud18: false,
      logs: false,
      graphs: false,
    };
    switch (menu) {
      case 'general':
        $scope.settingsMenu.general = true;
        break;
      case 'monitoring':
        $scope.settingsMenu.monitoring = true;
        break;
      case 'replication':
        $scope.settingsMenu.replication = true;
        break;
      case 'rejoin':
        $scope.settingsMenu.rejoin = true;
        break;
      case 'backups':
        $scope.settingsMenu.backups = true;
        break;
      case 'proxies':
        $scope.settingsMenu.proxies = true;
        break;
      case 'schedulers':
        $scope.settingsMenu.schedulers = true;
        break;
      case 'cloud18':
        $scope.settingsMenu.cloud18 = true;
        break;
      case 'logs':
        $scope.settingsMenu.logs = true;
        break;
      case 'graphs':
        $scope.settingsMenu.graphs = true;
        break;
      case 'global':
        $scope.settingsMenu.global = true;
        break;
      default:
        console.log(`Sorry, we are out of ${expr}.`);
    }
    //reset graphite wl and bl once
    $scope.gfilterUpdate = true;
  };

  $scope.toggleGC = function () {
    $scope.showGC = !$scope.showGC
    // console.log("toggled")
  }

  $scope.longQueryTime = "0";


  $scope.updateLongQueryTime = function (time, server, host, port) {
    if (confirm("Confirm change Long Query Time" + time + " on server: " + host + ":" + port + " (" + server + ")")) httpGetWithoutResponse(getClusterUrl() + '/servers/' + server + '/actions/set-long-query-time/' + time);
  };

  $scope.explainPlan = undefined;

  $scope.queryExplainPFS = function (digest) {
    $scope.selectedQuery = digest;
    ExplainPlanPFS.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer, queryDigest: $scope.selectedQuery }, function (data) {
      $scope.explainPlan = data;
      $scope.reserror = false;

    }, function () {
      $scope.reserror = true;
    });


  };

  $scope.queryExplainSlowLog = function (digest) {
    $scope.selectedQuery = digest;
    ExplainPlanSlowLog.query({ clusterName: $scope.selectedClusterName, serverName: $scope.selectedServer, queryDigest: $scope.selectedQuery }, function (data) {
      $scope.explainPlan = data;
      $scope.reserror = false;
    }, function () {
      $scope.reserror = true;
    });
  };

  $scope.closeExplain = function () {
    $scope.selectedQuery = undefined;
  };

  var httpGetExplainPlan = function (url) {
    $http.get(url)
      .subscribe(res => {
        $scope.explainPlan = res._body;
      });

  };
  $scope.toggleLeft = buildToggler('left');
  $scope.toggleRight = buildToggler('right');

  function buildToggler(componentId) {
    return function () {
      $mdSidenav(componentId).toggle();

    };
  }
  $scope.toogleTabular = function () {
    $scope.serverListTabular = !$scope.serverListTabular;
  };
  $scope.toogleTable = function () {
    $scope.table = !$scope.table;
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
  $scope.getTablePct = function (table, index) {
    return ((table + index) / ($scope.selectedCluster.workLoad.dbTableSize + $scope.selectedCluster.workLoad.dbTableSize + 1) * 100).toFixed(2);
  };
  $scope.start();
});
