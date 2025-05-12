// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"github.com/tenderly/net-flare/avalanchego/codec"
	"github.com/tenderly/net-flare/avalanchego/codec/linearcodec"
	"github.com/tenderly/net-flare/avalanchego/utils/units"
	"github.com/tenderly/net-flare/avalanchego/utils/wrappers"
)

const (
	codecVersion   = 0
	maxMessageSize = 512 * units.KiB
	maxSliceLen    = maxMessageSize
)

// Codec does serialization and deserialization
var c codec.Manager

func init() {
	c = codec.NewManager(maxMessageSize)
	lc := linearcodec.NewCustomMaxLength(maxSliceLen)

	errs := wrappers.Errs{}
	errs.Add(
		lc.RegisterType(&Tx{}),
		c.RegisterCodec(codecVersion, lc),
	)
	if errs.Errored() {
		panic(errs.Err)
	}
}
