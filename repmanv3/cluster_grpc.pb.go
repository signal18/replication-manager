// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package repmanv3

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// ClusterPublicServiceClient is the client API for ClusterPublicService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ClusterPublicServiceClient interface {
	ClusterStatus(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*StatusMessage, error)
	MasterPhysicalBackup(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

type clusterPublicServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewClusterPublicServiceClient(cc grpc.ClientConnInterface) ClusterPublicServiceClient {
	return &clusterPublicServiceClient{cc}
}

func (c *clusterPublicServiceClient) ClusterStatus(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*StatusMessage, error) {
	out := new(StatusMessage)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterPublicService/ClusterStatus", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterPublicServiceClient) MasterPhysicalBackup(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterPublicService/MasterPhysicalBackup", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClusterPublicServiceServer is the server API for ClusterPublicService service.
// All implementations must embed UnimplementedClusterPublicServiceServer
// for forward compatibility
type ClusterPublicServiceServer interface {
	ClusterStatus(context.Context, *Cluster) (*StatusMessage, error)
	MasterPhysicalBackup(context.Context, *Cluster) (*emptypb.Empty, error)
	mustEmbedUnimplementedClusterPublicServiceServer()
}

// UnimplementedClusterPublicServiceServer must be embedded to have forward compatible implementations.
type UnimplementedClusterPublicServiceServer struct {
}

func (UnimplementedClusterPublicServiceServer) ClusterStatus(context.Context, *Cluster) (*StatusMessage, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ClusterStatus not implemented")
}
func (UnimplementedClusterPublicServiceServer) MasterPhysicalBackup(context.Context, *Cluster) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method MasterPhysicalBackup not implemented")
}
func (UnimplementedClusterPublicServiceServer) mustEmbedUnimplementedClusterPublicServiceServer() {}

// UnsafeClusterPublicServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ClusterPublicServiceServer will
// result in compilation errors.
type UnsafeClusterPublicServiceServer interface {
	mustEmbedUnimplementedClusterPublicServiceServer()
}

func RegisterClusterPublicServiceServer(s grpc.ServiceRegistrar, srv ClusterPublicServiceServer) {
	s.RegisterService(&ClusterPublicService_ServiceDesc, srv)
}

func _ClusterPublicService_ClusterStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Cluster)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterPublicServiceServer).ClusterStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterPublicService/ClusterStatus",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterPublicServiceServer).ClusterStatus(ctx, req.(*Cluster))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterPublicService_MasterPhysicalBackup_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Cluster)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterPublicServiceServer).MasterPhysicalBackup(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterPublicService/MasterPhysicalBackup",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterPublicServiceServer).MasterPhysicalBackup(ctx, req.(*Cluster))
	}
	return interceptor(ctx, in, info, handler)
}

// ClusterPublicService_ServiceDesc is the grpc.ServiceDesc for ClusterPublicService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ClusterPublicService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "signal18.replication_manager.v3.ClusterPublicService",
	HandlerType: (*ClusterPublicServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "ClusterStatus",
			Handler:    _ClusterPublicService_ClusterStatus_Handler,
		},
		{
			MethodName: "MasterPhysicalBackup",
			Handler:    _ClusterPublicService_MasterPhysicalBackup_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "cluster.proto",
}

// ClusterServiceClient is the client API for ClusterService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ClusterServiceClient interface {
	GetCluster(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*structpb.Struct, error)
	GetSettingsForCluster(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*structpb.Struct, error)
	SetActionForClusterSettings(ctx context.Context, in *ClusterSetting, opts ...grpc.CallOption) (*emptypb.Empty, error)
	PerformClusterAction(ctx context.Context, in *ClusterAction, opts ...grpc.CallOption) (*emptypb.Empty, error)
	PerformClusterTest(ctx context.Context, in *ClusterTest, opts ...grpc.CallOption) (*structpb.Struct, error)
	RetrieveFromTopology(ctx context.Context, in *TopologyRetrieval, opts ...grpc.CallOption) (ClusterService_RetrieveFromTopologyClient, error)
	GetClientCertificates(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*Certificate, error)
	GetBackups(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetBackupsClient, error)
	GetTags(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetTagsClient, error)
	GetShards(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetShardsClient, error)
	GetQueryRules(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetQueryRulesClient, error)
	GetSchema(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetSchemaClient, error)
	ExecuteTableAction(ctx context.Context, in *TableAction, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

type clusterServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewClusterServiceClient(cc grpc.ClientConnInterface) ClusterServiceClient {
	return &clusterServiceClient{cc}
}

func (c *clusterServiceClient) GetCluster(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*structpb.Struct, error) {
	out := new(structpb.Struct)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/GetCluster", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) GetSettingsForCluster(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*structpb.Struct, error) {
	out := new(structpb.Struct)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/GetSettingsForCluster", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) SetActionForClusterSettings(ctx context.Context, in *ClusterSetting, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/SetActionForClusterSettings", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) PerformClusterAction(ctx context.Context, in *ClusterAction, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/PerformClusterAction", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) PerformClusterTest(ctx context.Context, in *ClusterTest, opts ...grpc.CallOption) (*structpb.Struct, error) {
	out := new(structpb.Struct)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/PerformClusterTest", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) RetrieveFromTopology(ctx context.Context, in *TopologyRetrieval, opts ...grpc.CallOption) (ClusterService_RetrieveFromTopologyClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[0], "/signal18.replication_manager.v3.ClusterService/RetrieveFromTopology", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceRetrieveFromTopologyClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_RetrieveFromTopologyClient interface {
	Recv() (*structpb.Struct, error)
	grpc.ClientStream
}

type clusterServiceRetrieveFromTopologyClient struct {
	grpc.ClientStream
}

func (x *clusterServiceRetrieveFromTopologyClient) Recv() (*structpb.Struct, error) {
	m := new(structpb.Struct)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) GetClientCertificates(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (*Certificate, error) {
	out := new(Certificate)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/GetClientCertificates", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clusterServiceClient) GetBackups(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetBackupsClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[1], "/signal18.replication_manager.v3.ClusterService/GetBackups", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceGetBackupsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_GetBackupsClient interface {
	Recv() (*Backup, error)
	grpc.ClientStream
}

type clusterServiceGetBackupsClient struct {
	grpc.ClientStream
}

func (x *clusterServiceGetBackupsClient) Recv() (*Backup, error) {
	m := new(Backup)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) GetTags(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetTagsClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[2], "/signal18.replication_manager.v3.ClusterService/GetTags", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceGetTagsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_GetTagsClient interface {
	Recv() (*Tag, error)
	grpc.ClientStream
}

type clusterServiceGetTagsClient struct {
	grpc.ClientStream
}

func (x *clusterServiceGetTagsClient) Recv() (*Tag, error) {
	m := new(Tag)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) GetShards(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetShardsClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[3], "/signal18.replication_manager.v3.ClusterService/GetShards", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceGetShardsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_GetShardsClient interface {
	Recv() (*Cluster, error)
	grpc.ClientStream
}

type clusterServiceGetShardsClient struct {
	grpc.ClientStream
}

func (x *clusterServiceGetShardsClient) Recv() (*Cluster, error) {
	m := new(Cluster)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) GetQueryRules(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetQueryRulesClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[4], "/signal18.replication_manager.v3.ClusterService/GetQueryRules", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceGetQueryRulesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_GetQueryRulesClient interface {
	Recv() (*structpb.Struct, error)
	grpc.ClientStream
}

type clusterServiceGetQueryRulesClient struct {
	grpc.ClientStream
}

func (x *clusterServiceGetQueryRulesClient) Recv() (*structpb.Struct, error) {
	m := new(structpb.Struct)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) GetSchema(ctx context.Context, in *Cluster, opts ...grpc.CallOption) (ClusterService_GetSchemaClient, error) {
	stream, err := c.cc.NewStream(ctx, &ClusterService_ServiceDesc.Streams[5], "/signal18.replication_manager.v3.ClusterService/GetSchema", opts...)
	if err != nil {
		return nil, err
	}
	x := &clusterServiceGetSchemaClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ClusterService_GetSchemaClient interface {
	Recv() (*Table, error)
	grpc.ClientStream
}

type clusterServiceGetSchemaClient struct {
	grpc.ClientStream
}

func (x *clusterServiceGetSchemaClient) Recv() (*Table, error) {
	m := new(Table)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *clusterServiceClient) ExecuteTableAction(ctx context.Context, in *TableAction, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/signal18.replication_manager.v3.ClusterService/ExecuteTableAction", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClusterServiceServer is the server API for ClusterService service.
// All implementations must embed UnimplementedClusterServiceServer
// for forward compatibility
type ClusterServiceServer interface {
	GetCluster(context.Context, *Cluster) (*structpb.Struct, error)
	GetSettingsForCluster(context.Context, *Cluster) (*structpb.Struct, error)
	SetActionForClusterSettings(context.Context, *ClusterSetting) (*emptypb.Empty, error)
	PerformClusterAction(context.Context, *ClusterAction) (*emptypb.Empty, error)
	PerformClusterTest(context.Context, *ClusterTest) (*structpb.Struct, error)
	RetrieveFromTopology(*TopologyRetrieval, ClusterService_RetrieveFromTopologyServer) error
	GetClientCertificates(context.Context, *Cluster) (*Certificate, error)
	GetBackups(*Cluster, ClusterService_GetBackupsServer) error
	GetTags(*Cluster, ClusterService_GetTagsServer) error
	GetShards(*Cluster, ClusterService_GetShardsServer) error
	GetQueryRules(*Cluster, ClusterService_GetQueryRulesServer) error
	GetSchema(*Cluster, ClusterService_GetSchemaServer) error
	ExecuteTableAction(context.Context, *TableAction) (*emptypb.Empty, error)
	mustEmbedUnimplementedClusterServiceServer()
}

// UnimplementedClusterServiceServer must be embedded to have forward compatible implementations.
type UnimplementedClusterServiceServer struct {
}

func (UnimplementedClusterServiceServer) GetCluster(context.Context, *Cluster) (*structpb.Struct, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetCluster not implemented")
}
func (UnimplementedClusterServiceServer) GetSettingsForCluster(context.Context, *Cluster) (*structpb.Struct, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetSettingsForCluster not implemented")
}
func (UnimplementedClusterServiceServer) SetActionForClusterSettings(context.Context, *ClusterSetting) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SetActionForClusterSettings not implemented")
}
func (UnimplementedClusterServiceServer) PerformClusterAction(context.Context, *ClusterAction) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PerformClusterAction not implemented")
}
func (UnimplementedClusterServiceServer) PerformClusterTest(context.Context, *ClusterTest) (*structpb.Struct, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PerformClusterTest not implemented")
}
func (UnimplementedClusterServiceServer) RetrieveFromTopology(*TopologyRetrieval, ClusterService_RetrieveFromTopologyServer) error {
	return status.Errorf(codes.Unimplemented, "method RetrieveFromTopology not implemented")
}
func (UnimplementedClusterServiceServer) GetClientCertificates(context.Context, *Cluster) (*Certificate, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetClientCertificates not implemented")
}
func (UnimplementedClusterServiceServer) GetBackups(*Cluster, ClusterService_GetBackupsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetBackups not implemented")
}
func (UnimplementedClusterServiceServer) GetTags(*Cluster, ClusterService_GetTagsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetTags not implemented")
}
func (UnimplementedClusterServiceServer) GetShards(*Cluster, ClusterService_GetShardsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetShards not implemented")
}
func (UnimplementedClusterServiceServer) GetQueryRules(*Cluster, ClusterService_GetQueryRulesServer) error {
	return status.Errorf(codes.Unimplemented, "method GetQueryRules not implemented")
}
func (UnimplementedClusterServiceServer) GetSchema(*Cluster, ClusterService_GetSchemaServer) error {
	return status.Errorf(codes.Unimplemented, "method GetSchema not implemented")
}
func (UnimplementedClusterServiceServer) ExecuteTableAction(context.Context, *TableAction) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ExecuteTableAction not implemented")
}
func (UnimplementedClusterServiceServer) mustEmbedUnimplementedClusterServiceServer() {}

// UnsafeClusterServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ClusterServiceServer will
// result in compilation errors.
type UnsafeClusterServiceServer interface {
	mustEmbedUnimplementedClusterServiceServer()
}

func RegisterClusterServiceServer(s grpc.ServiceRegistrar, srv ClusterServiceServer) {
	s.RegisterService(&ClusterService_ServiceDesc, srv)
}

func _ClusterService_GetCluster_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Cluster)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).GetCluster(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/GetCluster",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).GetCluster(ctx, req.(*Cluster))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_GetSettingsForCluster_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Cluster)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).GetSettingsForCluster(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/GetSettingsForCluster",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).GetSettingsForCluster(ctx, req.(*Cluster))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_SetActionForClusterSettings_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClusterSetting)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).SetActionForClusterSettings(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/SetActionForClusterSettings",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).SetActionForClusterSettings(ctx, req.(*ClusterSetting))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_PerformClusterAction_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClusterAction)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).PerformClusterAction(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/PerformClusterAction",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).PerformClusterAction(ctx, req.(*ClusterAction))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_PerformClusterTest_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClusterTest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).PerformClusterTest(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/PerformClusterTest",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).PerformClusterTest(ctx, req.(*ClusterTest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_RetrieveFromTopology_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(TopologyRetrieval)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).RetrieveFromTopology(m, &clusterServiceRetrieveFromTopologyServer{stream})
}

type ClusterService_RetrieveFromTopologyServer interface {
	Send(*structpb.Struct) error
	grpc.ServerStream
}

type clusterServiceRetrieveFromTopologyServer struct {
	grpc.ServerStream
}

func (x *clusterServiceRetrieveFromTopologyServer) Send(m *structpb.Struct) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_GetClientCertificates_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Cluster)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).GetClientCertificates(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/GetClientCertificates",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).GetClientCertificates(ctx, req.(*Cluster))
	}
	return interceptor(ctx, in, info, handler)
}

func _ClusterService_GetBackups_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Cluster)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).GetBackups(m, &clusterServiceGetBackupsServer{stream})
}

type ClusterService_GetBackupsServer interface {
	Send(*Backup) error
	grpc.ServerStream
}

type clusterServiceGetBackupsServer struct {
	grpc.ServerStream
}

func (x *clusterServiceGetBackupsServer) Send(m *Backup) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_GetTags_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Cluster)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).GetTags(m, &clusterServiceGetTagsServer{stream})
}

type ClusterService_GetTagsServer interface {
	Send(*Tag) error
	grpc.ServerStream
}

type clusterServiceGetTagsServer struct {
	grpc.ServerStream
}

func (x *clusterServiceGetTagsServer) Send(m *Tag) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_GetShards_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Cluster)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).GetShards(m, &clusterServiceGetShardsServer{stream})
}

type ClusterService_GetShardsServer interface {
	Send(*Cluster) error
	grpc.ServerStream
}

type clusterServiceGetShardsServer struct {
	grpc.ServerStream
}

func (x *clusterServiceGetShardsServer) Send(m *Cluster) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_GetQueryRules_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Cluster)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).GetQueryRules(m, &clusterServiceGetQueryRulesServer{stream})
}

type ClusterService_GetQueryRulesServer interface {
	Send(*structpb.Struct) error
	grpc.ServerStream
}

type clusterServiceGetQueryRulesServer struct {
	grpc.ServerStream
}

func (x *clusterServiceGetQueryRulesServer) Send(m *structpb.Struct) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_GetSchema_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Cluster)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ClusterServiceServer).GetSchema(m, &clusterServiceGetSchemaServer{stream})
}

type ClusterService_GetSchemaServer interface {
	Send(*Table) error
	grpc.ServerStream
}

type clusterServiceGetSchemaServer struct {
	grpc.ServerStream
}

func (x *clusterServiceGetSchemaServer) Send(m *Table) error {
	return x.ServerStream.SendMsg(m)
}

func _ClusterService_ExecuteTableAction_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TableAction)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ClusterServiceServer).ExecuteTableAction(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/signal18.replication_manager.v3.ClusterService/ExecuteTableAction",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ClusterServiceServer).ExecuteTableAction(ctx, req.(*TableAction))
	}
	return interceptor(ctx, in, info, handler)
}

// ClusterService_ServiceDesc is the grpc.ServiceDesc for ClusterService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ClusterService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "signal18.replication_manager.v3.ClusterService",
	HandlerType: (*ClusterServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetCluster",
			Handler:    _ClusterService_GetCluster_Handler,
		},
		{
			MethodName: "GetSettingsForCluster",
			Handler:    _ClusterService_GetSettingsForCluster_Handler,
		},
		{
			MethodName: "SetActionForClusterSettings",
			Handler:    _ClusterService_SetActionForClusterSettings_Handler,
		},
		{
			MethodName: "PerformClusterAction",
			Handler:    _ClusterService_PerformClusterAction_Handler,
		},
		{
			MethodName: "PerformClusterTest",
			Handler:    _ClusterService_PerformClusterTest_Handler,
		},
		{
			MethodName: "GetClientCertificates",
			Handler:    _ClusterService_GetClientCertificates_Handler,
		},
		{
			MethodName: "ExecuteTableAction",
			Handler:    _ClusterService_ExecuteTableAction_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "RetrieveFromTopology",
			Handler:       _ClusterService_RetrieveFromTopology_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetBackups",
			Handler:       _ClusterService_GetBackups_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetTags",
			Handler:       _ClusterService_GetTags_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetShards",
			Handler:       _ClusterService_GetShards_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetQueryRules",
			Handler:       _ClusterService_GetQueryRules_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetSchema",
			Handler:       _ClusterService_GetSchema_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "cluster.proto",
}
