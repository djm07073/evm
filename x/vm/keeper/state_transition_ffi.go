package keeper

import (
	"math/big"

	antetypes "github.com/cosmos/evm/ante/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/tracing"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"

	"github.com/cosmos/evm/x/vm/api"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/cosmos/evm/x/vm/types"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

// applyMessageWithFFI applies a transaction using the FFI-based EVM (revmc)
// This is called when k.ffiEnabled is true
func (k *Keeper) applyMessageWithFFI(
	ctx sdk.Context,
	msg core.Message,
	tracer *tracing.Hooks, // TODO: support tracer in FFI
	commit bool,
	cfg *statedb.EVMConfig,
	stateDB *statedb.StateDB,
) (*types.MsgEthereumTxResponse, error) {
	ethCfg := types.GetEthChainConfig()

	// Determine SpecId based on block height and timestamp
	spec := api.SpecIdFromChainConfig(
		ethCfg,
		ctx.BlockHeight(),
		uint64(ctx.BlockTime().Unix()),
	)

	// Set up block context in stateDB
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     k.GetHashFn(ctx),
		Coinbase:    cfg.CoinBase,
		GasLimit:    antetypes.BlockGasLimit(ctx),
		BlockNumber: big.NewInt(ctx.BlockHeight()),
		Time:        uint64(ctx.BlockHeader().Time.Unix()), //#nosec G115 -- int overflow is not a concern here
		Difficulty:  big.NewInt(0),                         // unused. Only required in PoW context
		BaseFee:     cfg.BaseFee,
		Random:      &common.MaxHash, // need to be different than nil to signal it is after the merge and pick up the right opcodes
	}
	stateDB.SetBlockContext(blockCtx)

	// Create EVM options
	var evmOpt *api.EvmOptions
	if k.compilerConfig != nil {
		evmOpt = &api.EvmOptions{
			Spec: spec,
			EvmCompilerOptions: &api.EvmCompilerOptions{
				Threshold:  k.compilerConfig.Threshold,
				MaxConcise: k.compilerConfig.MaxConcurrent,
			},
		}
	} else {
		evmOpt = &api.EvmOptions{
			Spec:               spec,
			EvmCompilerOptions: nil, // interpreter only
		}
	}

	// Create FFI EVM instance
	evm := api.NewEVM(*stateDB, evmOpt)
	defer api.FreeVM(evm)

	// Calculate intrinsic gas
	contractCreation := msg.To == nil
	intrinsicGas, err := k.GetEthIntrinsicGas(ctx, msg, ethCfg, contractCreation)
	if err != nil {
		return nil, errorsmod.Wrap(err, "intrinsic gas failed")
	}

	leftoverGas := msg.GasLimit
	if leftoverGas < intrinsicGas {
		return nil, errorsmod.Wrap(core.ErrIntrinsicGas, "apply message")
	}
	leftoverGas -= intrinsicGas

	// Prepare access list
	rules := ethCfg.Rules(big.NewInt(ctx.BlockHeight()), true, uint64(ctx.BlockTime().Unix()))
	// TODO: how to set precompile contracts ?
	stateDB.Prepare(rules, msg.From, cfg.CoinBase, msg.To, nil, msg.AccessList)

	// Build block context protobuf
	blockBytes, err := k.buildBlockContext(ctx, cfg)
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to build block context")
	}

	// Build transaction protobuf
	txBytes, err := k.buildTransaction(msg, leftoverGas)
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to build transaction")
	}

	// Execute transaction via FFI
	result, err := evm.Execute(&blockBytes, &txBytes)
	if err != nil {
		return nil, errorsmod.Wrap(err, "FFI EVM execution failed")
	}

	// Parse result
	gasUsed, ret, vmErr, logs, err := k.parseEvmResult(result)
	if err != nil {
		return nil, err
	}

	// Convert internal logs to stateDB logs
	for _, internalLog := range logs {
		ethLog := k.convertInternalLogToEthLog(internalLog)
		stateDB.AddLog(ethLog)
	}

	// Commit if needed
	if commit {
		if err := stateDB.Commit(); err != nil {
			return nil, errorsmod.Wrap(err, "failed to commit state")
		}
	}

	return &types.MsgEthereumTxResponse{
		GasUsed: gasUsed,
		VmError: vmErrString(vmErr),
		Ret:     ret,
		Logs:    types.NewLogsFromEth(stateDB.Logs()),
	}, nil
}

// parseEvmResult parses InternalEvmResult oneof and returns execution details
func (k *Keeper) parseEvmResult(result *types.InternalEvmResult) (gasUsed uint64, ret []byte, vmErr error, logs []*types.InternalLog, err error) {
	switch r := result.Result.(type) {
	case *types.InternalEvmResult_Success:
		gasUsed = r.Success.GasUsed
		logs = r.Success.Logs

		// Parse output (Call or Create)
		if r.Success.Output != nil {
			switch output := r.Success.Output.Output.(type) {
			case *types.InternalOutput_Call:
				ret = output.Call.Call
			case *types.InternalOutput_Create:
				ret = output.Create.Create
			}
		}

	case *types.InternalEvmResult_Revert:
		gasUsed = r.Revert.GasUsed
		ret = r.Revert.Output
		vmErr = errorsmod.Wrap(types.ErrVMExecution, "execution reverted")

	case *types.InternalEvmResult_Halt:
		gasUsed = r.Halt.GasUsed
		vmErr = k.convertHaltReason(r.Halt.Reason)

	case *types.InternalEvmResult_Error:
		err = errorsmod.Wrap(types.ErrVMExecution, r.Error.Message)

	default:
		err = errorsmod.Wrap(types.ErrInvalidState, "unknown result type")
	}

	return gasUsed, ret, vmErr, logs, err
}

// buildBlockContext creates block context protobuf for FFI EVM
func (k *Keeper) buildBlockContext(ctx sdk.Context, cfg *statedb.EVMConfig) ([]byte, error) {
	// Convert block number to 32-byte big-endian
	blockNumber := big.NewInt(ctx.BlockHeight())
	numberBytes := make([]byte, 32)
	blockNumber.FillBytes(numberBytes)

	// Convert timestamp to 32-byte big-endian
	timestamp := big.NewInt(ctx.BlockTime().Unix())
	timestampBytes := make([]byte, 32)
	timestamp.FillBytes(timestampBytes)

	// Convert gas limit to 32-byte big-endian
	gasLimit := big.NewInt(int64(antetypes.BlockGasLimit(ctx)))
	gasLimitBytes := make([]byte, 32)
	gasLimit.FillBytes(gasLimitBytes)

	blockCtx := &types.InternalBlock{
		Number:     numberBytes,
		Coinbase:   cfg.CoinBase.Bytes(),
		Timestamp:  timestampBytes,
		GasLimit:   gasLimitBytes,
		Basefee:    nil,                    // Will set below if available
		Difficulty: make([]byte, 32),       // 0 for post-merge
		Prevrandao: common.MaxHash.Bytes(), // Need non-nil for post-merge
	}

	if cfg.BaseFee != nil {
		baseFeeBytes := make([]byte, 32)
		cfg.BaseFee.FillBytes(baseFeeBytes)
		blockCtx.Basefee = baseFeeBytes
	}

	return proto.Marshal(blockCtx)
}

// buildTransaction creates transaction protobuf for FFI EVM
func (k *Keeper) buildTransaction(msg core.Message, gas uint64) ([]byte, error) {
	var toBytes []byte
	if msg.To != nil {
		toBytes = msg.To.Bytes()
	}

	// Build access list
	var accessList []*types.InternalAccessListItem
	for _, al := range msg.AccessList {
		storageKeys := make([]*types.InternalStorageKey, len(al.StorageKeys))
		for i, key := range al.StorageKeys {
			storageKeys[i] = &types.InternalStorageKey{
				Value: key.Bytes(),
			}
		}
		item := &types.InternalAccessListItem{
			Address:     al.Address.Bytes(),
			StorageKeys: storageKeys,
		}
		accessList = append(accessList, item)
	}

	// Convert value to 32-byte big-endian
	valueBytes := make([]byte, 32)
	msg.Value.FillBytes(valueBytes)

	// Convert gas price to 32-byte big-endian
	gasPriceBytes := make([]byte, 32)
	msg.GasPrice.FillBytes(gasPriceBytes)

	tx := &types.InternalTransaction{
		Caller:     msg.From.Bytes(),
		TransactTo: toBytes,
		Value:      valueBytes,
		Data:       msg.Data,
		GasLimit:   gas,
		GasPrice:   gasPriceBytes,
		AccessList: accessList,
	}

	// EIP-1559 fields
	if msg.GasFeeCap != nil && msg.GasFeeCap.Sign() > 0 {
		gasPriorityFeeBytes := make([]byte, 32)
		if msg.GasTipCap != nil {
			msg.GasTipCap.FillBytes(gasPriorityFeeBytes)
		}
		tx.GasPriorityFee = gasPriorityFeeBytes
	}

	return proto.Marshal(tx)
}

// convertHaltReason converts InternalHaltReasonEnum to error
func (k *Keeper) convertHaltReason(reason types.InternalHaltReasonEnum) error {
	switch reason {
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_OUT_OF_GAS_BASIC:
		return errorsmod.Wrap(types.ErrVMExecution, "out of gas")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_OUT_OF_GAS_MEMORY:
		return errorsmod.Wrap(types.ErrVMExecution, "out of gas (memory expansion)")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_OPCODE_NOT_FOUND:
		return errorsmod.Wrap(types.ErrVMExecution, "invalid opcode")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_INVALID_JUMP:
		return errorsmod.Wrap(types.ErrVMExecution, "invalid jump")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_STACK_UNDERFLOW:
		return errorsmod.Wrap(types.ErrVMExecution, "stack underflow")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_STACK_OVERFLOW:
		return errorsmod.Wrap(types.ErrVMExecution, "stack overflow")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_OUT_OF_FUNDS:
		return errorsmod.Wrap(types.ErrVMExecution, "insufficient funds")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_CALL_TOO_DEEP:
		return errorsmod.Wrap(types.ErrVMExecution, "call depth exceeded")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_CREATE_COLLISION:
		return errorsmod.Wrap(types.ErrVMExecution, "contract creation collision")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_CREATE_CONTRACT_SIZE_LIMIT:
		return errorsmod.Wrap(types.ErrVMExecution, "contract size limit exceeded")
	case types.InternalHaltReasonEnum_HALT_REASON_ENUM_STATE_CHANGE_DURING_STATIC_CALL:
		return errorsmod.Wrap(types.ErrVMExecution, "state change during static call")
	default:
		return errorsmod.Wrapf(types.ErrVMExecution, "halt reason: %s", reason.String())
	}
}

// vmErrString converts vm error to string
func vmErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// convertInternalLogToEthLog converts InternalLog to ethtypes.Log
func (k *Keeper) convertInternalLogToEthLog(internalLog *types.InternalLog) *ethtypes.Log {
	// Convert address
	address := common.BytesToAddress(internalLog.Address)

	// Convert topics
	var topics []common.Hash
	if internalLog.Data != nil {
		for _, topic := range internalLog.Data.Topics {
			topics = append(topics, common.BytesToHash(topic.Value))
		}
	}

	// Get data
	var data []byte
	if internalLog.Data != nil {
		data = internalLog.Data.Data
	}

	return &ethtypes.Log{
		Address: address,
		Topics:  topics,
		Data:    data,
		// Note: TxHash, BlockHash, TxIndex, Index are set by stateDB.AddLog()
	}
}
