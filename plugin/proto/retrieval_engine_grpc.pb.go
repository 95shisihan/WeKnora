// Code generated manually to match retrieval_engine.proto. DO NOT EDIT.
package pluginproto

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	RetrievalEnginePlugin_GetInfo_FullMethodName  = "/weknora.plugin.v1.RetrievalEnginePlugin/GetInfo"
	RetrievalEnginePlugin_Upsert_FullMethodName   = "/weknora.plugin.v1.RetrievalEnginePlugin/Upsert"
	RetrievalEnginePlugin_Search_FullMethodName   = "/weknora.plugin.v1.RetrievalEnginePlugin/Search"
	RetrievalEnginePlugin_Delete_FullMethodName   = "/weknora.plugin.v1.RetrievalEnginePlugin/Delete"
	RetrievalEnginePlugin_Copy_FullMethodName     = "/weknora.plugin.v1.RetrievalEnginePlugin/Copy"
	RetrievalEnginePlugin_Update_FullMethodName   = "/weknora.plugin.v1.RetrievalEnginePlugin/Update"
	RetrievalEnginePlugin_Estimate_FullMethodName = "/weknora.plugin.v1.RetrievalEnginePlugin/Estimate"
)

type RetrievalEnginePluginClient interface {
	GetInfo(context.Context, *emptypb.Empty, ...grpc.CallOption) (*wrapperspb.BytesValue, error)
	Upsert(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*emptypb.Empty, error)
	Search(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*wrapperspb.BytesValue, error)
	Delete(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*emptypb.Empty, error)
	Copy(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*emptypb.Empty, error)
	Update(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*emptypb.Empty, error)
	Estimate(context.Context, *wrapperspb.BytesValue, ...grpc.CallOption) (*wrapperspb.Int64Value, error)
}

type retrievalEnginePluginClient struct{ cc grpc.ClientConnInterface }

func NewRetrievalEnginePluginClient(cc grpc.ClientConnInterface) RetrievalEnginePluginClient {
	return &retrievalEnginePluginClient{cc: cc}
}

func (c *retrievalEnginePluginClient) GetInfo(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*wrapperspb.BytesValue, error) {
	out := new(wrapperspb.BytesValue)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_GetInfo_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Upsert(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Upsert_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Search(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*wrapperspb.BytesValue, error) {
	out := new(wrapperspb.BytesValue)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Search_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Delete(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Delete_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Copy(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Copy_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Update(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Update_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}
func (c *retrievalEnginePluginClient) Estimate(ctx context.Context, in *wrapperspb.BytesValue, opts ...grpc.CallOption) (*wrapperspb.Int64Value, error) {
	out := new(wrapperspb.Int64Value)
	err := c.cc.Invoke(ctx, RetrievalEnginePlugin_Estimate_FullMethodName, in, out, append([]grpc.CallOption{grpc.StaticMethod()}, opts...)...)
	return out, err
}

type RetrievalEnginePluginServer interface {
	GetInfo(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error)
	Upsert(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
	Search(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
	Delete(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
	Copy(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
	Update(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
	Estimate(context.Context, *wrapperspb.BytesValue) (*wrapperspb.Int64Value, error)
	mustEmbedUnimplementedRetrievalEnginePluginServer()
}

type UnimplementedRetrievalEnginePluginServer struct{}

func (UnimplementedRetrievalEnginePluginServer) GetInfo(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	return nil, status.Error(codes.Unimplemented, "method GetInfo not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Upsert(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method Upsert not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Search(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	return nil, status.Error(codes.Unimplemented, "method Search not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Delete(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method Delete not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Copy(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method Copy not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Update(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method Update not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) Estimate(context.Context, *wrapperspb.BytesValue) (*wrapperspb.Int64Value, error) {
	return nil, status.Error(codes.Unimplemented, "method Estimate not implemented")
}
func (UnimplementedRetrievalEnginePluginServer) mustEmbedUnimplementedRetrievalEnginePluginServer() {}
func (UnimplementedRetrievalEnginePluginServer) testEmbeddedByValue()                               {}

func RegisterRetrievalEnginePluginServer(s grpc.ServiceRegistrar, srv RetrievalEnginePluginServer) {
	if value, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		value.testEmbeddedByValue()
	}
	s.RegisterService(&RetrievalEnginePlugin_ServiceDesc, srv)
}

func retrievalUnary[Req any](method, full string, alloc func() *Req, call func(RetrievalEnginePluginServer, context.Context, *Req) (interface{}, error)) grpc.MethodDesc {
	return grpc.MethodDesc{MethodName: method, Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		in := alloc()
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(srv.(RetrievalEnginePluginServer), ctx, in)
		}
		return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: full}, func(ctx context.Context, req interface{}) (interface{}, error) {
			return call(srv.(RetrievalEnginePluginServer), ctx, req.(*Req))
		})
	}}
}

var RetrievalEnginePlugin_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "weknora.plugin.v1.RetrievalEnginePlugin", HandlerType: (*RetrievalEnginePluginServer)(nil),
	Methods: []grpc.MethodDesc{
		retrievalUnary("GetInfo", RetrievalEnginePlugin_GetInfo_FullMethodName, func() *emptypb.Empty { return new(emptypb.Empty) }, func(s RetrievalEnginePluginServer, c context.Context, r *emptypb.Empty) (interface{}, error) {
			return s.GetInfo(c, r)
		}),
		retrievalUnary("Upsert", RetrievalEnginePlugin_Upsert_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Upsert(c, r)
		}),
		retrievalUnary("Search", RetrievalEnginePlugin_Search_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Search(c, r)
		}),
		retrievalUnary("Delete", RetrievalEnginePlugin_Delete_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Delete(c, r)
		}),
		retrievalUnary("Copy", RetrievalEnginePlugin_Copy_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Copy(c, r)
		}),
		retrievalUnary("Update", RetrievalEnginePlugin_Update_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Update(c, r)
		}),
		retrievalUnary("Estimate", RetrievalEnginePlugin_Estimate_FullMethodName, func() *wrapperspb.BytesValue { return new(wrapperspb.BytesValue) }, func(s RetrievalEnginePluginServer, c context.Context, r *wrapperspb.BytesValue) (interface{}, error) {
			return s.Estimate(c, r)
		}),
	}, Streams: []grpc.StreamDesc{}, Metadata: "plugin/proto/retrieval_engine.proto",
}
