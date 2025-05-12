// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avalanche

import (
	"github.com/tenderly/net-flare/avalanchego/snow"
	"github.com/tenderly/net-flare/avalanchego/snow/consensus/avalanche"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/avalanche/vertex"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/common"
	"github.com/tenderly/net-flare/avalanchego/snow/validators"
)

// Config wraps all the parameters needed for an avalanche engine
type Config struct {
	Ctx *snow.ConsensusContext
	common.AllGetsServer
	VM         vertex.LinearizableVM
	Manager    vertex.Manager
	Sender     common.Sender
	Validators validators.Set

	Params    avalanche.Parameters
	Consensus avalanche.Consensus
}
