// (c) 2026, Flare Network. All rights reserved.
// See the file LICENSE for licensing terms.

// Package pchainreader implements pchain.Reader over raw P-chain consensus
// bytes: it decodes accepted blocks into the gateway's plain view and
// resolves the owners of consumed inputs from the transactions that produced
// them. It is ported from go-flare v1.15.1 vms/platformvm/pchainreader and
// parses with upstream avalanchego's platformvm block and txs codecs, which
// register the same types in the same order as go-flare's.
package pchainreader

import (
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/cache/lru"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/block"
	"github.com/tenderly/net-flare/coreth/pchain"
)

const (
	// decodedBlockCacheSize bounds the number of decoded blocks kept in
	// memory.
	decodedBlockCacheSize = 256
	// ownerCacheSize bounds the number of resolved consumed-output owners.
	ownerCacheSize = 8192
)

var _ pchain.Reader = (*Reader)(nil)

// Store is the source of accepted P-chain consensus bytes. The gas of a read
// is charged on the byte counts it returns, so they must be the bytes that the
// P-chain stores: the block bytes whose sha256 is the block ID and the signed
// transaction bytes whose sha256 is the transaction ID.
type Store interface {
	// BlockBytes returns the bytes of the accepted block at height, or an
	// error that wraps pchain.ErrNotAccepted when the source has not accepted
	// that height.
	BlockBytes(height uint64) ([]byte, error)
	// TxBytes returns the signed bytes of the accepted transaction txID.
	TxBytes(txID ids.ID) ([]byte, error)
	// RewardUTXOs returns the reward UTXOs of the staker transaction txID,
	// or none when it has none.
	RewardUTXOs(txID ids.ID) ([]*avax.UTXO, error)
}

// Cache holds decoded blocks and resolved owners. Accepted data never
// changes, so one Cache can serve every Reader of one network for the life of
// the process. It is safe for concurrent use.
type Cache struct {
	blocks *lru.Cache[uint64, *decodedBlock]
	owners *lru.Cache[utxoKey, ownerEntry]
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{
		blocks: lru.NewCache[uint64, *decodedBlock](decodedBlockCacheSize),
		owners: lru.NewCache[utxoKey, ownerEntry](ownerCacheSize),
	}
}

// Reader implements [pchain.Reader] over a [Store]. It is safe for
// concurrent use when its Store is.
type Reader struct {
	store   Store
	network pchain.Network
	cache   *Cache
	genesis map[utxoKey]*avax.UTXO
}

// New returns a Reader of network over store that keeps its decoded data in
// cache. The cache must belong to the same network.
func New(store Store, network pchain.Network, cache *Cache) *Reader {
	genesis := make(map[utxoKey]*avax.UTXO, len(network.GenesisUTXOs))
	for _, utxo := range network.GenesisUTXOs {
		if utxo != nil {
			genesis[utxoKey{txID: utxo.TxID, index: utxo.OutputIndex}] = utxo
		}
	}
	return &Reader{store: store, network: network, cache: cache, genesis: genesis}
}

// genesisUTXO returns the genesis UTXO consumed by key, if any.
func (r *Reader) genesisUTXO(key utxoKey) (*avax.UTXO, bool) {
	utxo, ok := r.genesis[key]
	return utxo, ok
}

// isDurango reports whether the P-chain Durango rules apply at a block
// timestamp.
func (r *Reader) isDurango(timestamp uint64) bool {
	return !time.Unix(int64(timestamp), 0).Before(r.network.DurangoTime)
}

// chargeBytes applies a possibly nil [pchain.ByteCharger].
func chargeBytes(charge pchain.ByteCharger, bytes uint64) error {
	if charge == nil {
		return nil
	}
	return charge(bytes)
}

func (r *Reader) GetBlock(height uint64, charge pchain.ByteCharger) (pchain.BlockReader, error) {
	db, err := r.decodedBlock(height, charge)
	if err != nil {
		return nil, err
	}
	return &blockReader{r: r, db: db}, nil
}

// blockReader implements [pchain.BlockReader] over a decoded block. Decoded
// blocks are immutable, so the reader stays valid after the block leaves the
// cache.
type blockReader struct {
	r  *Reader
	db *decodedBlock
}

func (b *blockReader) Block() *pchain.Block {
	return &b.db.block
}

// decodedBlock returns the decoded block at height. The block's stored size
// is charged before it is parsed, and again on every cached read, so the
// charge never depends on the cache. An error of charge is returned
// unchanged. Upstream block parsing initializes each transaction with
// txs.Codec, whose limit is 256 KiB, where go-flare allows 2 MiB; the
// P-chain mempool accepts transactions up to 64 KiB, so a block with a
// larger transaction gives ErrInvalidData here.
func (r *Reader) decodedBlock(height uint64, charge pchain.ByteCharger) (*decodedBlock, error) {
	if db, ok := r.cache.blocks.Get(height); ok {
		if err := chargeBytes(charge, db.bytes); err != nil {
			return nil, err
		}
		return db, nil
	}

	blkBytes, err := r.store.BlockBytes(height)
	if err != nil {
		return nil, fmt.Errorf("fetching P-chain block at height %d: %w", height, err)
	}
	size := uint64(len(blkBytes))
	if err := chargeBytes(charge, size); err != nil {
		return nil, err
	}

	blk, err := block.Parse(block.GenesisCodec, blkBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing P-chain block at height %d: %w", ErrInvalidData, height, err)
	}
	if blk.Height() != height {
		return nil, fmt.Errorf("%w: P-chain block %s at height %d has height %d", ErrInvalidData, blk.ID(), height, blk.Height())
	}
	db := r.decode(blk)
	db.bytes = size
	r.cache.blocks.Put(height, db)
	return db, nil
}
