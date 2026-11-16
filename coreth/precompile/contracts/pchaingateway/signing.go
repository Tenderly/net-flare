// (c) 2026, Flare Network. All rights reserved.
// See the file LICENSE for licensing terms.

package pchaingateway

import (
	"encoding/hex"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ethereum/go-ethereum/accounts"
)

// signingHashes are the message hashes a secp256k1 credential may sign on
// Flare: the plain sha256 of the unsigned transaction bytes (the Avalanche
// scheme) and the Ethereum signed-message hash (EIP-191) of the hex encoding
// of that digest, which Ethereum signing tools produce when asked to sign the
// hex string. go-flare's secp256k1fx accepts either; upstream avalanchego
// accepts only the first.
type signingHashes struct {
	plain       []byte
	ethPrefixed []byte
}

func newSigningHashes(unsignedBytes []byte) signingHashes {
	plain := hashing.ComputeHash256(unsignedBytes)
	return signingHashes{
		plain:       plain,
		ethPrefixed: accounts.TextHash([]byte(hex.EncodeToString(plain))),
	}
}

// recoverSigner returns the public key that produced sig over one of the
// hashes, tried in go-flare's order, and whose Avalanche address is expected.
// ok is false for a malformed signature or one that recovers to another
// address under both hashes. Every call recovers a new key, so no key is
// shared between callers.
func (h signingHashes) recoverSigner(sig []byte, expected ids.ShortID) (*secp256k1.PublicKey, bool) {
	for _, hash := range [][]byte{h.plain, h.ethPrefixed} {
		pk, err := secp256k1.RecoverPublicKeyFromHash(hash, sig)
		if err != nil {
			return nil, false
		}
		if pk.Address() == expected {
			return pk, true
		}
	}
	return nil, false
}
