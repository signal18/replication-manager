module github.com/signal18/replication-manager

go 1.16

replace github.com/kahing/goofys => github.com/georgyo/goofys v0.21.0

replace github.com/siddontang/go-mysql-org/go-mysql => github.com/go-mysql-org/go-mysql v1.7.0

require (
	github.com/Azure/azure-pipeline-go v0.2.2
	github.com/Azure/azure-sdk-for-go v44.0.0+incompatible
	github.com/Azure/azure-storage-blob-go v0.8.0
	github.com/Azure/go-autorest/autorest v0.11.1
	github.com/Azure/go-autorest/autorest/adal v0.9.5
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.0
	github.com/Azure/go-autorest/autorest/azure/cli v0.4.0
	github.com/BurntSushi/toml v0.3.1
	github.com/JaderDias/movingmedian v0.0.0-20170611140316-de8c410559fa
	github.com/NYTimes/gziphandler v1.0.1
	github.com/alyu/configparser v0.0.0-20151125021232-26b2fe18bee1
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/aws/aws-sdk-go v1.29.24
	github.com/bluele/logrus_slack v0.0.0-20170812021752-74aa3c9b7cc3
	github.com/bradfitz/gomemcache v0.0.0-20170208213004-1952afaa557d
	github.com/codegangsta/negroni v0.3.0
	github.com/davecgh/go-spew v1.1.1
	github.com/dgryski/carbonzipper v0.0.0-20170426152955-d1a3cec4169b
	github.com/dgryski/go-expirecache v0.0.0-20170314133854-743ef98b2adb
	github.com/dgryski/go-onlinestats v0.0.0-20170612111826-1c7d19468768
	github.com/dgryski/go-trigram v0.0.0-20160407183937-79ec494e1ad0
	github.com/dgryski/httputil v0.0.0-20160116060654-189c2918cd08
	github.com/dustin/go-humanize v1.0.0
	github.com/evmar/gocairo v0.0.0-20160222165215-ddd30f837497
	github.com/facebookgo/grace v0.0.0-20170218225239-4afe952a37a4
	github.com/facebookgo/pidfile v0.0.0-20150612191647-f242e2999868
	github.com/go-sql-driver/mysql v1.6.0
	github.com/gogo/protobuf v1.3.2
	github.com/gonum/matrix v0.0.0-20180124231301-a41cc49d4c29
	github.com/google/uuid v1.3.0
	github.com/gorilla/handlers v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.5.0
	github.com/gwenn/yacr v0.0.0-20180209192453-77093bdc7e72
	github.com/helloyi/go-sshclient v1.2.0
	github.com/howeyc/fsnotify v0.0.0-20151003194602-f0c08ee9c607
	github.com/hpcloud/tail v1.0.0
	github.com/hydrogen18/stalecucumber v0.0.0-20161215203336-0a94983f3e27
	github.com/improbable-eng/grpc-web v0.14.0
	github.com/iu0v1/gelada v1.2.2
	github.com/jacobsa/fuse v0.0.0-20211125163655-ffd6c474e806
	github.com/jmoiron/sqlx v1.3.3
	github.com/jordan-wright/email v0.0.0-20160301001728-a62870b0c368
	github.com/juju/errors v0.0.0-20220203013757-bd733f3c86b9
	github.com/kisielk/og-rek v0.0.0-20170425174049-dd41cde712de
	github.com/lestrrat/go-file-rotatelogs v0.0.0-20171229092148-f984502973a0
	github.com/lestrrat/go-strftime v0.0.0-20170113112000-04ef93e28531
	github.com/lib/pq v1.3.0
	github.com/magneticio/vamp-router v0.0.0-20151116102511-29379b621548
	github.com/micro/go-micro v0.27.1
	github.com/mitchellh/go-homedir v1.1.0
	github.com/mjibson/go-dsp v0.0.0-20170104183934-49dba8372707
	github.com/nsf/termbox-go v1.1.1
	github.com/percona/go-mysql v0.0.0-20190307200310-f5cfaf6a5e55
	github.com/peterbourgon/g2g v0.0.0-20161124161852-0c2bab2b173d
	github.com/pingcap/dumpling v0.0.0-20200319081211-255ce0d25719
	github.com/satori/go.uuid v1.2.0
	github.com/shirou/gopsutil v2.20.2+incompatible
	github.com/siddontang/go v0.0.0-20180604090527-bdc77568d726
	github.com/siddontang/go-mysql v0.0.0-20190311123328-7fc3b28d6104
	github.com/siddontang/go-mysql-elasticsearch v0.0.0-20180201161913-f34f371d4391
	github.com/sirupsen/logrus v1.7.0
	github.com/spf13/cobra v0.0.6
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.4.0
	github.com/stretchr/testify v1.8.4
	github.com/tebeka/strftime v0.1.5
	github.com/urfave/cli v1.22.3
	github.com/wangjohn/quickselect v0.0.0-20161129230411-ed8402a42d5f
	github.com/xwb1989/sqlparser v0.0.0-20171128062118-da747e0c62c4
	github.com/yoheimuta/protolint v0.32.0
	golang.org/x/crypto v0.17.0
	golang.org/x/net v0.10.0
	google.golang.org/genproto v0.0.0-20210617175327-b9e0b3197ced
	google.golang.org/grpc v1.38.0
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.1.0
	google.golang.org/protobuf v1.30.0
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c
	gopkg.in/ini.v1 v1.55.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
)

require (
	github.com/Azure/go-autorest/autorest/to v0.4.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.3.0 // indirect
	github.com/StackExchange/wmi v0.0.0-20210224194228-fe8f1750fd46 // indirect
	github.com/aclements/go-moremath v0.0.0-20170210193428-033754ab1fee // indirect
	github.com/atc0005/go-teams-notify/v2 v2.8.0
	github.com/bluele/slack v0.0.0-20180528010058-b4b4d354a079 // indirect
	github.com/buger/jsonparser v1.1.1
	github.com/coreos/go-oidc/v3 v3.6.0
	github.com/desertbit/timer v0.0.0-20180107155436-c41aec40b27f // indirect
	github.com/facebookgo/atomicfile v0.0.0-20151019160806-2de1f203e7d5 // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/facebookgo/ensure v0.0.0-20200202191622-63f1cf65ac4c // indirect
	github.com/facebookgo/freeport v0.0.0-20150612182905-d4adf43b75b9 // indirect
	github.com/facebookgo/httpdown v0.0.0-20160323221027-a3b1354551a2 // indirect
	github.com/facebookgo/stack v0.0.0-20160209184415-751773369052 // indirect
	github.com/facebookgo/stats v0.0.0-20151006221625-1b76add642e4 // indirect
	github.com/facebookgo/subset v0.0.0-20200203212716-c811ad88dec4 // indirect
	github.com/fastly/go-utils v0.0.0-20180712184237-d95a45783239 // indirect
	github.com/ggwhite/go-masker v1.0.9
	github.com/gin-gonic/gin v1.9.1 // indirect
	github.com/go-git/go-git/v5 v5.6.1
	github.com/go-mysql-org/go-mysql v1.7.0
	github.com/go-ole/go-ole v1.2.5 // indirect
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/gonum/blas v0.0.0-20180125090452-e7c5890b24cf // indirect
	github.com/gonum/floats v0.0.0-20180125090339-7de1f4ea7ab5 // indirect
	github.com/gonum/internal v0.0.0-20180125090855-fda53f8d2571 // indirect
	github.com/gonum/lapack v0.0.0-20180125091020-f0b8b25edece // indirect
	github.com/gorilla/context v1.1.1 // indirect
	github.com/gorilla/securecookie v1.1.1 // indirect
	github.com/gorilla/sessions v0.0.0-20180209192218-6ba88b7f1c1e // indirect
	github.com/gregdel/pushover v1.1.0
	github.com/hashicorp/go-retryablehttp v0.7.2 // indirect
	github.com/hashicorp/vault/api v1.9.0
	github.com/hashicorp/vault/api/auth/approle v0.4.0
	github.com/iancoleman/strcase v0.3.0
	github.com/jehiah/go-strftime v0.0.0-20171201141054-1d33003b3869 // indirect
	github.com/juju/testing v0.0.0-20220203020004-a0ff61f03494 // indirect
	github.com/klauspost/pgzip v1.2.6
	github.com/lestrrat/go-envload v0.0.0-20180220120943-6ed08b54a570 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/miekg/dns v1.1.43 // indirect
	github.com/pelletier/go-toml v1.9.5
	github.com/pkg/xattr v0.4.6
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rs/cors v1.7.0 // indirect
	github.com/siddontang/go-log v0.0.0-20190221022429-1e957dd83bed // indirect
	github.com/smartystreets/goconvey v1.7.2 // indirect
	golang.org/x/oauth2 v0.6.0
	golang.org/x/time v0.3.0 // indirect
	google.golang.org/grpc/examples v0.0.0-20220316190256-c4cabf78f4a2 // indirect
	nhooyr.io/websocket v1.8.7 // indirect
	software.sslmate.com/src/go-pkcs12 v0.4.0
)
