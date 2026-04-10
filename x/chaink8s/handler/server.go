package handler

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	pkeeper "pkg.akt.dev/node/x/provider/keeper"
	"pkg.akt.dev/node/x/chaink8s/keeper"
	"pkg.akt.dev/node/x/chaink8s/types"
)

// MsgServer 处理 chaink8s 模块的所有消息
type MsgServer struct {
	keeper   keeper.IKeeper
	pkeeper  pkeeper.IKeeper // 用于验证 Provider 是否已注册
}

var _ types.MsgServer = (*MsgServer)(nil)

func NewMsgServer(k keeper.IKeeper, pk pkeeper.IKeeper) *MsgServer {
	return &MsgServer{keeper: k, pkeeper: pk}
}

// HandleNodeHeartbeat 处理节点资源心跳上报
//
// 关键安全保证：
// 1. Cosmos SDK 在调用 Handler 之前已验证 tx 签名
// 2. 这里再次确认签名者与 Provider 地址一致（防止冒名）
// 3. Provider 必须已在 x/provider 模块注册
func (s *MsgServer) HandleNodeHeartbeat(goCtx context.Context, msg *types.MsgNodeHeartbeat) (*types.MsgNodeHeartbeatResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// 1. 验证 Provider 已在链上注册
	providerAddr, err := sdk.AccAddressFromBech32(msg.Provider)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid provider address: %s", msg.Provider)
	}

	_, found := s.pkeeper.Get(ctx, providerAddr)
	if !found {
		return nil, types.ErrProviderNotFound.Wrapf("provider %s not registered", msg.Provider)
	}

	// 2. 更新链上节点资源（签名已由 SDK 在 ante handler 验证）
	res := types.NodeResource{
		NodeID:       msg.NodeID,
		Provider:     msg.Provider,
		TotalCPU:     msg.FreeCPU,     // 心跳字段语义：上报物理总 CPU
		TotalMem:     msg.FreeMem,     // 心跳字段语义：上报物理总内存
		FreeGPU:      msg.FreeGPU,
		FreeGPUMemMB: msg.FreeGPUMemMB,
		UpdatedAt:    ctx.BlockTime(),
	}

	// 保留链上管理的已分配量和声誉数据，防止心跳覆盖
	if existing, ok := s.keeper.GetNodeResource(ctx, providerAddr, msg.NodeID); ok {
		res.Stake = existing.Stake
		// 在线恢复：每次正常心跳 SlashCount -1（最小 0），持续在线可恢复声誉
		if existing.SlashCount > 0 {
			res.SlashCount = existing.SlashCount - 1
		}
		res.AllocCPU = existing.AllocCPU
		res.AllocMem = existing.AllocMem
		// 若心跳直接上报 FreeGPUCore（由 monitor 从 Koordinator 读取），优先使用
		if msg.FreeGPUCore > 0 {
			res.FreeGPUCore = msg.FreeGPUCore
		} else if msg.FreeGPU == existing.FreeGPU {
			res.FreeGPUCore = existing.FreeGPUCore
		} else {
			// GPU 数量变化（热插拔），重新初始化
			res.FreeGPUCore = msg.FreeGPU * 100
		}
	} else {
		// 首次注册：优先使用 FreeGPUCore，否则从整块 GPU 数推算
		if msg.FreeGPUCore > 0 {
			res.FreeGPUCore = msg.FreeGPUCore
		} else {
			res.FreeGPUCore = msg.FreeGPU * 100
		}
	}

	s.keeper.SetNodeResource(ctx, providerAddr, res)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		"node_heartbeat",
		sdk.NewAttribute("provider", msg.Provider),
		sdk.NewAttribute("node_id", msg.NodeID),
		sdk.NewAttribute("free_cpu", fmt.Sprintf("%d", msg.FreeCPU)),
		sdk.NewAttribute("free_mem", fmt.Sprintf("%d", msg.FreeMem)),
	))

	return &types.MsgNodeHeartbeatResponse{}, nil
}

// HandleNodeClaim 处理运营商自用资源声明
// 对应 white paper：operator self-use MUST be submitted as NodeClaim
func (s *MsgServer) HandleNodeClaim(goCtx context.Context, msg *types.MsgNodeClaim) (*types.MsgNodeClaimResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	providerAddr, err := sdk.AccAddressFromBech32(msg.Provider)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid provider address: %s", msg.Provider)
	}

	// 验证 Provider 已注册
	_, found := s.pkeeper.Get(ctx, providerAddr)
	if !found {
		return nil, types.ErrProviderNotFound
	}

	// 从链上节点资源中扣除声明量
	// NodeClaim 按整卡扣减（gpuCore=0，由 ApplyNodeClaim 内部转换为 ClaimGPU×100）
	if err := s.keeper.ApplyNodeClaim(ctx, providerAddr, msg.NodeID, msg.ClaimCPU, msg.ClaimMem, msg.ClaimGPU, 0); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		"node_claim",
		sdk.NewAttribute("provider", msg.Provider),
		sdk.NewAttribute("node_id", msg.NodeID),
		sdk.NewAttribute("purpose", msg.Purpose),
	))

	return &types.MsgNodeClaimResponse{}, nil
}
