package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// MonoDecorator is a single decorator that handles all the prechecks for
// ethereum transactions.
type MonoDecorator struct {
	accountKeeper   anteinterfaces.AccountKeeper
	feeMarketKeeper anteinterfaces.FeeMarketKeeper
	evmKeeper       anteinterfaces.EVMKeeper
	maxGasWanted    uint64
}

// NewEVMMonoDecorator creates the 'mono' decorator, that is used to run the ante handle logic
// for EVM transactions on the chain.
//
// This runs all the default checks for EVM transactions enable through Cosmos EVM.
// Any partner chains can use this in their ante handler logic and build additional EVM
// decorators using the returned DecoratorUtils
func NewEVMMonoDecorator(
	accountKeeper anteinterfaces.AccountKeeper,
	feeMarketKeeper anteinterfaces.FeeMarketKeeper,
	evmKeeper anteinterfaces.EVMKeeper,
	maxGasWanted uint64,
) MonoDecorator {
	return MonoDecorator{
		accountKeeper:   accountKeeper,
		feeMarketKeeper: feeMarketKeeper,
		evmKeeper:       evmKeeper,
		maxGasWanted:    maxGasWanted,
	}
}

// AnteHandle handles the entire decorator chain using a mono decorator.
func (md MonoDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {

	// 0. Basic validation of the transaction
	var txFeeInfo *txtypes.Fee
	if !ctx.IsReCheckTx() {
		// NOTE: txFeeInfo is associated with the Cosmos stack, not the EVM. For
		// this reason, the fee is represented in the original decimals and
		// should be converted later when used.
		txFeeInfo, err = ValidateTx(tx)
		if err != nil {
			return
		}
	}

	// 1. setup ctx
	ctx, err = SetupContextAndResetTransientGas(ctx, tx, md.evmKeeper)
	if err != nil {
		return
	}

	// 2. get utils
	decUtils, err := NewMonoDecoratorUtils(ctx, md.evmKeeper)
	if err != nil {
		return
	}

	// NOTE: the protocol does not support multiple EVM messages currently so
	// this loop will complete after the first message.
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		err = errorsmod.Wrapf(errortypes.ErrInvalidRequest, "expected 1 message, got %d", len(msgs))
		return
	}
	msgIndex := 0

	ethMsg, txData, err := evmtypes.UnpackEthMsg(msgs[msgIndex])
	if err != nil {
		return
	}

	feeAmt := txData.Fee()
	gas := txData.GetGas()
	fee := sdkmath.LegacyNewDecFromBigInt(feeAmt)
	gasLimit := sdkmath.LegacyNewDecFromBigInt(new(big.Int).SetUint64(gas))

	// TODO: computation for mempool and global fee can be made using only
	// the price instead of the fee. This would save some computation.
	//
	// 2. mempool inclusion fee
	if ctx.IsCheckTx() && !simulate {
		// FIX: Mempool dec should be converted
		err = CheckMempoolFee(fee, decUtils.MempoolMinGasPrice, gasLimit, decUtils.Rules.IsLondon)
		if err != nil {
			return
		}
	}

	if txData.TxType() == ethtypes.DynamicFeeTxType && decUtils.BaseFee != nil {
		// If the base fee is not empty, we compute the effective gas price
		// according to current base fee price. The gas limit is specified
		// by the user, while the price is given by the minimum between the
		// max price paid for the entire tx, and the sum between the price
		// for the tip and the base fee.
		feeAmt = txData.EffectiveFee(decUtils.BaseFee)
		fee = sdkmath.LegacyNewDecFromBigInt(feeAmt)
	}

	// 3. min gas price (global min fee)
	err = CheckGlobalFee(fee, decUtils.GlobalMinGasPrice, gasLimit)
	if err != nil {
		return
	}

	// 4. validate msg contents
	err = ValidateMsg(
		decUtils.EvmParams,
		txData,
		ethMsg.GetFrom(),
	)
	if err != nil {
		return
	}

	// 5. signature verification
	err = SignatureVerification(
		ethMsg,
		decUtils.Signer,
		decUtils.EvmParams.AllowUnprotectedTxs,
	)
	if err != nil {
		return
	}

	// 6. Convert to core.Message for validation
	coreMsg, err := ethMsg.AsMessage(decUtils.BaseFee)
	if err != nil {
		err = errorsmod.Wrapf(
			err,
			"failed to create an ethereum core.Message from signer %T", decUtils.Signer,
		)
		return
	}

	// 7. Transaction cost validation
	// This combines account balance verification, fee validation, and transfer checks
	err = ValidateTransactionCosts(
		ctx,
		md.evmKeeper,
		coreMsg,
		txData,
		decUtils.BaseFee,
		decUtils.Rules,
	)
	if err != nil {
		return
	}

	// Store decoded message in context
	var evmMsgs *evmtypes.EVMMessages
	if existingEvmMsgs := ctx.Value(evmtypes.ContextKeyEVMDMessages); existingEvmMsgs != nil {
		evmMsgs, _ = existingEvmMsgs.(*evmtypes.EVMMessages)
	}
	if evmMsgs == nil {
		evmMsgs = &evmtypes.EVMMessages{
			Messages:     make([]*core.Message, 0),
			CurrentIndex: 0,
		}
	}
	evmMsgs.Messages = append(evmMsgs.Messages, coreMsg)
	ctx = ctx.WithValue(evmtypes.ContextKeyEVMDMessages, evmMsgs)

	gasWanted := UpdateCumulativeGasWanted(
		ctx,
		gas,
		md.maxGasWanted,
		decUtils.GasWanted,
	)
	decUtils.GasWanted = gasWanted

	minPriority := GetMsgPriority(
		txData,
		decUtils.MinPriority,
		decUtils.BaseFee,
	)
	decUtils.MinPriority = minPriority

	// Update the fee to be paid for the tx adding the fee specified for the
	// current message.
	decUtils.TxFee.Add(decUtils.TxFee, txData.Fee())

	// Update the transaction gas limit adding the gas specified in the
	// current message.
	decUtils.TxGasLimit += gas

	// 9. increment sequence - removed as nonce is managed in state_transition.go

	// 10. gas wanted
	err = CheckGasWanted(ctx, md.feeMarketKeeper, tx, decUtils.Rules.IsLondon)
	if err != nil {
		return
	}

	// 11. emit events
	txIdx := uint64(msgIndex) //nolint:gosec // G115
	EmitTxHashEvent(ctx, ethMsg, decUtils.BlockTxIndex, txIdx)

	// 12. check tx fee
	err = CheckTxFee(txFeeInfo, decUtils.TxFee, decUtils.TxGasLimit)
	if err != nil {
		return
	}

	// 13. check block gas limit
	ctx, err = CheckBlockGasLimit(ctx, decUtils.GasWanted, decUtils.MinPriority)
	if err != nil {
		return
	}

	return next(ctx, tx, simulate)
}
