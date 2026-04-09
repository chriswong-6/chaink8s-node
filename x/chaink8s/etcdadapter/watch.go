package etcdadapter

import (
	"io"

	etcdpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
)

// WatchServer 实现 etcd v3 WatchServer gRPC 接口
// kubelet 和 controller-manager 通过 Watch 监听 Pod/Node 状态变化
type WatchServer struct {
	store *Store
}

// Watch 处理双向流 Watch RPC
// 客户端发送 WatchCreateRequest 注册监听，
// 服务端持续推送 WatchResponse 事件
func (w *WatchServer) Watch(stream etcdpb.Watch_WatchServer) error {
	ctx := stream.Context()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch r := req.RequestUnion.(type) {
		case *etcdpb.WatchRequest_CreateRequest:
			cr := r.CreateRequest
			prefix := string(cr.Key)
			watchID, evCh, cancel := w.store.Watch(prefix)

			// 确认 watch 已建立
			if err := stream.Send(&etcdpb.WatchResponse{
				Header:  responseHeader(w.store.currentRevision()),
				WatchId: watchID,
				Created: true,
			}); err != nil {
				cancel()
				return err
			}

			// 在 goroutine 里持续推送事件
			go func() {
				defer cancel()
				for {
					select {
					case <-ctx.Done():
						return
					case ev, ok := <-evCh:
						if !ok {
							return
						}
						if err := stream.Send(buildWatchResponse(watchID, ev)); err != nil {
							return
						}
					}
				}
			}()

		case *etcdpb.WatchRequest_CancelRequest:
			// 客户端取消 watch，goroutine 会在 ctx.Done() 时自动退出
			_ = r.CancelRequest
		}
	}
}

// buildWatchResponse 把内部 watchEvent 转为 etcd WatchResponse
func buildWatchResponse(watchID int64, ev watchEvent) *etcdpb.WatchResponse {
	var evType mvccpb.Event_EventType
	if ev.typ == eventDelete {
		evType = mvccpb.DELETE
	} else {
		evType = mvccpb.PUT
	}

	event := &mvccpb.Event{
		Type: evType,
		Kv: &mvccpb.KeyValue{
			Key:         []byte(ev.key),
			Value:       ev.value,
			ModRevision: ev.revision,
		},
	}
	if ev.prevVal != nil {
		event.PrevKv = &mvccpb.KeyValue{
			Key:   []byte(ev.key),
			Value: ev.prevVal,
		}
	}

	return &etcdpb.WatchResponse{
		Header:  responseHeader(ev.revision),
		WatchId: watchID,
		Events:  []*mvccpb.Event{event},
	}
}

var _ etcdpb.WatchServer = (*WatchServer)(nil)
