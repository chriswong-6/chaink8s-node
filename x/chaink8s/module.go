package chaink8s

import (
	"context"
	"encoding/json"
	"fmt"

	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	dtypesv1beta4 "pkg.akt.dev/go/node/deployment/v1beta4"
	mv1beta5 "pkg.akt.dev/go/node/market/v1beta5"
	mkeeper "pkg.akt.dev/node/x/market/keeper"
	"pkg.akt.dev/node/x/chaink8s/handler"
	"pkg.akt.dev/node/x/chaink8s/keeper"
	"pkg.akt.dev/node/x/chaink8s/types"
	pkeeper "pkg.akt.dev/node/x/provider/keeper"
)

var (
	_ module.AppModuleBasic      = AppModuleBasic{}
	_ appmodule.AppModule        = AppModule{}
	_ module.HasConsensusVersion = AppModule{}
	_ module.HasServices         = AppModule{}
)

const (
	// 每隔多少块更新一次 Spot Market 价格
	spotPriceUpdateInterval = int64(10)
	// 基础价格：每 milli-core 每块 4 uakt
	basePricePerCPUMilli = int64(4)
	// 基础 GPU 价格：每整块 GPU 每块 32000 uakt
	// 参考 AWS/Azure GPU 实例溢价（以 T4 为基准，约 8x vCPU 单价）：
	//   T4  16GB : ~$0.356/hr GPU溢价 ≈  8x vCPU → basePricePerCPUMilli×1000×8  = 32,000
	//   A10G 24GB: ~$0.836/hr         ≈ 20x vCPU → basePricePerCPUMilli×1000×20 = 80,000
	//   V100 16GB: ~$2.676/hr         ≈ 63x vCPU → basePricePerCPUMilli×1000×63 = 252,000
	//   A100 40GB: ~$4.10/hr          ≈ 96x vCPU → basePricePerCPUMilli×1000×96 = 384,000
	basePricePerGPU = int64(32000)
	// 价格最高倍数上限（CPU 和 GPU 共用）
	maxPriceMultiplier = int64(5)
	// heartbeatTimeout 引用 types 包统一常量，避免与 SelectBestNode 阈值分叉
	heartbeatTimeout = types.HeartbeatTimeout
)

// ---------- AppModuleBasic ----------

type AppModuleBasic struct {
	cdc codec.Codec
}

func (AppModuleBasic) Name() string { return types.ModuleName }

func (AppModuleBasic) RegisterLegacyAminoCodec(_ *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(reg cdctypes.InterfaceRegistry) {
	reg.RegisterImplementations((*sdk.Msg)(nil),
		&types.MsgNodeHeartbeat{},
		&types.MsgNodeClaim{},
	)
}

func (AppModuleBasic) DefaultGenesis(_ codec.JSONCodec) json.RawMessage { return []byte("{}") }

func (AppModuleBasic) ValidateGenesis(_ codec.JSONCodec, _ interface{}, _ json.RawMessage) error {
	return nil
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(_ client.Context, _ *runtime.ServeMux) {}

// ---------- AppModule ----------

type AppModule struct {
	AppModuleBasic
	keeper  keeper.IKeeper
	mkeeper mkeeper.IKeeper
	pkeeper pkeeper.IKeeper
	server  *handler.MsgServer
}

func NewAppModule(cdc codec.Codec, k keeper.IKeeper, mk mkeeper.IKeeper, pk pkeeper.IKeeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{cdc: cdc},
		keeper:         k,
		mkeeper:        mk,
		pkeeper:        pk,
		server:         handler.NewMsgServer(k, pk),
	}
}

func (AppModule) ConsensusVersion() uint64 { return 1 }

// RegisterServices 注册 chaink8s MsgServer 和 QueryServer 到 gRPC 路由
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), am.server)
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServer(am.keeper, am.mkeeper))
}

func (am AppModule) IsOnePerModuleType() {}
func (am AppModule) IsAppModule()        {}

// ---------- EndBlock ----------

// EndBlock 每块结束时执行三件事：
//  1. 链上调度器：为待调度 Order 自动选最优节点
//  2. Spot Market：每 10 块动态更新价格
//  3. Slash 检测：对心跳超时节点执行 slash
func (am AppModule) EndBlock(goCtx context.Context) error {
	ctx := sdk.UnwrapSDKContext(goCtx)

	am.runScheduler(ctx)

	if ctx.BlockHeight()%spotPriceUpdateInterval == 0 {
		am.updateSpotPrice(ctx)
	}

	am.detectAndSlashOfflineNodes(ctx)

	return nil
}

// runScheduler 链上调度器
// 遍历全网节点资源，为每笔 open Order 选出最优节点，直接建立 Lease
// 对应 white paper Section 5：On-Chain Scheduler
func (am AppModule) runScheduler(ctx sdk.Context) {
	am.mkeeper.WithOrders(ctx, func(order mv1beta5.Order) bool {
		orderID := order.ID.String()

		// 跳过非 open 状态的 Order
		if order.State != mv1beta5.OrderOpen {
			// 如果该 Order 曾经绑定过但现在已关闭，归还资源、清理绑定记录并发出 unbind 事件
			if bound, ok := am.keeper.GetOrderBound(ctx, orderID); ok {
				providerAddr, err := sdk.AccAddressFromBech32(bound.Provider)
				if err == nil {
					am.keeper.ReleaseNodeClaim(ctx, providerAddr, bound.NodeID,
						bound.ReqCPU, bound.ReqMem, bound.ReqGPU, bound.ReqGPUCore)
				}
				am.keeper.DeleteOrderBound(ctx, orderID)
				ctx.EventManager().EmitEvent(sdk.NewEvent(
					"chaink8s_unbind",
					sdk.NewAttribute("order_id", orderID),
					sdk.NewAttribute("reason", "order_closed"),
				))
			}
			return false // continue
		}

		// 跳过已调度的 Order（幂等保证）
		if am.keeper.IsOrderBound(ctx, orderID) {
			return false // continue
		}

		reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB, image := extractResources(order.Spec)
		am.scheduleOrder(ctx, orderID, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB, image)
		return false // continue to next order
	})
}

// extractResources 从 GroupSpec 中提取资源需求和调度参数
//
// placement attributes 支持以下自定义键：
//
//	gpu_core:      koordinator gpu-core 百分比（1-100）
//	gpu_memory_mb: GPU 显存 MiB
//	image:         容器镜像，如 "pytorch/pytorch:2.3.0-cuda12.1-cudnn8-runtime"
//
// SDL 示例：
//
//	profiles:
//	  placement:
//	    dc1:
//	      attributes:
//	        - key: image
//	          value: "pytorch/pytorch:2.3.0-cuda12.1-cudnn8-runtime"
//	        - key: gpu_core
//	          value: "40"
//	        - key: gpu_memory_mb
//	          value: "4096"
func extractResources(spec dtypesv1beta4.GroupSpec) (cpu, mem, gpu, gpuCore, gpuMemMB int64, image string) {
	for _, unit := range spec.GetResourceUnits() {
		count := int64(unit.Count)
		if count == 0 {
			count = 1
		}
		if unit.CPU != nil {
			cpu += unit.CPU.Units.Val.Int64() * count
		}
		if unit.Memory != nil {
			mem += unit.Memory.Quantity.Val.Int64() * count
		}
		if unit.GPU != nil {
			gpu += unit.GPU.Units.Val.Int64() * count
		}
	}

	for _, attr := range spec.Requirements.Attributes {
		switch attr.Key {
		case "gpu_core":
			fmt.Sscanf(attr.Value, "%d", &gpuCore)
		case "gpu_memory_mb":
			fmt.Sscanf(attr.Value, "%d", &gpuMemMB)
		case "image":
			image = attr.Value
		}
	}
	return
}

// scheduleOrder 为单个 Order 执行调度并建立 Lease
func (am AppModule) scheduleOrder(ctx sdk.Context, orderID string, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64, image string) {
	bestNode, found := am.keeper.SelectBestNode(ctx, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB)
	if !found {
		return // 无满足条件节点，等下一块
	}

	providerAddr, err := sdk.AccAddressFromBech32(bestNode.Provider)
	if err != nil {
		return
	}

	// 从节点资源扣除分配量
	// reqGPUCore > 0：按百分比扣减 FreeGPUCore（如 40% → -40）
	// reqGPUCore == 0：按整卡扣减（1 GPU → -100）
	if err := am.keeper.ApplyNodeClaim(ctx, providerAddr, bestNode.NodeID, reqCPU, reqMem, reqGPU, reqGPUCore); err != nil {
		ctx.Logger().Error("chaink8s: ApplyNodeClaim failed, skipping order",
			"order", orderID, "node", bestNode.NodeID, "err", err)
		return
	}

	// 标记该 Order 已绑定（记录 provider+nodeID+资源需求+镜像，供 Monitor 重建 Pod spec）
	am.keeper.SetOrderBound(ctx, orderID, bestNode.Provider, bestNode.NodeID, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB, image)

	// 发出调度事件，携带资源需求和镜像供 Monitor 构建 Pod spec
	ctx.EventManager().EmitEvent(sdk.NewEvent(
		"chaink8s_bind",
		sdk.NewAttribute("order_id", orderID),
		sdk.NewAttribute("provider", bestNode.Provider),
		sdk.NewAttribute("node_id", bestNode.NodeID),
		sdk.NewAttribute("req_cpu", fmt.Sprintf("%d", reqCPU)),
		sdk.NewAttribute("req_mem", fmt.Sprintf("%d", reqMem)),
		sdk.NewAttribute("req_gpu", fmt.Sprintf("%d", reqGPU)),
		sdk.NewAttribute("req_gpu_core", fmt.Sprintf("%d", reqGPUCore)),
		sdk.NewAttribute("req_gpu_mem_mb", fmt.Sprintf("%d", reqGPUMemMB)),
		sdk.NewAttribute("image", image),
	))
}

// updateSpotPrice Spot Market 动态定价（CPU + GPU 独立定价）
//
// CPU 价格 = basePricePerCPUMilli × cpuMultiplier
// GPU 价格 = basePricePerGPU × gpuMultiplier
//
// multiplier 由各自资源的供需比决定，最高 maxPriceMultiplier 倍。
// GPU 基准价格参考 AWS T4 实例溢价（约 8x vCPU 单价），详见常量注释。
//
// 对应 white paper Section 7.2
func (am AppModule) updateSpotPrice(ctx sdk.Context) {
	nodes := am.keeper.GetAllNodes(ctx)

	var freeCPUTotal, freeGPUTotal int64
	for _, n := range nodes {
		freeCPUTotal += n.AvailCPU()
		freeGPUTotal += n.FreeGPU
	}

	// 单次遍历 open 状态 Order，分别统计 CPU 和 GPU 需求
	pendingCount := int64(0)
	gpuPendingCount := int64(0)
	am.mkeeper.WithOrders(ctx, func(order mv1beta5.Order) bool {
		if order.State == mv1beta5.OrderOpen {
			pendingCount++
			_, _, reqGPU, _, _, _ := extractResources(order.Spec)
			if reqGPU > 0 {
				gpuPendingCount++
			}
		}
		return false // continue to next order
	})

	// ── CPU 定价 ──────────────────────────────────────────────────────────────
	cpuSupply := freeCPUTotal / 1000 // milli → 核
	if cpuSupply <= 0 {
		cpuSupply = 1
	}
	cpuMultiplier := int64(1) + (pendingCount / cpuSupply)
	if cpuMultiplier > maxPriceMultiplier {
		cpuMultiplier = maxPriceMultiplier
	}
	newCPUPrice := basePricePerCPUMilli * cpuMultiplier

	// ── GPU 定价 ──────────────────────────────────────────────────────────────
	// 基准：T4 等级 GPU，参考 AWS g4dn 实例 GPU 溢价（约 8x vCPU 单价）
	gpuSupply := freeGPUTotal
	if gpuSupply <= 0 {
		gpuSupply = 1
	}
	gpuMultiplier := int64(1) + (gpuPendingCount / gpuSupply)
	if gpuMultiplier > maxPriceMultiplier {
		gpuMultiplier = maxPriceMultiplier
	}
	newGPUPrice := basePricePerGPU * gpuMultiplier

	// 读取当前 market Params，更新 BidMinDeposit 金额后写回（以 CPU 价格为基准）
	params := am.mkeeper.GetParams(ctx)
	params.BidMinDeposit.Amount = sdkmath.NewInt(newCPUPrice)
	if err := am.mkeeper.SetParams(ctx, params); err != nil {
		ctx.Logger().Error("chaink8s: failed to update spot price", "err", err)
		return
	}

	// 写入 KV 缓存，供 Query Server 读取
	am.keeper.SetSpotPrice(ctx, newCPUPrice, freeCPUTotal, pendingCount, newGPUPrice, freeGPUTotal)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		"spot_price_update",
		sdk.NewAttribute("price_per_cpu_milli", sdkmath.NewInt(newCPUPrice).String()),
		sdk.NewAttribute("price_per_gpu", sdkmath.NewInt(newGPUPrice).String()),
		sdk.NewAttribute("free_cpu_total", sdkmath.NewInt(freeCPUTotal).String()),
		sdk.NewAttribute("free_gpu_total", sdkmath.NewInt(freeGPUTotal).String()),
		sdk.NewAttribute("pending_orders", fmt.Sprintf("%d", pendingCount)),
		sdk.NewAttribute("gpu_pending_orders", fmt.Sprintf("%d", gpuPendingCount)),
	))
}

// detectAndSlashOfflineNodes 检测心跳超时节点并 slash
// 超过 heartbeatTimeout 未上报资源的节点视为离线
// 对应 white paper Section 8.2：Signed heartbeats and slashing
func (am AppModule) detectAndSlashOfflineNodes(ctx sdk.Context) {
	deadline := ctx.BlockTime().Add(-heartbeatTimeout)
	nodes := am.keeper.GetAllNodes(ctx)

	for _, node := range nodes {
		if node.UpdatedAt.Before(deadline) {
			providerAddr, err := sdk.AccAddressFromBech32(node.Provider)
			if err != nil {
				continue
			}
			am.keeper.SlashProvider(ctx, providerAddr)

			ctx.EventManager().EmitEvent(sdk.NewEvent(
				"node_slash",
				sdk.NewAttribute("provider", node.Provider),
				sdk.NewAttribute("node_id", node.NodeID),
				sdk.NewAttribute("reason", "heartbeat_timeout"),
			))
		}
	}
}
