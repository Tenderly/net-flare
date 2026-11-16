// (c) 2026, Flare Network. All rights reserved.
// See the file LICENSE for licensing terms.

package pchaingateway

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tenderly/net-flare/coreth/precompile/precompileconfig"
	"github.com/tenderly/net-flare/coreth/precompile/testutils"
	"github.com/tenderly/net-flare/coreth/utils"
	"go.uber.org/mock/gomock"
)

func TestVerify(t *testing.T) {
	tests := map[string]testutils.ConfigVerifyTest{
		"valid config": {
			Config: NewDefaultConfig(utils.NewUint64(3)),
		},
		"valid disable config": {
			Config: NewDisableConfig(utils.NewUint64(3)),
		},
		"missing timestamp": {
			Config:        &Config{},
			ExpectedError: errNoTimestamp.Error(),
		},
	}
	testutils.RunVerifyTests(t, tests)
}

func TestEqual(t *testing.T) {
	tests := map[string]testutils.ConfigEqualTest{
		"non-nil config and nil other": {
			Config:   NewDefaultConfig(utils.NewUint64(3)),
			Other:    nil,
			Expected: false,
		},
		"different type": {
			Config:   NewDefaultConfig(utils.NewUint64(3)),
			Other:    precompileconfig.NewMockConfig(gomock.NewController(t)),
			Expected: false,
		},
		"different timestamp": {
			Config:   NewDefaultConfig(utils.NewUint64(3)),
			Other:    NewDefaultConfig(utils.NewUint64(4)),
			Expected: false,
		},
		"enable vs disable": {
			Config:   NewDefaultConfig(utils.NewUint64(3)),
			Other:    NewDisableConfig(utils.NewUint64(3)),
			Expected: false,
		},
		"same config": {
			Config:   NewDefaultConfig(utils.NewUint64(3)),
			Other:    NewDefaultConfig(utils.NewUint64(3)),
			Expected: true,
		},
	}
	testutils.RunEqualTests(t, tests)
}

func TestKey(t *testing.T) {
	require.Equal(t, ConfigKey, NewDefaultConfig(nil).Key())
	require.Equal(t, ContractAddress, Module.Address)
}
