// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package blocks

import (
	"time"

	"github.com/tenderly/net-flare/avalanchego/codec"
	"github.com/tenderly/net-flare/avalanchego/ids"
	"github.com/tenderly/net-flare/avalanchego/snow"
	"github.com/tenderly/net-flare/avalanchego/vms/avm/txs"
)

// Block defines the common stateless interface for all blocks
type Block interface {
	snow.ContextInitializable

	ID() ids.ID
	Parent() ids.ID
	Height() uint64
	// Timestamp that this block was created at
	Timestamp() time.Time
	MerkleRoot() ids.ID
	Bytes() []byte

	// Txs returns the transactions contained in the block
	Txs() []*txs.Tx

	// note: initialize does not assume that the transactions are initialized,
	// and initializes them itself.
	initialize(bytes []byte, cm codec.Manager) error
}
