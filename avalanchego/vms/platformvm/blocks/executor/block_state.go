// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"time"

	"github.com/tenderly/net-flare/avalanchego/chains/atomic"
	"github.com/tenderly/net-flare/avalanchego/ids"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/blocks"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/state"
)

type standardBlockState struct {
	onAcceptFunc func()
	inputs       ids.Set
}

type proposalBlockState struct {
	initiallyPreferCommit bool
	onCommitState         state.Diff
	onAbortState          state.Diff
}

// The state of a block.
// Note that not all fields will be set for a given block.
type blockState struct {
	standardBlockState
	proposalBlockState
	statelessBlock blocks.Block
	onAcceptState  state.Diff

	timestamp      time.Time
	atomicRequests map[ids.ID]*atomic.Requests
}
