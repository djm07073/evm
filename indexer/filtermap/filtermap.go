package filtermap

import (
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/filtermaps"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type FilterMap []filtermaps.FilterRow

func NewFilterMap(params *Params) FilterMap {
	return make(FilterMap, params.mapHeight)
}

func (fm FilterMap) AddLogToMap(params *Params, mapIndex uint32, lvIndex uint64, address common.Address, topics []common.Hash) {
	addrValue := addressValue(address)
	fm.addValue(params, mapIndex, lvIndex, addrValue)

	for _, topic := range topics {
		topicVal := topicValue(topic)
		fm.addValue(params, mapIndex, lvIndex, topicVal)
	}
}

func (fm FilterMap) addValue(params *Params, mapIndex uint32, lvIndex uint64, logValue common.Hash) {
	colIndex := params.columnIndex(lvIndex, &logValue)

	for layerIndex := uint32(0); ; layerIndex++ {
		rowIdx := params.rowIndex(mapIndex, layerIndex, logValue)
		maxLen := params.maxRowLength(layerIndex)

		if fm[rowIdx] == nil {
			fm[rowIdx] = make(filtermaps.FilterRow, 0, maxLen)
		}

		if len(fm[rowIdx]) < int(maxLen) {
			fm[rowIdx] = append(fm[rowIdx], colIndex)
			return
		}
	}
}

type LogData struct {
	MapID      uint32
	StartBlock uint64
	EndBlock   uint64
	Logs       []*ethtypes.Log
}

type potentialMatches []uint64

func (params *Params) potentialMatches(rows []filtermaps.FilterRow, mapIndex uint32, logValue common.Hash) potentialMatches {
	results := make(potentialMatches, 0, 8)
	mapFirst := uint64(mapIndex) << params.logValuesPerMap

	for i, row := range rows {
		rowLen, maxLen := len(row), int(params.maxRowLength(uint32(i)))
		if rowLen > maxLen {
			rowLen = maxLen
		}

		for j := 0; j < rowLen; j++ {
			if potentialMatch := mapFirst + uint64(row[j]>>(params.logMapWidth-params.logValuesPerMap)); row[j] == params.columnIndex(potentialMatch, &logValue) {
				results = append(results, potentialMatch)
			}
		}

		if rowLen < maxLen {
			break
		}
	}

	// Sort and remove duplicates
	slices.Sort(results)
	j := 0
	for i, match := range results {
		if i == 0 || match != results[i-1] {
			results[j] = results[i]
			j++
		}
	}

	return results[:j]
}
