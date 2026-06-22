// (c) 2026, Tenderly. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// p256Input builds the 160-byte P256VERIFY input layout: hash | r | s | x | y.
func p256Input(hash []byte, r, s, x, y []byte) []byte {
	in := make([]byte, 0, 160)
	in = append(in, common.LeftPadBytes(hash, 32)...)
	in = append(in, common.LeftPadBytes(r, 32)...)
	in = append(in, common.LeftPadBytes(s, 32)...)
	in = append(in, common.LeftPadBytes(x, 32)...)
	in = append(in, common.LeftPadBytes(y, 32)...)
	return in
}

func TestP256VerifyPrecompile(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	digest := sha256.Sum256([]byte("granite"))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	valid := p256Input(digest[:], r.Bytes(), s.Bytes(), key.X.Bytes(), key.Y.Bytes())

	tampered := make([]byte, len(valid))
	copy(tampered, valid)
	tampered[63] ^= 0x01 // flip a bit in s

	tests := []struct {
		name      string
		input     []byte
		wantValid bool
	}{
		{"valid signature", valid, true},
		{"tampered signature", tampered, false},
		{"wrong input length", valid[:159], false},
	}

	p := &p256Verify{}
	if got := p.RequiredGas(nil); got != p256VerifyGas {
		t.Fatalf("RequiredGas = %d, want %d", got, p256VerifyGas)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := p.Run(tt.input)
			if err != nil {
				t.Fatalf("Run error: %v", err)
			}
			gotValid := len(out) == 32 && out[31] == 1
			if gotValid != tt.wantValid {
				t.Fatalf("validity = %v, want %v (out=%x)", gotValid, tt.wantValid, out)
			}
		})
	}
}

func TestGranitePrecompileActivation(t *testing.T) {
	if _, ok := PrecompiledContractsGranite[P256VerifyAddress]; !ok {
		t.Fatalf("P256Verify (%s) missing from Granite precompile set", P256VerifyAddress)
	}
	if _, ok := PrecompiledContractsCancun[P256VerifyAddress]; ok {
		t.Fatalf("P256Verify must not be active before Granite (Cancun set)")
	}

	granite := false
	for _, addr := range PrecompiledAddressesGranite {
		if addr == P256VerifyAddress {
			granite = true
		}
	}
	if !granite {
		t.Fatalf("P256VerifyAddress missing from PrecompiledAddressesGranite")
	}
}
