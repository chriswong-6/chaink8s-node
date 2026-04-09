package etcdadapter

import (
	"context"
	"fmt"
	"net"

	etcdpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Server 是 etcd Adapter 的主服务
// 对外暴露标准 etcd v3 gRPC 接口，内部数据来自链上事件
type Server struct {
	store      *Store
	subscriber *ChainSubscriber
	grpc       *grpc.Server
	listen     string
}

// NewServer 创建 etcd Adapter 服务
//   - listen:   gRPC 监听地址，如 "localhost:2379"
//   - nodeRPC:  链节点 CometBFT RPC，如 "http://localhost:26657"
//   - nodeGRPC: 链节点 Cosmos gRPC，如 "localhost:9190"
func NewServer(listen, nodeRPC, nodeGRPC string) (*Server, error) {
	store := NewStore()

	subscriber, err := NewChainSubscriber(nodeRPC, nodeGRPC, store)
	if err != nil {
		return nil, fmt.Errorf("chain subscriber: %w", err)
	}

	grpcServer := grpc.NewServer()
	kv := &KVServer{store: store}
	ws := &WatchServer{store: store}
	ls := &LeaseServer{}

	etcdpb.RegisterKVServer(grpcServer, kv)
	etcdpb.RegisterWatchServer(grpcServer, ws)
	etcdpb.RegisterLeaseServer(grpcServer, ls)

	// 健康检查（K8s 会探测）
	healthSvc := health.NewServer()
	healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSvc)

	// gRPC 反射（方便 etcdctl / grpcurl 调试）
	reflection.Register(grpcServer)

	return &Server{
		store:      store,
		subscriber: subscriber,
		grpc:       grpcServer,
		listen:     listen,
	}, nil
}

// Start 启动 gRPC 服务和链事件订阅，阻塞直到 ctx 取消
func (s *Server) Start(ctx context.Context) error {
	// 验证链节点连接
	if err := s.subscriber.Status(ctx); err != nil {
		return fmt.Errorf("chain node unreachable: %w", err)
	}

	lis, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listen, err)
	}

	// 启动链事件订阅（后台）
	go func() {
		if err := s.subscriber.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Printf("ERR etcd-adapter: chain subscriber: %v\n", err)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.grpc.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		s.grpc.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

// Store 暴露内部 store（供测试使用）
func (s *Server) Store() *Store {
	return s.store
}
