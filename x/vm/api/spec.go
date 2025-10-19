package api

import (
	"math/big"

	geth "github.com/ethereum/go-ethereum/params"
)

// SpecId represents the different Ethereum hard fork specifications
type SpecId uint8

const (
	FRONTIER         SpecId = 0
	FRONTIER_THAWING SpecId = 1
	HOMESTEAD        SpecId = 2
	DAO_FORK         SpecId = 3
	TANGERINE        SpecId = 4
	SPURIOUS_DRAGON  SpecId = 5
	BYZANTIUM        SpecId = 6
	CONSTANTINOPLE   SpecId = 7
	PETERSBURG       SpecId = 8
	ISTANBUL         SpecId = 9
	MUIR_GLACIER     SpecId = 10
	BERLIN           SpecId = 11
	LONDON           SpecId = 12
	ARROW_GLACIER    SpecId = 13
	GRAY_GLACIER     SpecId = 14
	MERGE            SpecId = 15
	SHANGHAI         SpecId = 16
	CANCUN           SpecId = 17
	PRAGUE           SpecId = 18
	OSAKA            SpecId = 19
	LATEST           SpecId = 255
)

// SpecIdFromChainConfig determines the appropriate SpecId based on the block height/timestamp
// and the Ethereum chain configuration
//
// Note: After the Merge, Ethereum switched from block-based to timestamp-based fork scheduling.
// This function requires both blockHeight and blockTimestamp to correctly determine the fork.
func SpecIdFromChainConfig(ethCfg *geth.ChainConfig, blockHeight int64, blockTimestamp uint64) SpecId {
	// Check forks in reverse chronological order

	// Timestamp-based forks (after The Merge)
	if ethCfg.OsakaTime != nil && blockTimestamp >= *ethCfg.OsakaTime {
		return OSAKA
	}
	if ethCfg.PragueTime != nil && blockTimestamp >= *ethCfg.PragueTime {
		return PRAGUE
	}
	if ethCfg.CancunTime != nil && blockTimestamp >= *ethCfg.CancunTime {
		return CANCUN
	}
	if ethCfg.ShanghaiTime != nil && blockTimestamp >= *ethCfg.ShanghaiTime {
		return SHANGHAI
	}

	// Block-based forks (before and including The Merge)
	// MERGE (Paris) - The Merge hard fork
	if ethCfg.MergeNetsplitBlock != nil && blockHeight >= ethCfg.MergeNetsplitBlock.Int64() {
		return MERGE
	}
	if ethCfg.GrayGlacierBlock != nil && blockHeight >= ethCfg.GrayGlacierBlock.Int64() {
		return GRAY_GLACIER
	}
	if ethCfg.ArrowGlacierBlock != nil && blockHeight >= ethCfg.ArrowGlacierBlock.Int64() {
		return ARROW_GLACIER
	}
	if ethCfg.LondonBlock != nil && blockHeight >= ethCfg.LondonBlock.Int64() {
		return LONDON
	}
	if ethCfg.BerlinBlock != nil && blockHeight >= ethCfg.BerlinBlock.Int64() {
		return BERLIN
	}
	if ethCfg.MuirGlacierBlock != nil && blockHeight >= ethCfg.MuirGlacierBlock.Int64() {
		return MUIR_GLACIER
	}
	if ethCfg.IstanbulBlock != nil && blockHeight >= ethCfg.IstanbulBlock.Int64() {
		return ISTANBUL
	}
	if ethCfg.PetersburgBlock != nil && blockHeight >= ethCfg.PetersburgBlock.Int64() {
		return PETERSBURG
	}
	if ethCfg.ConstantinopleBlock != nil && blockHeight >= ethCfg.ConstantinopleBlock.Int64() {
		return CONSTANTINOPLE
	}
	if ethCfg.ByzantiumBlock != nil && blockHeight >= ethCfg.ByzantiumBlock.Int64() {
		return BYZANTIUM
	}
	if ethCfg.EIP158Block != nil && blockHeight >= ethCfg.EIP158Block.Int64() {
		return SPURIOUS_DRAGON
	}
	if ethCfg.EIP150Block != nil && blockHeight >= ethCfg.EIP150Block.Int64() {
		return TANGERINE
	}
	if ethCfg.DAOForkBlock != nil && blockHeight >= ethCfg.DAOForkBlock.Int64() {
		return DAO_FORK
	}
	if ethCfg.HomesteadBlock != nil && blockHeight >= ethCfg.HomesteadBlock.Int64() {
		return HOMESTEAD
	}

	// Default to Frontier (the first Ethereum version)
	return FRONTIER
}

// SpecIdFromBlockNumber is a convenience function that takes a *big.Int block number
// Note: This cannot correctly determine timestamp-based forks (Shanghai+), so it's
// recommended to use SpecIdFromChainConfig with proper timestamp instead.
func SpecIdFromBlockNumber(ethCfg *geth.ChainConfig, blockNumber *big.Int, blockTimestamp uint64) SpecId {
	if blockNumber == nil {
		return FRONTIER
	}
	return SpecIdFromChainConfig(ethCfg, blockNumber.Int64(), blockTimestamp)
}
