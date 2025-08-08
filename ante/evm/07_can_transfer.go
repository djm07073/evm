package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/params"

	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	"github.com/cosmos/evm/utils"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
)

// ValidateTransactionCosts checks that the sender has enough balance to cover both transaction costs and value transfer
func ValidateTransactionCosts(
	ctx sdk.Context,
	evmKeeper anteinterfaces.EVMKeeper,
	msg *core.Message,
	txData evmtypes.TxData,
	baseFee *big.Int,
	rules params.Rules,
) error {
	// Check if max fee per gas is at least the block base fee (EIP-1559)
	if rules.IsLondon && msg.GasFeeCap != nil && msg.GasFeeCap.Cmp(baseFee) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFee,
			"max fee per gas less than block base fee (%s < %s)",
			msg.GasFeeCap, baseFee,
		)
	}

	// Get the sender's account balance
	balance := evmKeeper.GetBalance(ctx, msg.From)
	if balance == nil {
		return errorsmod.Wrapf(
			errortypes.ErrUnknownAddress,
			"account %s does not exist",
			msg.From.Hex(),
		)
	}

	// Check that the transaction cost is positive
	txCost := txData.Cost()
	if txCost.Sign() < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInvalidCoins,
			"tx cost (%s) is negative and invalid", txCost,
		)
	}

	// Check that sender has enough balance to cover tx cost
	// Convert balance to big.Int for comparison
	if balance.ToBig().Cmp(txCost) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFunds,
			"sender balance < tx cost (%s < %s)", balance, txCost,
		)
	}

	return nil
}

// CanTransfer checks if the sender is allowed to transfer funds according to the EVM block
// Deprecated: Use ValidateTransactionCosts instead
func CanTransfer(
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

	// NOTE: pass in an empty coinbase address and nil tracer as we don't need them for the check below
	cfg := &statedb.EVMConfig{
		Params:   params,
		CoinBase: common.Address{},
		BaseFee:  baseFee,
	}

	stateDB := statedb.New(ctx, evmKeeper, statedb.NewEmptyTxConfig(common.BytesToHash(ctx.HeaderHash())))
	evm := evmKeeper.NewEVM(ctx, msg, cfg, evmtypes.NewNoOpTracer(), stateDB)

	// check that caller has enough balance to cover asset transfer for **topmost** call
	// NOTE: here the gas consumed is from the context with the infinite gas meter
	convertedValue, err := utils.Uint256FromBigInt(msg.Value)
	if err != nil {
		return err
	}
	if msg.Value.Sign() > 0 && !evm.Context.CanTransfer(stateDB, msg.From, convertedValue) {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFunds,
			"failed to transfer %s from address %s using the EVM block context transfer function",
			msg.Value,
			msg.From,
		)
	}

	return nil
}
