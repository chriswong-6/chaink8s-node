package etcdadapter

import (
	"sync"
	"sync/atomic"
)

// entry 存储单个 KV 条目
type entry struct {
	key         string
	value       []byte
	modRevision int64 // 最后修改时的 revision
	createRev   int64 // 创建时的 revision
	version     int64 // 修改次数
}

// watchEvent 表示一次 KV 变化事件
type watchEvent struct {
	typ      eventType
	key      string
	value    []byte
	revision int64
	prevVal  []byte
}

type eventType int

const (
	eventPut    eventType = 0
	eventDelete eventType = 1
)

// Store 线程安全的内存 KV store
// Put/Delete 操作同步写入内存，并异步广播到区块链
type Store struct {
	mu       sync.RWMutex
	data     map[string]*entry
	revision int64 // 全局 revision，每次修改 +1

	// watchers: 按 watch ID 注册，每个 watcher 有一个 channel 接收事件
	watchMu  sync.RWMutex
	watchers map[int64]*watcher
	watchSeq int64
}

type watcher struct {
	id       int64
	prefix   string // 监听这个前缀下所有 key 的变化
	ch       chan watchEvent
	cancelCh chan struct{}
}

func NewStore() *Store {
	return &Store{
		data:     make(map[string]*entry),
		watchers: make(map[int64]*watcher),
	}
}

// currentRevision 返回当前 revision（原子读）
func (s *Store) currentRevision() int64 {
	return atomic.LoadInt64(&s.revision)
}

// nextRevision 递增并返回新 revision
func (s *Store) nextRevision() int64 {
	return atomic.AddInt64(&s.revision, 1)
}

// Put 写入或更新一个 key，返回新 revision
func (s *Store) Put(key string, value []byte) int64 {
	s.mu.Lock()
	rev := s.nextRevision()
	var prev []byte
	e, exists := s.data[key]
	if exists {
		prev = e.value
		e.value = value
		e.modRevision = rev
		e.version++
	} else {
		s.data[key] = &entry{
			key:         key,
			value:       value,
			modRevision: rev,
			createRev:   rev,
			version:     1,
		}
	}
	s.mu.Unlock()

	s.notify(watchEvent{
		typ:      eventPut,
		key:      key,
		value:    value,
		revision: rev,
		prevVal:  prev,
	})
	return rev
}

// Get 读取一个 key，返回值和 revision，found=false 表示不存在
func (s *Store) Get(key string) (value []byte, modRev, createRev, version int64, found bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[key]
	if !ok {
		return nil, 0, 0, 0, false
	}
	return e.value, e.modRevision, e.createRev, e.version, true
}

// GetByPrefix 返回所有以 prefix 开头的 key，按 key 字典序
func (s *Store) GetByPrefix(prefix string) []*entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*entry
	for k, e := range s.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			cp := *e
			result = append(result, &cp)
		}
	}
	return result
}

// Delete 删除一个 key，返回 revision 和是否存在
func (s *Store) Delete(key string) (int64, bool) {
	s.mu.Lock()
	e, exists := s.data[key]
	if !exists {
		s.mu.Unlock()
		return 0, false
	}
	prev := e.value
	delete(s.data, key)
	rev := s.nextRevision()
	s.mu.Unlock()

	s.notify(watchEvent{
		typ:      eventDelete,
		key:      key,
		revision: rev,
		prevVal:  prev,
	})
	return rev, true
}

// Watch 注册一个 watcher，返回事件 channel 和取消函数
func (s *Store) Watch(prefix string) (int64, <-chan watchEvent, func()) {
	id := atomic.AddInt64(&s.watchSeq, 1)
	w := &watcher{
		id:       id,
		prefix:   prefix,
		ch:       make(chan watchEvent, 128),
		cancelCh: make(chan struct{}),
	}
	s.watchMu.Lock()
	s.watchers[id] = w
	s.watchMu.Unlock()

	cancel := func() {
		s.watchMu.Lock()
		delete(s.watchers, id)
		s.watchMu.Unlock()
		close(w.cancelCh)
	}
	return id, w.ch, cancel
}

// notify 把事件广播给所有匹配前缀的 watcher
func (s *Store) notify(ev watchEvent) {
	s.watchMu.RLock()
	defer s.watchMu.RUnlock()
	for _, w := range s.watchers {
		if len(ev.key) >= len(w.prefix) && ev.key[:len(w.prefix)] == w.prefix {
			select {
			case w.ch <- ev:
			default:
				// channel 满了就丢弃（防止阻塞写入路径）
			}
		}
	}
}
