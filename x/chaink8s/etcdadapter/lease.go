package etcdadapter

import (
	"context"
	"io"

	etcdpb "go.etcd.io/etcd/api/v3/etcdserverpb"
)

// LeaseServer 实现 etcd v3 LeaseServer
// K8s 用 Lease 做 leader election 和节点心跳
// 当前实现为内存版本（不持久化），满足 K8s 基本运行需求
type LeaseServer struct {
	etcdpb.UnimplementedLeaseServer
}

// LeaseGrant 申请一个 Lease（TTL 租约）
func (l *LeaseServer) LeaseGrant(_ context.Context, req *etcdpb.LeaseGrantRequest) (*etcdpb.LeaseGrantResponse, error) {
	return &etcdpb.LeaseGrantResponse{
		Header: responseHeader(1),
		ID:     req.ID,
		TTL:    req.TTL,
	}, nil
}

// LeaseRevoke 撤销 Lease
func (l *LeaseServer) LeaseRevoke(_ context.Context, req *etcdpb.LeaseRevokeRequest) (*etcdpb.LeaseRevokeResponse, error) {
	return &etcdpb.LeaseRevokeResponse{
		Header: responseHeader(1),
	}, nil
}

// LeaseKeepAlive 续约（双向流）
func (l *LeaseServer) LeaseKeepAlive(stream etcdpb.Lease_LeaseKeepAliveServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&etcdpb.LeaseKeepAliveResponse{
			Header: responseHeader(1),
			ID:     req.ID,
			TTL:    60,
		}); err != nil {
			return err
		}
	}
}

// LeaseTimeToLive 查询 Lease 剩余时间
func (l *LeaseServer) LeaseTimeToLive(_ context.Context, req *etcdpb.LeaseTimeToLiveRequest) (*etcdpb.LeaseTimeToLiveResponse, error) {
	return &etcdpb.LeaseTimeToLiveResponse{
		Header: responseHeader(1),
		ID:     req.ID,
		TTL:    60,
	}, nil
}

var _ etcdpb.LeaseServer = (*LeaseServer)(nil)
