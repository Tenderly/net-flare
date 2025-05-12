// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowman

import (
	"github.com/tenderly/net-flare/avalanchego/snow/consensus/snowball"
	"github.com/tenderly/net-flare/avalanchego/snow/consensus/snowman"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/common"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/snowman/block"
	"github.com/tenderly/net-flare/avalanchego/snow/validators"
)

func DefaultConfigs() Config {
	commonCfg := common.DefaultConfigTest()
	return Config{
		Ctx:        commonCfg.Ctx,
		Sender:     commonCfg.Sender,
		Validators: validators.NewSet(),
		VM:         &block.TestVM{},
		Params: snowball.Parameters{
			K:                       1,
			Alpha:                   1,
			BetaVirtuous:            1,
			BetaRogue:               2,
			ConcurrentRepolls:       1,
			OptimalProcessing:       100,
			MaxOutstandingItems:     1,
			MaxItemProcessingTime:   1,
			MixedQueryNumPushNonVdr: 1,
		},
		Consensus: &snowman.Topological{},
	}
}
