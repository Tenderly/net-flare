// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"github.com/tenderly/net-flare/avalanchego/snow"
	"github.com/tenderly/net-flare/avalanchego/snow/uptime"
	"github.com/tenderly/net-flare/avalanchego/utils"
	"github.com/tenderly/net-flare/avalanchego/utils/timer/mockable"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/config"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/fx"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/reward"
	"github.com/tenderly/net-flare/avalanchego/vms/platformvm/utxo"
)

type Backend struct {
	Config       *config.Config
	Ctx          *snow.Context
	Clk          *mockable.Clock
	Fx           fx.Fx
	FlowChecker  utxo.Verifier
	Uptimes      uptime.Manager
	Rewards      reward.Calculator
	Bootstrapped *utils.AtomicBool
}
