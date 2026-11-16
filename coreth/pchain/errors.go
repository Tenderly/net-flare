// (c) 2026, Flare Network. All rights reserved.
// See the file LICENSE for licensing terms.

package pchain

import "errors"

var (
	// ErrNotAccepted is returned when a height above the data source's last
	// accepted P-chain height is requested.
	ErrNotAccepted           = errors.New("P-chain height not accepted yet")
	ErrTxIndexOutOfRange     = errors.New("transaction index out of range")
	ErrUnsupportedTx         = errors.New("unsupported transaction type")
	ErrInputIndexOutOfRange  = errors.New("input index out of range")
	ErrOutputIndexOutOfRange = errors.New("output index out of range")
)
