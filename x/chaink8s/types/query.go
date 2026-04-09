package types

import (
	"context"

	"google.golang.org/grpc"
)

// ── Query 消息类型 ────────────────────────────────────────────────────────────

// NodeInfo 节点资源的查询视图
type NodeInfo struct {
	Provider string `protobuf:"bytes,1,opt,name=provider,proto3" json:"provider,omitempty"`
	NodeID   string `protobuf:"bytes,2,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	// FreeCPU/FreeMem: 可调度量 = Total - Alloc（计算值）
	FreeCPU         int64 `protobuf:"varint,3,opt,name=free_cpu,json=freeCpu,proto3" json:"free_cpu,omitempty"`
	FreeMem         int64 `protobuf:"varint,4,opt,name=free_mem,json=freeMem,proto3" json:"free_mem,omitempty"`
	FreeGPU         int64 `protobuf:"varint,5,opt,name=free_gpu,json=freeGpu,proto3" json:"free_gpu,omitempty"`
	ReputationScore int64 `protobuf:"varint,6,opt,name=reputation_score,json=reputationScore,proto3" json:"reputation_score,omitempty"`
	FreeGPUCore     int64 `protobuf:"varint,7,opt,name=free_gpu_core,json=freeGpuCore,proto3" json:"free_gpu_core,omitempty"`
	SlashCount      int64 `protobuf:"varint,8,opt,name=slash_count,json=slashCount,proto3" json:"slash_count,omitempty"`
	FreeGPUMemMB    int64 `protobuf:"varint,9,opt,name=free_gpu_mem_mb,json=freeGpuMemMb,proto3" json:"free_gpu_mem_mb,omitempty"`
	// Total/Alloc: 物理总量和已分配量（心跳/链上管理）
	TotalCPU int64 `protobuf:"varint,10,opt,name=total_cpu,json=totalCpu,proto3" json:"total_cpu,omitempty"`
	TotalMem int64 `protobuf:"varint,11,opt,name=total_mem,json=totalMem,proto3" json:"total_mem,omitempty"`
	AllocCPU int64 `protobuf:"varint,12,opt,name=alloc_cpu,json=allocCpu,proto3" json:"alloc_cpu,omitempty"`
	AllocMem int64 `protobuf:"varint,13,opt,name=alloc_mem,json=allocMem,proto3" json:"alloc_mem,omitempty"`
}

func (m *NodeInfo) Reset()                  { *m = NodeInfo{} }
func (m *NodeInfo) String() string          { return m.NodeID }
func (m *NodeInfo) ProtoMessage()           {}
func (*NodeInfo) Descriptor() ([]byte, []int) { return fileDescriptorChaink8sTx, []int{4} }

// QueryNodesRequest 查询节点资源列表；Provider 为空则返回全部
type QueryNodesRequest struct {
	Provider string `protobuf:"bytes,1,opt,name=provider,proto3" json:"provider,omitempty"`
}

func (m *QueryNodesRequest) Reset()                      { *m = QueryNodesRequest{} }
func (m *QueryNodesRequest) String() string              { return "" }
func (m *QueryNodesRequest) ProtoMessage()               {}
func (*QueryNodesRequest) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{5} }

// QueryNodesResponse 返回节点资源列表
type QueryNodesResponse struct {
	Nodes []*NodeInfo `protobuf:"bytes,1,rep,name=nodes,proto3" json:"nodes,omitempty"`
}

func (m *QueryNodesResponse) Reset()                      { *m = QueryNodesResponse{} }
func (m *QueryNodesResponse) String() string              { return "" }
func (m *QueryNodesResponse) ProtoMessage()               {}
func (*QueryNodesResponse) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{6} }

// QuerySpotPriceRequest 查询当前 Spot 价格
type QuerySpotPriceRequest struct{}

func (m *QuerySpotPriceRequest) Reset()                      { *m = QuerySpotPriceRequest{} }
func (m *QuerySpotPriceRequest) String() string              { return "" }
func (m *QuerySpotPriceRequest) ProtoMessage()               {}
func (*QuerySpotPriceRequest) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{7} }

// QuerySpotPriceResponse Spot Market 价格快照
//
// GPU 定价参考 AWS/Azure 同等实例溢价（以 T4 为基准）：
//
//	T4  (16GB VRAM): ~$0.356/hr GPU溢价，约 8x vCPU 单价
//	A10G(24GB VRAM): ~$0.836/hr，约 20x vCPU 单价
//	V100(16GB VRAM): ~$2.676/hr，约 63x vCPU 单价
//	A100(40GB VRAM): ~$4.10/hr，约 96x vCPU 单价
//
// PricePerGPU = basePricePerGPU(32000 uakt) × gpuDemandMultiplier，随供需动态调整。
type QuerySpotPriceResponse struct {
	PricePerCPUMilli int64 `protobuf:"varint,1,opt,name=price_per_cpu_milli,json=pricePerCpuMilli,proto3" json:"price_per_cpu_milli,omitempty"`
	FreeCPUTotal     int64 `protobuf:"varint,2,opt,name=free_cpu_total,json=freeCpuTotal,proto3" json:"free_cpu_total,omitempty"`
	PendingOrders    int64 `protobuf:"varint,3,opt,name=pending_orders,json=pendingOrders,proto3" json:"pending_orders,omitempty"`
	// PricePerGPU 每整块 GPU 每块的价格（uakt），动态调整
	PricePerGPU  int64 `protobuf:"varint,4,opt,name=price_per_gpu,json=pricePerGpu,proto3" json:"price_per_gpu,omitempty"`
	FreeGPUTotal int64 `protobuf:"varint,5,opt,name=free_gpu_total,json=freeGpuTotal,proto3" json:"free_gpu_total,omitempty"`
}

func (m *QuerySpotPriceResponse) Reset()                      { *m = QuerySpotPriceResponse{} }
func (m *QuerySpotPriceResponse) String() string              { return "" }
func (m *QuerySpotPriceResponse) ProtoMessage()               {}
func (*QuerySpotPriceResponse) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{8} }

// QueryBoundOrdersRequest 查询已调度的 Order 列表
type QueryBoundOrdersRequest struct{}

func (m *QueryBoundOrdersRequest) Reset()                      { *m = QueryBoundOrdersRequest{} }
func (m *QueryBoundOrdersRequest) String() string              { return "" }
func (m *QueryBoundOrdersRequest) ProtoMessage()               {}
func (*QueryBoundOrdersRequest) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{9} }

// BoundOrderInfo 已绑定 Order 的完整调度信息（含资源需求，供 etcd-adapter 重建 Pod spec）
//
// GPU 分配字段说明：
//   - ReqGPU:       整块 GPU 数量（SDL gpu.units），0 表示无 GPU 需求
//   - ReqGPUCore:   koordinator gpu-core 百分比（0-100），0 表示使用 ReqGPU×100
//   - ReqGPUMemMB:  koordinator gpu-memory（MiB），0 表示由 webhook 自动计算
//
// 示例：用户在 SDL attributes 指定 gpu_core=40 gpu_memory_mb=4096
//   → ReqGPUCore=40, ReqGPUMemMB=4096
//   → Pod spec: koordinator.sh/gpu-core=40, koordinator.sh/gpu-memory=4096Mi
type BoundOrderInfo struct {
	OrderID      string `protobuf:"bytes,1,opt,name=order_id,json=orderId,proto3" json:"order_id,omitempty"`
	Provider     string `protobuf:"bytes,2,opt,name=provider,proto3" json:"provider,omitempty"`
	NodeID       string `protobuf:"bytes,3,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	ReqCPU       int64  `protobuf:"varint,4,opt,name=req_cpu,json=reqCpu,proto3" json:"req_cpu,omitempty"`
	ReqMem       int64  `protobuf:"varint,5,opt,name=req_mem,json=reqMem,proto3" json:"req_mem,omitempty"`
	ReqGPU       int64  `protobuf:"varint,6,opt,name=req_gpu,json=reqGpu,proto3" json:"req_gpu,omitempty"`
	ReqGPUCore   int64  `protobuf:"varint,7,opt,name=req_gpu_core,json=reqGpuCore,proto3" json:"req_gpu_core,omitempty"`
	ReqGPUMemMB  int64  `protobuf:"varint,8,opt,name=req_gpu_mem_mb,json=reqGpuMemMb,proto3" json:"req_gpu_mem_mb,omitempty"`
	// Image 用户在 SDL placement attributes 中指定的容器镜像
	// 未指定时为空，Monitor 回退到 DefaultImage
	Image        string `protobuf:"bytes,9,opt,name=image,proto3" json:"image,omitempty"`
}

func (m *BoundOrderInfo) Reset()                      { *m = BoundOrderInfo{} }
func (m *BoundOrderInfo) String() string              { return m.OrderID }
func (m *BoundOrderInfo) ProtoMessage()               {}
func (*BoundOrderInfo) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{11} }

// QueryBoundOrdersResponse 返回已绑定的 Order 列表（含调度信息）
type QueryBoundOrdersResponse struct {
	Orders []*BoundOrderInfo `protobuf:"bytes,1,rep,name=orders,proto3" json:"orders,omitempty"`
}

func (m *QueryBoundOrdersResponse) Reset()                      { *m = QueryBoundOrdersResponse{} }
func (m *QueryBoundOrdersResponse) String() string              { return "" }
func (m *QueryBoundOrdersResponse) ProtoMessage()               {}
func (*QueryBoundOrdersResponse) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{10} }

// ── QueryServer 接口 ──────────────────────────────────────────────────────────

// QueryServer 是 chaink8s 查询服务接口
type QueryServer interface {
	Nodes(context.Context, *QueryNodesRequest) (*QueryNodesResponse, error)
	SpotPrice(context.Context, *QuerySpotPriceRequest) (*QuerySpotPriceResponse, error)
	BoundOrders(context.Context, *QueryBoundOrdersRequest) (*QueryBoundOrdersResponse, error)
}

// ── QueryClient 接口（供 Monitor / etcd-adapter 等链外组件使用）────────────────

// QueryClient 是 chaink8s 查询客户端接口
type QueryClient interface {
	Nodes(ctx context.Context, in *QueryNodesRequest, opts ...grpc.CallOption) (*QueryNodesResponse, error)
	BoundOrders(ctx context.Context, in *QueryBoundOrdersRequest, opts ...grpc.CallOption) (*QueryBoundOrdersResponse, error)
}

type queryClientImpl struct{ cc grpc.ClientConnInterface }

// NewQueryClient 创建 chaink8s gRPC 查询客户端
func NewQueryClient(cc grpc.ClientConnInterface) QueryClient {
	return &queryClientImpl{cc}
}

func (c *queryClientImpl) Nodes(ctx context.Context, in *QueryNodesRequest, opts ...grpc.CallOption) (*QueryNodesResponse, error) {
	out := new(QueryNodesResponse)
	return out, c.cc.Invoke(ctx, "/chaink8s.Query/Nodes", in, out, opts...)
}

func (c *queryClientImpl) BoundOrders(ctx context.Context, in *QueryBoundOrdersRequest, opts ...grpc.CallOption) (*QueryBoundOrdersResponse, error) {
	out := new(QueryBoundOrdersResponse)
	return out, c.cc.Invoke(ctx, "/chaink8s.Query/BoundOrders", in, out, opts...)
}

// RegisterQueryServer 将 QueryServer 注册到 gRPC 服务注册器
func RegisterQueryServer(s grpc.ServiceRegistrar, srv QueryServer) {
	s.RegisterService(&_Query_serviceDesc, srv)
}

func _Query_Nodes_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryNodesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).Nodes(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/chaink8s.Query/Nodes"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).Nodes(ctx, req.(*QueryNodesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Query_SpotPrice_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QuerySpotPriceRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).SpotPrice(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/chaink8s.Query/SpotPrice"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).SpotPrice(ctx, req.(*QuerySpotPriceRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Query_BoundOrders_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryBoundOrdersRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).BoundOrders(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/chaink8s.Query/BoundOrders"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).BoundOrders(ctx, req.(*QueryBoundOrdersRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _Query_serviceDesc = grpc.ServiceDesc{
	ServiceName: "chaink8s.Query",
	HandlerType: (*QueryServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Nodes", Handler: _Query_Nodes_Handler},
		{MethodName: "SpotPrice", Handler: _Query_SpotPrice_Handler},
		{MethodName: "BoundOrders", Handler: _Query_BoundOrders_Handler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "chaink8s/tx.proto",
}
