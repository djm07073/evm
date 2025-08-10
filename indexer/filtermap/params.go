package filtermap

import (
	"crypto/sha256"
	"encoding/binary"
	"hash/fnv"
	"math"

	"github.com/ethereum/go-ethereum/common"
)

// copy from github.com/ethereum/go-ethereum/core/filtermaps/math.go

type Params struct {
	logMapHeight       uint
	logMapWidth        uint
	logMapsPerEpoch    uint
	logValuesPerMap    uint
	baseRowLengthRatio uint
	logLayerDiff       uint
	mapHeight          uint32
	mapsPerEpoch       uint32
	baseRowLength      uint32
	valuesPerMap       uint64
}

var DefaultParams = Params{
	logMapHeight:       16,
	logMapWidth:        24,
	logMapsPerEpoch:    10,
	logValuesPerMap:    16,
	baseRowLengthRatio: 8,
	logLayerDiff:       4,
}

func init() {
	DefaultParams.deriveFields()
}

func (p *Params) deriveFields() {
	p.mapHeight = uint32(1) << p.logMapHeight
	p.mapsPerEpoch = uint32(1) << p.logMapsPerEpoch
	p.valuesPerMap = uint64(1) << p.logValuesPerMap
	p.baseRowLength = uint32(p.valuesPerMap * uint64(p.baseRowLengthRatio) / uint64(p.mapHeight))
}

func addressValue(address common.Address) common.Hash {
	var result common.Hash
	hasher := sha256.New()
	hasher.Write(address[:])
	hasher.Sum(result[:0])
	return result
}

func topicValue(topic common.Hash) common.Hash {
	var result common.Hash
	hasher := sha256.New()
	hasher.Write(topic[:])
	hasher.Sum(result[:0])
	return result
}

func (p *Params) rowIndex(mapIndex, layerIndex uint32, logValue common.Hash) uint32 {
	hasher := sha256.New()
	hasher.Write(logValue[:])
	var indexEnc [8]byte
	binary.LittleEndian.PutUint32(indexEnc[0:4], p.maskedMapIndex(mapIndex, layerIndex))
	binary.LittleEndian.PutUint32(indexEnc[4:8], layerIndex)
	hasher.Write(indexEnc[:])
	var hash common.Hash
	hasher.Sum(hash[:0])
	return binary.LittleEndian.Uint32(hash[:4]) % p.mapHeight
}

func (p *Params) columnIndex(lvIndex uint64, logValue *common.Hash) uint32 {
	var indexEnc [8]byte
	binary.LittleEndian.PutUint64(indexEnc[:], lvIndex)
	hasher := fnv.New64a()
	hasher.Write(indexEnc[:])
	hasher.Write(logValue[:])
	hash := hasher.Sum64()
	hashBits := p.logMapWidth - p.logValuesPerMap
	return uint32(lvIndex%p.valuesPerMap)<<hashBits + (uint32(hash>>(64-hashBits)) ^ uint32(hash)>>(32-hashBits))
}

func (p *Params) maskedMapIndex(mapIndex, layerIndex uint32) uint32 {
	logLayerDiff := uint(layerIndex) * p.logLayerDiff
	if logLayerDiff > p.logMapsPerEpoch {
		logLayerDiff = p.logMapsPerEpoch
	}
	return mapIndex & (uint32(math.MaxUint32) << (p.logMapsPerEpoch - logLayerDiff))
}

func (p *Params) maxRowLength(layerIndex uint32) uint32 {
	logLayerDiff := uint(layerIndex) * p.logLayerDiff
	if logLayerDiff > p.logMapsPerEpoch {
		logLayerDiff = p.logMapsPerEpoch
	}
	return p.baseRowLength << logLayerDiff
}
