package keeper

import (
	"encoding/json"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"pkg.akt.dev/node/x/chaink8s/types"
)

// IKeeper 定义链上节点资源管理接口
type IKeeper interface {
	SetNodeResource(ctx sdk.Context, provider sdk.AccAddress, res types.NodeResource)
	GetNodeResource(ctx sdk.Context, provider sdk.AccAddress, nodeID string) (types.NodeResource, bool)
	GetAllNodes(ctx sdk.Context) []types.NodeResource
	ApplyNodeClaim(ctx sdk.Context, provider sdk.AccAddress, nodeID string, cpu, mem, gpu, gpuCore int64) error
	ReleaseNodeClaim(ctx sdk.Context, provider sdk.AccAddress, nodeID string, cpu, mem, gpu, gpuCore int64)
	SlashProvider(ctx sdk.Context, provider sdk.AccAddress)
	SelectBestNode(ctx sdk.Context, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64) (types.NodeResource, bool)

	// 已绑定 Order 管理（防止同一 Order 被重复调度）
	SetOrderBound(ctx sdk.Context, orderID, provider, nodeID string, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64, image string)
	IsOrderBound(ctx sdk.Context, orderID string) bool
	GetOrderBound(ctx sdk.Context, orderID string) (types.BoundOrderInfo, bool)
	DeleteOrderBound(ctx sdk.Context, orderID string)
	GetAllBoundOrders(ctx sdk.Context) []types.BoundOrderInfo

	// Spot 价格缓存（由 EndBlock 每 N 块写入，Query 读取）
	// pricePerGPU：每整块 GPU 每块的价格（uakt），基准参考 AWS T4 实例溢价
	SetSpotPrice(ctx sdk.Context, pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal int64)
	GetSpotPrice(ctx sdk.Context) (pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal int64)
}

// Keeper 实现链上节点资源的存储与调度
type Keeper struct {
	skey storetypes.StoreKey
	cdc  codec.BinaryCodec
}

func NewKeeper(cdc codec.BinaryCodec, skey storetypes.StoreKey) IKeeper {
	return &Keeper{skey: skey, cdc: cdc}
}

// SetNodeResource 存储或更新节点资源（Provider 签名后调用）
func (k *Keeper) SetNodeResource(ctx sdk.Context, provider sdk.AccAddress, res types.NodeResource) {
	store := ctx.KVStore(k.skey)
	key := types.NodeResourceKey(provider, res.NodeID)
	bz, err := json.Marshal(res)
	if err != nil {
		panic(fmt.Sprintf("chaink8s: marshal NodeResource: %v", err))
	}
	store.Set(key, bz)
}

// GetNodeResource 读取指定节点资源
func (k *Keeper) GetNodeResource(ctx sdk.Context, provider sdk.AccAddress, nodeID string) (types.NodeResource, bool) {
	store := ctx.KVStore(k.skey)
	key := types.NodeResourceKey(provider, nodeID)
	bz := store.Get(key)
	if bz == nil {
		return types.NodeResource{}, false
	}
	var res types.NodeResource
	if err := json.Unmarshal(bz, &res); err != nil {
		return types.NodeResource{}, false
	}
	return res, true
}

// GetAllNodes 返回链上所有已注册节点（调度器使用）
func (k *Keeper) GetAllNodes(ctx sdk.Context) []types.NodeResource {
	store := ctx.KVStore(k.skey)
	iter := storetypes.KVStorePrefixIterator(store, types.NodeResourceKeyPrefix)
	defer iter.Close()

	var nodes []types.NodeResource
	for ; iter.Valid(); iter.Next() {
		var res types.NodeResource
		if err := json.Unmarshal(iter.Value(), &res); err == nil {
			nodes = append(nodes, res)
		}
	}
	return nodes
}

// ApplyNodeClaim 减少节点可用资源（调度时调用）
//
// GPU 扣减规则：
//   - gpuCore > 0：按百分比单位扣减 FreeGPUCore（分数 GPU 模式）
//     例：gpuCore=40 → FreeGPUCore -= 40
//   - gpuCore == 0 且 gpu > 0：按整卡扣减（FreeGPUCore -= gpu×100）
//
// gpuMemMB > 0 时同步扣减 FreeGPUMemMB，防止显存超分配
func (k *Keeper) ApplyNodeClaim(ctx sdk.Context, provider sdk.AccAddress, nodeID string, cpu, mem, gpu, gpuCore int64) error {
	res, found := k.GetNodeResource(ctx, provider, nodeID)
	if !found {
		return types.ErrNodeNotFound
	}

	// 计算本次需要扣减的 gpu-core 单位
	gpuCoreDeduct := gpuCore
	if gpuCoreDeduct == 0 && gpu > 0 {
		gpuCoreDeduct = gpu * 100
	}

	// 显存按整卡均分估算：每块 GPU 占用 FreeGPUMemMB / FreeGPU
	var gpuMemDeduct int64
	if gpu > 0 && res.FreeGPU > 0 && res.FreeGPUMemMB > 0 {
		gpuMemDeduct = res.FreeGPUMemMB / res.FreeGPU * gpu
	}

	if res.AvailCPU() < cpu || res.AvailMem() < mem || res.FreeGPUCore < gpuCoreDeduct {
		return types.ErrInsufficientResource
	}
	res.AllocCPU += cpu
	res.AllocMem += mem
	res.FreeGPUCore -= gpuCoreDeduct
	if gpuMemDeduct > 0 {
		res.FreeGPUMemMB -= gpuMemDeduct
		if res.FreeGPUMemMB < 0 {
			res.FreeGPUMemMB = 0
		}
	}
	res.UpdatedAt = ctx.BlockTime()
	k.SetNodeResource(ctx, provider, res)
	return nil
}

// ReleaseNodeClaim 恢复节点可用资源（Order 关闭时调用）
func (k *Keeper) ReleaseNodeClaim(ctx sdk.Context, provider sdk.AccAddress, nodeID string, cpu, mem, gpu, gpuCore int64) {
	res, found := k.GetNodeResource(ctx, provider, nodeID)
	if !found {
		return
	}
	gpuCoreRelease := gpuCore
	if gpuCoreRelease == 0 && gpu > 0 {
		gpuCoreRelease = gpu * 100
	}
	// 显存按整卡均分恢复
	var gpuMemRelease int64
	if gpu > 0 && res.FreeGPU > 0 {
		// 用总显存除以物理 GPU 数估算每块显存容量
		totalGPUMem := res.FreeGPUMemMB + (res.FreeGPU-res.FreeGPUCore/100)*0 // keep simple
		_ = totalGPUMem
		// 通过心跳里的 FreeGPUMemMB 和 FreeGPU 反推每卡显存
		// 这里保守做法：恢复到物理上限（由下次心跳更新准确值）
		gpuMemRelease = 0 // 显存会在下次心跳时由 monitor 用实际值覆盖
	}
	_ = gpuMemRelease

	res.AllocCPU -= cpu
	if res.AllocCPU < 0 {
		res.AllocCPU = 0
	}
	res.AllocMem -= mem
	if res.AllocMem < 0 {
		res.AllocMem = 0
	}
	res.FreeGPUCore += gpuCoreRelease
	// FreeGPUCore 不超过物理上限
	if maxCore := res.FreeGPU * 100; res.FreeGPUCore > maxCore {
		res.FreeGPUCore = maxCore
	}
	// FreeGPUMemMB 由下次心跳从 K8s 实际状态更新，此处不修改
	res.UpdatedAt = ctx.BlockTime()
	k.SetNodeResource(ctx, provider, res)
}

// SlashProvider 惩罚 Provider（slash），降低声誉分
// 触发条件：链上资源与实际 Pod 执行记录不符
func (k *Keeper) SlashProvider(ctx sdk.Context, provider sdk.AccAddress) {
	store := ctx.KVStore(k.skey)
	iter := storetypes.KVStorePrefixIterator(store, append(types.NodeResourceKeyPrefix, provider...))
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		var res types.NodeResource
		if err := json.Unmarshal(iter.Value(), &res); err != nil {
			continue
		}
		res.SlashCount++
		bz, err := json.Marshal(res)
		if err != nil {
			panic(fmt.Sprintf("chaink8s: marshal NodeResource in slash: %v", err))
		}
		store.Set(iter.Key(), bz)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		"slash",
		sdk.NewAttribute("provider", provider.String()),
		sdk.NewAttribute("block", fmt.Sprintf("%d", ctx.BlockHeight())),
	))
}

// SetOrderBound 标记 orderID 已被调度，记录节点和资源需求（供 etcd-adapter 重建 Pod spec）
// reqGPUCore=0 表示整块 GPU（webhook 自动转换），>0 表示用户指定的 koordinator gpu-core 百分比
// reqGPUMemMB=0 表示由 webhook 自动计算，>0 表示用户指定的 GPU 显存（MiB）
func (k *Keeper) SetOrderBound(ctx sdk.Context, orderID, provider, nodeID string, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64, image string) {
	store := ctx.KVStore(k.skey)
	bz, err := json.Marshal(types.BoundOrderInfo{
		OrderID:     orderID,
		Provider:    provider,
		NodeID:      nodeID,
		ReqCPU:      reqCPU,
		ReqMem:      reqMem,
		ReqGPU:      reqGPU,
		ReqGPUCore:  reqGPUCore,
		ReqGPUMemMB: reqGPUMemMB,
		Image:       image,
	})
	if err != nil {
		panic(fmt.Sprintf("chaink8s: marshal BoundOrderInfo: %v", err))
	}
	store.Set(types.BoundOrderKey(orderID), bz)
}

// IsOrderBound 检查 orderID 是否已被调度
// 旧格式条目（[]byte{1}）视为未绑定，触发调度器用新格式重写
func (k *Keeper) IsOrderBound(ctx sdk.Context, orderID string) bool {
	store := ctx.KVStore(k.skey)
	bz := store.Get(types.BoundOrderKey(orderID))
	if bz == nil {
		return false
	}
	// 旧格式：仅 1 字节，视为未绑定（会被调度器覆写为新格式）
	if len(bz) == 1 {
		return false
	}
	return true
}

// GetOrderBound 读取单个已绑定 Order 的调度信息
func (k *Keeper) GetOrderBound(ctx sdk.Context, orderID string) (types.BoundOrderInfo, bool) {
	store := ctx.KVStore(k.skey)
	bz := store.Get(types.BoundOrderKey(orderID))
	if bz == nil || len(bz) == 1 {
		return types.BoundOrderInfo{}, false
	}
	var info types.BoundOrderInfo
	if err := json.Unmarshal(bz, &info); err != nil {
		return types.BoundOrderInfo{}, false
	}
	return info, true
}

// DeleteOrderBound 删除 orderID 的绑定记录（Order 关闭时调用）
func (k *Keeper) DeleteOrderBound(ctx sdk.Context, orderID string) {
	store := ctx.KVStore(k.skey)
	store.Delete(types.BoundOrderKey(orderID))
}

// GetAllBoundOrders 返回所有已绑定的 Order 及其调度信息
func (k *Keeper) GetAllBoundOrders(ctx sdk.Context) []types.BoundOrderInfo {
	store := ctx.KVStore(k.skey)
	iter := storetypes.KVStorePrefixIterator(store, types.BoundOrderKeyPrefix)
	defer iter.Close()

	prefixLen := len(types.BoundOrderKeyPrefix)
	var orders []types.BoundOrderInfo
	for ; iter.Valid(); iter.Next() {
		var info types.BoundOrderInfo
		if err := json.Unmarshal(iter.Value(), &info); err != nil {
			// 兼容旧格式（仅存 []byte{1}）
			info = types.BoundOrderInfo{OrderID: string(iter.Key()[prefixLen:])}
		}
		orders = append(orders, info)
	}
	return orders
}

var spotPriceKey = []byte{0x04}

// SetSpotPrice 写入 Spot 价格快照（由 EndBlock 每 N 块调用）
// 存储格式：[pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal]
func (k *Keeper) SetSpotPrice(ctx sdk.Context, pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal int64) {
	store := ctx.KVStore(k.skey)
	bz, err := json.Marshal([5]int64{pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal})
	if err != nil {
		panic(fmt.Sprintf("chaink8s: marshal spot price: %v", err))
	}
	store.Set(spotPriceKey, bz)
}

// GetSpotPrice 读取最新 Spot 价格快照
// 通过切片长度区分新格式（5元素）和旧格式（3元素），避免 arr[3]==0 歧义
func (k *Keeper) GetSpotPrice(ctx sdk.Context) (pricePerCPUMilli, freeCPUTotal, pendingOrders, pricePerGPU, freeGPUTotal int64) {
	store := ctx.KVStore(k.skey)
	bz := store.Get(spotPriceKey)
	if bz == nil {
		return 4, 0, 0, 32000, 0 // 默认值：CPU=4 uakt/milli，GPU=32000 uakt/GPU
	}
	var arr []int64
	if err := json.Unmarshal(bz, &arr); err != nil {
		return 4, 0, 0, 32000, 0
	}
	if len(arr) >= 5 {
		return arr[0], arr[1], arr[2], arr[3], arr[4]
	}
	// 兼容旧格式（3元素），GPU 价格使用默认值
	if len(arr) >= 3 {
		return arr[0], arr[1], arr[2], 32000, 0
	}
	return 4, 0, 0, 32000, 0
}

// SelectBestNode 链上调度器核心：选出最适合的节点
// 算法：ALPHA * resourceFitScore + BETA * reputationScore
// 对应 white paper Section 5.2
//
// reqGPUCore > 0：按 gpu-core 百分比单位过滤（分数 GPU 模式）
// reqGPUCore == 0 且 reqGPU > 0：按整卡过滤（reqGPU×100 gpu-core 单位）
// reqGPUMemMB > 0：过滤显存不足的节点（node.FreeGPUMemMB==0 时跳过过滤，兼容旧心跳）
func (k *Keeper) SelectBestNode(ctx sdk.Context, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64) (types.NodeResource, bool) {
	const (
		alpha = 6 // resource fit 权重 60%
		beta  = 4 // reputation 权重 40%
	)

	// 统一换算为 gpu-core 单位进行过滤
	reqGPUCoreUnits := reqGPUCore
	if reqGPUCoreUnits == 0 && reqGPU > 0 {
		reqGPUCoreUnits = reqGPU * 100
	}

	nodes := k.GetAllNodes(ctx)
	bestScore := int64(-1)
	var bestNode types.NodeResource

	deadline := ctx.BlockTime().Add(-types.HeartbeatTimeout)

	for _, node := range nodes {
		if node.UpdatedAt.Before(deadline) {
			continue
		}
		if node.AvailCPU() < reqCPU || node.AvailMem() < reqMem {
			continue
		}
		if reqGPUCoreUnits > 0 && node.FreeGPUCore < reqGPUCoreUnits {
			continue
		}
		if reqGPUMemMB > 0 && node.FreeGPUMemMB > 0 && node.FreeGPUMemMB < reqGPUMemMB {
			continue
		}
		fitScore := resourceFitScore(node, reqCPU, reqMem, reqGPUCoreUnits)
		repScore := node.ReputationScore()
		score := alpha*fitScore + beta*repScore

		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}

	return bestNode, bestScore >= 0
}

// resourceFitScore 计算资源适配分（0-100）
// 剩余资源刚好满足需求时分数最高，过剩太多反而分数低（bin-packing 优化）
func resourceFitScore(node types.NodeResource, reqCPU, reqMem, reqGPUCore int64) int64 {
	availCPU := node.AvailCPU()
	availMem := node.AvailMem()
	if availCPU == 0 || availMem == 0 {
		return 0
	}
	cpuUtil := reqCPU * 100 / availCPU
	memUtil := reqMem * 100 / availMem
	score := (cpuUtil + memUtil) / 2

	// GPU 利用率也纳入评分
	if reqGPUCore > 0 && node.FreeGPUCore > 0 {
		gpuUtil := reqGPUCore * 100 / node.FreeGPUCore
		score = (score + gpuUtil) / 2
	}

	if score > 100 {
		score = 100
	}
	return score
}
