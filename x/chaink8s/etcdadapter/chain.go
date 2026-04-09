// chain.go — ChainK8s 链事件订阅器
//
// etcd Adapter 只从链上读取调度事件，不向链写入任何数据。
// 数据流（单向）：
//
//	链上 chaink8s_bind / chaink8s_unbind 事件
//	    └─▶ ChainSubscriber.Run()
//	         └─▶ Store.Put / Store.Delete
//	              └─▶ K8s Watch 通知
package etcdadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	cmthttp "github.com/cometbft/cometbft/rpc/client/http"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ck8stypes "pkg.akt.dev/node/x/chaink8s/types"
)

// ChainSubscriber 订阅链上调度事件，将结果写入 Store。
// 它不向链发送任何数据——链是只读的事件源。
type ChainSubscriber struct {
	rpc      *cmthttp.HTTP
	grpcConn *grpc.ClientConn
	store    *Store
	nodeURL  string
	grpcAddr string

	// 定期同步节点资源快照的间隔
	syncPeriod time.Duration
}

// NewChainSubscriber 创建订阅器，连接链节点 RPC 和 gRPC。
//   - nodeRPC:  CometBFT RPC 地址，如 "http://localhost:26657"
//   - nodeGRPC: Cosmos gRPC 地址，如 "localhost:9190"
func NewChainSubscriber(nodeRPC, nodeGRPC string, store *Store) (*ChainSubscriber, error) {
	rpc, err := cmthttp.New(nodeRPC, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("chain subscriber rpc: %w", err)
	}
	conn, err := grpc.Dial(nodeGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("chain subscriber grpc: %w", err)
	}
	return &ChainSubscriber{
		rpc:        rpc,
		grpcConn:   conn,
		store:      store,
		nodeURL:    nodeRPC,
		grpcAddr:   nodeGRPC,
		syncPeriod: 30 * time.Second,
	}, nil
}

// Run 启动事件订阅和定期同步，阻塞直到 ctx 取消。
func (s *ChainSubscriber) Run(ctx context.Context) error {
	if err := s.rpc.Start(); err != nil {
		return fmt.Errorf("chain subscriber rpc start: %w", err)
	}
	defer s.rpc.Stop() //nolint:errcheck

	// 订阅新块事件（用于 bind/unbind）
	blockEvents, err := s.rpc.Subscribe(ctx, "ck8s-etcd-adapter", "tm.event='NewBlock'", 256)
	if err != nil {
		return fmt.Errorf("chain subscriber subscribe: %w", err)
	}

	// 启动时做一次全量同步
	s.syncAll(ctx)

	ticker := time.NewTicker(s.syncPeriod)
	defer ticker.Stop()

	log.Printf("INF etcd-adapter: chain subscriber running  rpc=%s  grpc=%s", s.nodeURL, s.grpcAddr)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.syncAll(ctx)
		case ev, ok := <-blockEvents:
			if !ok {
				return fmt.Errorf("chain subscriber: event channel closed")
			}
			s.handleBlock(ev)
		}
	}
}

// ── 链上事件处理 ──────────────────────────────────────────────────────────────

func (s *ChainSubscriber) handleBlock(ev ctypes.ResultEvent) {
	// chaink8s_bind → 写入 Pod 记录
	if attrs := parseChainEventAttrs(ev.Events, "chaink8s_bind"); len(attrs) > 0 {
		orderID := attrs["order_id"]
		provider := attrs["provider"]
		nodeID := attrs["node_id"]
		reqCPU    := parseAttrInt64(attrs, "req_cpu")
		reqMem    := parseAttrInt64(attrs, "req_mem")
		reqGPU    := parseAttrInt64(attrs, "req_gpu")
		reqGPUCore  := parseAttrInt64(attrs, "req_gpu_core")
		reqGPUMemMB := parseAttrInt64(attrs, "req_gpu_mem_mb")
		if orderID != "" {
			s.onBind(orderID, provider, nodeID, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB)
		}
	}

	// chaink8s_unbind → 删除 Pod 记录
	if attrs := parseChainEventAttrs(ev.Events, "chaink8s_unbind"); len(attrs) > 0 {
		orderID := attrs["order_id"]
		if orderID != "" {
			s.onUnbind(orderID)
		}
	}

	// spot_price_update → 更新价格快照
	if attrs := parseChainEventAttrs(ev.Events, "spot_price_update"); len(attrs) > 0 {
		s.onSpotPriceUpdate(attrs)
	}
}

// onBind 处理 chaink8s_bind 事件，写入 Pod 记录和绑定记录
// reqGPUCore>0 表示用户指定了分数 GPU（直接写 koordinator 资源，bypass webhook 转换）
// reqGPUCore=0 表示整块 GPU（写 nvidia.com/gpu，由 webhook 转为 gpu-core:N×100）
func (s *ChainSubscriber) onBind(orderID, provider, nodeID string, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64) {
	log.Printf("INF etcd-adapter: bind  order=%s  provider=%s  node=%s  cpu=%d  mem=%d  gpu=%d  gpu-core=%d  gpu-mem=%dMi",
		orderID, provider, nodeID, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB)

	// 写入绑定记录：/chaink8s/bindings/<orderID>
	binding := map[string]string{
		"order_id": orderID,
		"provider": provider,
		"node_id":  nodeID,
		"bound_at": time.Now().UTC().Format(time.RFC3339),
	}
	bz, _ := json.Marshal(binding)
	s.store.Put(bindingKey(orderID), bz)

	// 写入 Pod spec：/chaink8s/pods/<namespace>/<podName>
	podName := orderIDToPodName(orderID)
	podSpec := buildPodSpec(podName, "default", orderID, provider, nodeID, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB)
	podBz, _ := json.Marshal(podSpec)
	s.store.Put(podKey("default", podName), podBz)
}

// onUnbind 处理 chaink8s_unbind 事件，删除 Pod 记录和绑定记录
func (s *ChainSubscriber) onUnbind(orderID string) {
	log.Printf("INF etcd-adapter: unbind  order=%s", orderID)

	podName := orderIDToPodName(orderID)
	s.store.Delete(bindingKey(orderID))
	s.store.Delete(podKey("default", podName))
}

// onSpotPriceUpdate 更新 Spot 价格快照
func (s *ChainSubscriber) onSpotPriceUpdate(attrs map[string]string) {
	bz, _ := json.Marshal(attrs)
	s.store.Put("/chaink8s/spot-price", bz)
}

// ── 全量同步 ──────────────────────────────────────────────────────────────────

// syncAll 通过 QueryServer 全量同步节点资源和已绑定 Order。
// 在启动时和周期性调用，确保 Store 与链上状态一致。
func (s *ChainSubscriber) syncAll(ctx context.Context) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 同步节点资源
	nodesReq := &ck8stypes.QueryNodesRequest{}
	nodesResp := &ck8stypes.QueryNodesResponse{}
	if err := s.grpcConn.Invoke(ctx2, "/chaink8s.Query/Nodes", nodesReq, nodesResp); err != nil {
		log.Printf("WRN etcd-adapter: sync nodes: %v", err)
	} else {
		for _, n := range nodesResp.Nodes {
			bz, _ := json.Marshal(n)
			s.store.Put(nodeKey(n.Provider, n.NodeID), bz)
		}
		log.Printf("INF etcd-adapter: synced %d nodes", len(nodesResp.Nodes))
	}

	// 同步已绑定 Order 列表（含 provider+nodeID，用于重建 pod spec）
	ordersReq := &ck8stypes.QueryBoundOrdersRequest{}
	ordersResp := &ck8stypes.QueryBoundOrdersResponse{}
	if err := s.grpcConn.Invoke(ctx2, "/chaink8s.Query/BoundOrders", ordersReq, ordersResp); err != nil {
		log.Printf("WRN etcd-adapter: sync bound orders: %v", err)
	} else {
		for _, info := range ordersResp.Orders {
			if info == nil {
				continue
			}
			// 写入 binding 记录（如未存在）
			key := bindingKey(info.OrderID)
			if _, _, _, _, found := s.store.Get(key); !found {
				binding := map[string]string{
					"order_id": info.OrderID,
					"provider": info.Provider,
					"node_id":  info.NodeID,
				}
				bz, _ := json.Marshal(binding)
				s.store.Put(key, bz)
			}
			// 写入 pod spec（如未存在）
			podName := orderIDToPodName(info.OrderID)
			podKey := podKey("default", podName)
			if _, _, _, _, found := s.store.Get(podKey); !found && info.Provider != "" {
				podSpec := buildPodSpec(podName, "default", info.OrderID, info.Provider, info.NodeID,
					info.ReqCPU, info.ReqMem, info.ReqGPU, info.ReqGPUCore, info.ReqGPUMemMB)
				podBz, _ := json.Marshal(podSpec)
				s.store.Put(podKey, podBz)
			}
		}
		log.Printf("INF etcd-adapter: synced %d bound orders", len(ordersResp.Orders))
	}

	// 同步 Spot 价格
	priceReq := &ck8stypes.QuerySpotPriceRequest{}
	priceResp := &ck8stypes.QuerySpotPriceResponse{}
	if err := s.grpcConn.Invoke(ctx2, "/chaink8s.Query/SpotPrice", priceReq, priceResp); err != nil {
		log.Printf("WRN etcd-adapter: sync spot price: %v", err)
	} else {
		bz, _ := json.Marshal(priceResp)
		s.store.Put("/chaink8s/spot-price", bz)
	}
}

// Status 检查链节点是否在线（服务器启动前调用）
func (s *ChainSubscriber) Status(ctx context.Context) error {
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.rpc.Status(ctx2)
	return err
}

// ── Store key helpers ─────────────────────────────────────────────────────────

func nodeKey(provider, nodeID string) string {
	return "/chaink8s/nodes/" + sanitize(provider) + "/" + nodeID
}

func bindingKey(orderID string) string {
	return "/chaink8s/bindings/" + sanitize(orderID)
}

func podKey(namespace, podName string) string {
	return "/chaink8s/pods/" + namespace + "/" + podName
}

func orderIDToPodName(orderID string) string {
	parts := strings.Split(orderID, "/")
	if len(parts) >= 4 {
		return "ck8s-" + strings.Join(parts[len(parts)-3:], "-")
	}
	safe := strings.NewReplacer("/", "-", ".", "-").Replace(orderID)
	if len(safe) > 60 {
		safe = safe[len(safe)-60:]
	}
	return "ck8s-" + safe
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(s)
}

// ── Pod spec builder ──────────────────────────────────────────────────────────

// ResourceRequirements mirrors K8s ResourceRequirements for JSON storage.
type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

// ContainerSpec is a minimal K8s container spec for storage in the etcd Adapter.
type ContainerSpec struct {
	Name      string               `json:"name"`
	Image     string               `json:"image"`
	Resources ResourceRequirements `json:"resources,omitempty"`
}

// PodSpec is a minimal representation of a K8s Pod for storage in the etcd Adapter.
type PodSpec struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Containers  []ContainerSpec   `json:"containers"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Status      string            `json:"status"` // "Pending" | "Running" | "Succeeded"
}

// buildPodSpec constructs a PodSpec for the given order.
//
// GPU 资源模式（两种互斥）：
//   - 分数模式（reqGPUCore>0）：直接写 koordinator.sh/gpu-core + gpu-memory，
//     webhook 检测到已有 koordinator 资源时跳过转换，koord-scheduler 直接调度
//   - 整块模式（reqGPUCore=0，reqGPU>0）：写 nvidia.com/gpu:N，
//     webhook 将其转为 gpu-core:N×100 + gpu-memory（全显存）
func buildPodSpec(name, namespace, orderID, provider, nodeID string, reqCPU, reqMem, reqGPU, reqGPUCore, reqGPUMemMB int64) PodSpec {
	resources := ResourceRequirements{}

	if reqGPUCore > 0 {
		// 分数模式：用户指定了 gpu_core 和 gpu_memory_mb，直接写 koordinator 格式
		coreVal := fmt.Sprintf("%d", reqGPUCore)
		memVal := fmt.Sprintf("%dMi", reqGPUMemMB)
		resources.Limits = map[string]string{
			"koordinator.sh/gpu-core":   coreVal,
			"koordinator.sh/gpu-memory": memVal,
			"koordinator.sh/gpu.shared": "1",
		}
		resources.Requests = map[string]string{
			"koordinator.sh/gpu-core":   coreVal,
			"koordinator.sh/gpu-memory": memVal,
			"koordinator.sh/gpu.shared": "1",
		}
	} else if reqGPU > 0 {
		// 整块模式：写 nvidia.com/gpu，由 phantom-webhook 转换为 gpu-core:N×100
		gpuVal := fmt.Sprintf("%d", reqGPU)
		resources.Limits = map[string]string{
			"nvidia.com/gpu": gpuVal,
		}
		resources.Requests = map[string]string{
			"nvidia.com/gpu": gpuVal,
		}
	}

	container := ContainerSpec{
		Name:      "workload",
		Image:     "nginx:alpine", // placeholder; real image comes from SDL manifest
		Resources: resources,
	}

	annotations := map[string]string{
		"ck8s/order-id":    orderID,
		"ck8s/provider":    provider,
		"ck8s/node-id":     nodeID,
		"ck8s/created":     time.Now().UTC().Format(time.RFC3339),
		"ck8s/req-cpu":     fmt.Sprintf("%d", reqCPU),
		"ck8s/req-mem":     fmt.Sprintf("%d", reqMem),
		"ck8s/req-gpu":     fmt.Sprintf("%d", reqGPU),
		"ck8s/req-gpu-core":   fmt.Sprintf("%d", reqGPUCore),
		"ck8s/req-gpu-mem-mb": fmt.Sprintf("%d", reqGPUMemMB),
	}
	// 分数模式需要加 HAMi-core isolation provider label
	labels := map[string]string{
		"app":           "ck8s-workload",
		"ck8s-order":    sanitize(orderID),
		"ck8s-provider": sanitize(provider),
		"ck8s-node":     nodeID,
	}
	if reqGPUCore > 0 {
		labels["koordinator.sh/gpu-isolation-provider"] = "HAMi-core"
	}

	return PodSpec{
		Name:        name,
		Namespace:   namespace,
		Containers:  []ContainerSpec{container},
		Labels:      labels,
		Annotations: annotations,
		Status:      "Pending",
	}
}

// ── Event attr parser ─────────────────────────────────────────────────────────

// parseAttrInt64 解析事件属性中的 int64 值，解析失败返回 0
func parseAttrInt64(attrs map[string]string, key string) int64 {
	if v, ok := attrs[key]; ok {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func parseChainEventAttrs(events map[string][]string, eventType string) map[string]string {
	prefix := eventType + "."
	out := make(map[string]string)
	for k, vals := range events {
		if strings.HasPrefix(k, prefix) && len(vals) > 0 {
			out[strings.TrimPrefix(k, prefix)] = vals[0]
		}
	}
	return out
}

// ── Legacy alias (kept for backward compat) ───────────────────────────────────

// ChainClient is kept as a type alias so existing code that imports it compiles.
// Use ChainSubscriber for new code.
type ChainClient = ChainSubscriber

// NewChainClient creates a ChainSubscriber with empty gRPC (for backward compat).
func NewChainClient(nodeURL string) (*ChainSubscriber, error) {
	return NewChainSubscriber(nodeURL, "", NewStore())
}
