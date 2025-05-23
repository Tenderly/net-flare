// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	stdjson "encoding/json"

	"github.com/golang/mock/gomock"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/utils/formatting/address"
	"github.com/ava-labs/avalanchego/utils/json"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/sampler"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/avm/blocks"
	"github.com/ava-labs/avalanchego/vms/avm/blocks/executor"
	"github.com/ava-labs/avalanchego/vms/avm/states"
	"github.com/ava-labs/avalanchego/vms/avm/txs"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/index"
	"github.com/ava-labs/avalanchego/vms/components/keystore"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/nftfx"
	"github.com/ava-labs/avalanchego/vms/propertyfx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var testChangeAddr = ids.GenerateTestShortID()

var testCases = []struct {
	name      string
	avaxAsset bool
}{
	{"genesis asset is AVAX", true},
	{"genesis asset is TEST", false},
}

// Returns:
// 1) genesis bytes of vm
// 2) the VM
// 3) The service that wraps the VM
// 4) atomic memory to use in tests
func setup(t *testing.T, isAVAXAsset bool) ([]byte, *VM, *Service, *atomic.Memory, *txs.Tx) {
	var genesisBytes []byte
	var vm *VM
	var m *atomic.Memory
	var genesisTx *txs.Tx
	if isAVAXAsset {
		genesisBytes, _, vm, m = GenesisVM(t)
		genesisTx = GetAVAXTxFromGenesisTest(genesisBytes, t)
	} else {
		genesisBytes, _, vm, m = setupTxFeeAssets(t)
		genesisTx = GetCreateTxFromGenesisTest(t, genesisBytes, feeAssetName)
	}
	s := &Service{vm: vm}
	return genesisBytes, vm, s, m, genesisTx
}

// Returns:
// 1) genesis bytes of vm
// 2) the VM
// 3) The service that wraps the VM
// 4) Issuer channel
// 5) atomic memory to use in tests
func setupWithIssuer(t *testing.T, isAVAXAsset bool) ([]byte, *VM, *Service, chan common.Message) {
	var genesisBytes []byte
	var vm *VM
	var issuer chan common.Message
	if isAVAXAsset {
		genesisBytes, issuer, vm, _ = GenesisVM(t)
	} else {
		genesisBytes, issuer, vm, _ = setupTxFeeAssets(t)
	}
	s := &Service{vm: vm}
	return genesisBytes, vm, s, issuer
}

// Returns:
// 1) genesis bytes of vm
// 2) the VM
// 3) The service that wraps the VM
// 4) atomic memory to use in tests
func setupWithKeys(t *testing.T, isAVAXAsset bool) ([]byte, *VM, *Service, *atomic.Memory, *txs.Tx) {
	genesisBytes, vm, s, m, tx := setup(t, isAVAXAsset)

	// Import the initially funded private keys
	user, err := keystore.NewUserFromKeystore(s.vm.ctx.Keystore, username, password)
	if err != nil {
		t.Fatal(err)
	}

	if err := user.PutKeys(keys...); err != nil {
		t.Fatalf("Failed to set key for user: %s", err)
	}

	if err := user.Close(); err != nil {
		t.Fatal(err)
	}
	return genesisBytes, vm, s, m, tx
}

// Sample from a set of addresses and return them raw and formatted as strings.
// The size of the sample is between 1 and len(addrs)
// If len(addrs) == 0, returns nil
func sampleAddrs(t *testing.T, vm *VM, addrs []ids.ShortID) ([]ids.ShortID, []string) {
	sampledAddrs := []ids.ShortID{}
	sampledAddrsStr := []string{}

	sampler := sampler.NewUniform()
	if err := sampler.Initialize(uint64(len(addrs))); err != nil {
		t.Fatal(err)
	}

	numAddrs := 1 + rand.Intn(len(addrs)) // #nosec G404
	indices, err := sampler.Sample(numAddrs)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indices {
		addr := addrs[index]
		addrStr, err := vm.FormatLocalAddress(addr)
		if err != nil {
			t.Fatal(err)
		}

		sampledAddrs = append(sampledAddrs, addr)
		sampledAddrsStr = append(sampledAddrsStr, addrStr)
	}
	return sampledAddrs, sampledAddrsStr
}

// Returns error if [numTxFees] tx fees was not deducted from the addresses in [fromAddrs]
// relative to their starting balance
func verifyTxFeeDeducted(t *testing.T, s *Service, fromAddrs []ids.ShortID, numTxFees int) error {
	totalTxFee := uint64(numTxFees) * s.vm.TxFee
	fromAddrsStartBalance := startBalance * uint64(len(fromAddrs))

	// Key: Address
	// Value: AVAX balance
	balances := map[ids.ShortID]uint64{}

	for _, addr := range addrs { // get balances for all addresses
		addrStr, err := s.vm.FormatLocalAddress(addr)
		if err != nil {
			t.Fatal(err)
		}
		reply := &GetBalanceReply{}
		err = s.GetBalance(nil,
			&GetBalanceArgs{
				Address: addrStr,
				AssetID: s.vm.feeAssetID.String(),
			},
			reply,
		)
		if err != nil {
			return fmt.Errorf("couldn't get balance of %s: %w", addr, err)
		}
		balances[addr] = uint64(reply.Balance)
	}

	fromAddrsTotalBalance := uint64(0)
	for _, addr := range fromAddrs {
		fromAddrsTotalBalance += balances[addr]
	}

	if fromAddrsTotalBalance != fromAddrsStartBalance-totalTxFee {
		return fmt.Errorf("expected fromAddrs to have %d balance but have %d",
			fromAddrsStartBalance-totalTxFee,
			fromAddrsTotalBalance,
		)
	}
	return nil
}

func TestServiceIssueTx(t *testing.T) {
	genesisBytes, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	txArgs := &api.FormattedTx{}
	txReply := &api.JSONTxID{}
	err := s.IssueTx(nil, txArgs, txReply)
	if err == nil {
		t.Fatal("Expected empty transaction to return an error")
	}
	tx := NewTx(t, genesisBytes, vm)
	txArgs.Tx, err = formatting.Encode(formatting.Hex, tx.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	txArgs.Encoding = formatting.Hex
	txReply = &api.JSONTxID{}
	if err := s.IssueTx(nil, txArgs, txReply); err != nil {
		t.Fatal(err)
	}
	if txReply.TxID != tx.ID() {
		t.Fatalf("Expected %q, got %q", txReply.TxID, tx.ID())
	}
}

func TestServiceGetTxStatus(t *testing.T) {
	require := require.New(t)

	genesisBytes, vm, s, issuer := setupWithIssuer(t, true)
	ctx := vm.ctx
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	statusArgs := &api.JSONTxID{}
	statusReply := &GetTxStatusReply{}
	err := s.GetTxStatus(nil, statusArgs, statusReply)
	require.ErrorIs(err, errNilTxID)

	newTx := newAvaxBaseTxWithOutputs(t, genesisBytes, vm)
	txID := newTx.ID()

	statusArgs = &api.JSONTxID{
		TxID: txID,
	}
	statusReply = &GetTxStatusReply{}
	require.NoError(s.GetTxStatus(nil, statusArgs, statusReply))
	require.Equal(choices.Unknown, statusReply.Status)

	_, err = vm.IssueTx(newTx.Bytes())
	require.NoError(err)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 1)
	require.NoError(txs[0].Accept(context.Background()))

	statusReply = &GetTxStatusReply{}
	require.NoError(s.GetTxStatus(nil, statusArgs, statusReply))
	require.Equal(choices.Accepted, statusReply.Status)
}

// Test the GetBalance method when argument Strict is true
func TestServiceGetBalanceStrict(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := vm.FormatLocalAddress(addr)
	if err != nil {
		t.Fatal(err)
	}

	// A UTXO with a 2 out of 2 multisig
	// where one of the addresses is [addr]
	twoOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(twoOfTwoUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs := &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply := &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(t, uint64(1337), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 1, "should have only returned 1 utxoID")

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Equal(t, uint64(0), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 0, "should have returned 0 utxoIDs")

	// A UTXO with a 1 out of 2 multisig
	// where one of the addresses is [addr]
	oneOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(oneOfTwoUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply = &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(t, uint64(1337+1337), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 2, "should have only returned 2 utxoIDs")

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Equal(t, uint64(0), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 0, "should have returned 0 utxoIDs")

	// A UTXO with a 1 out of 1 multisig
	// but with a locktime in the future
	now := vm.clock.Time()
	futureUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Locktime:  uint64(now.Add(10 * time.Hour).Unix()),
				Threshold: 1,
				Addrs:     []ids.ShortID{addr},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(futureUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply = &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(t, uint64(1337*3), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 3, "should have returned 3 utxoIDs")

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	err = s.GetBalance(nil, balanceArgs, balanceReply)
	require.NoError(t, err)
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Equal(t, uint64(0), uint64(balanceReply.Balance))
	require.Len(t, balanceReply.UTXOIDs, 0, "should have returned 0 utxoIDs")
}

func TestServiceGetTxs(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	var err error
	vm.addressTxsIndexer, err = index.NewIndexer(vm.db, vm.ctx.Log, "", prometheus.NewRegistry(), false)
	require.NoError(t, err)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := vm.FormatLocalAddress(addr)
	if err != nil {
		t.Fatal(err)
	}

	testTxCount := 25
	testTxs := setupTestTxsInDB(t, vm.db, addr, assetID, testTxCount)

	// get the first page
	getTxsArgs := &GetAddressTxsArgs{
		PageSize:    10,
		JSONAddress: api.JSONAddress{Address: addrStr},
		AssetID:     assetID.String(),
	}
	getTxsReply := &GetAddressTxsReply{}
	err = s.GetAddressTxs(nil, getTxsArgs, getTxsReply)
	require.NoError(t, err)
	require.Len(t, getTxsReply.TxIDs, 10)
	require.Equal(t, getTxsReply.TxIDs, testTxs[:10])

	// get the second page
	getTxsArgs.Cursor = getTxsReply.Cursor
	getTxsReply = &GetAddressTxsReply{}
	err = s.GetAddressTxs(nil, getTxsArgs, getTxsReply)
	require.NoError(t, err)
	require.Len(t, getTxsReply.TxIDs, 10)
	require.Equal(t, getTxsReply.TxIDs, testTxs[10:20])
}

func TestServiceGetAllBalances(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := vm.FormatLocalAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	// A UTXO with a 2 out of 2 multisig
	// where one of the addresses is [addr]
	twoOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(twoOfTwoUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs := &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply := &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(t, reply.Balances, 1)
	require.Equal(t, assetID.String(), reply.Balances[0].AssetID)
	require.Equal(t, uint64(1337), uint64(reply.Balances[0].Balance))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	require.Len(t, reply.Balances, 0)

	// A UTXO with a 1 out of 2 multisig
	// where one of the addresses is [addr]
	oneOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(oneOfTwoUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(t, reply.Balances, 1)
	require.Equal(t, assetID.String(), reply.Balances[0].AssetID)
	require.Equal(t, uint64(1337*2), uint64(reply.Balances[0].Balance))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Len(t, reply.Balances, 0)

	// A UTXO with a 1 out of 1 multisig
	// but with a locktime in the future
	now := vm.clock.Time()
	futureUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Locktime:  uint64(now.Add(10 * time.Hour).Unix()),
				Threshold: 1,
				Addrs:     []ids.ShortID{addr},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(futureUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(t, reply.Balances, 1)
	require.Equal(t, assetID.String(), reply.Balances[0].AssetID)
	require.Equal(t, uint64(1337*3), uint64(reply.Balances[0].Balance))
	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Len(t, reply.Balances, 0)

	// A UTXO for a different asset
	otherAssetID := ids.GenerateTestID()
	otherAssetUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: otherAssetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	vm.state.AddUTXO(otherAssetUTXO)
	require.NoError(t, vm.state.Commit())

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(t, reply.Balances, 2)
	gotAssetIDs := []string{reply.Balances[0].AssetID, reply.Balances[1].AssetID}
	require.Contains(t, gotAssetIDs, assetID.String())
	require.Contains(t, gotAssetIDs, otherAssetID.String())
	gotBalances := []uint64{uint64(reply.Balances[0].Balance), uint64(reply.Balances[1].Balance)}
	require.Contains(t, gotBalances, uint64(1337))
	require.Contains(t, gotBalances, uint64(1337*3))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	err = s.GetAllBalances(nil, balanceArgs, reply)
	require.NoError(t, err)
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(t, reply.Balances, 0)
}

func TestServiceGetTx(t *testing.T) {
	_, vm, s, _, genesisTx := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	txID := genesisTx.ID()

	reply := api.GetTxReply{}
	err := s.GetTx(nil, &api.GetTxArgs{
		TxID: txID,
	}, &reply)
	require.NoError(t, err)
	if err != nil {
		t.Fatal(err)
	}
	txBytes, err := formatting.Decode(reply.Encoding, reply.Tx.(string))
	if err != nil {
		t.Fatal(err)
	}
	require.Equal(t, genesisTx.Bytes(), txBytes, "Wrong tx returned from service.GetTx")
}

func TestServiceGetTxJSON_BaseTx(t *testing.T) {
	require := require.New(t)

	genesisBytes, vm, s, issuer := setupWithIssuer(t, true)
	ctx := vm.ctx
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	newTx := newAvaxBaseTxWithOutputs(t, genesisBytes, vm)

	txID, err := vm.IssueTx(newTx.Bytes())
	require.NoError(err)
	require.Equal(newTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 1)
	require.NoError(txs[0].Accept(context.Background()))

	reply := api.GetTxReply{}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)
	// fxID in the VM is really set to 11111111111111111111111111111111LpoYY for [secp256k1fx.TransferOutput]
	require.Contains(jsonString, "\"memo\":\"0x0102030405060708\"")
	require.Contains(jsonString, "\"inputs\":[{\"txID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"outputIndex\":2,\"assetID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"fxID\":\"11111111111111111111111111111111LpoYY\",\"input\":{\"amount\":50000,\"signatureIndices\":[0]}}]")
	require.Contains(jsonString, "\"outputs\":[{\"assetID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"fxID\":\"11111111111111111111111111111111LpoYY\",\"output\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"amount\":49000,\"locktime\":0,\"threshold\":1}}]")
}

func TestServiceGetTxJSON_ExportTx(t *testing.T) {
	require := require.New(t)

	genesisBytes, vm, s, issuer := setupWithIssuer(t, true)
	ctx := vm.ctx
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	newTx := newAvaxExportTxWithOutputs(t, genesisBytes, vm)

	txID, err := vm.IssueTx(newTx.Bytes())
	require.NoError(err)
	require.Equal(newTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)

	ctx.Lock.Lock()
	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 1)
	require.NoError(txs[0].Accept(context.Background()))

	reply := api.GetTxReply{}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)
	// fxID in the VM is really set to 11111111111111111111111111111111LpoYY for [secp256k1fx.TransferOutput]
	require.Contains(jsonString, "\"inputs\":[{\"txID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"outputIndex\":2,\"assetID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"fxID\":\"11111111111111111111111111111111LpoYY\",\"input\":{\"amount\":50000,\"signatureIndices\":[0]}}]")
	require.Contains(jsonString, "\"exportedOutputs\":[{\"assetID\":\"2XGxUr7VF7j1iwUp2aiGe4b6Ue2yyNghNS1SuNTNmZ77dPpXFZ\",\"fxID\":\"11111111111111111111111111111111LpoYY\",\"output\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"amount\":49000,\"locktime\":0,\"threshold\":1}}]}")
}

func TestServiceGetTxJSON_CreateAssetTx(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	txID, err := vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)
	require.Equal(createAssetTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 1)
	require.NoError(txs[0].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// contains the address in the right format
	require.Contains(jsonString, "\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"groupID\":1,\"locktime\":0,\"threshold\":1},{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"groupID\":2,\"locktime\":0,\"threshold\":1}]}")
	require.Contains(jsonString, "\"initialStates\":[{\"fxIndex\":0,\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\",\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"locktime\":0,\"threshold\":1},{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"locktime\":0,\"threshold\":1}]},{\"fxIndex\":1,\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\",\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"groupID\":1,\"locktime\":0,\"threshold\":1},{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"groupID\":2,\"locktime\":0,\"threshold\":1}]},{\"fxIndex\":2,\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\",\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"locktime\":0,\"threshold\":1},{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"locktime\":0,\"threshold\":1}]}]},\"credentials\":[]}")
}

func TestServiceGetTxJSON_OperationTxWithNftxMintOp(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	mintNFTTx := buildOperationTxWithOp(buildNFTxMintOp(createAssetTx, key, 2, 1))
	err = mintNFTTx.SignNFTFx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintNFTTx.Bytes())
	require.NoError(err)
	require.Equal(mintNFTTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)
	// assert memo and payload are in hex
	require.Contains(jsonString, "\"memo\":\"0x\"")
	require.Contains(jsonString, "\"payload\":\"0x68656c6c6f\"")
	// contains the address in the right format
	require.Contains(jsonString, "\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")
	// contains the fxID
	require.Contains(jsonString, "\"operations\":[{\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":2}],\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\",\"credential\":{\"signatures\":[\"0x571f18cfdb254263ab6b987f742409bd5403eafe08b4dbc297c5cd8d1c85eb8812e4541e11d3dc692cd14b5f4bccc1835ec001df6d8935ce881caf97017c2a4801\"]}}]")
}

func TestServiceGetTxJSON_OperationTxWithMultipleNftxMintOp(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	mintOp1 := buildNFTxMintOp(createAssetTx, key, 2, 1)
	mintOp2 := buildNFTxMintOp(createAssetTx, key, 3, 2)
	mintNFTTx := buildOperationTxWithOp(mintOp1, mintOp2)

	err = mintNFTTx.SignNFTFx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}, {key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintNFTTx.Bytes())
	require.NoError(err)
	require.Equal(mintNFTTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// contains the address in the right format
	require.Contains(jsonString, "\"outputs\":[{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")

	// contains the fxID
	require.Contains(jsonString, "\"operations\":[{\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":2}],\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\",\"credential\":{\"signatures\":[\"0x2400cf2cf978697b3484d5340609b524eb9dfa401e5b2bd5d1bc6cee2a6b1ae41926550f00ae0651c312c35e225cb3f39b506d96c5170fb38a820dcfed11ccd801\"]}},{\"fxID\":\"TtF4d2QWbk5vzQGTEPrN48x6vwgAoAmKQ9cbp79inpQmcRKES\",\"credential\":{\"signatures\":[\"0x2400cf2cf978697b3484d5340609b524eb9dfa401e5b2bd5d1bc6cee2a6b1ae41926550f00ae0651c312c35e225cb3f39b506d96c5170fb38a820dcfed11ccd801\"]}}]")
}

func TestServiceGetTxJSON_OperationTxWithSecpMintOp(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	mintSecpOpTx := buildOperationTxWithOp(buildSecpMintOp(createAssetTx, key, 0))
	err = mintSecpOpTx.SignSECP256K1Fx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintSecpOpTx.Bytes())
	require.NoError(err)
	require.Equal(mintSecpOpTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// ensure memo is in hex
	require.Contains(jsonString, "\"memo\":\"0x\"")
	// contains the address in the right format
	require.Contains(jsonString, "\"mintOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")
	require.Contains(jsonString, "\"transferOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"amount\":1,\"locktime\":0,\"threshold\":1}}}]}")

	// contains the fxID
	require.Contains(jsonString, "\"operations\":[{\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":0}],\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\",\"credential\":{\"signatures\":[\"0x6d7406d5e1bdb1d80de542e276e2d162b0497d0df1170bec72b14d40e84ecf7929cb571211d60149404413a9342fdfa0a2b5d07b48e6f3eaea1e2f9f183b480500\"]}}]")
}

func TestServiceGetTxJSON_OperationTxWithMultipleSecpMintOp(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	op1 := buildSecpMintOp(createAssetTx, key, 0)
	op2 := buildSecpMintOp(createAssetTx, key, 1)
	mintSecpOpTx := buildOperationTxWithOp(op1, op2)

	err = mintSecpOpTx.SignSECP256K1Fx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}, {key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintSecpOpTx.Bytes())
	require.NoError(err)
	require.Equal(mintSecpOpTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// contains the address in the right format
	require.Contains(jsonString, "\"mintOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")
	require.Contains(jsonString, "\"transferOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"],\"amount\":1,\"locktime\":0,\"threshold\":1}}}")

	// contains the fxID
	require.Contains(jsonString, "\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":1}],\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\",\"credential\":{\"signatures\":[\"0xcc650f48341601c348d8634e8d207e07ea7b4ee4fbdeed3055fa1f1e4f4e27556d25056447a3bd5d949e5f1cbb0155bb20216ac3a4055356e3c82dca74323e7401\"]}},{\"fxID\":\"LUC1cmcxnfNR9LdkACS2ccGKLEK7SYqB4gLLTycQfg1koyfSq\",\"credential\":{\"signatures\":[\"0xcc650f48341601c348d8634e8d207e07ea7b4ee4fbdeed3055fa1f1e4f4e27556d25056447a3bd5d949e5f1cbb0155bb20216ac3a4055356e3c82dca74323e7401\"]}}]")
}

func TestServiceGetTxJSON_OperationTxWithPropertyFxMintOp(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	mintPropertyFxOpTx := buildOperationTxWithOp(buildPropertyFxMintOp(createAssetTx, key, 4))
	err = mintPropertyFxOpTx.SignPropertyFx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintPropertyFxOpTx.Bytes())
	require.NoError(err)
	require.Equal(mintPropertyFxOpTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// ensure memo is in hex
	require.Contains(jsonString, "\"memo\":\"0x\"")
	// contains the address in the right format
	require.Contains(jsonString, "\"mintOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")

	// contains the fxID
	require.Contains(jsonString, "\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":4}],\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\",\"credential\":{\"signatures\":[\"0xa3a00a03d3f1551ff696d6c0abdde73ae7002cd6dcce1c37d720de3b7ed80757411c9698cd9681a0fa55ca685904ca87056a3b8abc858a8ac08f45483b32a80201\"]}}]")
}

func TestServiceGetTxJSON_OperationTxWithPropertyFxMintOpMultiple(t *testing.T) {
	require := require.New(t)

	vm := &VM{}
	ctx := NewContext(t)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	baseDBManager := manager.NewMemDB(version.Semantic1_0_0)
	m := atomic.NewMemory(prefixdb.New([]byte{0}, baseDBManager.Current().Database))
	ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	genesisBytes := BuildGenesisTest(t)
	issuer := make(chan common.Message, 1)
	err := vm.Initialize(
		context.Background(),
		ctx,
		baseDBManager,
		genesisBytes,
		nil,
		nil,
		issuer,
		[]*common.Fx{
			{
				ID: ids.Empty.Prefix(0),
				Fx: &secp256k1fx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(1),
				Fx: &nftfx.Fx{},
			},
			{
				ID: ids.Empty.Prefix(2),
				Fx: &propertyfx.Fx{},
			},
		},
		&common.SenderTest{T: t},
	)
	require.NoError(err)
	vm.batchTimeout = 0

	require.NoError(vm.SetState(context.Background(), snow.Bootstrapping))
	require.NoError(vm.SetState(context.Background(), snow.NormalOp))

	key := keys[0]
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, vm)
	_, err = vm.IssueTx(createAssetTx.Bytes())
	require.NoError(err)

	op1 := buildPropertyFxMintOp(createAssetTx, key, 4)
	op2 := buildPropertyFxMintOp(createAssetTx, key, 5)
	mintPropertyFxOpTx := buildOperationTxWithOp(op1, op2)

	err = mintPropertyFxOpTx.SignPropertyFx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}, {key}})
	require.NoError(err)

	txID, err := vm.IssueTx(mintPropertyFxOpTx.Bytes())
	require.NoError(err)
	require.Equal(mintPropertyFxOpTx.ID(), txID)
	ctx.Lock.Unlock()

	msg := <-issuer
	require.Equal(common.PendingTxs, msg)
	ctx.Lock.Lock()

	txs := vm.PendingTxs(context.Background())
	require.Len(txs, 2)
	require.NoError(txs[0].Accept(context.Background()))
	require.NoError(txs[1].Accept(context.Background()))

	reply := api.GetTxReply{}
	s := &Service{vm: vm}
	err = s.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.JSON,
	}, &reply)
	require.NoError(err)

	require.Equal(reply.Encoding, formatting.JSON)
	jsonTxBytes, err := stdjson.Marshal(reply.Tx)
	require.NoError(err)
	jsonString := string(jsonTxBytes)

	// contains the address in the right format
	require.Contains(jsonString, "\"mintOutput\":{\"addresses\":[\"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e\"]")

	// contains the fxID
	require.Contains(jsonString, "\"operations\":[{\"assetID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"inputIDs\":[{\"txID\":\"2MDgrsBHMRsEPa4D4NA1Bo1pjkVLUK173S3dd9BgT2nCJNiDuS\",\"outputIndex\":4}],\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\"")
	require.Contains(jsonString, "\"credentials\":[{\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\",\"credential\":{\"signatures\":[\"0x25b7ca14df108d4a32877bda4f10d84eda6d653c620f4c8d124265bdcf0ac91f45712b58b33f4b62a19698325a3c89adff214b77f772d9f311742860039abb5601\"]}},{\"fxID\":\"2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w\",\"credential\":{\"signatures\":[\"0x25b7ca14df108d4a32877bda4f10d84eda6d653c620f4c8d124265bdcf0ac91f45712b58b33f4b62a19698325a3c89adff214b77f772d9f311742860039abb5601\"]}}]")
}

func newAvaxBaseTxWithOutputs(t *testing.T, genesisBytes []byte, vm *VM) *txs.Tx {
	avaxTx := GetAVAXTxFromGenesisTest(genesisBytes, t)
	key := keys[0]
	tx := buildBaseTx(avaxTx, vm, key)
	if err := tx.SignSECP256K1Fx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}}); err != nil {
		t.Fatal(err)
	}
	return tx
}

func newAvaxExportTxWithOutputs(t *testing.T, genesisBytes []byte, vm *VM) *txs.Tx {
	avaxTx := GetAVAXTxFromGenesisTest(genesisBytes, t)
	key := keys[0]
	tx := buildExportTx(avaxTx, vm, key)
	if err := tx.SignSECP256K1Fx(vm.parser.Codec(), [][]*secp256k1.PrivateKey{{key}}); err != nil {
		t.Fatal(err)
	}
	return tx
}

func newAvaxCreateAssetTxWithOutputs(t *testing.T, vm *VM) *txs.Tx {
	key := keys[0]
	tx := buildCreateAssetTx(key)
	if err := vm.parser.InitializeTx(tx); err != nil {
		t.Fatal(err)
	}
	return tx
}

func buildBaseTx(avaxTx *txs.Tx, vm *VM, key *secp256k1.PrivateKey) *txs.Tx {
	return &txs.Tx{Unsigned: &txs.BaseTx{
		BaseTx: avax.BaseTx{
			NetworkID:    constants.UnitTestID,
			BlockchainID: chainID,
			Memo:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
			Ins: []*avax.TransferableInput{{
				UTXOID: avax.UTXOID{
					TxID:        avaxTx.ID(),
					OutputIndex: 2,
				},
				Asset: avax.Asset{ID: avaxTx.ID()},
				In: &secp256k1fx.TransferInput{
					Amt: startBalance,
					Input: secp256k1fx.Input{
						SigIndices: []uint32{
							0,
						},
					},
				},
			}},
			Outs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: avaxTx.ID()},
				Out: &secp256k1fx.TransferOutput{
					Amt: startBalance - vm.TxFee,
					OutputOwners: secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{key.PublicKey().Address()},
					},
				},
			}},
		},
	}}
}

func buildExportTx(avaxTx *txs.Tx, vm *VM, key *secp256k1.PrivateKey) *txs.Tx {
	return &txs.Tx{Unsigned: &txs.ExportTx{
		BaseTx: txs.BaseTx{
			BaseTx: avax.BaseTx{
				NetworkID:    constants.UnitTestID,
				BlockchainID: chainID,
				Ins: []*avax.TransferableInput{{
					UTXOID: avax.UTXOID{
						TxID:        avaxTx.ID(),
						OutputIndex: 2,
					},
					Asset: avax.Asset{ID: avaxTx.ID()},
					In: &secp256k1fx.TransferInput{
						Amt:   startBalance,
						Input: secp256k1fx.Input{SigIndices: []uint32{0}},
					},
				}},
			},
		},
		DestinationChain: constants.PlatformChainID,
		ExportedOuts: []*avax.TransferableOutput{{
			Asset: avax.Asset{ID: avaxTx.ID()},
			Out: &secp256k1fx.TransferOutput{
				Amt: startBalance - vm.TxFee,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		}},
	}}
}

func buildCreateAssetTx(key *secp256k1.PrivateKey) *txs.Tx {
	return &txs.Tx{Unsigned: &txs.CreateAssetTx{
		BaseTx: txs.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    constants.UnitTestID,
			BlockchainID: chainID,
		}},
		Name:         "Team Rocket",
		Symbol:       "TR",
		Denomination: 0,
		States: []*txs.InitialState{
			{
				FxIndex: 0,
				Outs: []verify.State{
					&secp256k1fx.MintOutput{
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{key.PublicKey().Address()},
						},
					}, &secp256k1fx.MintOutput{
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{key.PublicKey().Address()},
						},
					},
				},
			},
			{
				FxIndex: 1,
				Outs: []verify.State{
					&nftfx.MintOutput{
						GroupID: 1,
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{key.PublicKey().Address()},
						},
					},
					&nftfx.MintOutput{
						GroupID: 2,
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{key.PublicKey().Address()},
						},
					},
				},
			},
			{
				FxIndex: 2,
				Outs: []verify.State{
					&propertyfx.MintOutput{
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
						},
					},
					&propertyfx.MintOutput{
						OutputOwners: secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
						},
					},
				},
			},
		},
	}}
}

func buildNFTxMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex, groupID uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &nftfx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			GroupID: groupID,
			Payload: []byte{'h', 'e', 'l', 'l', 'o'},
			Outputs: []*secp256k1fx.OutputOwners{{
				Threshold: 1,
				Addrs:     []ids.ShortID{key.PublicKey().Address()},
			}},
		},
	}
}

func buildPropertyFxMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &propertyfx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			MintOutput: propertyfx.MintOutput{OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs: []ids.ShortID{
					key.PublicKey().Address(),
				},
			}},
		},
	}
}

func buildSecpMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &secp256k1fx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			MintOutput: secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs: []ids.ShortID{
						key.PublicKey().Address(),
					},
				},
			},
			TransferOutput: secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Locktime:  0,
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		},
	}
}

func buildOperationTxWithOp(op ...*txs.Operation) *txs.Tx {
	return &txs.Tx{Unsigned: &txs.OperationTx{
		BaseTx: txs.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    constants.UnitTestID,
			BlockchainID: chainID,
		}},
		Ops: op,
	}}
}

func TestServiceGetNilTx(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	reply := api.GetTxReply{}
	err := s.GetTx(nil, &api.GetTxArgs{}, &reply)
	require.Error(t, err, "Nil TxID should have returned an error")
}

func TestServiceGetUnknownTx(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	reply := api.GetTxReply{}
	err := s.GetTx(nil, &api.GetTxArgs{TxID: ids.Empty}, &reply)
	require.Error(t, err, "Unknown TxID should have returned an error")
}

func TestServiceGetUTXOs(t *testing.T) {
	_, vm, s, m, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	rawAddr := ids.GenerateTestShortID()
	rawEmptyAddr := ids.GenerateTestShortID()

	numUTXOs := 10
	// Put a bunch of UTXOs
	for i := 0; i < numUTXOs; i++ {
		utxo := &avax.UTXO{
			UTXOID: avax.UTXOID{
				TxID: ids.GenerateTestID(),
			},
			Asset: avax.Asset{ID: vm.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{rawAddr},
				},
			},
		}
		vm.state.AddUTXO(utxo)
	}
	require.NoError(t, vm.state.Commit())

	sm := m.NewSharedMemory(constants.PlatformChainID)

	elems := make([]*atomic.Element, numUTXOs)
	codec := vm.parser.Codec()
	for i := range elems {
		utxo := &avax.UTXO{
			UTXOID: avax.UTXOID{
				TxID: ids.GenerateTestID(),
			},
			Asset: avax.Asset{ID: vm.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{rawAddr},
				},
			},
		}

		utxoBytes, err := codec.Marshal(txs.CodecVersion, utxo)
		if err != nil {
			t.Fatal(err)
		}
		utxoID := utxo.InputID()
		elems[i] = &atomic.Element{
			Key:   utxoID[:],
			Value: utxoBytes,
			Traits: [][]byte{
				rawAddr.Bytes(),
			},
		}
	}

	if err := sm.Apply(map[ids.ID]*atomic.Requests{vm.ctx.ChainID: {PutRequests: elems}}); err != nil {
		t.Fatal(err)
	}

	hrp := constants.GetHRP(vm.ctx.NetworkID)
	xAddr, err := vm.FormatLocalAddress(rawAddr)
	if err != nil {
		t.Fatal(err)
	}
	pAddr, err := vm.FormatAddress(constants.PlatformChainID, rawAddr)
	if err != nil {
		t.Fatal(err)
	}
	unknownChainAddr, err := address.Format("R", hrp, rawAddr.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	xEmptyAddr, err := vm.FormatLocalAddress(rawEmptyAddr)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		label     string
		count     int
		shouldErr bool
		args      *api.GetUTXOsArgs
	}{
		{
			label:     "invalid address: ''",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{""},
			},
		},
		{
			label:     "invalid address: '-'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{"-"},
			},
		},
		{
			label:     "invalid address: 'foo'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{"foo"},
			},
		},
		{
			label:     "invalid address: 'foo-bar'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{"foo-bar"},
			},
		},
		{
			label:     "invalid address: '<ChainID>'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{vm.ctx.ChainID.String()},
			},
		},
		{
			label:     "invalid address: '<ChainID>-'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{fmt.Sprintf("%s-", vm.ctx.ChainID.String())},
			},
		},
		{
			label:     "invalid address: '<Unknown ID>-<addr>'",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{unknownChainAddr},
			},
		},
		{
			label:     "no addresses",
			shouldErr: true,
			args:      &api.GetUTXOsArgs{},
		},
		{
			label: "get all X-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
			},
		},
		{
			label: "get one X-chain UTXO",
			count: 1,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				Limit: 1,
			},
		},
		{
			label: "limit greater than number of UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				Limit: json.Uint32(numUTXOs + 1),
			},
		},
		{
			label: "no utxos to return",
			count: 0,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xEmptyAddr,
				},
			},
		},
		{
			label: "multiple address with utxos",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xEmptyAddr,
					xAddr,
				},
			},
		},
		{
			label: "get all P-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "P",
			},
		},
		{
			label:     "invalid source chain ID",
			shouldErr: true,
			count:     numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "HomeRunDerby",
			},
		},
		{
			label: "get all P-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "P",
			},
		},
		{
			label:     "get UTXOs from multiple chains",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
					pAddr,
				},
			},
		},
		{
			label:     "get UTXOs for an address on a different chain",
			shouldErr: true,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					pAddr,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			reply := &api.GetUTXOsReply{}
			err := s.GetUTXOs(nil, test.args, reply)
			if err != nil {
				if !test.shouldErr {
					t.Fatal(err)
				}
				return
			}
			if test.shouldErr {
				t.Fatal("should have erred")
			}
			if test.count != len(reply.UTXOs) {
				t.Fatalf("Expected %d utxos, got %d", test.count, len(reply.UTXOs))
			}
		})
	}
}

func TestGetAssetDescription(t *testing.T) {
	_, vm, s, _, genesisTx := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	avaxAssetID := genesisTx.ID()

	reply := GetAssetDescriptionReply{}
	err := s.GetAssetDescription(nil, &GetAssetDescriptionArgs{
		AssetID: avaxAssetID.String(),
	}, &reply)
	if err != nil {
		t.Fatal(err)
	}

	if reply.Name != "AVAX" {
		t.Fatalf("Wrong name returned from GetAssetDescription %s", reply.Name)
	}
	if reply.Symbol != "SYMB" {
		t.Fatalf("Wrong name returned from GetAssetDescription %s", reply.Symbol)
	}
}

func TestGetBalance(t *testing.T) {
	_, vm, s, _, genesisTx := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	avaxAssetID := genesisTx.ID()

	reply := GetBalanceReply{}
	addrStr, err := vm.FormatLocalAddress(keys[0].PublicKey().Address())
	if err != nil {
		t.Fatal(err)
	}
	err = s.GetBalance(nil, &GetBalanceArgs{
		Address: addrStr,
		AssetID: avaxAssetID.String(),
	}, &reply)
	if err != nil {
		t.Fatal(err)
	}

	if uint64(reply.Balance) != startBalance {
		t.Fatalf("Wrong balance returned from GetBalance %d", reply.Balance)
	}
}

func TestCreateFixedCapAsset(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, vm, s, _, _ := setupWithKeys(t, tc.avaxAsset)
			defer func() {
				if err := vm.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				vm.ctx.Lock.Unlock()
			}()

			reply := AssetIDChangeAddr{}
			addrStr, err := vm.FormatLocalAddress(keys[0].PublicKey().Address())
			if err != nil {
				t.Fatal(err)
			}

			changeAddrStr, err := vm.FormatLocalAddress(testChangeAddr)
			if err != nil {
				t.Fatal(err)
			}
			_, fromAddrsStr := sampleAddrs(t, vm, addrs)

			err = s.CreateFixedCapAsset(nil, &CreateAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Name:         "testAsset",
				Symbol:       "TEST",
				Denomination: 1,
				InitialHolders: []*Holder{{
					Amount:  123456789,
					Address: addrStr,
				}},
			}, &reply)
			if err != nil {
				t.Fatal(err)
			} else if reply.ChangeAddr != changeAddrStr {
				t.Fatalf("expected change address %s but got %s", changeAddrStr, reply.ChangeAddr)
			}
		})
	}
}

func TestCreateVariableCapAsset(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, vm, s, _, _ := setupWithKeys(t, tc.avaxAsset)
			defer func() {
				if err := vm.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				vm.ctx.Lock.Unlock()
			}()

			reply := AssetIDChangeAddr{}
			minterAddrStr, err := vm.FormatLocalAddress(keys[0].PublicKey().Address())
			if err != nil {
				t.Fatal(err)
			}
			_, fromAddrsStr := sampleAddrs(t, vm, addrs)
			changeAddrStr := fromAddrsStr[0]
			if err != nil {
				t.Fatal(err)
			}

			err = s.CreateVariableCapAsset(nil, &CreateAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Name:   "test asset",
				Symbol: "TEST",
				MinterSets: []Owners{
					{
						Threshold: 1,
						Minters: []string{
							minterAddrStr,
						},
					},
				},
			}, &reply)
			if err != nil {
				t.Fatal(err)
			} else if reply.ChangeAddr != changeAddrStr {
				t.Fatalf("expected change address %s but got %s", changeAddrStr, reply.ChangeAddr)
			}

			createAssetTx := UniqueTx{
				vm:   vm,
				txID: reply.AssetID,
			}
			if status := createAssetTx.Status(); status != choices.Processing {
				t.Fatalf("CreateVariableCapAssetTx status should have been Processing, but was %s", status)
			}
			if err := createAssetTx.Accept(context.Background()); err != nil {
				t.Fatalf("Failed to accept CreateVariableCapAssetTx due to: %s", err)
			}

			createdAssetID := reply.AssetID.String()
			// Test minting of the created variable cap asset
			mintArgs := &MintArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Amount:  200,
				AssetID: createdAssetID,
				To:      minterAddrStr, // Send newly minted tokens to this address
			}
			mintReply := &api.JSONTxIDChangeAddr{}
			if err := s.Mint(nil, mintArgs, mintReply); err != nil {
				t.Fatalf("Failed to mint variable cap asset due to: %s", err)
			} else if mintReply.ChangeAddr != changeAddrStr {
				t.Fatalf("expected change address %s but got %s", changeAddrStr, mintReply.ChangeAddr)
			}

			mintTx := UniqueTx{
				vm:   vm,
				txID: mintReply.TxID,
			}

			if status := mintTx.Status(); status != choices.Processing {
				t.Fatalf("MintTx status should have been Processing, but was %s", status)
			}
			if err := mintTx.Accept(context.Background()); err != nil {
				t.Fatalf("Failed to accept MintTx due to: %s", err)
			}

			sendArgs := &SendArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: []string{minterAddrStr}},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				SendOutput: SendOutput{
					Amount:  200,
					AssetID: createdAssetID,
					To:      fromAddrsStr[0],
				},
			}
			sendReply := &api.JSONTxIDChangeAddr{}
			if err := s.Send(nil, sendArgs, sendReply); err != nil {
				t.Fatalf("Failed to send newly minted variable cap asset due to: %s", err)
			} else if sendReply.ChangeAddr != changeAddrStr {
				t.Fatalf("expected change address to be %s but got %s", changeAddrStr, sendReply.ChangeAddr)
			}
		})
	}
}

func TestNFTWorkflow(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, vm, s, _, _ := setupWithKeys(t, tc.avaxAsset)
			defer func() {
				if err := vm.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				vm.ctx.Lock.Unlock()
			}()

			fromAddrs, fromAddrsStr := sampleAddrs(t, vm, addrs)

			// Test minting of the created variable cap asset
			addrStr, err := vm.FormatLocalAddress(keys[0].PublicKey().Address())
			if err != nil {
				t.Fatal(err)
			}

			createArgs := &CreateNFTAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				Name:   "BIG COIN",
				Symbol: "COIN",
				MinterSets: []Owners{
					{
						Threshold: 1,
						Minters: []string{
							addrStr,
						},
					},
				},
			}
			createReply := &AssetIDChangeAddr{}
			if err := s.CreateNFTAsset(nil, createArgs, createReply); err != nil {
				t.Fatalf("Failed to mint variable cap asset due to: %s", err)
			} else if createReply.ChangeAddr != fromAddrsStr[0] {
				t.Fatalf("expected change address to be %s but got %s", fromAddrsStr[0], createReply.ChangeAddr)
			}

			assetID := createReply.AssetID
			createNFTTx := UniqueTx{
				vm:   vm,
				txID: createReply.AssetID,
			}
			// Accept the transaction so that we can Mint NFTs for the test
			if createNFTTx.Status() != choices.Processing {
				t.Fatalf("CreateNFTTx should have been processing after creating the NFT")
			}
			if err := createNFTTx.Accept(context.Background()); err != nil {
				t.Fatalf("Failed to accept CreateNFT transaction: %s", err)
			} else if err := verifyTxFeeDeducted(t, s, fromAddrs, 1); err != nil {
				t.Fatal(err)
			}

			payload, err := formatting.Encode(formatting.Hex, []byte{1, 2, 3, 4, 5})
			if err != nil {
				t.Fatal(err)
			}
			mintArgs := &MintNFTArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				AssetID:  assetID.String(),
				Payload:  payload,
				To:       addrStr,
				Encoding: formatting.Hex,
			}
			mintReply := &api.JSONTxIDChangeAddr{}

			if err := s.MintNFT(nil, mintArgs, mintReply); err != nil {
				t.Fatalf("MintNFT returned an error: %s", err)
			} else if createReply.ChangeAddr != fromAddrsStr[0] {
				t.Fatalf("expected change address to be %s but got %s", fromAddrsStr[0], mintReply.ChangeAddr)
			}

			mintNFTTx := UniqueTx{
				vm:   vm,
				txID: mintReply.TxID,
			}
			if mintNFTTx.Status() != choices.Processing {
				t.Fatal("MintNFTTx should have been processing after minting the NFT")
			}

			// Accept the transaction so that we can send the newly minted NFT
			if err := mintNFTTx.Accept(context.Background()); err != nil {
				t.Fatalf("Failed to accept MintNFTTx: %s", err)
			}

			sendArgs := &SendNFTArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				AssetID: assetID.String(),
				GroupID: 0,
				To:      addrStr,
			}
			sendReply := &api.JSONTxIDChangeAddr{}
			if err := s.SendNFT(nil, sendArgs, sendReply); err != nil {
				t.Fatalf("Failed to send NFT due to: %s", err)
			} else if sendReply.ChangeAddr != fromAddrsStr[0] {
				t.Fatalf("expected change address to be %s but got %s", fromAddrsStr[0], sendReply.ChangeAddr)
			}
		})
	}
}

func TestImportExportKey(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	factory := secp256k1.Factory{}
	sk, err := factory.NewPrivateKey()
	if err != nil {
		t.Fatalf("problem generating private key: %s", err)
	}

	importArgs := &ImportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		PrivateKey: sk,
	}
	importReply := &api.JSONAddress{}
	if err := s.ImportKey(nil, importArgs, importReply); err != nil {
		t.Fatal(err)
	}

	addrStr, err := vm.FormatLocalAddress(sk.PublicKey().Address())
	if err != nil {
		t.Fatal(err)
	}
	exportArgs := &ExportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		Address: addrStr,
	}
	exportReply := &ExportKeyReply{}
	if err := s.ExportKey(nil, exportArgs, exportReply); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(sk.Bytes(), exportReply.PrivateKey.Bytes()) {
		t.Fatal("Unexpected key was found in ExportKeyReply")
	}
}

func TestImportAVMKeyNoDuplicates(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	ctx := vm.ctx
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx.Lock.Unlock()
	}()

	factory := secp256k1.Factory{}
	sk, err := factory.NewPrivateKey()
	if err != nil {
		t.Fatalf("problem generating private key: %s", err)
	}
	args := ImportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		PrivateKey: sk,
	}
	reply := api.JSONAddress{}
	if err := s.ImportKey(nil, &args, &reply); err != nil {
		t.Fatal(err)
	}

	expectedAddress, err := vm.FormatLocalAddress(sk.PublicKey().Address())
	if err != nil {
		t.Fatal(err)
	}

	if reply.Address != expectedAddress {
		t.Fatalf("Reply address: %s did not match expected address: %s", reply.Address, expectedAddress)
	}

	reply2 := api.JSONAddress{}
	if err := s.ImportKey(nil, &args, &reply2); err != nil {
		t.Fatal(err)
	}

	if reply2.Address != expectedAddress {
		t.Fatalf("Reply address: %s did not match expected address: %s", reply2.Address, expectedAddress)
	}

	addrsArgs := api.UserPass{
		Username: username,
		Password: password,
	}
	addrsReply := api.JSONAddresses{}
	if err := s.ListAddresses(nil, &addrsArgs, &addrsReply); err != nil {
		t.Fatal(err)
	}

	if len(addrsReply.Addresses) != 1 {
		t.Fatal("Importing the same key twice created duplicate addresses")
	}

	if addrsReply.Addresses[0] != expectedAddress {
		t.Fatal("List addresses returned an incorrect address")
	}
}

func TestSend(t *testing.T) {
	_, vm, s, _, genesisTx := setupWithKeys(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	assetID := genesisTx.ID()
	addr := keys[0].PublicKey().Address()

	addrStr, err := vm.FormatLocalAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	changeAddrStr, err := vm.FormatLocalAddress(testChangeAddr)
	if err != nil {
		t.Fatal(err)
	}
	_, fromAddrsStr := sampleAddrs(t, vm, addrs)

	args := &SendArgs{
		JSONSpendHeader: api.JSONSpendHeader{
			UserPass: api.UserPass{
				Username: username,
				Password: password,
			},
			JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
			JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
		},
		SendOutput: SendOutput{
			Amount:  500,
			AssetID: assetID.String(),
			To:      addrStr,
		},
	}
	reply := &api.JSONTxIDChangeAddr{}
	vm.timer.Cancel()
	if err := s.Send(nil, args, reply); err != nil {
		t.Fatalf("Failed to send transaction: %s", err)
	} else if reply.ChangeAddr != changeAddrStr {
		t.Fatalf("expected change address to be %s but got %s", changeAddrStr, reply.ChangeAddr)
	}

	pendingTxs := vm.txs
	if len(pendingTxs) != 1 {
		t.Fatalf("Expected to find 1 pending tx after send, but found %d", len(pendingTxs))
	}

	if reply.TxID != pendingTxs[0].ID() {
		t.Fatal("Transaction ID returned by Send does not match the transaction found in vm's pending transactions")
	}
}

func TestSendMultiple(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, vm, s, _, genesisTx := setupWithKeys(t, tc.avaxAsset)
			defer func() {
				if err := vm.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				vm.ctx.Lock.Unlock()
			}()

			assetID := genesisTx.ID()
			addr := keys[0].PublicKey().Address()

			addrStr, err := vm.FormatLocalAddress(addr)
			if err != nil {
				t.Fatal(err)
			}
			changeAddrStr, err := vm.FormatLocalAddress(testChangeAddr)
			if err != nil {
				t.Fatal(err)
			}
			_, fromAddrsStr := sampleAddrs(t, vm, addrs)

			args := &SendMultipleArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Outputs: []SendOutput{
					{
						Amount:  500,
						AssetID: assetID.String(),
						To:      addrStr,
					},
					{
						Amount:  1000,
						AssetID: assetID.String(),
						To:      addrStr,
					},
				},
			}
			reply := &api.JSONTxIDChangeAddr{}
			vm.timer.Cancel()
			if err := s.SendMultiple(nil, args, reply); err != nil {
				t.Fatalf("Failed to send transaction: %s", err)
			} else if reply.ChangeAddr != changeAddrStr {
				t.Fatalf("expected change address to be %s but got %s", changeAddrStr, reply.ChangeAddr)
			}

			pendingTxs := vm.txs
			if len(pendingTxs) != 1 {
				t.Fatalf("Expected to find 1 pending tx after send, but found %d", len(pendingTxs))
			}

			if reply.TxID != pendingTxs[0].ID() {
				t.Fatal("Transaction ID returned by SendMultiple does not match the transaction found in vm's pending transactions")
			}

			if _, err := vm.GetTx(context.Background(), reply.TxID); err != nil {
				t.Fatalf("Failed to retrieve created transaction: %s", err)
			}
		})
	}
}

func TestCreateAndListAddresses(t *testing.T) {
	_, vm, s, _, _ := setup(t, true)
	defer func() {
		if err := vm.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		vm.ctx.Lock.Unlock()
	}()

	createArgs := &api.UserPass{
		Username: username,
		Password: password,
	}
	createReply := &api.JSONAddress{}

	if err := s.CreateAddress(nil, createArgs, createReply); err != nil {
		t.Fatalf("Failed to create address: %s", err)
	}

	newAddr := createReply.Address

	listArgs := &api.UserPass{
		Username: username,
		Password: password,
	}
	listReply := &api.JSONAddresses{}

	if err := s.ListAddresses(nil, listArgs, listReply); err != nil {
		t.Fatalf("Failed to list addresses: %s", err)
	}

	for _, addr := range listReply.Addresses {
		if addr == newAddr {
			return
		}
	}
	t.Fatalf("Failed to find newly created address among %d addresses", len(listReply.Addresses))
}

func TestImport(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, vm, s, m, genesisTx := setupWithKeys(t, tc.avaxAsset)
			defer func() {
				if err := vm.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				vm.ctx.Lock.Unlock()
			}()
			assetID := genesisTx.ID()
			addr0 := keys[0].PublicKey().Address()

			utxo := &avax.UTXO{
				UTXOID: avax.UTXOID{TxID: ids.Empty},
				Asset:  avax.Asset{ID: assetID},
				Out: &secp256k1fx.TransferOutput{
					Amt: 7,
					OutputOwners: secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{addr0},
					},
				},
			}
			utxoBytes, err := vm.parser.Codec().Marshal(txs.CodecVersion, utxo)
			if err != nil {
				t.Fatal(err)
			}

			peerSharedMemory := m.NewSharedMemory(constants.PlatformChainID)
			utxoID := utxo.InputID()
			if err := peerSharedMemory.Apply(map[ids.ID]*atomic.Requests{vm.ctx.ChainID: {PutRequests: []*atomic.Element{{
				Key:   utxoID[:],
				Value: utxoBytes,
				Traits: [][]byte{
					addr0.Bytes(),
				},
			}}}}); err != nil {
				t.Fatal(err)
			}

			addrStr, err := vm.FormatLocalAddress(keys[0].PublicKey().Address())
			if err != nil {
				t.Fatal(err)
			}
			args := &ImportArgs{
				UserPass: api.UserPass{
					Username: username,
					Password: password,
				},
				SourceChain: "P",
				To:          addrStr,
			}
			reply := &api.JSONTxID{}
			if err := s.Import(nil, args, reply); err != nil {
				t.Fatalf("Failed to import AVAX due to %s", err)
			}
		})
	}
}

func TestServiceGetBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	blockID := ids.GenerateTestID()

	type test struct {
		name                        string
		serviceAndExpectedBlockFunc func(ctrl *gomock.Controller) (*Service, interface{})
		encoding                    formatting.Encoding
		expectedErr                 error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: errNotLinearized,
		},
		{
			name: "block not found",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "JSON format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				block.EXPECT().InitCtx(gomock.Any())
				block.EXPECT().Txs().Return(nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, block
			},
			encoding:    formatting.JSON,
			expectedErr: nil,
		},
		{
			name: "hex format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.Hex, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.Hex,
			expectedErr: nil,
		},
		{
			name: "hexc format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.HexC, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexC,
			expectedErr: nil,
		},
		{
			name: "hexnc format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.HexNC, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexNC,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, expected := tt.serviceAndExpectedBlockFunc(ctrl)

			args := &api.GetBlockArgs{
				BlockID:  blockID,
				Encoding: tt.encoding,
			}
			reply := &api.GetBlockResponse{}
			err := service.GetBlock(nil, args, reply)
			require.ErrorIs(err, tt.expectedErr)
			if err == nil {
				require.Equal(tt.encoding, reply.Encoding)
				require.Equal(expected, reply.Block)
			}
		})
	}
}

func TestServiceGetBlockByHeight(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	blockID := ids.GenerateTestID()
	blockHeight := uint64(1337)

	type test struct {
		name                        string
		serviceAndExpectedBlockFunc func(ctrl *gomock.Controller) (*Service, interface{})
		encoding                    formatting.Encoding
		expectedErr                 error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: errNotLinearized,
		},
		{
			name: "block height not found",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(ids.Empty, database.ErrNotFound)

				manager := executor.NewMockManager(ctrl)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "block not found",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(blockID, nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "JSON format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				block.EXPECT().InitCtx(gomock.Any())
				block.EXPECT().Txs().Return(nil)

				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(blockID, nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, block
			},
			encoding:    formatting.JSON,
			expectedErr: nil,
		},
		{
			name: "hex format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.Hex, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.Hex,
			expectedErr: nil,
		},
		{
			name: "hexc format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.HexC, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexC,
			expectedErr: nil,
		},
		{
			name: "hexnc format",
			serviceAndExpectedBlockFunc: func(ctrl *gomock.Controller) (*Service, interface{}) {
				block := blocks.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := states.NewMockState(ctrl)
				state.EXPECT().GetBlockID(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.HexNC, blockBytes)
				require.NoError(err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexNC,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, expected := tt.serviceAndExpectedBlockFunc(ctrl)

			args := &api.GetBlockByHeightArgs{
				Height:   blockHeight,
				Encoding: tt.encoding,
			}
			reply := &api.GetBlockResponse{}
			err := service.GetBlockByHeight(nil, args, reply)
			require.ErrorIs(err, tt.expectedErr)
			if err == nil {
				require.Equal(tt.encoding, reply.Encoding)
				require.Equal(expected, reply.Block)
			}
		})
	}
}

func TestServiceGetHeight(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	blockID := ids.GenerateTestID()
	blockHeight := uint64(1337)

	type test struct {
		name        string
		serviceFunc func(ctrl *gomock.Controller) *Service
		expectedErr error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceFunc: func(ctrl *gomock.Controller) *Service {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: errNotLinearized,
		},
		{
			name: "block not found",
			serviceFunc: func(ctrl *gomock.Controller) *Service {
				state := states.NewMockState(ctrl)
				state.EXPECT().GetLastAccepted().Return(blockID)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: database.ErrNotFound,
		},
		{
			name: "happy path",
			serviceFunc: func(ctrl *gomock.Controller) *Service {
				state := states.NewMockState(ctrl)
				state.EXPECT().GetLastAccepted().Return(blockID)

				block := blocks.NewMockBlock(ctrl)
				block.EXPECT().Height().Return(blockHeight)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.serviceFunc(ctrl)

			reply := &api.GetHeightResponse{}
			err := service.GetHeight(nil, nil, reply)
			require.ErrorIs(err, tt.expectedErr)
			if err == nil {
				require.Equal(json.Uint64(blockHeight), reply.Height)
			}
		})
	}
}
