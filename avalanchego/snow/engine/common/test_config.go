// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package common

import (
	"github.com/tenderly/net-flare/avalanchego/ids"
	"github.com/tenderly/net-flare/avalanchego/snow"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/common/tracker"
	"github.com/tenderly/net-flare/avalanchego/snow/validators"
)

// DefaultConfigTest returns a test configuration
func DefaultConfigTest() Config {
	isBootstrapped := false
	bootstrapTracker := &BootstrapTrackerTest{
		IsBootstrappedF: func() bool {
			return isBootstrapped
		},
		BootstrappedF: func(ids.ID) {
			isBootstrapped = true
		},
	}

	beacons := validators.NewSet()

	connectedPeers := tracker.NewPeers()
	startupTracker := tracker.NewStartup(connectedPeers, 0)
	beacons.RegisterCallbackListener(startupTracker)

	return Config{
		Ctx:                            snow.DefaultConsensusContextTest(),
		Beacons:                        beacons,
		StartupTracker:                 startupTracker,
		Sender:                         &SenderTest{},
		Bootstrapable:                  &BootstrapableTest{},
		BootstrapTracker:               bootstrapTracker,
		Timer:                          &TimerTest{},
		AncestorsMaxContainersSent:     2000,
		AncestorsMaxContainersReceived: 2000,
		SharedCfg:                      &SharedConfig{},
	}
}
