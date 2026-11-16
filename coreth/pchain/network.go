package pchain

import (
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/components/avax"
)

// Network holds the genesis constants of a Flare network that P-chain reads
// depend on. They are identical on every node of the network.
type Network struct {
	// ID is the Avalanche network ID.
	ID uint32
	// HRP is the bech32 human-readable part of the network's P-chain
	// addresses.
	HRP string
	// NativeAssetID is the asset ID of the native token.
	NativeAssetID ids.ID
	// DurangoTime is the P-chain Durango activation. From it, a bounded
	// staker's effective start is the block time, not its own start field.
	DurangoTime time.Time
	// GenesisUTXOs are the P-chain genesis UTXOs, which have no producing
	// transaction.
	GenesisUTXOs []*avax.UTXO
}

// FlareMainnet is Flare mainnet (C-chain ID 14). Its genesis has no P-chain
// UTXOs: the only allocation is initially staked, so go-flare's genesis build
// turns it into validator stake.
var FlareMainnet = Network{
	ID:  14,
	HRP: "flare",
	// 2MxKSeEWXViLdYyDhW1SQ46AECZEbE2bnVRZptv42JrxqyUX5k
	NativeAssetID: ids.ID{
		0xb3, 0x46, 0x2f, 0xc3, 0x95, 0x68, 0xbf, 0x99, 0xfd, 0x34, 0x6d, 0x3c, 0xdc, 0xfe, 0x1f, 0xb9,
		0x00, 0xf1, 0x4c, 0xdc, 0xf2, 0x76, 0xe3, 0xc0, 0xd9, 0x5f, 0x81, 0x4a, 0xf8, 0x53, 0x78, 0x24,
	},
	DurangoTime: time.Date(2025, time.August, 5, 12, 0, 0, 0, time.UTC),
}

// NetworkByChainID returns the network of a Flare C-chain ID.
func NetworkByChainID(chainID uint64) (Network, bool) {
	switch chainID {
	case 14:
		return FlareMainnet, true
	default:
		return Network{}, false
	}
}
