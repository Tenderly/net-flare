// (c) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tenderly/net-flare/coreth/core/rawdb"
	"github.com/tenderly/net-flare/coreth/core/state"
	"github.com/tenderly/net-flare/coreth/params"
	"github.com/tenderly/net-flare/coreth/precompile/contract"
	"github.com/tenderly/net-flare/coreth/precompile/modules"
	"github.com/tenderly/net-flare/coreth/precompile/precompileconfig"
	"github.com/tenderly/net-flare/coreth/vmerrs"
)

func TestIsProhibited(t *testing.T) {
	// reserved addresses
	assert.True(t, IsProhibited(common.HexToAddress("0x0100000000000000000000000000000000000000")))
	assert.True(t, IsProhibited(common.HexToAddress("0x0100000000000000000000000000000000000010")))
	assert.True(t, IsProhibited(common.HexToAddress("0x01000000000000000000000000000000000000f0")))
	assert.True(t, IsProhibited(common.HexToAddress("0x01000000000000000000000000000000000000ff")))
	assert.True(t, IsProhibited(common.HexToAddress("0x0200000000000000000000000000000000000000")))
	assert.True(t, IsProhibited(common.HexToAddress("0x0200000000000000000000000000000000000010")))
	assert.True(t, IsProhibited(common.HexToAddress("0x02000000000000000000000000000000000000f0")))
	assert.True(t, IsProhibited(common.HexToAddress("0x02000000000000000000000000000000000000ff")))
	// reserved addresses (custom precompiles)
	assert.True(t, IsProhibited(common.HexToAddress("0x0300000000000000000000000000000000000000")))
	assert.True(t, IsProhibited(common.HexToAddress("0x0300000000000000000000000000000000000010")))
	assert.True(t, IsProhibited(common.HexToAddress("0x03000000000000000000000000000000000000f0")))
	assert.True(t, IsProhibited(common.HexToAddress("0x03000000000000000000000000000000000000ff")))

	// allowed for use
	assert.False(t, IsProhibited(common.HexToAddress("0x00000000000000000000000000000000000000ff")))
	assert.False(t, IsProhibited(common.HexToAddress("0x00ffffffffffffffffffffffffffffffffffffff")))
	assert.False(t, IsProhibited(common.HexToAddress("0x0100000000000000000000000000000000000100")))
	assert.False(t, IsProhibited(common.HexToAddress("0x0200000000000000000000000000000000000100")))
	assert.False(t, IsProhibited(common.HexToAddress("0x0300000000000000000000000000000000000100")))
}

type stubPrecompile struct{ out []byte }

func (s stubPrecompile) RequiredGas([]byte) uint64 { return 0 }

func (s stubPrecompile) Run([]byte) ([]byte, error) { return s.out, nil }

func TestOverridePrecompiles(t *testing.T) {
	newEVM := func() *EVM {
		return NewEVM(BlockContext{BlockNumber: big.NewInt(0)}, TxContext{}, nil, params.TestFlareChainConfig, Config{})
	}
	ordinary := common.HexToAddress("0x00000000000000000000000000000000000000f1")
	ecrecover := common.BytesToAddress([]byte{1})
	sha256 := common.BytesToAddress([]byte{2})
	ripemd := common.BytesToAddress([]byte{3})

	evm := newEVM()
	evm.OverridePrecompiles(map[common.Address]PrecompiledContract{
		ordinary:  stubPrecompile{out: []byte("ordinary")},
		ecrecover: stubPrecompile{out: []byte("ecrecover")},
		sha256:    nil,
	})

	tests := []struct {
		name string
		addr common.Address
		want []byte
	}{
		{name: "an address without a precompile gets the override", addr: ordinary, want: []byte("ordinary")},
		{name: "the override replaces the fork's precompile", addr: ecrecover, want: []byte("ecrecover")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := evm.precompile(tt.addr)
			require.True(t, ok)
			got, _, err := p.Run(nil, common.Address{}, tt.addr, nil, 0, false)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("a nil override removes the fork's precompile", func(t *testing.T) {
		_, ok := evm.precompile(sha256)
		assert.False(t, ok)
	})

	t.Run("an address without an override keeps the fork's precompile", func(t *testing.T) {
		want, wantOK := newEVM().precompile(ripemd)
		got, ok := evm.precompile(ripemd)
		require.True(t, wantOK)
		assert.True(t, ok)
		assert.Same(t, want, got)
	})
}

// hostContextPrecompile returns its EVM's host context, or invalidates the
// execution with invalidate when that is set.
type hostContextPrecompile struct{ invalidate error }

func (p hostContextPrecompile) Run(accessibleState contract.AccessibleState, _ common.Address, _ common.Address, _ []byte, suppliedGas uint64, _ bool) ([]byte, uint64, error) {
	if p.invalidate != nil {
		accessibleState.InvalidateExecution(p.invalidate)
		return nil, 0, p.invalidate
	}
	value, _ := accessibleState.GetHostContext().(string)
	return []byte(value), suppliedGas, nil
}

func TestHostContextAndExecutionInvalidation(t *testing.T) {
	precompileAddr := common.HexToAddress("0x03000000000000000000000000000000000000f1")
	caller := AccountRef(common.HexToAddress("0x00000000000000000000000000000000000000c1"))
	newEVM := func(t *testing.T, precompile contract.StatefulPrecompiledContract) *EVM {
		statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		require.NoError(t, err)
		blockCtx := BlockContext{BlockNumber: big.NewInt(0), CanTransfer: CanTransfer, Transfer: Transfer}
		evm := NewEVM(blockCtx, TxContext{}, statedb, params.TestFlareChainConfig, Config{})
		evm.precompileOverrides = map[common.Address]contract.StatefulPrecompiledContract{precompileAddr: precompile}
		return evm
	}

	t.Run("a precompile reads the host context of its EVM", func(t *testing.T) {
		evm := newEVM(t, hostContextPrecompile{})
		evm.SetHostContext("host value")
		ret, _, err := evm.Call(caller, precompileAddr, nil, 100_000, new(uint256.Int))
		require.NoError(t, err)
		assert.Equal(t, []byte("host value"), ret)
		assert.NoError(t, evm.ExecutionInvalidation())
	})

	t.Run("an EVM without a host context gives nil", func(t *testing.T) {
		evm := newEVM(t, hostContextPrecompile{})
		assert.Nil(t, evm.GetHostContext())
		ret, _, err := evm.Call(caller, precompileAddr, nil, 100_000, new(uint256.Int))
		require.NoError(t, err)
		assert.Empty(t, ret)
	})

	t.Run("the first invalidation stays on the EVM", func(t *testing.T) {
		errFirst := errors.New("first")
		evm := newEVM(t, hostContextPrecompile{invalidate: errFirst})
		_, _, err := evm.Call(caller, precompileAddr, nil, 100_000, new(uint256.Int))
		require.ErrorIs(t, err, errFirst)
		evm.InvalidateExecution(errors.New("second"))
		evm.InvalidateExecution(nil)
		assert.ErrorIs(t, evm.ExecutionInvalidation(), errFirst)
	})
}

func TestModuleRejectsDelegateCallAndCallCodeFromGranite(t *testing.T) {
	moduleAddr := common.HexToAddress("0x03000000000000000000000000000000000000f2")
	if _, ok := modules.GetPrecompileModuleByAddress(moduleAddr); !ok {
		require.NoError(t, modules.RegisterModule(modules.Module{
			ConfigKey: "testCallTypeModuleConfig",
			Address:   moduleAddr,
			Contract:  hostContextPrecompile{},
		}))
	}
	caller := AccountRef(common.HexToAddress("0x00000000000000000000000000000000000000c1"))
	call := func(evm *EVM) ([]byte, uint64, error) {
		return evm.Call(caller, moduleAddr, nil, 100_000, new(uint256.Int))
	}
	callCode := func(evm *EVM) ([]byte, uint64, error) {
		return evm.CallCode(caller, moduleAddr, nil, 100_000, new(uint256.Int))
	}
	delegateCall := func(evm *EVM) ([]byte, uint64, error) {
		return evm.DelegateCall(caller, moduleAddr, nil, 100_000)
	}

	tests := []struct {
		name    string
		granite bool
		call    func(*EVM) ([]byte, uint64, error)
		reverts bool
	}{
		{name: "delegatecall from Granite reverts", granite: true, call: delegateCall, reverts: true},
		{name: "callcode from Granite reverts", granite: true, call: callCode, reverts: true},
		{name: "call from Granite runs", granite: true, call: call},
		{name: "delegatecall before Granite runs", granite: false, call: delegateCall},
		{name: "callcode before Granite runs", granite: false, call: callCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
			require.NoError(t, err)
			blockCtx := BlockContext{BlockNumber: big.NewInt(0), CanTransfer: CanTransfer, Transfer: Transfer}
			evm := NewEVM(blockCtx, TxContext{}, statedb, params.TestFlareChainConfig, Config{})
			evm.chainRules.IsGranite = tt.granite
			evm.chainRules.ActivePrecompiles = map[common.Address]precompileconfig.Config{moduleAddr: nil}
			evm.SetHostContext("host value")

			ret, gas, err := tt.call(evm)
			if tt.reverts {
				require.ErrorIs(t, err, vmerrs.ErrExecutionReverted)
				assert.Empty(t, ret)
				assert.Zero(t, gas)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []byte("host value"), ret)
			assert.Equal(t, uint64(100_000), gas)
		})
	}
}
