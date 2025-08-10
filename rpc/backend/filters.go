package backend

import (
	"math/big"
	
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/pkg/errors"
	
	"github.com/cosmos/evm/indexer"
	
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (b *Backend) GetLogs(hash common.Hash) ([][]*ethtypes.Log, error) {
	resBlock, err := b.TendermintBlockByHash(hash)
	if err != nil {
		return nil, err
	}
	if resBlock == nil {
		return nil, errors.Errorf("block not found for hash %s", hash)
	}
	return b.GetLogsByHeight(&resBlock.Block.Height)
}

func (b *Backend) GetLogsByHeight(height *int64) ([][]*ethtypes.Log, error) {
	blockRes, err := b.RPCClient.BlockResults(b.Ctx, height)
	if err != nil {
		return nil, err
	}

	return GetLogsFromBlockResults(blockRes)
}

func (b *Backend) BloomStatus() (uint64, uint64) {
	return 0, 0
}

func (b *Backend) GetFilterLogs(ctx sdk.Context, fromBlock, toBlock *big.Int, addresses []common.Address, topics [][]common.Hash) ([]*ethtypes.Log, error) {
	if kvIndexer, ok := b.Indexer.(*indexer.KVIndexer); ok {
		if filterMaps := kvIndexer.GetFilterMaps(); filterMaps != nil {
			return filterMaps.GetLogs(ctx, fromBlock, toBlock, addresses, topics)
		}
	}
	
	return []*ethtypes.Log{}, nil
}

func (b *Backend) GetLogsFromBloomFilter(fromBlock, toBlock *big.Int, addresses []common.Address, topics [][]common.Hash) ([]*ethtypes.Log, error) {
	return b.GetFilterLogs(sdk.Context{}, fromBlock, toBlock, addresses, topics)
}
