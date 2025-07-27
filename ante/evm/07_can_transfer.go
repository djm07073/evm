package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core"
	"github.com/holiman/uint256"

	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
)

// CheckInsufficientBalance checks if the sender has enough balance for fee + value
func CheckInsufficientBalance(
	ctx sdk.Context,
	evmKeeper anteinterfaces.EVMKeeper,
	msg core.Message,
	baseFee *big.Int,
	params evmtypes.Params,
	isLondon bool,
) error {
	if isLondon && msg.GasFeeCap.Cmp(baseFee) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFee,
			"max fee per gas less than block base fee (%s < %s)",
			msg.GasFeeCap, baseFee,
		)
	}

	maxFee := new(big.Int).Mul(new(big.Int).SetUint64(msg.GasLimit), msg.GasPrice)
	totalRequired := new(big.Int).Add(maxFee, msg.Value)

	account := evmKeeper.GetAccount(ctx, msg.From)
	if account == nil {
		return errorsmod.Wrapf(
			errortypes.ErrUnknownAddress,
			"account %s does not exist",
			msg.From,
		)
	}

	totalRequiredUint256, overflow := uint256.FromBig(totalRequired)
	if overflow {
		return errorsmod.Wrapf(
			errortypes.ErrInvalidRequest,
			"fee + value overflow: %s",
			totalRequired,
		)
	}

	if account.Balance.Cmp(totalRequiredUint256) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFunds,
			"insufficient balance for fee (%s) + value (%s) = %s from address %s (balance: %s)",
			maxFee,
			msg.Value,
			totalRequired,
			msg.From,
			account.Balance,
		)
	}

	return nil
}
