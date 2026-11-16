package pchaingateway

import (
	"fmt"

	"github.com/tenderly/net-flare/coreth/pchain"
	"github.com/tenderly/net-flare/coreth/pchain/pchainreader"
	"github.com/tenderly/net-flare/coreth/precompile/contract"
)

// Env is the per-execution data that the gateway reads from the EVM's host
// context (vm.EVM.SetHostContext). A Flare execution whose host sets no Env
// cannot answer a gateway call, so the call invalidates the execution.
type Env struct {
	reader  pchain.Reader
	network pchain.Network
	// bound is the P-chain height committed in the block header; hasBound
	// is false for a block before Helicon.
	bound    uint64
	hasBound bool
}

// NewEnv returns the gateway data of one Flare execution on chainID. The
// reader reads store and keeps decoded data in cache, which must belong to
// the same network. pChainHeight is the header's pChainHeight, nil before
// Helicon.
func NewEnv(
	chainID uint64,
	store pchainreader.Store,
	cache *pchainreader.Cache,
	pChainHeight *uint64,
) (*Env, error) {
	if store == nil || cache == nil {
		return nil, fmt.Errorf("%w: a P-chain store and cache are required", ErrEnvUnavailable)
	}
	network, ok := pchain.NetworkByChainID(chainID)
	if !ok {
		return nil, fmt.Errorf("%w: chain ID %d", ErrUnsupportedNetwork, chainID)
	}
	env := &Env{reader: pchainreader.New(store, network, cache), network: network}
	if pChainHeight != nil {
		env.bound, env.hasBound = *pChainHeight, true
	}
	return env, nil
}

// hostEnv returns the gateway Env that the host set on the executing EVM.
func hostEnv(accessibleState contract.AccessibleState) (*Env, error) {
	env, ok := accessibleState.GetHostContext().(*Env)
	if !ok || env == nil || env.reader == nil {
		return nil, fmt.Errorf("%w: no P-chain reader for this execution", ErrEnvUnavailable)
	}
	return env, nil
}

// checkBound returns a revert reason unless height is at or below the P-chain
// height committed in the block header.
func (e *Env) checkBound(height uint64) (string, bool) {
	switch {
	case !e.hasBound:
		return reasonHeightNotCommitted, false
	case height > e.bound:
		return reasonHeightAboveBound, false
	default:
		return "", true
	}
}
