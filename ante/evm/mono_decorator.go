package evm

import (
	"context"
	"math/big"
	"runtime/pprof"

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
	evmLabels := pprof.Labels()

	pprof.Do(ctx.Context(), evmLabels, func(ppctx context.Context) {
		ctx = ctx.WithContext(ppctx)

		// 0. Basic validation of the transaction
		var txFeeInfo *txtypes.Fee
		if !ctx.IsReCheckTx() {
			// NOTE: txFeeInfo is associated with the Cosmos stack, not the EVM. For
			// this reason, the fee is represented in the original decimals and
			// should be converted later when used.
			validateTxLabels := pprof.Labels("Ante Handler", "ValidateTx")
			pprof.Do(ppctx, validateTxLabels, func(ctx2 context.Context) {
				txFeeInfo, err = ValidateTx(tx)
			})
			if err != nil {
				return
			}
		}

		// 1. setup ctx
		setupCtxLabels := pprof.Labels("Ante Handler", "SetupContextAndResetTransientGas")
		pprof.Do(ppctx, setupCtxLabels, func(ctx2 context.Context) {
			ctx, err = SetupContextAndResetTransientGas(ctx, tx, md.evmKeeper)
		})
		if err != nil {
			return
		}

		// 2. get utils
		var decUtils *DecoratorUtils
		utilsLabels := pprof.Labels("Ante Handler", "NewMonoDecoratorUtils")
		pprof.Do(ppctx, utilsLabels, func(ctx2 context.Context) {
			decUtils, err = NewMonoDecoratorUtils(ctx, md.evmKeeper)
		})
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

		var ethMsg *evmtypes.MsgEthereumTx
		var txData evmtypes.TxData
		unpackLabels := pprof.Labels("Ante Handler", "UnpackEthMsg")
		pprof.Do(ppctx, unpackLabels, func(ctx2 context.Context) {
			ethMsg, txData, err = evmtypes.UnpackEthMsg(msgs[msgIndex])
		})
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
			mempoolLabels := pprof.Labels("Ante Handler", "CheckMempoolFee")
			pprof.Do(ppctx, mempoolLabels, func(ctx2 context.Context) {
				err = CheckMempoolFee(fee, decUtils.MempoolMinGasPrice, gasLimit, decUtils.Rules.IsLondon)
			})
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
		globalFeeLabels := pprof.Labels("Ante Handler", "CheckGlobalFee")
		pprof.Do(ppctx, globalFeeLabels, func(ctx2 context.Context) {
			err = CheckGlobalFee(fee, decUtils.GlobalMinGasPrice, gasLimit)
		})
		if err != nil {
			return
		}

		// 4. validate msg contents
		validateMsgLabels := pprof.Labels("Ante Handler", "ValidateMsg")
		pprof.Do(ppctx, validateMsgLabels, func(ctx2 context.Context) {
			err = ValidateMsg(
				decUtils.EvmParams,
				txData,
				ethMsg.GetFrom(),
			)
		})
		if err != nil {
			return
		}

		// 5. signature verification
		signatureLabels := pprof.Labels("Ante Handler", "SignatureVerification")
		pprof.Do(ppctx, signatureLabels, func(ctx2 context.Context) {
			err = SignatureVerification(
				ethMsg,
				decUtils.Signer,
				decUtils.EvmParams.AllowUnprotectedTxs,
			)
		})
		if err != nil {
			return
		}

		from := ethMsg.GetFrom()

		// 6. Convert to core.Message for validation
		var coreMsg *core.Message
		asMsgLabels := pprof.Labels("Ante Handler", "AsMessage")
		pprof.Do(ppctx, asMsgLabels, func(ctx2 context.Context) {
			coreMsg, err = ethMsg.AsMessage(decUtils.BaseFee)
		})
		if err != nil {
			err = errorsmod.Wrapf(
				err,
				"failed to create an ethereum core.Message from signer %T", decUtils.Signer,
			)
			return
		}

		// 7. Transaction cost validation
		// This combines account balance verification, fee validation, and transfer checks
		validateCostsLabels := pprof.Labels("Ante Handler", "ValidateTransactionCosts")
		pprof.Do(ppctx, validateCostsLabels, func(ctx2 context.Context) {
			err = ValidateTransactionCosts(
				ctx,
				md.evmKeeper,
				coreMsg,
				txData,
				decUtils.BaseFee,
				decUtils.Rules,
			)
		})
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
		
		var gasWanted uint64
		gasWantedLabels := pprof.Labels("Ante Handler", "UpdateCumulativeGasWanted")
		pprof.Do(ppctx, gasWantedLabels, func(ctx2 context.Context) {
			gasWanted = UpdateCumulativeGasWanted(
				ctx,
				gas,
				md.maxGasWanted,
				decUtils.GasWanted,
			)
		})
		decUtils.GasWanted = gasWanted

		var minPriority int64
		priorityLabels := pprof.Labels("Ante Handler", "GetMsgPriority")
		pprof.Do(ppctx, priorityLabels, func(ctx2 context.Context) {
			minPriority = GetMsgPriority(
				txData,
				decUtils.MinPriority,
				decUtils.BaseFee,
			)
		})
		decUtils.MinPriority = minPriority

		// Update the fee to be paid for the tx adding the fee specified for the
		// current message.
		decUtils.TxFee.Add(decUtils.TxFee, txData.Fee())

		// Update the transaction gas limit adding the gas specified in the
		// current message.
		decUtils.TxGasLimit += gas

		// 9. increment sequence
		nonceLabels := pprof.Labels("Ante Handler", "IncrementNonce")
		pprof.Do(ppctx, nonceLabels, func(ctx2 context.Context) {
			acc := md.accountKeeper.GetAccount(ctx, from)
			if acc == nil {
				// safety check: shouldn't happen
				err = errorsmod.Wrapf(
					errortypes.ErrUnknownAddress,
					"account %s does not exist",
					from,
				)
				return
			}
			err = IncrementNonce(ctx, md.accountKeeper, acc, txData.GetNonce())
		})
		if err != nil {
			return
		}

		// 10. gas wanted
		gasCheckLabels := pprof.Labels("Ante Handler", "CheckGasWanted")
		pprof.Do(ppctx, gasCheckLabels, func(ctx2 context.Context) {
			err = CheckGasWanted(ctx, md.feeMarketKeeper, tx, decUtils.Rules.IsLondon)
		})
		if err != nil {
			return
		}

		// 11. emit events
		emitLabels := pprof.Labels("Ante Handler", "EmitTxHashEvent")
		pprof.Do(ppctx, emitLabels, func(ctx2 context.Context) {
			txIdx := uint64(msgIndex) //nolint:gosec // G115
			EmitTxHashEvent(ctx, ethMsg, decUtils.BlockTxIndex, txIdx)
		})

		// 12. check tx fee
		txFeeLabels := pprof.Labels("Ante Handler", "CheckTxFee")
		pprof.Do(ppctx, txFeeLabels, func(ctx2 context.Context) {
			err = CheckTxFee(txFeeInfo, decUtils.TxFee, decUtils.TxGasLimit)
		})
		if err != nil {
			return
		}

		// 13. check block gas limit
		blockGasLabels := pprof.Labels("Ante Handler", "CheckBlockGasLimit")
		pprof.Do(ppctx, blockGasLabels, func(ctx2 context.Context) {
			ctx, err = CheckBlockGasLimit(ctx, decUtils.GasWanted, decUtils.MinPriority)
		})
		if err != nil {
			return
		}
	})

	return next(ctx, tx, simulate)
}