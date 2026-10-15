// (c) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tenderly/net-flare/coreth/params"
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
