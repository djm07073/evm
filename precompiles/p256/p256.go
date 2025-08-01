// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package p256

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"

	"github.com/cosmos/evm/crypto/secp256r1"
	evmtypes "github.com/cosmos/evm/x/vm/types"
)

var _ vm.PrecompiledContract = &Precompile{}

const (
	// VerifyGas is the secp256r1 elliptic curve signature verifier gas price.
	VerifyGas uint64 = 3450
	// VerifyInputLength defines the required input length (160 bytes).
	VerifyInputLength = 160
)

// Precompile secp256r1 (P256) signature verification
// implemented as a native contract as per EIP-7212.
// See https://github.com/ethereum/RIPs/blob/master/RIPS/rip-7212.md for details.
type Precompile struct{}

// Address defines the address of the p256 precompiled contract.
func (Precompile) Address() common.Address {
	return common.HexToAddress(evmtypes.P256PrecompileAddress)
}

// RequiredGas returns the static gas required to execute the precompiled contract.
func (p Precompile) RequiredGas(_ []byte) uint64 {
	return VerifyGas
}

// Run executes the p256 signature verification using ECDSA.
//
// Input data: 160 bytes of data including:
//   - 32 bytes of the signed data hash
//   - 32 bytes of the r component of the signature
//   - 32 bytes of the s component of the signature
//   - 32 bytes of the x coordinate of the public key
//   - 32 bytes of the y coordinate of the public key
//
// Output data: 32 bytes of result data and error
//   - If the signature verification process succeeds, it returns 1 in 32 bytes format
func (p *Precompile) Run(_ *vm.EVM, contract *vm.Contract, _ bool) (bz []byte, err error) {
	input := contract.Input
	// Check the input length
	if len(input) != VerifyInputLength {
		// Input length is invalid
		return nil, nil
	}

	// Extract the hash, r, s, x, y from the input
	hash := input[0:32]
	r, s := new(big.Int).SetBytes(input[32:64]), new(big.Int).SetBytes(input[64:96])
	x, y := new(big.Int).SetBytes(input[96:128]), new(big.Int).SetBytes(input[128:160])

	// Verify the secp256r1 signature
	if secp256r1.Verify(hash, r, s, x, y) {
		// Signature is valid
		result := make([]byte, 32)
		common.Big1.FillBytes(result)
		return result, nil
	}

	// Signature is invalid
	return nil, nil
}
