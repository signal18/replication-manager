module github.com/signal18/replication-manager

go 1.24.6

replace github.com/kahing/goofys => github.com/georgyo/goofys v0.21.0

replace github.com/siddontang/go-mysql-org/go-mysql => github.com/go-mysql-org/go-mysql v1.7.0

require (
	github.com/Azure/azure-pipeline-go v0.2.3
	github.com/Azure/azure-sdk-for-go v44.0.0+incompatible
	github.com/Azure/azure-storage-blob-go v0.14.0
	github.com/Azure/go-autorest/autorest v0.11.1
	github.com/Azure/go-autorest/autorest/adal v0.9.14
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.0
	github.com/Azure/go-autorest/autorest/azure/cli v0.4.0
	github.com/BurntSushi/toml v1.3.2
	github.com/JaderDias/movingmedian v0.0.0-20170611140316-de8c410559fa
	github.com/NYTimes/gziphandler v1.0.1
	github.com/alyu/configparser v0.0.0-20151125021232-26b2fe18bee1
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/aws/aws-sdk-go v1.55.8
	github.com/bluele/logrus_slack v0.0.0-20170812021752-74aa3c9b7cc3
	github.com/bradfitz/gomemcache v0.0.0-20170208213004-1952afaa557d
	github.com/codegangsta/negroni v0.3.0
	github.com/creack/pty v1.1.24
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc
	github.com/dgryski/carbonzipper v0.0.0-20170426152955-d1a3cec4169b
	github.com/dgryski/go-expirecache v0.0.0-20170314133854-743ef98b2adb
	github.com/dgryski/go-onlinestats v0.0.0-20170612111826-1c7d19468768
	github.com/dgryski/go-trigram v0.0.0-20160407183937-79ec494e1ad0
	github.com/dgryski/httputil v0.0.0-20160116060654-189c2918cd08
	github.com/dustin/go-humanize v1.0.1
	github.com/evmar/gocairo v0.0.0-20160222165215-ddd30f837497
	github.com/facebookgo/grace v0.0.0-20170218225239-4afe952a37a4
	github.com/facebookgo/pidfile v0.0.0-20150612191647-f242e2999868
	github.com/go-sql-driver/mysql v1.9.3
	github.com/gogo/protobuf v1.3.2
	github.com/gonum/matrix v0.0.0-20180124231301-a41cc49d4c29
	github.com/google/go-containerregistry v0.20.3
	github.com/google/go-github/v72 v72.0.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/handlers v1.5.2
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.11.3
	github.com/gwenn/yacr v0.0.0-20180209192453-77093bdc7e72
	github.com/helloyi/go-sshclient v1.2.0
	github.com/howeyc/fsnotify v0.0.0-20151003194602-f0c08ee9c607
	github.com/hpcloud/tail v1.0.0
	github.com/hydrogen18/stalecucumber v0.0.0-20161215203336-0a94983f3e27
	github.com/improbable-eng/grpc-web v0.14.0
	github.com/iu0v1/gelada v1.2.2
	github.com/jacoblockett/sanitizefilename v1.0.1
	github.com/jacobsa/fuse v0.0.0-20211125163655-ffd6c474e806
	github.com/jmoiron/sqlx v1.4.0
	github.com/jordan-wright/email v4.0.1-0.20210109023952-943e75fe5223+incompatible
	github.com/juju/errors v0.0.0-20220203013757-bd733f3c86b9
	github.com/kisielk/og-rek v0.0.0-20170425174049-dd41cde712de
	github.com/klauspost/compress v1.18.0
	github.com/lestrrat/go-file-rotatelogs v0.0.0-20171229092148-f984502973a0
	github.com/lestrrat/go-strftime v0.0.0-20170113112000-04ef93e28531
	github.com/lib/pq v1.10.9
	github.com/liip/sheriff/v2 v2.0.1
	github.com/magneticio/vamp-router v0.0.0-20151116102511-29379b621548
	github.com/mattermost/mattermost/server/public v0.1.21
	github.com/micro/go-micro v0.27.1
	github.com/mitchellh/go-homedir v1.1.0
	github.com/mitchellh/mapstructure v1.5.0
	github.com/mjibson/go-dsp v0.0.0-20170104183934-49dba8372707
	github.com/nsf/termbox-go v1.1.1
	github.com/percona/go-mysql v0.0.0-20190307200310-f5cfaf6a5e55
	github.com/peterbourgon/g2g v0.0.0-20161124161852-0c2bab2b173d
	github.com/pingcap/dumpling v0.0.0-20200319081211-255ce0d25719
	github.com/satori/go.uuid v1.2.0
	github.com/shirou/gopsutil v3.21.11+incompatible
	github.com/siddontang/go v0.0.0-20180604090527-bdc77568d726
	github.com/siddontang/go-mysql v0.0.0-20190311123328-7fc3b28d6104
	github.com/siddontang/go-mysql-elasticsearch v0.0.0-20180201161913-f34f371d4391
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.10.1
	github.com/spf13/pflag v1.0.10
	github.com/spf13/viper v1.21.0
	github.com/stretchr/testify v1.11.1
	github.com/swaggo/http-swagger v1.3.4
	github.com/swaggo/swag v1.16.4
	github.com/tebeka/strftime v0.1.5
	github.com/tidwall/gjson v1.18.0
	github.com/tidwall/sjson v1.2.5
	github.com/urfave/cli v1.22.15
	github.com/wangjohn/quickselect v0.0.0-20161129230411-ed8402a42d5f
	github.com/xwb1989/sqlparser v0.0.0-20171128062118-da747e0c62c4
	github.com/yoheimuta/protolint v0.32.0
	gitlab.com/gitlab-org/api/client-go v0.129.0
	golang.org/x/crypto v0.45.0
	golang.org/x/net v0.47.0
	google.golang.org/genproto/googleapis/api v0.0.0-20250804133106-a7a43d27e69b
	google.golang.org/grpc v1.76.0
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.1.0
	google.golang.org/protobuf v1.36.10
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c
	gopkg.in/ini.v1 v1.67.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	k8s.io/api v0.31.3
	k8s.io/apimachinery v0.31.3
	k8s.io/client-go v0.31.3
)

require (
	dario.cat/mergo v1.0.0 // indirect
	filippo.io/edwards25519 v1.1.1 // indirect
	github.com/Azure/go-autorest v14.2.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest/date v0.3.0 // indirect
	github.com/Azure/go-autorest/logger v0.2.1 // indirect
	github.com/Azure/go-autorest/tracing v0.6.0 // indirect
	github.com/DATA-DOG/go-sqlmock v1.4.1 // indirect
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.16.3 // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/cyphar/filepath-securejoin v0.4.1 // indirect
	github.com/dimchansky/utfbom v1.1.0 // indirect
	github.com/docker/cli v27.5.0+incompatible // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.8.2 // indirect
	github.com/dyatlov/go-opengraph/opengraph v0.0.0-20220524092352-606d7b1e5f8a // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/form3tech-oss/jwt-go v3.2.5+incompatible // indirect
	github.com/francoispqt/gojay v1.2.13 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gertd/go-pluralize v0.1.1 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.7 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.6.2 // indirect
	github.com/go-jose/go-jose/v3 v3.0.4 // indirect
	github.com/go-jose/go-jose/v4 v4.1.2 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/spec v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/golang/glog v1.2.5 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-plugin v1.7.0 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.2.0 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-7 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattermost/go-i18n v1.11.1-0.20211013152124-5c415071e404 // indirect
	github.com/mattermost/ldap v0.0.0-20231116144001-0f480c025956 // indirect
	github.com/mattermost/logr/v2 v2.0.22 // indirect
	github.com/mattermost/mattermost/server/v8 v8.0.0-20251229192601-96f159c25115 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-ieproxy v0.0.1 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/micro/mdns v0.1.0 // indirect
	github.com/mitchellh/hashstructure v1.0.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/oklog/run v1.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0 // indirect
	github.com/pborman/uuid v1.2.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pingcap/errors v0.11.5-0.20210425183316-da1aaba5fb63 // indirect
	github.com/pingcap/log v0.0.0-20210625125904-98ed8e2eb1c7 // indirect
	github.com/pingcap/tidb-tools v4.0.0-beta.1.0.20200306103835-530c669f7112+incompatible // indirect
	github.com/pjbgf/sha1cd v0.3.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/shopspring/decimal v1.2.0 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/soheilhy/cmux v0.1.4 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/swaggo/files v1.0.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tinylib/msgp v1.4.0 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/vbatts/tar-split v0.11.6 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/wiggin77/merror v1.0.5 // indirect
	github.com/wiggin77/srslog v1.0.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/yoheimuta/go-protoparser/v4 v4.3.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.18.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.29.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/term v0.37.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	golang.org/x/time v0.12.0 // indirect
	golang.org/x/tools v0.38.0 // indirect
	google.golang.org/genproto v0.0.0-20230530153820-e85fd2cbaebc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251007200510-49b9836ed3ff // indirect
	gopkg.in/fsnotify.v1 v1.4.7 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20240228011516-70dd3763d340 // indirect
	k8s.io/utils v0.0.0-20240711033017-18e509b52bc8 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

require (
	github.com/Azure/go-autorest/autorest/to v0.4.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.3.0 // indirect
	github.com/aclements/go-moremath v0.0.0-20170210193428-033754ab1fee // indirect
	github.com/atc0005/go-teams-notify/v2 v2.8.0
	github.com/bluele/slack v0.0.0-20180528010058-b4b4d354a079 // indirect
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
	github.com/go-git/go-git/v5 v5.16.5
	github.com/go-mysql-org/go-mysql v1.7.0
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/gonum/blas v0.0.0-20180125090452-e7c5890b24cf // indirect
	github.com/gonum/floats v0.0.0-20180125090339-7de1f4ea7ab5 // indirect
	github.com/gonum/internal v0.0.0-20180125090855-fda53f8d2571 // indirect
	github.com/gonum/lapack v0.0.0-20180125091020-f0b8b25edece // indirect
	github.com/gorilla/context v1.1.1 // indirect
	github.com/gorilla/securecookie v1.1.1 // indirect
	github.com/gorilla/sessions v0.0.0-20180209192218-6ba88b7f1c1e // indirect
	github.com/gregdel/pushover v1.1.0
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/vault/api v1.22.0
	github.com/hashicorp/vault/api/auth/approle v0.11.0
	github.com/iancoleman/strcase v0.3.0
	github.com/imdario/mergo v0.3.13 // indirect
	github.com/jehiah/go-strftime v0.0.0-20171201141054-1d33003b3869 // indirect
	github.com/juju/testing v0.0.0-20220203020004-a0ff61f03494 // indirect
	github.com/klauspost/pgzip v1.2.6
	github.com/lestrrat/go-envload v0.0.0-20180220120943-6ed08b54a570 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/miekg/dns v1.1.68 // indirect
	github.com/pelletier/go-toml v1.9.5
	github.com/pkg/xattr v0.4.6
	github.com/rs/cors v1.11.1 // indirect
	github.com/siddontang/go-log v0.0.0-20190221022429-1e957dd83bed
	golang.org/x/oauth2 v0.32.0
	google.golang.org/grpc/examples v0.0.0-20220316190256-c4cabf78f4a2 // indirect
	nhooyr.io/websocket v1.8.7 // indirect
	software.sslmate.com/src/go-pkcs12 v0.4.0
)
