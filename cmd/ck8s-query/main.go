// ck8s-query — ChainK8s 链上状态查询工具
//
// 用法:
//
//	ck8s-query nodes                         列出所有节点资源
//	ck8s-query nodes --provider akash1...    只看指定 Provider 的节点
//	ck8s-query spot-price                    查询当前 Spot Market 价格
//	ck8s-query bound-orders                  列出已调度的 Order
//	ck8s-query serve [--port 8080]           启动 HTTP API 服务
//	  GET /resources  → 汇总可用 CPU/GPU/Mem
//	  GET /nodes      → 完整节点列表
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ck8stypes "pkg.akt.dev/node/x/chaink8s/types"
)

const defaultGRPC = "localhost:9190"

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	grpcAddr := defaultGRPC
	for i, arg := range os.Args {
		if arg == "--grpc" && i+1 < len(os.Args) {
			grpcAddr = os.Args[i+1]
		}
	}

	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc dial %s: %v", grpcAddr, err)
	}
	defer conn.Close()

	ctx := context.Background()

	switch os.Args[1] {
	case "nodes":
		provider := ""
		for i, arg := range os.Args {
			if arg == "--provider" && i+1 < len(os.Args) {
				provider = os.Args[i+1]
			}
		}
		queryNodes(ctx, conn, provider)

	case "spot-price":
		querySpotPrice(ctx, conn)

	case "bound-orders":
		queryBoundOrders(ctx, conn)

	case "serve":
		port := "8080"
		for i, arg := range os.Args {
			if arg == "--port" && i+1 < len(os.Args) {
				port = os.Args[i+1]
			}
		}
		serveHTTP(grpcAddr, port)

	default:
		usage()
		os.Exit(1)
	}
}

func queryNodes(ctx context.Context, conn *grpc.ClientConn, provider string) {
	req := &ck8stypes.QueryNodesRequest{Provider: provider}
	resp := &ck8stypes.QueryNodesResponse{}
	if err := conn.Invoke(ctx, "/chaink8s.Query/Nodes", req, resp); err != nil {
		log.Fatalf("QueryNodes: %v", err)
	}
	if len(resp.Nodes) == 0 {
		fmt.Println("（暂无在线节点）")
		return
	}
	fmt.Printf("%-52s  %-12s  %10s  %10s  %5s  %12s  %10s\n",
		"Provider", "NodeID", "CPU(milli)", "Mem(GiB)", "GPU", "GPUMem(MiB)", "Reputation")
	fmt.Println("-----------------------------------------------------------------------------------------------------------------------------")
	for _, n := range resp.Nodes {
		fmt.Printf("%-52s  %-12s  %10d  %10d  %5d  %12d  %10d\n",
			n.Provider, n.NodeID, n.FreeCPU, n.FreeMem>>30, n.FreeGPU, n.FreeGPUMemMB, n.ReputationScore)
	}
}

func querySpotPrice(ctx context.Context, conn *grpc.ClientConn) {
	resp := &ck8stypes.QuerySpotPriceResponse{}
	if err := conn.Invoke(ctx, "/chaink8s.Query/SpotPrice", &ck8stypes.QuerySpotPriceRequest{}, resp); err != nil {
		log.Fatalf("QuerySpotPrice: %v", err)
	}
	b, _ := json.MarshalIndent(map[string]interface{}{
		"price_per_cpu_milli": resp.PricePerCPUMilli,
		"free_cpu_total_milli": resp.FreeCPUTotal,
		"free_cpu_total_cores": resp.FreeCPUTotal / 1000,
		"pending_orders":      resp.PendingOrders,
	}, "", "  ")
	fmt.Println(string(b))
}

func queryBoundOrders(ctx context.Context, conn *grpc.ClientConn) {
	resp := &ck8stypes.QueryBoundOrdersResponse{}
	if err := conn.Invoke(ctx, "/chaink8s.Query/BoundOrders", &ck8stypes.QueryBoundOrdersRequest{}, resp); err != nil {
		log.Fatalf("QueryBoundOrders: %v", err)
	}
	if len(resp.Orders) == 0 {
		fmt.Println("（暂无已调度 Order）")
		return
	}
	fmt.Printf("已调度 Order 共 %d 个:\n", len(resp.Orders))
	for i, o := range resp.Orders {
		fmt.Printf("  %d. %s  provider=%s  node=%s\n", i+1, o.OrderID, o.Provider, o.NodeID)
	}
}

func serveHTTP(grpcAddr, port string) {
	newConn := func() (*grpc.ClientConn, error) {
		return grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	http.HandleFunc("/resources", func(w http.ResponseWriter, r *http.Request) {
		conn, err := newConn()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		resp := &ck8stypes.QueryNodesResponse{}
		if err := conn.Invoke(ctx, "/chaink8s.Query/Nodes", &ck8stypes.QueryNodesRequest{}, resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var totalCPU, totalMem, totalGPU, totalGPUMemMB int64
		for _, n := range resp.Nodes {
			totalCPU += n.FreeCPU
			totalMem += n.FreeMem
			totalGPU += n.FreeGPU
			totalGPUMemMB += n.FreeGPUMemMB
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"cpu_milli":   totalCPU,
			"cpu_cores":   totalCPU / 1000,
			"mem_bytes":   totalMem,
			"mem_gib":     totalMem / (1 << 30),
			"gpu":         totalGPU,
			"gpu_mem_mib": totalGPUMemMB,
			"node_count":  len(resp.Nodes),
		})
	})

	http.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		conn, err := newConn()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		resp := &ck8stypes.QueryNodesResponse{}
		if err := conn.Invoke(ctx, "/chaink8s.Query/Nodes", &ck8stypes.QueryNodesRequest{}, resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp.Nodes)
	})

	log.Printf("INF ck8s-query serve: listening on :%s  GET /resources  GET /nodes", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `ck8s-query — ChainK8s 链上状态查询

用法:
  ck8s-query nodes [--provider <addr>]   查询节点资源
  ck8s-query spot-price                  查询 Spot 价格
  ck8s-query bound-orders                查询已调度 Order
  ck8s-query serve [--port 8080]         启动 HTTP API 服务

选项:
  --grpc <addr>    gRPC 地址 (默认: localhost:9190)`)
}
