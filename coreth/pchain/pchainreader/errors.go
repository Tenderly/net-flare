package pchainreader

import "errors"

// ErrInvalidData is P-chain data from the store that does not parse or does
// not match the requested key.
var ErrInvalidData = errors.New("invalid P-chain data")
