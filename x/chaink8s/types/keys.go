package types

import sdk "github.com/cosmos/cosmos-sdk/types"

var (
	// NodeResourceKeyPrefix KV store 前缀：存储节点资源
	// key = NodeResourceKeyPrefix + provider_address + "/" + node_id
	NodeResourceKeyPrefix = []byte{0x01}

	// NodeClaimKeyPrefix KV store 前缀：存储运营商自用声明
	NodeClaimKeyPrefix = []byte{0x02}

	// BoundOrderKeyPrefix KV store 前缀：记录已绑定的 Order，防止重复调度
	// key = BoundOrderKeyPrefix + orderID
	BoundOrderKeyPrefix = []byte{0x03}
)

// NodeResourceKey 生成节点资源的 store key
func NodeResourceKey(provider sdk.AccAddress, nodeID string) []byte {
	key := make([]byte, 0, len(NodeResourceKeyPrefix)+len(provider)+1+len(nodeID))
	key = append(key, NodeResourceKeyPrefix...)
	key = append(key, provider...)
	key = append(key, '/')
	key = append(key, []byte(nodeID)...)
	return key
}

// NodeClaimKey 生成自用声明的 store key
func NodeClaimKey(provider sdk.AccAddress, nodeID string) []byte {
	key := make([]byte, 0, len(NodeClaimKeyPrefix)+len(provider)+1+len(nodeID))
	key = append(key, NodeClaimKeyPrefix...)
	key = append(key, provider...)
	key = append(key, '/')
	key = append(key, []byte(nodeID)...)
	return key
}

// BoundOrderKey 生成已绑定 Order 的 store key
func BoundOrderKey(orderID string) []byte {
	key := make([]byte, 0, len(BoundOrderKeyPrefix)+len(orderID))
	key = append(key, BoundOrderKeyPrefix...)
	key = append(key, []byte(orderID)...)
	return key
}
