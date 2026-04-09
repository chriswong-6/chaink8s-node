package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"pkg.akt.dev/node/x/chaink8s/keeper"
	"pkg.akt.dev/node/x/chaink8s/types"
)

// setupKeeper 创建一个带内存 KV store 的测试 Keeper
func setupKeeper(t testing.TB) (sdk.Context, keeper.IKeeper) {
	t.Helper()

	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	ms.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, ms.LoadLatestVersion())

	ctx := sdk.NewContext(ms, cmtproto.Header{}, false, log.NewNopLogger()).
		WithBlockTime(time.Now())

	ir := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(ir)

	k := keeper.NewKeeper(cdc, storeKey)
	return ctx, k
}

// --- NodeHeartbeat / SetNodeResource ---

func TestSetAndGetNodeResource(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	res := types.NodeResource{
		NodeID:    "node-1",
		Provider:  provider.String(),
		TotalCPU:  8000,
		TotalMem:  16 * 1024 * 1024 * 1024,
		FreeGPU:   2,
		UpdatedAt: ctx.BlockTime(),
	}
	k.SetNodeResource(ctx, provider, res)

	got, found := k.GetNodeResource(ctx, provider, "node-1")
	require.True(t, found)
	require.Equal(t, res.NodeID, got.NodeID)
	require.Equal(t, res.TotalCPU, got.TotalCPU)
	require.Equal(t, res.TotalMem, got.TotalMem)
	require.Equal(t, res.FreeGPU, got.FreeGPU)
}

func TestGetNodeResourceNotFound(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	_, found := k.GetNodeResource(ctx, provider, "nonexistent")
	require.False(t, found)
}

func TestGetAllNodes(t *testing.T) {
	ctx, k := setupKeeper(t)
	p1 := sdk.AccAddress([]byte("provider1"))
	p2 := sdk.AccAddress([]byte("provider2"))

	k.SetNodeResource(ctx, p1, types.NodeResource{NodeID: "node-a", Provider: p1.String(), TotalCPU: 4000, TotalMem: 8000, UpdatedAt: ctx.BlockTime()})
	k.SetNodeResource(ctx, p1, types.NodeResource{NodeID: "node-b", Provider: p1.String(), TotalCPU: 4000, TotalMem: 8000, UpdatedAt: ctx.BlockTime()})
	k.SetNodeResource(ctx, p2, types.NodeResource{NodeID: "node-c", Provider: p2.String(), TotalCPU: 4000, TotalMem: 8000, UpdatedAt: ctx.BlockTime()})

	all := k.GetAllNodes(ctx)
	require.Len(t, all, 3)
}

// --- NodeClaim (运营商自用声明) ---

func TestApplyNodeClaimSuccess(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:      "node-1",
		Provider:    provider.String(),
		TotalCPU:    8000,
		TotalMem:    16000,
		FreeGPU:     4,
		FreeGPUCore: 400, // 4 GPU × 100
		UpdatedAt:   ctx.BlockTime(),
	})

	// 声明使用 2 核 CPU、4000 内存、1 整块 GPU（gpuCore=0 → 扣 100 gpu-core 单位）
	err := k.ApplyNodeClaim(ctx, provider, "node-1", 2000, 4000, 1, 0)
	require.NoError(t, err)

	// 验证 AllocCPU/AllocMem 增加，AvailCPU/AvailMem 减少
	updated, found := k.GetNodeResource(ctx, provider, "node-1")
	require.True(t, found)
	require.Equal(t, int64(2000), updated.AllocCPU)
	require.Equal(t, int64(4000), updated.AllocMem)
	require.Equal(t, int64(6000), updated.AvailCPU())
	require.Equal(t, int64(12000), updated.AvailMem())
	require.Equal(t, int64(300), updated.FreeGPUCore) // 4 GPU×100 - 1×100 = 300
}

func TestApplyNodeClaimInsufficientResource(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:    "node-1",
		Provider:  provider.String(),
		TotalCPU:  1000, // 只有 1 核
		TotalMem:  2000,
		FreeGPU:   0,
		UpdatedAt: ctx.BlockTime(),
	})

	err := k.ApplyNodeClaim(ctx, provider, "node-1", 5000, 2000, 0, 0)
	require.ErrorIs(t, err, types.ErrInsufficientResource)
}

func TestApplyNodeClaimNodeNotFound(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	err := k.ApplyNodeClaim(ctx, provider, "nonexistent", 1000, 1000, 0, 0)
	require.ErrorIs(t, err, types.ErrNodeNotFound)
}

func TestReleaseNodeClaim(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:      "node-1",
		Provider:    provider.String(),
		TotalCPU:    4000,
		TotalMem:    8000,
		FreeGPU:     2,
		FreeGPUCore: 200,
		UpdatedAt:   ctx.BlockTime(),
	})

	require.NoError(t, k.ApplyNodeClaim(ctx, provider, "node-1", 2000, 4000, 1, 0))
	k.ReleaseNodeClaim(ctx, provider, "node-1", 2000, 4000, 1, 0)

	restored, _ := k.GetNodeResource(ctx, provider, "node-1")
	require.Equal(t, int64(0), restored.AllocCPU)
	require.Equal(t, int64(0), restored.AllocMem)
	require.Equal(t, int64(4000), restored.AvailCPU())
	require.Equal(t, int64(8000), restored.AvailMem())
	require.Equal(t, int64(200), restored.FreeGPUCore) // 2 GPU × 100
}

// --- Slash ---

func TestSlashProvider(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:     "node-1",
		Provider:   provider.String(),
		TotalCPU:   4000,
		SlashCount: 0,
		UpdatedAt:  ctx.BlockTime(),
	})

	k.SlashProvider(ctx, provider)

	updated, found := k.GetNodeResource(ctx, provider, "node-1")
	require.True(t, found)
	require.Equal(t, int64(1), updated.SlashCount)
}

func TestReputationScoreDecreaseAfterSlash(t *testing.T) {
	node := &types.NodeResource{Stake: 1000, SlashCount: 0}
	require.Equal(t, int64(100), node.ReputationScore())

	node.SlashCount = 5
	require.Equal(t, int64(50), node.ReputationScore())

	node.SlashCount = 10
	require.Equal(t, int64(0), node.ReputationScore())

	node.SlashCount = 20 // 超过上限
	require.Equal(t, int64(0), node.ReputationScore())
}

// --- 链上调度器 SelectBestNode ---

func TestSelectBestNodeBasic(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:      "node-1",
		Provider:    provider.String(),
		TotalCPU:    8000,
		TotalMem:    16000,
		FreeGPU:     2,
		FreeGPUCore: 200,
		Stake:       1000,
		UpdatedAt:   ctx.BlockTime(),
	})

	best, found := k.SelectBestNode(ctx, 2000, 4000, 1, 0, 0)
	require.True(t, found)
	require.Equal(t, "node-1", best.NodeID)
}

func TestSelectBestNodeInsufficientResource(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:    "node-1",
		Provider:  provider.String(),
		TotalCPU:  1000, // 只有 1 核
		TotalMem:  2000,
		FreeGPU:   0,
		Stake:     1000,
		UpdatedAt: ctx.BlockTime(),
	})

	_, found := k.SelectBestNode(ctx, 4000, 2000, 0, 0, 0)
	require.False(t, found)
}

func TestSelectBestNodePicksHigherReputation(t *testing.T) {
	ctx, k := setupKeeper(t)
	p1 := sdk.AccAddress([]byte("provider111"))
	p2 := sdk.AccAddress([]byte("provider222"))

	// node-low: 资源充足但被 slash 过 5 次（声誉低）
	k.SetNodeResource(ctx, p1, types.NodeResource{
		NodeID:     "node-low",
		Provider:   p1.String(),
		TotalCPU:   8000,
		TotalMem:   16000,
		FreeGPU:    0,
		Stake:      1000,
		SlashCount: 5, // 声誉分 = 50
		UpdatedAt:  ctx.BlockTime(),
	})
	// node-high: 同样资源，从未被 slash（声誉高）
	k.SetNodeResource(ctx, p2, types.NodeResource{
		NodeID:     "node-high",
		Provider:   p2.String(),
		TotalCPU:   8000,
		TotalMem:   16000,
		FreeGPU:    0,
		Stake:      1000,
		SlashCount: 0, // 声誉分 = 100
		UpdatedAt:  ctx.BlockTime(),
	})

	best, found := k.SelectBestNode(ctx, 2000, 4000, 0, 0, 0)
	require.True(t, found)
	require.Equal(t, "node-high", best.NodeID, "调度器应优先选择声誉更高的节点")
}

func TestSelectBestNodeSkipsOffline(t *testing.T) {
	ctx, k := setupKeeper(t)
	provider := sdk.AccAddress([]byte("provider1"))

	// 上次心跳是 10 分钟前 → 超时离线
	oldTime := ctx.BlockTime().Add(-10 * time.Minute)
	k.SetNodeResource(ctx, provider, types.NodeResource{
		NodeID:    "node-offline",
		Provider:  provider.String(),
		TotalCPU:  8000,
		TotalMem:  16000,
		FreeGPU:   0,
		Stake:     1000,
		UpdatedAt: oldTime,
	})

	_, found := k.SelectBestNode(ctx, 1000, 1000, 0, 0, 0)
	require.False(t, found, "超时节点不应被调度")
}

// --- OrderBound with GPU resources ---

func TestSetOrderBoundWithGPU(t *testing.T) {
	ctx, k := setupKeeper(t)

	k.SetOrderBound(ctx, "order-1", "provider-abc", "node-xyz", 2000, 4096, 2, 0, 0, "")
	require.True(t, k.IsOrderBound(ctx, "order-1"))

	orders := k.GetAllBoundOrders(ctx)
	require.Len(t, orders, 1)
	require.Equal(t, "order-1", orders[0].OrderID)
	require.Equal(t, "provider-abc", orders[0].Provider)
	require.Equal(t, "node-xyz", orders[0].NodeID)
	require.Equal(t, int64(2000), orders[0].ReqCPU)
	require.Equal(t, int64(4096), orders[0].ReqMem)
	require.Equal(t, int64(2), orders[0].ReqGPU)
}

func TestSetOrderBoundNoGPU(t *testing.T) {
	ctx, k := setupKeeper(t)

	k.SetOrderBound(ctx, "order-cpu-only", "provider-abc", "node-xyz", 4000, 8192, 0, 0, 0, "")
	require.True(t, k.IsOrderBound(ctx, "order-cpu-only"))

	orders := k.GetAllBoundOrders(ctx)
	require.Len(t, orders, 1)
	require.Equal(t, int64(0), orders[0].ReqGPU, "CPU-only order should have ReqGPU=0")
}

func TestDeleteOrderBoundRemovesGPUInfo(t *testing.T) {
	ctx, k := setupKeeper(t)

	k.SetOrderBound(ctx, "order-del", "provider-abc", "node-xyz", 2000, 4096, 1, 0, 0, "")
	require.True(t, k.IsOrderBound(ctx, "order-del"))

	k.DeleteOrderBound(ctx, "order-del")
	require.False(t, k.IsOrderBound(ctx, "order-del"))
	require.Empty(t, k.GetAllBoundOrders(ctx))
}
