// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package x

import (
	"time"

	"github.com/tenderly/net-flare/avalanchego/vms/avm/blocks"
	"github.com/tenderly/net-flare/avalanchego/vms/avm/fxs"
	"github.com/tenderly/net-flare/avalanchego/vms/nftfx"
	"github.com/tenderly/net-flare/avalanchego/vms/propertyfx"
	"github.com/tenderly/net-flare/avalanchego/vms/secp256k1fx"
)

const (
	SECP256K1FxIndex = 0
	NFTFxIndex       = 1
	PropertyFxIndex  = 2
)

// Parser to support serialization and deserialization
var Parser blocks.Parser

func init() {
	var err error
	Parser, err = blocks.NewParser(
		time.Time{},
		[]fxs.Fx{
			&secp256k1fx.Fx{},
			&nftfx.Fx{},
			&propertyfx.Fx{},
		},
	)
	if err != nil {
		panic(err)
	}
}
