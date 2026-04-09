package types

import cerrors "cosmossdk.io/errors"

var (
	ErrNodeNotFound         = cerrors.Register(ModuleName, 1, "node not found")
	ErrProviderNotFound     = cerrors.Register(ModuleName, 2, "provider not found")
	ErrInsufficientResource = cerrors.Register(ModuleName, 3, "insufficient node resources for claim")
	ErrUnauthorized         = cerrors.Register(ModuleName, 4, "sender is not the registered provider")
	ErrStaleHeartbeat       = cerrors.Register(ModuleName, 5, "heartbeat interval too short")
)
