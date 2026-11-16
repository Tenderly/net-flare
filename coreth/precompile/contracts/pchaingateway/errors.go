// (c) 2026, Flare Network. All rights reserved.
// See the file LICENSE for licensing terms.

package pchaingateway

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tenderly/net-flare/coreth/pchain"
	"github.com/tenderly/net-flare/coreth/precompile/contract"
	"github.com/tenderly/net-flare/coreth/vmerrs"
)

// Revert reasons for caller errors. They are returned as a Solidity
// Error(string) revert so that calling contracts can handle them.
const (
	reasonHeightNotCommitted    = "PChainGateway: P-chain height not committed"
	reasonHeightAboveBound      = "PChainGateway: height above committed P-chain height"
	reasonInvalidInput          = "PChainGateway: invalid input"
	reasonPageSizeExceedsCap    = "PChainGateway: page size exceeds cap"
	reasonTxIndexOutOfRange     = "PChainGateway: tx index out of range"
	reasonUnsupportedTx         = "PChainGateway: unsupported tx type"
	reasonInputIndexOutOfRange  = "PChainGateway: input index out of range"
	reasonOutputIndexOutOfRange = "PChainGateway: output index out of range"
)

var (
	// ErrEnvUnavailable is an execution whose host set no gateway [Env].
	ErrEnvUnavailable = errors.New("P-chain gateway environment unavailable")
	// ErrUnsupportedNetwork is a chain ID without Flare P-chain constants.
	ErrUnsupportedNetwork = errors.New("no Flare P-chain constants for this chain")

	errorSelector = crypto.Keccak256([]byte("Error(string)"))[:4]
)

// revertReason encodes reason as the data of a Solidity Error(string)
// revert: the selector, then the ABI encoding of one string (offset, length,
// and the bytes padded to 32).
func revertReason(reason string) []byte {
	padded := (len(reason) + 31) / 32 * 32
	data := make([]byte, 4+32+32+padded)
	copy(data, errorSelector)
	data[4+31] = 0x20
	binary.BigEndian.PutUint64(data[4+32+24:], uint64(len(reason)))
	copy(data[4+64:], reason)
	return data
}

// revert returns a Solidity revert with reason, keeping remainingGas: the EVM
// keeps the return data and the gas on vmerrs.ErrExecutionReverted.
func revert(remainingGas uint64, reason string) ([]byte, uint64, error) {
	return revertReason(reason), remainingGas, vmerrs.ErrExecutionReverted
}

// invalidate reports a node-local failure. It marks the whole execution as
// invalid, so the host gets the error instead of an execution result; it
// never becomes a Solidity-visible revert that a daemon could swallow. The
// error wraps err, so a host can tell a height that its source has not
// accepted yet (pchain.ErrNotAccepted) from other failures.
func invalidate(accessibleState contract.AccessibleState, err error) ([]byte, uint64, error) {
	err = fmt.Errorf("P-chain gateway: %w", err)
	accessibleState.InvalidateExecution(err)
	return nil, 0, err
}

// readerError classifies an error returned by the P-chain reader: caller
// errors map to a revert reason, everything else (including a height the
// source has not accepted yet) is node-local.
func readerError(err error) (reason string, callerError bool) {
	switch {
	case errors.Is(err, pchain.ErrTxIndexOutOfRange):
		return reasonTxIndexOutOfRange, true
	case errors.Is(err, pchain.ErrUnsupportedTx):
		return reasonUnsupportedTx, true
	case errors.Is(err, pchain.ErrInputIndexOutOfRange):
		return reasonInputIndexOutOfRange, true
	case errors.Is(err, pchain.ErrOutputIndexOutOfRange):
		return reasonOutputIndexOutOfRange, true
	default:
		return "", false
	}
}
