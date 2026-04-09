package etcdadapter

import (
	"context"

	etcdpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// KVServer 实现 etcd v3 KVServer gRPC 接口
// K8s API Server 所有的 Put/Get/Delete/Txn 操作都经过这里
// 注意：KVServer 是只读的——链事件由 ChainSubscriber 写入 Store
type KVServer struct {
	store *Store
}

// Range 实现 etcd KVServer.Range（即 Get 操作）
// K8s API Server 用它来读取 Pod、Service 等对象
func (k *KVServer) Range(_ context.Context, req *etcdpb.RangeRequest) (*etcdpb.RangeResponse, error) {
	rev := k.store.currentRevision()

	// 单 key 查询
	if len(req.RangeEnd) == 0 {
		val, modRev, createRev, ver, found := k.store.Get(string(req.Key))
		if !found {
			return &etcdpb.RangeResponse{
				Header: responseHeader(rev),
				Count:  0,
			}, nil
		}
		return &etcdpb.RangeResponse{
			Header: responseHeader(rev),
			Kvs: []*mvccpb.KeyValue{{
				Key:            req.Key,
				Value:          val,
				ModRevision:    modRev,
				CreateRevision: createRev,
				Version:        ver,
			}},
			Count: 1,
		}, nil
	}

	// 前缀范围查询（K8s 最常用：list all pods）
	entries := k.store.GetByPrefix(string(req.Key))
	kvs := make([]*mvccpb.KeyValue, 0, len(entries))
	for _, e := range entries {
		kvs = append(kvs, &mvccpb.KeyValue{
			Key:            []byte(e.key),
			Value:          e.value,
			ModRevision:    e.modRevision,
			CreateRevision: e.createRev,
			Version:        e.version,
		})
	}
	if req.Limit > 0 && int64(len(kvs)) > req.Limit {
		kvs = kvs[:req.Limit]
	}
	return &etcdpb.RangeResponse{
		Header: responseHeader(rev),
		Kvs:    kvs,
		Count:  int64(len(kvs)),
	}, nil
}

// Put 实现 etcd KVServer.Put（仅内存写入，不向链广播）
func (k *KVServer) Put(_ context.Context, req *etcdpb.PutRequest) (*etcdpb.PutResponse, error) {
	rev := k.store.Put(string(req.Key), req.Value)
	return &etcdpb.PutResponse{Header: responseHeader(rev)}, nil
}

// DeleteRange 实现 etcd KVServer.DeleteRange
func (k *KVServer) DeleteRange(_ context.Context, req *etcdpb.DeleteRangeRequest) (*etcdpb.DeleteRangeResponse, error) {
	key := string(req.Key)
	rev, deleted := k.store.Delete(key)
	return &etcdpb.DeleteRangeResponse{
		Header:  responseHeader(rev),
		Deleted: boolToInt64(deleted),
	}, nil
}

// Txn 实现 etcd KVServer.Txn（原子事务）
// K8s 用它来做 Compare-And-Swap（乐观锁更新）
func (k *KVServer) Txn(ctx context.Context, req *etcdpb.TxnRequest) (*etcdpb.TxnResponse, error) {
	// 评估所有 compare 条件
	success := true
	for _, cmp := range req.Compare {
		if !k.evaluateCompare(cmp) {
			success = false
			break
		}
	}

	// 根据结果执行对应的操作序列
	ops := req.Success
	if !success {
		ops = req.Failure
	}

	responses := make([]*etcdpb.ResponseOp, 0, len(ops))
	var lastRev int64

	for _, op := range ops {
		switch r := op.Request.(type) {
		case *etcdpb.RequestOp_RequestPut:
			resp, err := k.Put(ctx, r.RequestPut)
			if err != nil {
				return nil, err
			}
			lastRev = resp.Header.Revision
			responses = append(responses, &etcdpb.ResponseOp{
				Response: &etcdpb.ResponseOp_ResponsePut{ResponsePut: resp},
			})
		case *etcdpb.RequestOp_RequestRange:
			resp, err := k.Range(ctx, r.RequestRange)
			if err != nil {
				return nil, err
			}
			responses = append(responses, &etcdpb.ResponseOp{
				Response: &etcdpb.ResponseOp_ResponseRange{ResponseRange: resp},
			})
		case *etcdpb.RequestOp_RequestDeleteRange:
			resp, err := k.DeleteRange(ctx, r.RequestDeleteRange)
			if err != nil {
				return nil, err
			}
			lastRev = resp.Header.Revision
			responses = append(responses, &etcdpb.ResponseOp{
				Response: &etcdpb.ResponseOp_ResponseDeleteRange{ResponseDeleteRange: resp},
			})
		}
	}

	if lastRev == 0 {
		lastRev = k.store.currentRevision()
	}

	return &etcdpb.TxnResponse{
		Header:    responseHeader(lastRev),
		Succeeded: success,
		Responses: responses,
	}, nil
}

// Compact 实现 etcd KVServer.Compact（历史压缩，stub）
func (k *KVServer) Compact(_ context.Context, req *etcdpb.CompactionRequest) (*etcdpb.CompactionResponse, error) {
	return &etcdpb.CompactionResponse{
		Header: responseHeader(k.store.currentRevision()),
	}, nil
}

// evaluateCompare 判断单个 Compare 条件是否成立
func (k *KVServer) evaluateCompare(cmp *etcdpb.Compare) bool {
	val, modRev, createRev, ver, found := k.store.Get(string(cmp.Key))
	switch cmp.Target {
	case etcdpb.Compare_VERSION:
		cur := int64(0)
		if found {
			cur = ver
		}
		return compareInt64(cur, cmp.Result, cmp.GetVersion())
	case etcdpb.Compare_CREATE:
		cur := int64(0)
		if found {
			cur = createRev
		}
		return compareInt64(cur, cmp.Result, cmp.GetCreateRevision())
	case etcdpb.Compare_MOD:
		cur := int64(0)
		if found {
			cur = modRev
		}
		return compareInt64(cur, cmp.Result, cmp.GetModRevision())
	case etcdpb.Compare_VALUE:
		if !found {
			return cmp.Result == etcdpb.Compare_EQUAL && len(cmp.GetValue()) == 0
		}
		return compareBytes(val, cmp.Result, cmp.GetValue())
	}
	return false
}

func compareInt64(cur int64, op etcdpb.Compare_CompareResult, target int64) bool {
	switch op {
	case etcdpb.Compare_EQUAL:
		return cur == target
	case etcdpb.Compare_NOT_EQUAL:
		return cur != target
	case etcdpb.Compare_LESS:
		return cur < target
	case etcdpb.Compare_GREATER:
		return cur > target
	}
	return false
}

func compareBytes(cur []byte, op etcdpb.Compare_CompareResult, target []byte) bool {
	switch op {
	case etcdpb.Compare_EQUAL:
		return string(cur) == string(target)
	case etcdpb.Compare_NOT_EQUAL:
		return string(cur) != string(target)
	}
	return false
}

func responseHeader(rev int64) *etcdpb.ResponseHeader {
	return &etcdpb.ResponseHeader{
		ClusterId: 1,
		MemberId:  1,
		Revision:  rev,
		RaftTerm:  1,
	}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Ensure KVServer implements the interface at compile time
var _ etcdpb.KVServer = (*KVServer)(nil)

// unimplemented stubs required by the interface (not used by K8s)
func (k *KVServer) mustEmbedUnimplementedKVServer() {}

// grpc status helper
func unimplemented(method string) error {
	return status.Errorf(codes.Unimplemented, "%s not implemented", method)
}
