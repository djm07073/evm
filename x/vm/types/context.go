package types

import "github.com/ethereum/go-ethereum/core"

type ContextKey string

const ContextKeyEVMDMessages ContextKey = "evm_messages"

type EVMMessages struct {
	Messages     []*core.Message
	CurrentIndex int
}