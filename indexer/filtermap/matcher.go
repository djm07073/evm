package filtermap

import (
	"context"
	"slices"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/filtermaps"
	"github.com/ethereum/go-ethereum/core/types"
)

func (fmi *FilterMapsIndexer) FindLogsByRange(
	ctx context.Context,
	firstBlock, lastBlock uint64,
	addresses []common.Address,
	topics [][]common.Hash,
) ([]*types.Log, error) {
	firstIndex, lastIndex := fmi.getLogIndexRange(firstBlock, lastBlock)
	if firstIndex > lastIndex {
		return nil, nil
	}

	m := &matcher{
		ctx:        ctx,
		indexer:    fmi,
		params:     fmi.params,
		addresses:  addresses,
		topics:     topics,
		firstBlock: firstBlock,
		lastBlock:  lastBlock,
		firstIndex: firstIndex,
		lastIndex:  lastIndex,
		firstMap:   uint32(firstIndex >> fmi.params.logValuesPerMap),
		lastMap:    uint32(lastIndex >> fmi.params.logValuesPerMap),
	}

	return m.process()
}

type matcher struct {
	ctx                   context.Context
	indexer               *FilterMapsIndexer
	params                *Params
	addresses             []common.Address
	topics                [][]common.Hash
	firstBlock, lastBlock uint64 // Block range
	firstIndex, lastIndex uint64 // Log index range
	firstMap, lastMap     uint32
}

func (m *matcher) process() ([]*types.Log, error) {
	type task struct {
		epochIndex uint32
		logs       []*types.Log
		err        error
		done       chan struct{}
	}

	taskCh := make(chan *task)
	var wg sync.WaitGroup
	defer func() {
		close(taskCh)
		wg.Wait()
	}()

	worker := func() {
		for task := range taskCh {
			if task == nil {
				break
			}
			task.logs, task.err = m.processEpoch(task.epochIndex)
			close(task.done)
		}
		wg.Done()
	}

	for range 4 {
		wg.Add(1)
		go worker()
	}

	firstEpoch := m.firstMap >> m.params.logMapsPerEpoch
	lastEpoch := m.lastMap >> m.params.logMapsPerEpoch

	var logs []*types.Log
	startEpoch, waitEpoch := firstEpoch, firstEpoch
	tasks := make(map[uint32]*task)
	tasks[startEpoch] = &task{epochIndex: startEpoch, done: make(chan struct{})}

	for waitEpoch <= lastEpoch {
		select {
		case taskCh <- tasks[startEpoch]:
			startEpoch++
			if startEpoch <= lastEpoch {
				if tasks[startEpoch] == nil {
					tasks[startEpoch] = &task{epochIndex: startEpoch, done: make(chan struct{})}
				}
			}

		case <-tasks[waitEpoch].done:
			if tasks[waitEpoch].err != nil {
				return nil, tasks[waitEpoch].err
			}
			logs = append(logs, tasks[waitEpoch].logs...)
			delete(tasks, waitEpoch)
			waitEpoch++

		case <-m.ctx.Done():
			return nil, m.ctx.Err()
		}
	}

	return logs, nil
}

func (m *matcher) processEpoch(epochIndex uint32) ([]*types.Log, error) {
	firstMap := epochIndex << m.params.logMapsPerEpoch
	lastMap := firstMap + m.params.mapsPerEpoch - 1
	if firstMap < m.firstMap {
		firstMap = m.firstMap
	}
	if lastMap > m.lastMap {
		lastMap = m.lastMap
	}

	var logs []*types.Log
	for mapIndex := firstMap; mapIndex <= lastMap; mapIndex++ {
		mapLogs := m.processMap(mapIndex)
		logs = append(logs, mapLogs...)
	}

	return logs, nil
}

func (m *matcher) processMap(mapIndex uint32) []*types.Log {
	fm := m.indexer.getFilterMap(mapIndex)
	if fm == nil {
		return nil
	}

	logData := m.indexer.getLogData(mapIndex)
	if logData == nil {
		return nil
	}

	matches := make(map[uint64]bool)

	if len(m.addresses) > 0 {
		for _, addr := range m.addresses {
			addrValue := addressValue(addr)
			rows := m.getRowsForValue(fm, mapIndex, addrValue)
			potentials := m.params.potentialMatches(rows, mapIndex, addrValue)
			for _, p := range potentials {
				matches[p] = true
			}
		}
	} else {
		mapFirst := uint64(mapIndex) << m.params.logValuesPerMap
		for i := uint64(0); i < uint64(len(logData.Logs)); i++ {
			matches[mapFirst+i] = true
		}
	}

	for _, topicList := range m.topics {
		if len(topicList) == 0 {
			continue
		}

		topicMatches := make(map[uint64]bool)
		for _, topic := range topicList {
			topicVal := topicValue(topic)
			rows := m.getRowsForValue(fm, mapIndex, topicVal)
			potentials := m.params.potentialMatches(rows, mapIndex, topicVal)
			for _, p := range potentials {
				if matches[p] {
					topicMatches[p] = true
				}
			}
		}

		matches = topicMatches
	}

	var result []*types.Log
	mapFirst := uint64(mapIndex) << m.params.logValuesPerMap

	for matchIdx := range matches {
		localIdx := matchIdx - mapFirst
		if localIdx >= uint64(len(logData.Logs)) {
			continue
		}

		log := logData.Logs[localIdx]

		if log.BlockNumber < m.firstBlock || log.BlockNumber > m.lastBlock {
			continue
		}

		if m.verifyLog(log) {
			result = append(result, log)
		}
	}

	return result
}

func (m *matcher) getRowsForValue(fm FilterMap, mapIndex uint32, logValue common.Hash) []filtermaps.FilterRow {
	var rows []filtermaps.FilterRow
	for layerIndex := uint32(0); ; layerIndex++ {
		rowIdx := m.params.rowIndex(mapIndex, layerIndex, logValue)
		if rowIdx >= uint32(len(fm)) || fm[rowIdx] == nil {
			break
		}

		rows = append(rows, fm[rowIdx])

		if len(fm[rowIdx]) < int(m.params.maxRowLength(layerIndex)) {
			break
		}
	}
	return rows
}

func (m *matcher) verifyLog(log *types.Log) bool {
	if len(m.addresses) > 0 {
		if !slices.Contains(m.addresses, log.Address) {
			return false
		}
	}

	for i, topicList := range m.topics {
		if len(topicList) == 0 {
			continue
		}
		if i >= len(log.Topics) {
			return false
		}

		if !slices.Contains(topicList, log.Topics[i]) {
			return false
		}
	}

	return true
}

func (fmi *FilterMapsIndexer) getLogIndexRange(firstBlock, lastBlock uint64) (uint64, uint64) {
	fmi.mu.RLock()
	defer fmi.mu.RUnlock()

	if lastBlock > fmi.latestBlock {
		lastBlock = fmi.latestBlock
	}

	// Get exact log index from BlockLvPointer
	firstIndex, err := fmi.getBlockLvPointer(firstBlock)
	if err != nil {
		// Fallback to estimation if block not indexed yet
		firstIndex = firstBlock * 10
	}
	
	lastIndex, err := fmi.getBlockLvPointer(lastBlock + 1)
	if err != nil {
		// Fallback to estimation if block not indexed yet
		lastIndex = (lastBlock + 1) * 10
		if lastIndex > fmi.totalLogIndex {
			lastIndex = fmi.totalLogIndex
		}
	}

	if lastIndex > 0 {
		lastIndex--
	}

	return firstIndex, lastIndex
}

func (fmi *FilterMapsIndexer) getFilterMap(mapIndex uint32) FilterMap {
	fmi.mu.RLock()
	defer fmi.mu.RUnlock()

	if mapIndex == fmi.nextMapID && fmi.currentMap != nil {
		return fmi.currentMap
	}

	if fm, ok := fmi.filterMapCache.Get(mapIndex); ok {
		return fm
	}

	fm := fmi.loadFilterMap(mapIndex)
	if fm != nil {
		fmi.filterMapCache.Add(mapIndex, fm)
	}

	return fm
}

func (fmi *FilterMapsIndexer) getLogData(mapIndex uint32) *LogData {
	fmi.mu.RLock()
	defer fmi.mu.RUnlock()

	if mapIndex == fmi.nextMapID && fmi.currentLogData != nil {
		return fmi.currentLogData
	}

	if ld, ok := fmi.logDataCache.Get(mapIndex); ok {
		return ld
	}

	ld := fmi.loadLogData(mapIndex)
	if ld != nil {
		fmi.logDataCache.Add(mapIndex, ld)
	}

	return ld
}
