package keeper

import (
	evmtypes "github.com/cosmos/evm/x/vm/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BeginBlock emits a base fee event which will be adjusted to the evm decimals
func (k *Keeper) BeginBlock(ctx sdk.Context) error {
	logger := ctx.Logger().With("begin_block", "evm")

	// Base fee is already set on FeeMarket BeginBlock
	// that runs before this one
	// We emit this event on the EVM and FeeMarket modules
	// because they can be different if the evm denom has 6 decimals
	res, err := k.BaseFee(ctx, &evmtypes.QueryBaseFeeRequest{})
	if err != nil {
		logger.Error("error when getting base fee", "error", err.Error())
	}
	if res != nil && res.BaseFee != nil && !res.BaseFee.IsNil() {
		// Store current base fee in event
		ctx.EventManager().EmitEvents(sdk.Events{
			sdk.NewEvent(
				evmtypes.EventTypeFeeMarket,
				sdk.NewAttribute(evmtypes.AttributeKeyBaseFee, res.BaseFee.String()),
			),
		})
	}
	return nil
}

// EndBlock performs end block logic for the EVM module.
// The EVM end block logic doesn't update the validator set, thus it returns
// an empty slice.
func (k *Keeper) EndBlock(ctx sdk.Context) error {
	// No bloom filter operations needed - using filtermaps instead
	return nil
}
