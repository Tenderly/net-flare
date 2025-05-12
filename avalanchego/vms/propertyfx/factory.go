// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package propertyfx

import (
	"github.com/tenderly/net-flare/avalanchego/ids"
	"github.com/tenderly/net-flare/avalanchego/utils/logging"
	"github.com/tenderly/net-flare/avalanchego/vms"
)

var (
	_ vms.Factory = (*Factory)(nil)

	// ID that this Fx uses when labeled
	ID = ids.ID{'p', 'r', 'o', 'p', 'e', 'r', 't', 'y', 'f', 'x'}
)

type Factory struct{}

func (*Factory) New(logging.Logger) (interface{}, error) {
	return &Fx{}, nil
}
