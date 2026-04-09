// etcd-adapter — ChainK8s etcd 兼容适配器
//
// 暴露标准 etcd v3 gRPC 接口，内部数据来自链上调度事件。
//
// 数据流（单向，链 → Adapter → K8s）：
//
//	链上 chaink8s_bind/unbind → ChainSubscriber → Store
//	K8s etcdctl / controller → etcd v3 gRPC → Store（只读）
//
// 用法:
//
//	etcd-adapter --listen localhost:12379 \
//	             --rpc http://localhost:26657 \
//	             --grpc localhost:9190
//
// 测试:
//
//	etcdctl --endpoints=localhost:12379 get /chaink8s --prefix
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"pkg.akt.dev/node/x/chaink8s/etcdadapter"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	var (
		listen   = flag.String("listen", "localhost:12379", "etcd adapter gRPC listen address")
		nodeRPC  = flag.String("rpc", "http://localhost:26657", "Akash chain CometBFT RPC URL")
		nodeGRPC = flag.String("grpc", "localhost:9190", "Akash chain Cosmos gRPC address")
	)
	flag.Parse()

	srv, err := etcdadapter.NewServer(*listen, *nodeRPC, *nodeGRPC)
	if err != nil {
		log.Fatalf("etcd-adapter: init: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("=== etcd-adapter starting  listen=%s  rpc=%s  grpc=%s ===", *listen, *nodeRPC, *nodeGRPC)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("etcd-adapter: %v", err)
	}
	log.Println("=== etcd-adapter stopped ===")
}
