package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"

	log "github.com/sirupsen/logrus"
)

type Repmanv3Config struct {
	Listen Repmanv3ListenAddress
	TLS    Repmanv3TLS
}

type Repmanv3ListenAddress struct {
	Address string
	Port    string
}

func (l *Repmanv3ListenAddress) AddressWithPort() string {
	var str strings.Builder
	str.WriteString(l.Address)
	str.WriteString(`:`)
	str.WriteString(l.Port)
	return str.String()
}

type Repmanv3TLS struct {
	Enabled            bool
	CertificatePath    string
	CertificateKeyPath string
}

func (s *ReplicationManager) SetV3Config(config Repmanv3Config) {
	s.v3Config = config
}

func (s *ReplicationManager) StartServerV3(debug bool, router *mux.Router) error {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := net.Listen("tcp", s.v3Config.Listen.AddressWithPort())
	if err != nil {
		return err
	}

	var serverOpts []grpc.ServerOption
	var dopts []grpc.DialOption
	var tlsConfig *tls.Config
	serverOpts, dopts, tlsConfig, err = s.getCredentials()
	if err != nil {
		return err
	}

	if debug {
		serverOpts = append(serverOpts, grpc.UnaryInterceptor(
			grpc_middleware.ChainUnaryServer(
				s.unaryInterceptor,
				// grpc_zap.UnaryServerInterceptor(s.log),
			),
		))
		serverOpts = append(serverOpts, grpc.StreamInterceptor(
			grpc_middleware.ChainStreamServer(
				s.streamInterceptor,
				// grpc_zap.StreamServerInterceptor(s.log),
			),
		))
	} else {
		serverOpts = append(serverOpts,
			grpc.UnaryInterceptor(s.unaryInterceptor),
			grpc.StreamInterceptor(s.streamInterceptor),
		)
	}

	s.grpcServer = grpc.NewServer(serverOpts...)
	v3.RegisterClusterPublicServiceServer(s.grpcServer, s)
	v3.RegisterClusterServiceServer(s.grpcServer, s)

	/* Bootstrap the Muxed connection */
	httpmux := http.NewServeMux()
	gwmux := runtime.NewServeMux()

	err = v3.RegisterClusterPublicServiceHandlerFromEndpoint(ctx,
		gwmux,
		s.v3Config.Listen.AddressWithPort(),
		dopts,
	)
	if err != nil {
		return fmt.Errorf("could not register service ClusterPublicService: %w", err)
	}

	err = v3.RegisterClusterServiceHandlerFromEndpoint(ctx,
		gwmux,
		s.v3Config.Listen.AddressWithPort(),
		dopts,
	)

	if err != nil {
		return fmt.Errorf("could not register service ClusterService: %w", err)
	}

	httpmux.Handle("/", gwmux)

	srv := &http.Server{
		Addr: s.v3Config.Listen.AddressWithPort(),
		Handler: grpcHandlerFunc(s,
			httpmux,
			handlers.CORS(
				handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "Authorization"}),
				handlers.AllowedMethods([]string{"GET", "POST", "PUT", "HEAD", "OPTIONS"}),
				handlers.AllowedOrigins([]string{"*"}),
			)(router),
		),

		// ErrorLog: zap.NewStdLog(s.log),
	}

	s.grpcWrapped = grpcweb.WrapServer(s.grpcServer, grpcweb.WithOriginFunc(func(origin string) bool {
		return true
	}))

	// s.V3Up <- true
	if s.v3Config.TLS.Enabled {
		log.Info("starting multiplexed TLS HTTP/2.0 and HTTP/1.1 Gateway server: ", s.v3Config.Listen.AddressWithPort())
		srv.TLSConfig = tlsConfig
		err = srv.Serve(tls.NewListener(conn, srv.TLSConfig))
	} else {
		log.Info("starting multiplexed HTTP/2.0 and HTTP/1.1 Gateway server: ", s.v3Config.Listen.AddressWithPort())
		// we need to wrap the non-tls connection inside h2c because http2 in Go enforces TLS
		srv.Handler = h2c.NewHandler(srv.Handler, &http2.Server{})
		err = srv.Serve(conn)
	}

	if err != nil {
		return err
	}

	return nil
}

func (s *ReplicationManager) streamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if strings.Contains(info.FullMethod, "Public") {
		return handler(srv, stream)
	}

	// handle ACL
	log.Infof("grpc stream srv: %v", srv)
	return handler(srv, stream)
}

func (s *ReplicationManager) unaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// check the fullmethod if public
	if strings.Contains(info.FullMethod, "Public") {
		return handler(ctx, req)
	}

	log.Infof("grpc unary req: %v", req)

	if cMsg, ok := req.(v3.ContainsClusterMessage); ok {
		// mycluster, err := s.getClusterFromFromRequest(cMsg)
		// if err != nil {
		// 	return nil, err
		// }
		// ctx, err = s.authorize(ctx, mycluster)
		// if err != nil {
		// 	return nil, v3.NewError(codes.Unauthenticated, err).Err()
		// }

		// log.Infof("new ctx: %v", ctx)
		return handler(ctx, cMsg)
	}
	return nil, v3.NewError(codes.InvalidArgument, fmt.Errorf("no message sent with a cluster property")).Err()

	// handle ACL

}

func (s *ReplicationManager) getClusterFromFromRequest(req v3.ContainsClusterMessage) (*cluster.Cluster, error) {
	c, err := req.GetClusterMessage()
	if err != nil {
		return nil, err
	}

	if c.Name == "" {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotSet, "Name", c.Name).Err()
	}

	mycluster := s.getClusterByName(c.Name)
	if mycluster == nil {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotFound, "Name", c.Name).Err()
	}

	return mycluster, nil
}

type ContextKey string

func (s *ReplicationManager) getClusterAndUser(ctx context.Context, req v3.ContainsClusterMessage) (cluster.APIUser, *cluster.Cluster, error) {
	mycluster, err := s.getClusterFromFromRequest(req)
	if err != nil {
		return cluster.APIUser{}, nil, err
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return cluster.APIUser{}, nil, fmt.Errorf("metadata missing")
	}
	log.Info("md", md)

	auth := md.Get("authorization")
	if len(auth) == 0 {
		return cluster.APIUser{}, nil, fmt.Errorf("authorization header missing")
	}

	if len(auth[0]) > 6 && strings.ToUpper(auth[0][0:7]) == "BEARER " {
		token, err := jwt.Parse(auth[0][7:], func(token *jwt.Token) (interface{}, error) {
			vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
			return vk, nil
		})
		if err != nil {
			return cluster.APIUser{}, nil, fmt.Errorf("failed to parse jwt token: %w", err)
		}

		claims := token.Claims.(jwt.MapClaims)
		userinfo := claims["CustomUserInfo"]
		mycutinfo := userinfo.(map[string]interface{})

		user, err := mycluster.GetAPIUser(mycutinfo["Name"].(string), mycutinfo["Password"].(string))
		if err != nil {
			return cluster.APIUser{}, nil, err
		}

		return user, mycluster, nil
	}

	return cluster.APIUser{}, nil, fmt.Errorf("bearer is missing")
}

func (s *ReplicationManager) getCredentials() (opts []grpc.ServerOption, dopts []grpc.DialOption, tlsConfig *tls.Config, err error) {
	if s.v3Config.TLS.Enabled {
		// TLS for the gRPC server
		grpcCreds, err := credentials.NewServerTLSFromFile(s.v3Config.TLS.CertificatePath, s.v3Config.TLS.CertificateKeyPath)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error configuring credentials for TLS: %w", err)
		}

		opts = append(opts, grpc.Creds(grpcCreds))

		// TLS for the REST Gateway to gRPC
		gatewayCreds := credentials.NewTLS(&tls.Config{
			ServerName: s.v3Config.Listen.Address, // this is critical
		})
		dopts = []grpc.DialOption{grpc.WithTransportCredentials(gatewayCreds)}

		cer, err := tls.LoadX509KeyPair(s.v3Config.TLS.CertificatePath, s.v3Config.TLS.CertificateKeyPath)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error loading certificates for TLS: %w", err)
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cer},
			// declare that the listener supports http/2.0
			NextProtos: []string{"h2"},
		}
	} else {
		dopts = []grpc.DialOption{grpc.WithInsecure()}
	}

	return opts, dopts, tlsConfig, nil
}

// grpcHandlerFunc returns an http.Handler that delegates to grpcServer on incoming gRPC
// connections or otherHandler otherwise. Adapter from cockroachdb.
func grpcHandlerFunc(s *ReplicationManager, otherHandler http.Handler, legacyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.ProtoMajor == 2 && req.Header.Get("Content-Type") == "application/grpc" {
			s.grpcServer.ServeHTTP(resp, req)
		} else {
			if s.grpcWrapped.IsAcceptableGrpcCorsRequest(req) || s.grpcWrapped.IsGrpcWebRequest(req) {
				s.grpcWrapped.ServeHTTP(resp, req)
			}

			// check if we need to serve the new API or the old one
			if strings.Contains(req.URL.Path, "/v3") {
				otherHandler.ServeHTTP(resp, req)
			} else {
				legacyHandler.ServeHTTP(resp, req)
			}
		}
	})
}

func (s *ReplicationManager) ClusterStatus(ctx context.Context, in *v3.Cluster) (*v3.StatusMessage, error) {
	mycluster, err := s.getClusterFromFromRequest(in)
	if err != nil {
		return nil, err
	}

	if mycluster.GetStatus() {
		return &v3.StatusMessage{
			Alive: v3.ServiceStatus_RUNNING,
		}, nil
	}
	return &v3.StatusMessage{
		Alive: v3.ServiceStatus_ERRORS,
	}, nil

}

func (s *ReplicationManager) MasterPhysicalBackup(ctx context.Context, in *v3.Cluster) (*emptypb.Empty, error) {
	mycluster, err := s.getClusterFromFromRequest(in)
	if err != nil {
		return nil, err
	}

	mycluster.GetMaster().JobBackupPhysical()
	return &emptypb.Empty{}, nil
}

func (s *ReplicationManager) GetSettingsForCluster(ctx context.Context, in *v3.Cluster) (*structpb.Struct, error) {
	user, mycluster, err := s.getClusterAndUser(ctx, in)
	if err != nil {
		return nil, err
	}
	if err = user.Granted(config.GrantClusterSettings); err != nil {
		return nil, err
	}

	b, err := json.Marshal(mycluster.Conf)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not marshal config")
	}

	out := &structpb.Struct{}
	err = protojson.Unmarshal(b, out)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not unmarshal json config to struct")
	}

	return out, nil
}

func (s *ReplicationManager) SetActionForClusterSettings(ctx context.Context, in *v3.ClusterSetting) (res *emptypb.Empty, err error) {
	user, mycluster, err := s.getClusterAndUser(ctx, in)
	if err != nil {
		return nil, err
	}

	if strings.Contains(in.Action.String(), "TAG") {
		if in.TagValue == "" {
			return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrFieldNotSet, "TagValue", "").Err()
		}
	}

	log.Printf("incoming: %v", in)

	// check if we are doing a set or switch
	if in.Action == v3.ClusterSetting_UNSPECIFIED {
		if in.Setting != nil {
			in.Action = v3.ClusterSetting_SET
		}
		if in.Switch != nil {
			in.Action = v3.ClusterSetting_SWITCH
		}
	}

	switch in.Action {
	case v3.ClusterSetting_UNSPECIFIED:
		return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrEnumNotSet, "action", "").Err()

	case v3.ClusterSetting_DISCOVER:
		if err = user.Granted(config.GrantClusterSettings); err != nil {
			return nil, err
		}
		mycluster.ConfigDiscovery()

	case v3.ClusterSetting_RELOAD:
		if err = user.Granted(config.GrantClusterSettings); err != nil {
			return nil, err
		}
		s.InitConfig(s.Conf)
		mycluster.ReloadConfig(s.Confs[in.Cluster.Name])

	case v3.ClusterSetting_ADD_PROXY_TAG:
		if err = user.Granted(config.GrantProxyConfigFlag); err != nil {
			return nil, err
		}
		mycluster.AddProxyTag(in.TagValue)

	case v3.ClusterSetting_DROP_PROXY_TAG:
		if err = user.Granted(config.GrantProxyConfigFlag); err != nil {
			return nil, err
		}
		mycluster.DropProxyTag(in.TagValue)

	case v3.ClusterSetting_SET:
		if err = user.Granted(config.GrantClusterSettings); err != nil {
			return nil, err
		}
		if in.Setting.Name == v3.ClusterSetting_Setting_UNSPECIFIED {
			return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrFieldNotSet, "setting.name", "").Err()
		}

		if in.Setting.Value == "" {
			return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrFieldNotSet, "setting.value", "").Err()
		}

		s.setSetting(mycluster, in.Setting.Name.Legacy(), in.Setting.Value)

	case v3.ClusterSetting_SWITCH:
		if err = user.Granted(config.GrantClusterSettings); err != nil {
			return nil, err
		}

		if in.Switch.Name == v3.ClusterSetting_Switch_UNSPECIFIED {
			return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrEnumNotSet, "switch", "").Err()
		}

		s.switchSettings(mycluster, in.Switch.Name.Legacy())

	case v3.ClusterSetting_APPLY_DYNAMIC_CONFIG:
		if err = user.Granted(config.GrantDBConfigFlag); err != nil {
			return nil, err
		}
		go mycluster.SetDBDynamicConfig()

	case v3.ClusterSetting_ADD_DB_TAG:
		if err = user.Granted(config.GrantDBConfigFlag); err != nil {
			return nil, err
		}
		mycluster.AddDBTag(in.TagValue)

	case v3.ClusterSetting_DROP_DB_TAG:
		if err = user.Granted(config.GrantDBConfigFlag); err != nil {
			return nil, err
		}
		mycluster.DropDBTag(in.TagValue)
	}

	return res, nil
}

func (s *ReplicationManager) PerformClusterAction(ctx context.Context, in *v3.ClusterAction) (res *emptypb.Empty, err error) {
	if in.Cluster == nil {
		return nil, v3.NewError(codes.InvalidArgument, v3.ErrClusterNotSet).Err()
	}

	// WARNING: this one cannot be validated for ACL, as there is no cluster to validate against
	// special case, the clustername doesn't exist yet
	if in.Cluster.ClusterShardingName == "" {
		if in.Action == v3.ClusterAction_ADD {
			err = s.AddCluster(in.Cluster.Name, "")
			if err != nil {
				return nil, v3.NewError(codes.Unknown, err).Err()
			}
			return
		}
	}

	user, mycluster, err := s.getClusterAndUser(ctx, in)
	if err != nil {
		return nil, err
	}

	switch in.Action {
	case v3.ClusterAction_ADD:
		if err = user.Granted(config.GrantProvCluster); err != nil {
			return nil, err
		}
		err = s.AddCluster(in.Cluster.ClusterShardingName, in.Cluster.Name)
		if err != nil {
			return nil, v3.NewError(codes.Unknown, err).Err()
		}
		err = mycluster.RollingRestart()
	case v3.ClusterAction_ADDSERVER:
		switch in.Server.Type {
		case v3.ClusterAction_Server_TYPE_UNSPECIFIED:
			err = mycluster.AddSeededServer(in.Server.GetURI())
		case v3.ClusterAction_Server_PROXY:
			if in.Server.Proxy == v3.ClusterAction_Server_PROXY_UNSPECIFIED {
				return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrEnumNotSet, "Proxy", v3.ClusterAction_Server_PROXY_UNSPECIFIED.String()).Err()
			}
			err = mycluster.AddSeededProxy(
				strings.ToLower(in.Server.Proxy.String()),
				in.Server.Host,
				fmt.Sprintf("%d", in.Server.Port), "", "")
		case v3.ClusterAction_Server_DATABASE:
			switch in.Server.Database {
			case v3.ClusterAction_Server_DATABASE_UNSPECIFIED:
				return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrEnumNotSet, "Database", v3.ClusterAction_Server_DATABASE_UNSPECIFIED.String()).Err()
			case v3.ClusterAction_Server_MARIADB:
				mycluster.Conf.ProvDbImg = "mariadb:latest"
			case v3.ClusterAction_Server_PERCONA:
				mycluster.Conf.ProvDbImg = "percona:latest"
			case v3.ClusterAction_Server_MYSQL:
				mycluster.Conf.ProvDbImg = "mysql:latest"
				// TODO: Postgres is an option but previous code doesn't mention it
			}
			err = mycluster.AddSeededServer(in.Server.GetURI())
		}
	case v3.ClusterAction_REPLICATION_BOOTSTRAP:
		if in.Topology == v3.ClusterAction_RT_UNSPECIFIED {
			return nil, v3.NewErrorResource(codes.InvalidArgument, v3.ErrEnumNotSet, "Topology", v3.ClusterAction_RT_UNSPECIFIED.String()).Err()
		}
		s.bootstrapTopology(mycluster, in.Topology.Legacy())
		err = mycluster.BootstrapReplication(true)
	case v3.ClusterAction_CANCEL_ROLLING_REPROV:
		err = mycluster.CancelRollingReprov()
	case v3.ClusterAction_CANCEL_ROLLING_RESTART:
		err = mycluster.CancelRollingRestart()
	case v3.ClusterAction_CHECKSUM_ALL_TABLES:
		go mycluster.CheckAllTableChecksum()
	case v3.ClusterAction_FAILOVER:
		mycluster.MasterFailover(true)
	case v3.ClusterAction_MASTER_PHYSICAL_BACKUP:
		_, err = mycluster.GetMaster().JobBackupPhysical()
	case v3.ClusterAction_OPTIMIZE:
		mycluster.RollingOptimize()
	case v3.ClusterAction_RESET_FAILOVER_CONTROL:
		mycluster.ResetFailoverCtr()
	case v3.ClusterAction_RESET_SLA:
		mycluster.SetEmptySla()
	case v3.ClusterAction_ROLLING:
		err = mycluster.RollingRestart()
	case v3.ClusterAction_ROTATEKEYS:
		mycluster.KeyRotation()
	case v3.ClusterAction_START_TRAFFIC:
		mycluster.SetTraffic(true)
	case v3.ClusterAction_STOP_TRAFFIC:
		mycluster.SetTraffic(false)
	case v3.ClusterAction_SWITCHOVER:
		mycluster.LogPrintf("INFO", "API force for prefered master: %s", in.Server.GetURI())
		if mycluster.IsInHostList(in.Server.GetURI()) {
			mycluster.SetPrefMaster(in.Server.GetURI())
			mycluster.MasterFailover(false)
			return
		} else {
			return nil, v3.NewErrorResource(codes.NotFound, v3.ErrServerNotFound, "Server", in.Server.GetURI()).Err()
		}
	case v3.ClusterAction_SYSBENCH:
		go mycluster.RunSysbench()
	case v3.ClusterAction_WAITDATABASES:
		err = mycluster.WaitDatabaseCanConn()
	case v3.ClusterAction_REPLICATION_CLEANUP:
		err = mycluster.BootstrapReplicationCleanup()
	}

	if err != nil {
		mycluster.LogPrintf("ERROR", "API Error: %s", err)
		return nil, v3.NewError(codes.Unknown, err).Err()
	}

	return
}
