package statedb

import (
	"github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
)

var (
	_ FFIDatabase       = &StateDB{}
	_ FFIDatabaseCommit = &StateDB{}
)

// This one doesn't use stateobject because it doesn't need to.
// Basic implements FFIDatabase.
// ffi call from basic
func (s *StateDB) Basic(addrBytes []byte) ([]byte, error) {
	addr := common.BytesToAddress(addrBytes)
	account := s.keeper.GetAccount(s.ctx, addr)
	iAccount, err := proto.Marshal(&types.InternalAccount{
		Balance:  account.Balance.Bytes(),
		Nonce:    account.Nonce,
		CodeHash: account.CodeHash,
	})
	if err != nil {
		return nil, err
	}
	return iAccount, err
}

// ffi call from code_by_hash
func (s *StateDB) GetCodeByHash(ch []byte) []byte {
	codeHash := common.BytesToHash(ch)
	return s.keeper.GetCode(s.ctx, codeHash)
}

// ffi call from block_hash
func (s *StateDB) GetBlockHash(number uint64) []byte {
	hash := s.blockCtx.GetHash(number)
	return hash.Bytes()
}

// ffi call from storage
func (s *StateDB) GetStorage(addr []byte, k []byte) []byte {
	address := common.BytesToAddress(addr)
	hash := common.BytesToHash(k)
	obj := s.GetState(address, hash)
	return obj.Bytes()
}

func (s *StateDB) BatchCommit(st []byte, acc []byte, del []byte) error {
	cacheCtx, write := s.ctx.CacheContext()
	// set istorages
	var istorages types.InternalStorages
	err := proto.Unmarshal(st, &istorages)
	if err != nil {
		return err
	}
	for addr, kv := range istorages.GetStorages() {
		for key, value := range kv.GetStorage() {
			s.keeper.SetState(cacheCtx, common.HexToAddress(addr), common.HexToHash(key), value)
		}
	}
	// set accounts
	var iaccounts types.InternalAccounts
	err = proto.Unmarshal(acc, &iaccounts)
	if err != nil {
		return err
	}
	for addr, acc := range iaccounts.GetAccounts() {
		balance := new(uint256.Int).SetBytes(acc.GetBalance())
		codeHash := acc.GetCodeHash()
		stateacc := Account{
			Balance:  balance,
			Nonce:    acc.GetNonce(),
			CodeHash: codeHash,
		}
		s.keeper.SetAccount(cacheCtx, common.HexToAddress(addr), stateacc)
		s.keeper.SetCode(cacheCtx, codeHash, acc.GetCode())
	}
	// delete contract by self destructed opcodes
	var ideleted types.InternalDeleted
	err = proto.Unmarshal(del, &ideleted)
	if err != nil {
		return err
	}
	for _, del := range ideleted.GetDeleted() {
		s.SelfDestruct6780(common.BytesToAddress(del))
	}

	write()

	return nil
}
