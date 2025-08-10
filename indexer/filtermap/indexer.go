package filtermap

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	LogsPerMap          = 65536
	MapsPerEpoch        = 1024
	MaxCachedFilterMaps = 100

	KeyPrefixFilterMap    = 0x10
	KeyPrefixLogData      = 0x11
	KeyPrefixRawLogs      = 0x12
	KeyLatestBlock        = 0x13
	KeyNextMapID          = 0x14
	KeyPrefixBlockLvPointer = 0x15
)

type FilterMapsIndexer struct {
	mu     sync.RWMutex
	db     dbm.DB
	logger log.Logger
	params *Params

	enabled       bool
	latestBlock   uint64
	nextMapID     uint32
	totalLogIndex uint64 // Global log index counter

	// Caches
	filterMapCache  *lru.Cache[uint32, FilterMap]
	logDataCache    *lru.Cache[uint32, *LogData]
	lvPointerCache  *lru.Cache[uint64, uint64]  // block number -> first log index
	rawLogs         map[uint64][]*ethtypes.Log

	// Current working map
	currentMap     FilterMap
	currentLogData *LogData
	logCounter     uint64 // Counter within current map
}

func NewFilterMapsIndexer(db dbm.DB, logger log.Logger) *FilterMapsIndexer {
	params := DefaultParams
	params.deriveFields()

	return &FilterMapsIndexer{
		db:             db,
		logger:         logger.With("module", "filtermaps"),
		params:         &params,
		enabled:        true,
		filterMapCache: lru.NewCache[uint32, FilterMap](MaxCachedFilterMaps),
		logDataCache:   lru.NewCache[uint32, *LogData](MaxCachedFilterMaps),
		lvPointerCache: lru.NewCache[uint64, uint64](1000),  // cache last 1000 blocks
		rawLogs:        make(map[uint64][]*ethtypes.Log),
	}
}

func (fmi *FilterMapsIndexer) IndexLogs(blockNumber uint64, logs []*ethtypes.Log) {
	fmi.mu.Lock()
	defer fmi.mu.Unlock()

	if !fmi.enabled {
		return
	}

	// Store block's first log index even if no logs
	blockFirstIndex := fmi.totalLogIndex
	defer func() {
		fmi.storeBlockLvPointer(blockNumber, blockFirstIndex)
	}()

	if len(logs) == 0 {
		return
	}

	// Initialize current map if needed
	if fmi.currentMap == nil {
		fmi.currentMap = NewFilterMap(fmi.params)
		fmi.currentLogData = &LogData{
			MapID:      fmi.nextMapID,
			StartBlock: blockNumber,
			EndBlock:   blockNumber,
			Logs:       make([]*ethtypes.Log, 0, LogsPerMap),
		}
	}

	// Process each log
	for _, log := range logs {
		// Check if current map is full
		if fmi.logCounter >= LogsPerMap {
			// Save current map
			fmi.persistCurrentMap()

			// Start new map
			fmi.nextMapID++
			fmi.currentMap = NewFilterMap(fmi.params)
			fmi.currentLogData = &LogData{
				MapID:      fmi.nextMapID,
				StartBlock: blockNumber,
				EndBlock:   blockNumber,
				Logs:       make([]*ethtypes.Log, 0, LogsPerMap),
			}
			fmi.logCounter = 0
		}

		// Calculate global log index
		globalIndex := uint64(fmi.nextMapID)*LogsPerMap + fmi.logCounter

		// Add to filter map
		fmi.currentMap.AddLogToMap(fmi.params, fmi.nextMapID, globalIndex, log.Address, log.Topics)

		// Add to log data
		fmi.currentLogData.Logs = append(fmi.currentLogData.Logs, log)
		fmi.currentLogData.EndBlock = blockNumber

		fmi.logCounter++
	}

	fmi.latestBlock = blockNumber
	fmi.totalLogIndex = uint64(fmi.nextMapID)*LogsPerMap + fmi.logCounter
}

func (fmi *FilterMapsIndexer) GetLogs(
	ctx sdk.Context,
	fromBlock, toBlock *big.Int,
	addresses []common.Address,
	topics [][]common.Hash,
) ([]*ethtypes.Log, error) {
	if !fmi.enabled {
		return nil, nil
	}

	var from, to uint64
	if fromBlock != nil {
		from = fromBlock.Uint64()
	}
	if toBlock != nil {
		to = toBlock.Uint64()
	} else {
		fmi.mu.RLock()
		to = fmi.latestBlock
		fmi.mu.RUnlock()
	}

	return fmi.FindLogsByRange(ctx.Context(), from, to, addresses, topics)
}

func (fmi *FilterMapsIndexer) persistCurrentMap() {
	if fmi.currentMap == nil || fmi.currentLogData == nil {
		return
	}

	mapKey := append([]byte{KeyPrefixFilterMap}, sdk.Uint64ToBigEndian(uint64(fmi.nextMapID))...)
	mapData, _ := json.Marshal(fmi.currentMap)
	fmi.db.Set(mapKey, mapData)

	logKey := append([]byte{KeyPrefixLogData}, sdk.Uint64ToBigEndian(uint64(fmi.nextMapID))...)
	logData, _ := json.Marshal(fmi.currentLogData)
	fmi.db.Set(logKey, logData)

	fmi.filterMapCache.Add(fmi.nextMapID, fmi.currentMap)
	fmi.logDataCache.Add(fmi.nextMapID, fmi.currentLogData)
}

func (fmi *FilterMapsIndexer) loadFilterMap(mapID uint32) FilterMap {
	key := append([]byte{KeyPrefixFilterMap}, sdk.Uint64ToBigEndian(uint64(mapID))...)
	data, err := fmi.db.Get(key)
	if err != nil || len(data) == 0 {
		return nil
	}

	var fm FilterMap
	json.Unmarshal(data, &fm)
	return fm
}

func (fmi *FilterMapsIndexer) loadLogData(mapID uint32) *LogData {
	key := append([]byte{KeyPrefixLogData}, sdk.Uint64ToBigEndian(uint64(mapID))...)
	data, err := fmi.db.Get(key)
	if err != nil || len(data) == 0 {
		return nil
	}

	var ld LogData
	json.Unmarshal(data, &ld)
	return &ld
}

func (fmi *FilterMapsIndexer) storeBlockLvPointer(blockNumber, lvPointer uint64) {
	fmi.lvPointerCache.Add(blockNumber, lvPointer)
	key := append([]byte{KeyPrefixBlockLvPointer}, sdk.Uint64ToBigEndian(blockNumber)...)
	fmi.db.Set(key, sdk.Uint64ToBigEndian(lvPointer))
}

func (fmi *FilterMapsIndexer) getBlockLvPointer(blockNumber uint64) (uint64, error) {
	if lvPointer, ok := fmi.lvPointerCache.Get(blockNumber); ok {
		return lvPointer, nil
	}
	
	key := append([]byte{KeyPrefixBlockLvPointer}, sdk.Uint64ToBigEndian(blockNumber)...)
	data, err := fmi.db.Get(key)
	if err != nil || len(data) == 0 {
		return 0, fmt.Errorf("block %d not indexed yet", blockNumber)
	}
	
	lvPointer := sdk.BigEndianToUint64(data)
	fmi.lvPointerCache.Add(blockNumber, lvPointer)
	return lvPointer, nil
}
