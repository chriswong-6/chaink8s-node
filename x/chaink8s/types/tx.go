package types

import (
	"context"

	"google.golang.org/grpc"
)

// MsgServer 是 chaink8s 消息服务接口
type MsgServer interface {
	HandleNodeHeartbeat(context.Context, *MsgNodeHeartbeat) (*MsgNodeHeartbeatResponse, error)
	HandleNodeClaim(context.Context, *MsgNodeClaim) (*MsgNodeClaimResponse, error)
}

// RegisterMsgServer 将 MsgServer 注册到 gRPC 服务注册器
func RegisterMsgServer(s grpc.ServiceRegistrar, srv MsgServer) {
	s.RegisterService(&_Msg_serviceDesc, srv)
}

func _Msg_NodeHeartbeat_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgNodeHeartbeat)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).HandleNodeHeartbeat(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/chaink8s.Msg/NodeHeartbeat",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).HandleNodeHeartbeat(ctx, req.(*MsgNodeHeartbeat))
	}
	return interceptor(ctx, in, info, handler)
}

func _Msg_NodeClaim_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgNodeClaim)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).HandleNodeClaim(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/chaink8s.Msg/NodeClaim",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).HandleNodeClaim(ctx, req.(*MsgNodeClaim))
	}
	return interceptor(ctx, in, info, handler)
}

var _Msg_serviceDesc = grpc.ServiceDesc{
	ServiceName: "chaink8s.Msg",
	HandlerType: (*MsgServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "NodeHeartbeat",
			Handler:    _Msg_NodeHeartbeat_Handler,
		},
		{
			MethodName: "NodeClaim",
			Handler:    _Msg_NodeClaim_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "chaink8s/tx.proto",
}
