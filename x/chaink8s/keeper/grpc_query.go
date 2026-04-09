package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	mv1beta5 "pkg.akt.dev/go/node/market/v1beta5"
	mkeeper "pkg.akt.dev/node/x/market/keeper"
	"pkg.akt.dev/node/x/chaink8s/types"
)

// queryServer 实现 types.QueryServer 接口
type queryServer struct {
	keeper  IKeeper
	mkeeper mkeeper.IKeeper
}

func NewQueryServer(k IKeeper, mk mkeeper.IKeeper) types.QueryServer {
	return &queryServer{keeper: k, mkeeper: mk}
}

// Nodes 返回链上所有（或指定 Provider 的）节点资源
func (q *queryServer) Nodes(goCtx context.Context, req *types.QueryNodesRequest) (*types.QueryNodesResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	all := q.keeper.GetAllNodes(ctx)
	resp := &types.QueryNodesResponse{}

	for _, n := range all {
		// 如果指定了 Provider，只返回该 Provider 的节点
		if req.Provider != "" && n.Provider != req.Provider {
			continue
		}
		resp.Nodes = append(resp.Nodes, &types.NodeInfo{
			Provider:        n.Provider,
			NodeID:          n.NodeID,
			FreeCPU:         n.AvailCPU(),
			FreeMem:         n.AvailMem(),
			TotalCPU:        n.TotalCPU,
			TotalMem:        n.TotalMem,
			AllocCPU:        n.AllocCPU,
			AllocMem:        n.AllocMem,
			FreeGPU:         n.FreeGPU,
			FreeGPUCore:     n.FreeGPUCore,
			FreeGPUMemMB:    n.FreeGPUMemMB,
			ReputationScore: n.ReputationScore(),
			SlashCount:      n.SlashCount,
		})
	}
	return resp, nil
}

// SpotPrice 返回当前 Spot Market 价格快照（CPU + GPU 独立定价）
func (q *queryServer) SpotPrice(goCtx context.Context, _ *types.QuerySpotPriceRequest) (*types.QuerySpotPriceResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	price, freeCPU, pending, gpuPrice, freeGPU := q.keeper.GetSpotPrice(ctx)

	// 如果缓存尚未初始化（链刚启动，还未到 spotPriceUpdateInterval），实时计算一次
	if freeCPU == 0 {
		nodes := q.keeper.GetAllNodes(ctx)
		for _, n := range nodes {
			freeCPU += n.AvailCPU()
			freeGPU += n.FreeGPU
		}
		q.mkeeper.WithOrders(ctx, func(order mv1beta5.Order) bool {
			if order.State == mv1beta5.OrderOpen {
				pending++
			}
			return false
		})
	}

	return &types.QuerySpotPriceResponse{
		PricePerCPUMilli: price,
		FreeCPUTotal:     freeCPU,
		PendingOrders:    pending,
		PricePerGPU:      gpuPrice,
		FreeGPUTotal:     freeGPU,
	}, nil
}

// BoundOrders 返回所有已被调度的 Order 及其调度信息
func (q *queryServer) BoundOrders(goCtx context.Context, _ *types.QueryBoundOrdersRequest) (*types.QueryBoundOrdersResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	orders := q.keeper.GetAllBoundOrders(ctx)
	resp := &types.QueryBoundOrdersResponse{}
	for i := range orders {
		resp.Orders = append(resp.Orders, &orders[i])
	}
	return resp, nil
}
