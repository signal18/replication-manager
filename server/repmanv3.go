package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
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
	log.Info("grpc stream srv", srv)
	return handler(srv, stream)
}

func (s *ReplicationManager) unaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// check the fullmethod if public
	if strings.Contains(info.FullMethod, "Public") {
		return handler(ctx, req)
	}

	// handle ACL
	log.Info("grpc unary req", req)
	return handler(ctx, req)
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

func (repman *ReplicationManager) ClusterStatus(ctx context.Context, in *v3.Cluster) (*v3.StatusMessage, error) {
	mycluster := repman.getClusterByName(in.Name)
	if mycluster == nil {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotFound, "Name", in.Name).Err()
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

func (repman *ReplicationManager) MasterPhysicalBackup(ctx context.Context, in *v3.Cluster) (*emptypb.Empty, error) {
	mycluster := repman.getClusterByName(in.Name)
	if mycluster == nil {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotFound, "Name", in.Name).Err()
	}

	mycluster.GetMaster().JobBackupPhysical()
	return &emptypb.Empty{}, nil
}

func (repman *ReplicationManager) GetSettingsForCluster(ctx context.Context, in *v3.Cluster) (*structpb.Struct, error) {
	mycluster := repman.getClusterByName(in.Name)
	if mycluster == nil {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotFound, "Name", in.Name).Err()
	}

	b, err := json.Marshal(mycluster.Conf)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not marshal config")
	}

	s := &structpb.Struct{}
	err = protojson.Unmarshal(b, s)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not unmarshal json config to struct")
	}

	return s, nil
}

var (
	StErrTagValueMustBeSet = status.Errorf(codes.InvalidArgument, "Tag value must be set")
)

func (repman *ReplicationManager) SetActionForClusterSettings(ctx context.Context, in *v3.ClusterSetting) (*emptypb.Empty, error) {
	if in.Cluster == nil {
		return nil, status.Errorf(codes.InvalidArgument, "Cluster must be set")
	}
	mycluster := repman.getClusterByName(in.Cluster.Name)
	if mycluster == nil {
		return nil, v3.NewErrorResource(codes.NotFound, v3.ErrClusterNotFound, "Name", in.Cluster.Name).Err()
	}

	if strings.Contains(in.Action.String(), "TAG") {
		if in.TagValue == "" {
			return nil, StErrTagValueMustBeSet
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

	res := &emptypb.Empty{}

	switch in.Action {
	case v3.ClusterSetting_UNSPECIFIED:
		return nil, status.Errorf(codes.InvalidArgument, "Action must be set")
	case v3.ClusterSetting_APPLY_DYNAMIC_CONFIG:
		go mycluster.SetDBDynamicConfig()
	case v3.ClusterSetting_DISCOVER:
		mycluster.ConfigDiscovery()
	case v3.ClusterSetting_RELOAD:
		repman.InitConfig(repman.Conf)
		mycluster.ReloadConfig(repman.Confs[in.Cluster.Name])
	case v3.ClusterSetting_ADD_DB_TAG:
		mycluster.AddDBTag(in.TagValue)
	case v3.ClusterSetting_DROP_DB_TAG:
		mycluster.DropDBTag(in.TagValue)
	case v3.ClusterSetting_ADD_PROXY_TAG:
		mycluster.AddProxyTag(in.TagValue)
	case v3.ClusterSetting_DROP_PROXY_TAG:
		mycluster.DropProxyTag(in.TagValue)
	case v3.ClusterSetting_SET:
		if in.Setting.Name == v3.ClusterSetting_Setting_UNSPECIFIED {
			return nil, status.Errorf(codes.InvalidArgument, "Setting name must be set")
		}

		if in.Setting.Value == "" {
			return nil, status.Errorf(codes.InvalidArgument, "Setting value must be set")
		}

		repman.setSetting(mycluster, in.Setting.Name.Legacy(), in.Setting.Value)
	case v3.ClusterSetting_SWITCH:
		if in.Switch.Name == v3.ClusterSetting_Switch_UNSPECIFIED {
			return nil, status.Errorf(codes.InvalidArgument, "Switch name must be set")
		}

		repman.switchSettings(mycluster, in.Switch.Name.Legacy())
	}

	return res, nil
}
