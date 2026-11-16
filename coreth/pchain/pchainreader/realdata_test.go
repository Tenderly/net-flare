package pchainreader

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/formatting/address"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/stretchr/testify/require"
	"github.com/tenderly/net-flare/coreth/pchain"
)

// mainnetData is testdata/flare_mainnet_pchain.json: Flare mainnet P-chain
// blocks and producing transactions as the node stores them (the hex
// encoding of platform.getBlockByHeight and platform.getTx without its
// checksum) together with the node's own JSON encoding of the same data.
type mainnetData struct {
	Blocks []struct {
		Height uint64    `json:"height"`
		Bytes  string    `json:"bytes"`
		JSON   nodeBlock `json:"json"`
	} `json:"blocks"`
	Txs []struct {
		ID    string `json:"id"`
		Bytes string `json:"bytes"`
		JSON  nodeTx `json:"json"`
	} `json:"txs"`
}

type nodeBlock struct {
	ID       string   `json:"id"`
	ParentID string   `json:"parentID"`
	Height   uint64   `json:"height"`
	Time     uint64   `json:"time"`
	Txs      []nodeTx `json:"txs"`
	Tx       *nodeTx  `json:"tx"`
}

type nodeTx struct {
	ID       string `json:"id"`
	Unsigned struct {
		Validator *struct {
			NodeID string      `json:"nodeID"`
			End    json.Number `json:"end"`
			Weight json.Number `json:"weight"`
		} `json:"validator"`
		Shares           uint32       `json:"shares"`
		SourceChain      string       `json:"sourceChain"`
		DestinationChain string       `json:"destinationChain"`
		TxID             string       `json:"txID"`
		Inputs           []nodeInput  `json:"inputs"`
		ImportedInputs   []nodeInput  `json:"importedInputs"`
		Outputs          []nodeOutput `json:"outputs"`
		Stake            []nodeOutput `json:"stake"`
		ExportedOutputs  []nodeOutput `json:"exportedOutputs"`
	} `json:"unsignedTx"`
}

type nodeInput struct {
	TxID        string `json:"txID"`
	OutputIndex uint32 `json:"outputIndex"`
	Input       struct {
		Amount           json.Number `json:"amount"`
		SignatureIndices []uint32    `json:"signatureIndices"`
	} `json:"input"`
}

type nodeOutput struct {
	Output struct {
		Addresses []string    `json:"addresses"`
		Amount    json.Number `json:"amount"`
		Threshold uint32      `json:"threshold"`
	} `json:"output"`
}

// bytesStore serves fixed consensus bytes by height and transaction ID.
type bytesStore struct {
	blocks map[uint64][]byte
	txs    map[ids.ID][]byte
}

func (s *bytesStore) BlockBytes(height uint64) ([]byte, error) {
	b, ok := s.blocks[height]
	if !ok {
		return nil, pchain.ErrNotAccepted
	}
	return b, nil
}

func (s *bytesStore) TxBytes(txID ids.ID) ([]byte, error) {
	b, ok := s.txs[txID]
	if !ok {
		return nil, fmt.Errorf("%w: no producing tx in the test data", ErrInvalidData)
	}
	return b, nil
}

func (*bytesStore) RewardUTXOs(ids.ID) ([]*avax.UTXO, error) {
	return nil, nil
}

func loadMainnetData(t *testing.T) (mainnetData, *bytesStore) {
	t.Helper()
	raw, err := os.ReadFile("testdata/flare_mainnet_pchain.json")
	require.NoError(t, err)
	var data mainnetData
	require.NoError(t, json.Unmarshal(raw, &data))

	store := &bytesStore{blocks: map[uint64][]byte{}, txs: map[ids.ID][]byte{}}
	for _, b := range data.Blocks {
		bytes, err := hex.DecodeString(b.Bytes)
		require.NoError(t, err)
		store.blocks[b.Height] = bytes
	}
	for _, tx := range data.Txs {
		bytes, err := hex.DecodeString(tx.Bytes)
		require.NoError(t, err)
		store.txs[ids.FromStringOrPanic(tx.ID)] = bytes
	}
	return data, store
}

func numberUint64(t *testing.T, n json.Number) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(n.String(), 10, 64)
	require.NoError(t, err)
	return v
}

func pAddresses(t *testing.T, addrs []ids.ShortID) []string {
	t.Helper()
	out := make([]string, len(addrs))
	for i, addr := range addrs {
		formatted, err := address.Format("P", pchain.FlareMainnet.HRP, addr.Bytes())
		require.NoError(t, err)
		out[i] = formatted
	}
	return out
}

func TestMainnetBlocksDecodeLikeTheNode(t *testing.T) {
	data, store := loadMainnetData(t)
	reader := New(store, pchain.FlareMainnet, NewCache())

	wantTypes := map[uint64][]pchain.TxType{
		2163958: {pchain.TxAddPermissionlessValidator},
		2164189: {pchain.TxImport},
		2164206: {pchain.TxAddPermissionlessDelegator},
		2164214: {pchain.TxExport},
		2164215: {pchain.TxRewardValidator},
		2164216: {},
	}
	require.Len(t, data.Blocks, len(wantTypes))

	for _, b := range data.Blocks {
		t.Run(strconv.FormatUint(b.Height, 10), func(t *testing.T) {
			stored := store.blocks[b.Height]
			require.Equal(
				t,
				b.JSON.ID,
				ids.ID(sha256.Sum256(stored)).String(),
				"block ID is the sha256 of the stored bytes",
			)

			var charged uint64
			blkReader, err := reader.GetBlock(b.Height, func(n uint64) error {
				charged += n
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, uint64(len(stored)), charged)

			blk := blkReader.Block()
			require.Equal(t, b.Height, blk.Height)
			require.Equal(t, b.JSON.ID, blk.ID.String())
			require.Equal(t, b.JSON.ParentID, blk.ParentID.String())
			require.Equal(t, b.JSON.Time, blk.Timestamp)

			nodeTxs := b.JSON.Txs
			switch {
			case b.JSON.Tx != nil:
				require.Equal(t, pchain.BlockProposal, blk.Kind)
				nodeTxs = append(nodeTxs, *b.JSON.Tx)
			case b.JSON.Txs != nil:
				require.Equal(t, pchain.BlockStandard, blk.Kind)
			default:
				require.Contains(t, []pchain.BlockKind{pchain.BlockCommit, pchain.BlockAbort}, blk.Kind)
			}
			require.Len(t, blk.Txs, len(wantTypes[b.Height]))
			require.Len(t, nodeTxs, len(blk.Txs))

			for i, tx := range blk.Txs {
				node := nodeTxs[i].Unsigned
				require.Equal(t, wantTypes[b.Height][i], tx.Type)
				require.Equal(t, nodeTxs[i].ID, tx.ID.String())
				require.Equal(t, uint32(i), tx.Index)
				require.Equal(t, uint32(len(node.Inputs)+len(node.ImportedInputs)), tx.InputCount)
				require.Equal(
					t,
					uint32(len(node.Outputs)+len(node.Stake)+len(node.ExportedOutputs)),
					tx.OutputCount,
				)

				switch tx.Type {
				case pchain.TxAddPermissionlessValidator, pchain.TxAddPermissionlessDelegator:
					require.NotNil(t, node.Validator)
					require.Equal(t, node.Validator.NodeID, tx.NodeID.String())
					require.Equal(t, numberUint64(t, node.Validator.Weight), tx.Weight)
					require.Equal(t, numberUint64(t, node.Validator.End), tx.EndTime)
					// Durango is active, so a staker starts at the block time.
					require.Equal(t, b.JSON.Time, tx.StartTime)
					require.Equal(t, node.Shares, tx.DelegationShares)
				case pchain.TxImport:
					require.Equal(t, node.SourceChain, tx.RemoteChainID.String())
				case pchain.TxExport:
					require.Equal(t, node.DestinationChain, tx.RemoteChainID.String())
				case pchain.TxRewardValidator:
					require.Equal(t, node.TxID, tx.StakerTxID.String())
				}

				outputs, total, err := blkReader.GetOutputs(uint32(i), 0, tx.OutputCount)
				require.NoError(t, err)
				require.Equal(t, tx.OutputCount, total)
				nodeOutputs := append(
					append(append([]nodeOutput{}, node.Outputs...), node.Stake...),
					node.ExportedOutputs...)
				for j, out := range outputs {
					require.Equal(t, nodeOutputs[j].Output.Addresses, pAddresses(t, out.Addresses))
					require.Equal(t, numberUint64(t, nodeOutputs[j].Output.Amount), out.Amount)
					require.Equal(t, nodeOutputs[j].Output.Threshold, out.Threshold)
					require.Equal(t, pchain.FlareMainnet.NativeAssetID, out.AssetID)
				}
			}
		})
	}
}

func TestMainnetInputOwnersResolveFromProducingTxs(t *testing.T) {
	data, store := loadMainnetData(t)
	producing := map[string]nodeTx{}
	for _, tx := range data.Txs {
		producing[tx.ID] = tx.JSON
	}

	tests := []struct {
		name   string
		height uint64
		// fromStake is true when the consumed output is a returned stake
		// output of the producing staker, not one of its BaseTx outputs.
		fromStake bool
	}{
		{name: "delegator input spends a BaseTx output", height: 2164206},
		{name: "export input spends a returned stake output", height: 2164214, fromStake: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := New(store, pchain.FlareMainnet, NewCache())
			var charged uint64
			charge := func(n uint64) error {
				charged += n
				return nil
			}
			blkReader, err := reader.GetBlock(tt.height, charge)
			require.NoError(t, err)
			nodeIn := blockTxs(t, data, tt.height)[0].Unsigned.Inputs[0]

			inputs, _, err := blkReader.GetInputs(0, 0, 1, charge)
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			in := inputs[0]

			producer, ok := producing[nodeIn.TxID]
			require.True(t, ok)
			producerOutputs := producer.Unsigned.Outputs
			if tt.fromStake {
				require.Empty(t, producerOutputs)
				producerOutputs = producer.Unsigned.Stake
			}
			want := producerOutputs[nodeIn.OutputIndex].Output

			require.Equal(t, nodeIn.TxID, in.UTXOTxID.String())
			require.Equal(t, nodeIn.OutputIndex, in.UTXOOutputIndex)
			require.Equal(t, numberUint64(t, nodeIn.Input.Amount), in.Amount)
			require.Equal(t, nodeIn.Input.SignatureIndices, in.SigIndices)
			require.True(t, in.Resolved)
			require.False(t, in.Imported)
			require.False(t, in.Genesis)
			require.Equal(t, want.Addresses, pAddresses(t, in.Addresses))
			require.Equal(t, want.Threshold, in.Threshold)
			require.NotEmpty(t, in.Signatures)

			blockBytes := uint64(len(store.blocks[tt.height]))
			producerBytes := uint64(len(store.txs[in.UTXOTxID]))
			require.Equal(t, blockBytes+producerBytes, charged)
		})
	}
}

func blockTxs(t *testing.T, data mainnetData, height uint64) []nodeTx {
	t.Helper()
	for _, b := range data.Blocks {
		if b.Height == height {
			return b.JSON.Txs
		}
	}
	require.FailNow(t, "no block in the test data", "height %d", height)
	return nil
}
