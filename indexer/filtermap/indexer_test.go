package filtermap

import (
	"context"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	
	dbm "github.com/cosmos/cosmos-db"
	"cosmossdk.io/log"
	
	"github.com/stretchr/testify/require"
)

func TestBlockLvPointer(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	logs1 := []*ethtypes.Log{
		{Address: common.HexToAddress("0x1"), Topics: []common.Hash{{0x1}}},
		{Address: common.HexToAddress("0x2"), Topics: []common.Hash{{0x2}}},
		{Address: common.HexToAddress("0x3"), Topics: []common.Hash{{0x3}}},
	}
	indexer.IndexLogs(1, logs1)
	
	indexer.IndexLogs(2, []*ethtypes.Log{})
	
	logs3 := []*ethtypes.Log{
		{Address: common.HexToAddress("0x4"), Topics: []common.Hash{{0x4}}},
		{Address: common.HexToAddress("0x5"), Topics: []common.Hash{{0x5}}},
	}
	indexer.IndexLogs(3, logs3)
	
	ptr1, err := indexer.getBlockLvPointer(1)
	require.NoError(t, err)
	require.Equal(t, uint64(0), ptr1)
	
	ptr2, err := indexer.getBlockLvPointer(2)
	require.NoError(t, err)
	require.Equal(t, uint64(3), ptr2)
	
	ptr3, err := indexer.getBlockLvPointer(3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), ptr3)
	
	firstIdx, lastIdx := indexer.getLogIndexRange(1, 3)
	require.Equal(t, uint64(0), firstIdx)
	require.Equal(t, uint64(4), lastIdx)
}

func TestBlockLvPointerWithManyLogs(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	for block := uint64(1); block <= 1000; block++ {
		var logs []*ethtypes.Log
		numLogs := (block % 100) + 1
		for i := uint64(0); i < numLogs; i++ {
			logs = append(logs, &ethtypes.Log{
				Address: common.HexToAddress("0x1"),
				Topics:  []common.Hash{{byte(i)}},
			})
		}
		indexer.IndexLogs(block, logs)
	}
	
	ptr1, err := indexer.getBlockLvPointer(1)
	require.NoError(t, err)
	require.Equal(t, uint64(0), ptr1)
	
	ptr500, err := indexer.getBlockLvPointer(500)
	require.NoError(t, err)
	require.Greater(t, ptr500, uint64(10000))
	
	firstIdx, lastIdx := indexer.getLogIndexRange(100, 200)
	require.Less(t, firstIdx, lastIdx)
	
	_, err = indexer.getBlockLvPointer(500)
	require.NoError(t, err)
}

func TestMapBoundaryTransition(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	logsPerBlock := 1000
	blocksForFirstMap := (LogsPerMap / logsPerBlock) + 1
	
	totalLogs := uint64(0)
	for block := uint64(1); block <= uint64(blocksForFirstMap); block++ {
		var logs []*ethtypes.Log
		numLogs := logsPerBlock
		if totalLogs + uint64(numLogs) > LogsPerMap {
			numLogs = int(LogsPerMap - totalLogs)
		}
		for i := 0; i < numLogs; i++ {
			logs = append(logs, &ethtypes.Log{
				Address: common.HexToAddress("0x1"),
				Topics:  []common.Hash{{byte(i)}},
			})
		}
		indexer.IndexLogs(block, logs)
		totalLogs += uint64(numLogs)
		
		if totalLogs >= LogsPerMap {
			break
		}
	}
	
	require.Equal(t, uint64(LogsPerMap), indexer.totalLogIndex)
	require.Equal(t, uint64(LogsPerMap), indexer.logCounter)
	require.Equal(t, uint32(0), indexer.nextMapID)
	
	extraLogs := []*ethtypes.Log{{
		Address: common.HexToAddress("0x2"),
		Topics:  []common.Hash{{0xff}},
	}}
	indexer.IndexLogs(100, extraLogs)
	
	require.Equal(t, uint32(1), indexer.nextMapID)
	require.Equal(t, uint64(1), indexer.logCounter)
	
	ptr, err := indexer.getBlockLvPointer(100)
	require.NoError(t, err)
	require.Equal(t, uint64(LogsPerMap), ptr)
}

func TestFilterMapSearch(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	targetAddr := common.HexToAddress("0xDEADBEEF")
	targetTopic := common.HexToHash("0xCAFEBABE")
	
	logs1 := []*ethtypes.Log{
		{
			Address:     targetAddr,
			Topics:      []common.Hash{targetTopic},
			BlockNumber: 1,
		},
		{
			Address:     common.HexToAddress("0x1"),
			Topics:      []common.Hash{{0x1}},
			BlockNumber: 1,
		},
	}
	indexer.IndexLogs(1, logs1)
	
	logs2 := []*ethtypes.Log{
		{
			Address:     common.HexToAddress("0x2"),
			Topics:      []common.Hash{{0x2}},
			BlockNumber: 2,
		},
	}
	indexer.IndexLogs(2, logs2)
	
	logs3 := []*ethtypes.Log{
		{
			Address:     targetAddr,
			Topics:      []common.Hash{targetTopic, {0x3}},
			BlockNumber: 3,
		},
	}
	indexer.IndexLogs(3, logs3)
	
	ctx := &mockContext{}
	results, err := indexer.FindLogsByRange(
		ctx.Context(),
		1, 3,
		[]common.Address{targetAddr},
		[][]common.Hash{{targetTopic}},
	)
	
	require.NoError(t, err)
	require.Len(t, results, 2)
	
	for _, log := range results {
		require.Equal(t, targetAddr, log.Address)
		require.Equal(t, targetTopic, log.Topics[0])
	}
}

func TestEmptyBlockHandling(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	indexer.IndexLogs(1, []*ethtypes.Log{})
	indexer.IndexLogs(2, []*ethtypes.Log{
		{Address: common.HexToAddress("0x1"), Topics: []common.Hash{{0x1}}},
	})
	indexer.IndexLogs(3, []*ethtypes.Log{})
	indexer.IndexLogs(4, []*ethtypes.Log{})
	indexer.IndexLogs(5, []*ethtypes.Log{
		{Address: common.HexToAddress("0x2"), Topics: []common.Hash{{0x2}}},
		{Address: common.HexToAddress("0x3"), Topics: []common.Hash{{0x3}}},
	})
	
	ptr1, _ := indexer.getBlockLvPointer(1)
	ptr2, _ := indexer.getBlockLvPointer(2)
	ptr3, _ := indexer.getBlockLvPointer(3)
	ptr4, _ := indexer.getBlockLvPointer(4)
	ptr5, _ := indexer.getBlockLvPointer(5)
	
	require.Equal(t, uint64(0), ptr1)
	require.Equal(t, uint64(0), ptr2)
	require.Equal(t, uint64(1), ptr3)
	require.Equal(t, uint64(1), ptr4)
	require.Equal(t, uint64(1), ptr5)
	
	require.Equal(t, uint64(3), indexer.totalLogIndex)
}

func TestPersistenceAcrossRestart(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	
	indexer1 := NewFilterMapsIndexer(db, logger)
	
	logs := []*ethtypes.Log{
		{Address: common.HexToAddress("0x1"), Topics: []common.Hash{{0x1}}},
		{Address: common.HexToAddress("0x2"), Topics: []common.Hash{{0x2}}},
	}
	indexer1.IndexLogs(1, logs)
	indexer1.IndexLogs(2, logs)
	
	ptr1Before, err := indexer1.getBlockLvPointer(1)
	require.NoError(t, err)
	ptr2Before, err := indexer1.getBlockLvPointer(2)
	require.NoError(t, err)
	
	indexer2 := NewFilterMapsIndexer(db, logger)
	
	ptr1After, err := indexer2.getBlockLvPointer(1)
	require.NoError(t, err)
	ptr2After, err := indexer2.getBlockLvPointer(2)
	require.NoError(t, err)
	
	require.Equal(t, ptr1Before, ptr1After)
	require.Equal(t, ptr2Before, ptr2After)
}

func TestConcurrentAccess(t *testing.T) {
	db := dbm.NewMemDB()
	logger := log.NewNopLogger()
	indexer := NewFilterMapsIndexer(db, logger)
	
	for i := uint64(1); i <= 100; i++ {
		logs := []*ethtypes.Log{{
			Address: common.HexToAddress("0x1"),
			Topics:  []common.Hash{{byte(i)}},
		}}
		indexer.IndexLogs(i, logs)
	}
	
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := uint64(1); j <= 100; j++ {
				_, err := indexer.getBlockLvPointer(j)
				require.NoError(t, err)
			}
		}(i)
	}
	
	wg.Wait()
}

type mockContext struct{}

func (m *mockContext) Context() context.Context {
	return context.Background()
}