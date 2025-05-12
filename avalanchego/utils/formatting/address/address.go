// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package address

import (
	"errors"
	"fmt"
	"strings"
)

const addressSep = "-"

var (
	errNoSeparator = errors.New("no separator found in address")
	errBits5To8    = errors.New("unable to convert address from 5-bit to 8-bit formatting")
	errBits8To5    = errors.New("unable to convert address from 8-bit to 5-bit formatting")
)

// Parse takes in an address string and splits returns the corresponding parts.
// This returns the chain ID alias, bech32 HRP, address bytes, and an error if
// it occurs.
func Parse(addrStr string) (string, string, []byte, error) {
	addressParts := strings.SplitN(addrStr, addressSep, 2)
	if len(addressParts) < 2 {
		return "", "", nil, errNoSeparator
	}
	chainID := addressParts[0]
	rawAddr := addressParts[1]

	hrp, addr, err := ParseBech32(rawAddr)
	return chainID, hrp, addr, err
}

// Format takes in a chain prefix, HRP, and byte slice to produce a string for
// an address.
func Format(chainIDAlias string, hrp string, addr []byte) (string, error) {
	addrStr, err := FormatBech32(hrp, addr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s%s", chainIDAlias, addressSep, addrStr), nil
}

// ParseBech32 takes a bech32 address as input and returns the HRP and data
// section of a bech32 address
func ParseBech32(addrStr string) (string, []byte, error) {
	return "", []byte{}, nil
}

// FormatBech32 takes an address's bytes as input and returns a bech32 address
func FormatBech32(hrp string, payload []byte) (string, error) {
	return "", nil
}
