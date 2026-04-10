package types

import (
	"time"

	cerrors "cosmossdk.io/errors"
	gogoproto "github.com/cosmos/gogoproto/proto"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// MsgNodeHeartbeatResponse Heartbeat 响应（空消息）
type MsgNodeHeartbeatResponse struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *MsgNodeHeartbeatResponse) Reset()                      { *m = MsgNodeHeartbeatResponse{} }
func (m *MsgNodeHeartbeatResponse) String() string              { return "" }
func (m *MsgNodeHeartbeatResponse) ProtoMessage()               {}
func (*MsgNodeHeartbeatResponse) Descriptor() ([]byte, []int)   { return fileDescriptorChaink8sTx, []int{1} }

// MsgNodeClaimResponse NodeClaim 响应（空消息）
type MsgNodeClaimResponse struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *MsgNodeClaimResponse) Reset()                   { *m = MsgNodeClaimResponse{} }
func (m *MsgNodeClaimResponse) String() string           { return "" }
func (m *MsgNodeClaimResponse) ProtoMessage()            {}
func (*MsgNodeClaimResponse) Descriptor() ([]byte, []int) { return fileDescriptorChaink8sTx, []int{3} }

func init() {
	// 在 proto 注册表中注册消息类型，赋予唯一 typeURL
	// 这使得 Cosmos SDK 的 InterfaceRegistry 和 Any 类型可以正确解析
	gogoproto.RegisterType((*MsgNodeHeartbeat)(nil), "chaink8s.MsgNodeHeartbeat")
	gogoproto.RegisterType((*MsgNodeClaim)(nil), "chaink8s.MsgNodeClaim")
	gogoproto.RegisterType((*MsgNodeHeartbeatResponse)(nil), "chaink8s.MsgNodeHeartbeatResponse")
	gogoproto.RegisterType((*MsgNodeClaimResponse)(nil), "chaink8s.MsgNodeClaimResponse")

	// Query 消息类型
	gogoproto.RegisterType((*NodeInfo)(nil), "chaink8s.NodeInfo")
	gogoproto.RegisterType((*QueryNodesRequest)(nil), "chaink8s.QueryNodesRequest")
	gogoproto.RegisterType((*QueryNodesResponse)(nil), "chaink8s.QueryNodesResponse")
	gogoproto.RegisterType((*QuerySpotPriceRequest)(nil), "chaink8s.QuerySpotPriceRequest")
	gogoproto.RegisterType((*QuerySpotPriceResponse)(nil), "chaink8s.QuerySpotPriceResponse")
	gogoproto.RegisterType((*QueryBoundOrdersRequest)(nil), "chaink8s.QueryBoundOrdersRequest")
	gogoproto.RegisterType((*QueryBoundOrdersResponse)(nil), "chaink8s.QueryBoundOrdersResponse")
	gogoproto.RegisterType((*BoundOrderInfo)(nil), "chaink8s.BoundOrderInfo")
}

const (
	ModuleName = "chaink8s"
	StoreKey   = ModuleName
	RouterKey  = ModuleName

	// HeartbeatTimeout 节点心跳超时阈值：超过此时间未上报则排除调度，并在 EndBlock 触发 slash
	HeartbeatTimeout = 2 * time.Minute
)

// NodeResource 存储节点的资源容量与分配状态
//
// 设计原则：心跳只上报物理总容量（Total*），链上只管理已分配量（Alloc*）。
// 可用量 = Total - Alloc，按需计算，不单独存储，防止心跳覆盖链上记账。
type NodeResource struct {
	NodeID   string // 节点唯一标识（主机名）
	Provider string // 所属 Provider 的区块链地址

	// ── 物理总容量（心跳写入，稳定值）────────────────────────────────
	TotalCPU     int64 // 物理 CPU 总量（milli-cores，1000 = 1核）
	TotalMem     int64 // 物理内存总量（字节）
	FreeGPU      int64 // 物理 GPU 数量（用于心跳和展示）
	FreeGPUMemMB int64 // GPU 空闲显存总量（MiB），心跳上报

	// ── 已分配量（链上 ApplyNodeClaim / ReleaseNodeClaim 管理）─────────
	AllocCPU     int64 // 已分配 CPU（milli-cores）
	AllocMem     int64 // 已分配内存（字节）
	FreeGPUCore  int64 // 可用 GPU 算力单位（100 per GPU）；调度时按此扣减

	UpdatedAt  time.Time // 最后心跳时间
	Stake      int64     // Provider 质押量（影响调度优先级）
	SlashCount int64     // 被 slash 次数（影响声誉分）
}

// AvailCPU 返回当前可调度 CPU（物理总量 - 已分配）
func (n *NodeResource) AvailCPU() int64 {
	v := n.TotalCPU - n.AllocCPU
	if v < 0 {
		return 0
	}
	return v
}

// AvailMem 返回当前可调度内存（物理总量 - 已分配）
func (n *NodeResource) AvailMem() int64 {
	v := n.TotalMem - n.AllocMem
	if v < 0 {
		return 0
	}
	return v
}

// ReputationScore 计算节点声誉分（0-100）
// slash 次数越少分数越高；Stake 为 0 时不惩罚（用于无质押的本地测试节点）
func (n *NodeResource) ReputationScore() int64 {
	base := int64(100)
	penalty := n.SlashCount * 10
	if penalty >= base {
		return 0
	}
	return base - penalty
}

// MsgNodeHeartbeat Provider 定期上报节点资源到链上
// 每笔消息必须由 Provider 私钥签名，防止冒名上报
type MsgNodeHeartbeat struct {
	Provider     string `protobuf:"bytes,1,opt,name=provider,proto3" json:"provider,omitempty"`
	NodeID       string `protobuf:"bytes,2,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	FreeCPU      int64  `protobuf:"varint,3,opt,name=free_cpu,json=freeCpu,proto3" json:"free_cpu,omitempty"`
	FreeMem      int64  `protobuf:"varint,4,opt,name=free_mem,json=freeMem,proto3" json:"free_mem,omitempty"`
	FreeGPU      int64  `protobuf:"varint,5,opt,name=free_gpu,json=freeGpu,proto3" json:"free_gpu,omitempty"`
	FreeGPUMemMB int64  `protobuf:"varint,6,opt,name=free_gpu_mem_mb,json=freeGpuMemMb,proto3" json:"free_gpu_mem_mb,omitempty"`
	FreeGPUCore  int64  `protobuf:"varint,7,opt,name=free_gpu_core,json=freeGpuCore,proto3" json:"free_gpu_core,omitempty"`
}

var _ sdk.Msg = &MsgNodeHeartbeat{}

func NewMsgNodeHeartbeat(provider sdk.AccAddress, nodeID string, cpu, mem, gpu, gpuMemMB, gpuCore int64) *MsgNodeHeartbeat {
	return &MsgNodeHeartbeat{
		Provider:     provider.String(),
		NodeID:       nodeID,
		FreeCPU:      cpu,
		FreeMem:      mem,
		FreeGPU:      gpu,
		FreeGPUMemMB: gpuMemMB,
		FreeGPUCore:  gpuCore,
	}
}

// ValidateBasic 验证消息格式合法性
func (msg *MsgNodeHeartbeat) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Provider); err != nil {
		return cerrors.Wrap(sdkerrors.ErrInvalidAddress, "invalid provider address")
	}
	if msg.NodeID == "" {
		return cerrors.Wrap(sdkerrors.ErrInvalidRequest, "node_id cannot be empty")
	}
	if msg.FreeCPU < 0 || msg.FreeMem < 0 || msg.FreeGPU < 0 {
		return cerrors.Wrap(sdkerrors.ErrInvalidRequest, "resource values cannot be negative")
	}
	return nil
}

// GetSigners 返回需要签名的地址——必须是 Provider 自己签名
func (msg *MsgNodeHeartbeat) GetSigners() []sdk.AccAddress {
	provider, err := sdk.AccAddressFromBech32(msg.Provider)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{provider}
}

func (msg *MsgNodeHeartbeat) ProtoMessage()               {}
func (msg *MsgNodeHeartbeat) Reset()                      { *msg = MsgNodeHeartbeat{} }
func (msg *MsgNodeHeartbeat) String() string              { return msg.NodeID }
func (*MsgNodeHeartbeat) Descriptor() ([]byte, []int)     { return fileDescriptorChaink8sTx, []int{0} }

// MsgNodeClaim 运营商声明自用资源（必须上链，防止隐性占用）
// 对应 white paper 6.2 节：operator self-use via NodeClaim
type MsgNodeClaim struct {
	Provider string `protobuf:"bytes,1,opt,name=provider,proto3" json:"provider,omitempty"`
	NodeID   string `protobuf:"bytes,2,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	ClaimCPU int64  `protobuf:"varint,3,opt,name=claim_cpu,json=claimCpu,proto3" json:"claim_cpu,omitempty"`
	ClaimMem int64  `protobuf:"varint,4,opt,name=claim_mem,json=claimMem,proto3" json:"claim_mem,omitempty"`
	ClaimGPU int64  `protobuf:"varint,5,opt,name=claim_gpu,json=claimGpu,proto3" json:"claim_gpu,omitempty"`
	Purpose  string `protobuf:"bytes,6,opt,name=purpose,proto3" json:"purpose,omitempty"`
}

var _ sdk.Msg = &MsgNodeClaim{}

func NewMsgNodeClaim(provider sdk.AccAddress, nodeID string, cpu, mem, gpu int64, purpose string) *MsgNodeClaim {
	return &MsgNodeClaim{
		Provider: provider.String(),
		NodeID:   nodeID,
		ClaimCPU: cpu,
		ClaimMem: mem,
		ClaimGPU: gpu,
		Purpose:  purpose,
	}
}

func (msg *MsgNodeClaim) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Provider); err != nil {
		return cerrors.Wrap(sdkerrors.ErrInvalidAddress, "invalid provider address")
	}
	if msg.NodeID == "" {
		return cerrors.Wrap(sdkerrors.ErrInvalidRequest, "node_id cannot be empty")
	}
	if msg.ClaimCPU < 0 || msg.ClaimMem < 0 || msg.ClaimGPU < 0 {
		return cerrors.Wrap(sdkerrors.ErrInvalidRequest, "claim values cannot be negative")
	}
	return nil
}

func (msg *MsgNodeClaim) GetSigners() []sdk.AccAddress {
	provider, err := sdk.AccAddressFromBech32(msg.Provider)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{provider}
}

func (msg *MsgNodeClaim) ProtoMessage()              {}
func (msg *MsgNodeClaim) Reset()                     { *msg = MsgNodeClaim{} }
func (msg *MsgNodeClaim) String() string             { return msg.NodeID }
func (*MsgNodeClaim) Descriptor() ([]byte, []int)    { return fileDescriptorChaink8sTx, []int{2} }
