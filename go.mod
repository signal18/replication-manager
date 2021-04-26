module github.com/signal18/replication-manager

go 1.12

replace github.com/jacobsa/fuse => github.com/kahing/fusego v0.0.0-20190829004836-cb66eea2377f

replace github.com/kahing/goofys => github.com/georgyo/goofys v0.21.0

require (
	github.com/Azure/azure-pipeline-go v0.2.2
	github.com/Azure/azure-sdk-for-go v33.2.0+incompatible
	github.com/Azure/azure-storage-blob-go v0.8.0
	github.com/Azure/go-autorest v12.2.0+incompatible
	github.com/Azure/go-autorest/autorest v0.9.0
	github.com/Azure/go-autorest/autorest/adal v0.6.0
	github.com/Azure/go-autorest/autorest/azure/auth v0.3.0
	github.com/Azure/go-autorest/autorest/azure/cli v0.3.0
	github.com/Azure/go-autorest/autorest/to v0.3.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.2.0 // indirect
	github.com/BurntSushi/toml v0.3.1
	github.com/JaderDias/movingmedian v0.0.0-20170611140316-de8c410559fa
	github.com/NYTimes/gziphandler v0.0.0-20180125165240-289a3b81f5ae
	github.com/aclements/go-moremath v0.0.0-20170210193428-033754ab1fee
	github.com/alyu/configparser v0.0.0-20151125021232-26b2fe18bee1
	github.com/armon/go-metrics v0.0.0-20171117184120-7aa49fde8082
	github.com/asaskevich/govalidator v0.0.0-20180115102450-4b3d68f87f17
	github.com/aws/aws-sdk-go v1.29.24
	github.com/bluele/logrus_slack v0.0.0-20170812021752-74aa3c9b7cc3
	github.com/bluele/slack v0.0.0-20180528010058-b4b4d354a079
	github.com/bradfitz/gomemcache v0.0.0-20170208213004-1952afaa557d
	github.com/codegangsta/negroni v0.3.0
	github.com/davecgh/go-spew v1.1.1
	github.com/dgrijalva/jwt-go v3.2.0+incompatible
	github.com/dgryski/carbonzipper v0.0.0-20170426152955-d1a3cec4169b
	github.com/dgryski/go-expirecache v0.0.0-20170314133854-743ef98b2adb
	github.com/dgryski/go-onlinestats v0.0.0-20170612111826-1c7d19468768
	github.com/dgryski/go-trigram v0.0.0-20160407183937-79ec494e1ad0
	github.com/dgryski/httputil v0.0.0-20160116060654-189c2918cd08
	github.com/dustin/go-humanize v0.0.0-20171111073723-bb3d318650d4
	github.com/evmar/gocairo v0.0.0-20160222165215-ddd30f837497
	github.com/facebookgo/atomicfile v0.0.0-20151019160806-2de1f203e7d5
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/facebookgo/grace v0.0.0-20170218225239-4afe952a37a4
	github.com/facebookgo/httpdown v0.0.0-20160323221027-a3b1354551a2
	github.com/facebookgo/pidfile v0.0.0-20150612191647-f242e2999868
	github.com/facebookgo/stats v0.0.0-20151006221625-1b76add642e4
	github.com/fsnotify/fsnotify v1.4.7
	github.com/go-sql-driver/mysql v1.5.0
	github.com/gogo/protobuf v1.2.2-0.20190723190241-65acae22fc9d
	github.com/golang/protobuf v1.5.0
	github.com/gonum/blas v0.0.0-20180125090452-e7c5890b24cf
	github.com/gonum/floats v0.0.0-20180125090339-7de1f4ea7ab5
	github.com/gonum/internal v0.0.0-20180125090855-fda53f8d2571
	github.com/gonum/lapack v0.0.0-20180125091020-f0b8b25edece
	github.com/gonum/matrix v0.0.0-20180124231301-a41cc49d4c29
	github.com/google/uuid v1.1.2
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/gorilla/context v1.1.1
	github.com/gorilla/handlers v1.3.0
	github.com/gorilla/mux v0.0.0-20180120075819-c0091a029979
	github.com/gorilla/securecookie v1.1.1
	github.com/gorilla/sessions v0.0.0-20180209192218-6ba88b7f1c1e
	github.com/gwenn/yacr v0.0.0-20180209192453-77093bdc7e72
	github.com/hashicorp/consul v0.0.0-20180215214858-1ce90e2a19ea
	github.com/hashicorp/go-cleanhttp v0.0.0-20171218145408-d5fe4b57a186
	github.com/hashicorp/go-immutable-radix v0.0.0-20180129170900-7f3cd4390caa
	github.com/hashicorp/go-rootcerts v0.0.0-20160503143440-6bb64b370b90
	github.com/hashicorp/golang-lru v0.5.1
	github.com/hashicorp/hcl v1.0.0
	github.com/hashicorp/serf v0.0.0-20180213013805-d4f33d5b6a0b
	github.com/helloyi/go-sshclient v0.0.0-20191203124208-f1e205501005
	github.com/howeyc/fsnotify v0.0.0-20151003194602-f0c08ee9c607
	github.com/hpcloud/tail v1.0.0
	github.com/hydrogen18/stalecucumber v0.0.0-20161215203336-0a94983f3e27
	github.com/inconshreveable/mousetrap v1.0.0
	github.com/iu0v1/gelada v1.2.2
	github.com/jacobsa/fuse v0.0.0-20180417054321-cd3959611bcb
	github.com/jmoiron/sqlx v1.2.0
	github.com/jordan-wright/email v0.0.0-20160301001728-a62870b0c368
	github.com/juju/errors v0.0.0-20170703010042-c7d06af17c68
	github.com/kahing/goofys v0.23.1
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/kisielk/og-rek v0.0.0-20170425174049-dd41cde712de
	github.com/lestrrat/go-file-rotatelogs v0.0.0-20171229092148-f984502973a0
	github.com/lestrrat/go-strftime v0.0.0-20170113112000-04ef93e28531
	github.com/lib/pq v1.2.0
	github.com/magiconair/properties v1.8.0
	github.com/magneticio/vamp-router v0.0.0-20151116102511-29379b621548
	github.com/mattn/go-runewidth v0.0.0-20170510074858-97311d9f7767
	github.com/mattn/go-sqlite3 v1.14.6
	github.com/micro/go-log v0.1.0 // indirect
	github.com/micro/go-micro v0.1.4
	github.com/micro/misc v0.1.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/mitchellh/hashstructure v0.0.0-20170609045927-2bca23e0e452
	github.com/mitchellh/mapstructure v1.1.2
	github.com/mjibson/go-dsp v0.0.0-20170104183934-49dba8372707
	github.com/nsf/termbox-go v0.0.0-20180129072728-88b7b944be8b
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/pelletier/go-toml v1.2.0
	github.com/percona/go-mysql v0.0.0-20190307200310-f5cfaf6a5e55
	github.com/peterbourgon/g2g v0.0.0-20161124161852-0c2bab2b173d
	github.com/pingcap/dumpling v0.0.0-20200319081211-255ce0d25719
	github.com/pingcap/errors v0.11.4
	github.com/pires/go-proxyproto v0.0.0-20190615163442-2c19fd512994
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/satori/go.uuid v1.2.0
	github.com/shirou/gopsutil v2.20.2+incompatible
	github.com/shopspring/decimal v0.0.0-20180709203117-cd690d0c9e24
	github.com/siddontang/go v0.0.0-20180604090527-bdc77568d726
	github.com/siddontang/go-log v0.0.0-20190221022429-1e957dd83bed
	github.com/siddontang/go-mysql v0.0.0-20190311123328-7fc3b28d6104
	github.com/siddontang/go-mysql-elasticsearch v0.0.0-20180201161913-f34f371d4391
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/afero v1.1.2
	github.com/spf13/cast v1.3.0
	github.com/spf13/cobra v0.0.6
	github.com/spf13/jwalterweatherman v1.0.0
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.4.0
	github.com/stretchr/testify v1.5.1
	github.com/urfave/cli v1.22.3
	github.com/walle/lll v1.0.1 // indirect
	github.com/wangjohn/quickselect v0.0.0-20161129230411-ed8402a42d5f
	github.com/xwb1989/sqlparser v0.0.0-20171128062118-da747e0c62c4
	github.com/zeebo/blake3 v0.1.1
	golang.org/x/crypto v0.0.0-20190510104115-cbcb75029529
	golang.org/x/net v0.0.0-20200202094626-16171245cfb2
	golang.org/x/sys v0.0.0-20201014080544-cc95f250f6bc
	golang.org/x/text v0.3.2
	google.golang.org/appengine v1.5.0
	google.golang.org/grpc v1.36.0
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.1.0 // indirect
	google.golang.org/protobuf v1.26.0
	gopkg.in/airbrake/gobrake.v2 v2.0.9 // indirect
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	gopkg.in/fsnotify/fsnotify.v1 v1.4.7
	gopkg.in/gemnasium/logrus-airbrake-hook.v2 v2.1.2 // indirect
	gopkg.in/ini.v1 v1.55.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7
	gopkg.in/yaml.v2 v2.2.4
	k8s.io/api v0.0.0-20191016225839-816a9b7df678
	k8s.io/apimachinery v0.0.0-20191017185446-6e68a40eebf9
	k8s.io/client-go v0.0.0-20190620085101-78d2af792bab
	mvdan.cc/interfacer v0.0.0-20180901003855-c20040233aed // indirect
	mvdan.cc/lint v0.0.0-20170908181259-adc824a0674b // indirect
)
