package statedb

type FFIDatabase interface {
	Basic([]byte) ([]byte, error)
	GetBlockHash(blockNumber uint64) []byte
	GetCodeByHash(codeHash []byte) []byte
	GetStorage(address []byte, storageKey []byte) []byte
}

type FFIDatabaseCommit interface {
	BatchCommit(st []byte, acc []byte, del []byte) error
}
