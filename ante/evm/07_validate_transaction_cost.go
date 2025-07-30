package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"

	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
)

// calculateIntrinsicGas computes the intrinsic gas for the transaction
func calculateIntrinsicGas(txData evmtypes.TxData, msg *core.Message, rules params.Rules) (uint64, error) {
	isContractCreation := msg.To == nil

	var accessList ethtypes.AccessList
	if txData.GetAccessList() != nil {
		accessList = txData.GetAccessList()
	}

	var authList []ethtypes.SetCodeAuthorization
	ethTx := ethtypes.NewTx(txData.AsEthereumData())
	if ethTx != nil {
		authList = ethTx.SetCodeAuthorizations()
	}

	return core.IntrinsicGas(
		txData.GetData(),
		accessList,
		authList,
		isContractCreation,
		rules.IsHomestead,
		rules.IsIstanbul,
		rules.IsShanghai,
	)
}

// validateBalance checks if the account has sufficient balance for fee + value
func validateBalance(account *statedb.Account, msg *core.Message) error {
	// Calculate total required balance (fee + value)
	maxFee := new(big.Int).Mul(new(big.Int).SetUint64(msg.GasLimit), msg.GasPrice)
	totalRequired := new(big.Int).Add(maxFee, msg.Value)

	// Check for negative values
	if totalRequired.Sign() < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInvalidCoins,
			"tx cost (%s) is negative and invalid",
			totalRequired,
		)
	}

	// Convert to uint256 for comparison
	totalRequiredUint256, overflow := uint256.FromBig(totalRequired)
	if overflow {
		return errorsmod.Wrapf(
			errortypes.ErrInvalidRequest,
			"fee + value overflow: %s",
			totalRequired,
		)
	}

	// Check balance
	if account.Balance.Cmp(totalRequiredUint256) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFunds,
			"insufficient balance for transaction: need %s (fee: %s + value: %s), have %s",
			totalRequired, maxFee, msg.Value, account.Balance,
		)
	}

	return nil
}

// ValidateTransactionCosts performs all transaction cost validations in a single pass:
// 1. Account existence and EOA verification
// 2. Balance check for fee + value
// 3. Base fee validation (EIP-1559)
// 4. Intrinsic gas validation
// This replaces VerifyAccountBalance, CheckInsufficientBalance, and VerifyFee
func ValidateTransactionCosts(
	ctx sdk.Context,
	evmKeeper anteinterfaces.EVMKeeper,
	msg *core.Message,
	txData evmtypes.TxData,
	baseFee *big.Int,
	rules params.Rules,
) error {
	// 1. Get account
	account := evmKeeper.GetAccount(ctx, msg.From)
	if account == nil {
		return errorsmod.Wrapf(
			errortypes.ErrUnknownAddress,
			"account %s does not exist",
			msg.From,
		)
	}

	// 2. Verify sender is EOA
	if account.IsContract() {
		return errorsmod.Wrapf(
			errortypes.ErrInvalidType,
			"sender is not EOA: address %s",
			msg.From,
		)
	}

	// 3. Check base fee
	if rules.IsLondon && msg.GasFeeCap.Cmp(baseFee) < 0 {
		return errorsmod.Wrapf(
			errortypes.ErrInsufficientFee,
			"max fee per gas less than block base fee (%s < %s)",
			msg.GasFeeCap, baseFee,
		)
	}

	// 4. Calculate and verify intrinsic gas
	intrinsicGas, err := calculateIntrinsicGas(txData, msg, rules)
	if err != nil {
		return errorsmod.Wrap(err, "failed to calculate intrinsic gas")
	}

	if msg.GasLimit < intrinsicGas {
		return errorsmod.Wrapf(
			errortypes.ErrOutOfGas,
			"gas limit too low: %d < %d (intrinsic gas)",
			msg.GasLimit, intrinsicGas,
		)
	}

	// 5. Validate balance
	return validateBalance(account, msg)
}
