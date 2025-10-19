package api

// #include <stdlib.h>
// #include "bindings.h"
import "C"

import (
	"runtime"
	"syscall"

	"github.com/cosmos/evm/x/vm/statedb"
	vmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/proto"
)

// EVM represents an Ethereum Virtual Machine instance
type EVM struct {
	evm_ptr     *C.evm_t
	StateDB     statedb.StateDB
	hasCompiler bool
}

type EvmOptions struct {
	Spec SpecId
	*EvmCompilerOptions
}

type EvmCompilerOptions struct {
	Threshold  uint64
	MaxConcise uint
}

// FreeVM releases the VM instance
func FreeVM(vm EVM) {
	C.free_vm(vm.evm_ptr, C.bool(vm.hasCompiler))
}

// NewEVM initializes a new VM instance
func NewEVM(statedb statedb.StateDB, opt *EvmOptions) EVM {
	if opt == nil {
		opt = &EvmOptions{
			Spec: LATEST,
		}
	}

	if opt.EvmCompilerOptions != nil {
		return EVM{
			evm_ptr:     C.new_vm_with_compiler(cu8(opt.Spec), cu64(opt.Threshold), cusize(opt.MaxConcise)),
			StateDB:     statedb,
			hasCompiler: true,
		}
	}

	return EVM{
		evm_ptr:     C.new_vm(cu8(opt.Spec)),
		StateDB:     statedb,
		hasCompiler: false,
	}
}

// `Execute` executes a transaction on the VM
func (evm *EVM) Execute(
	block *[]byte,
	tx *[]byte,
) (*vmtypes.InternalEvmResult, error) {
	var err error
	dbState := buildDBState(evm.StateDB)
	db := buildDB(&dbState)

	blockBytesSliceView := makeView(*block)
	defer runtime.KeepAlive(blockBytesSliceView)
	txByteSliceView := makeView(*tx)
	defer runtime.KeepAlive(txByteSliceView)

	errmsg := uninitializedUnmanagedVector()
	res, err := C.execute_tx(evm.evm_ptr, C.bool(evm.hasCompiler), db, blockBytesSliceView, txByteSliceView, &errmsg)
	if err != nil && err.(syscall.Errno) != C.Success {
		// ignore the opereation times out error
		errno, ok := err.(syscall.Errno)
		if ok && errno == syscall.ETIMEDOUT || errno == syscall.ENOENT {
			return unmarshalResult(res)
		}
		return &vmtypes.InternalEvmResult{}, errorWithMessage(err, errmsg)
	}

	return unmarshalResult(res)
}

// unmarshalResult decodes the EVM result from the unmanaged vector
func unmarshalResult(res C.UnmanagedVector) (*vmtypes.InternalEvmResult, error) {
	vec := copyAndDestroyUnmanagedVector(res)
	var result vmtypes.InternalEvmResult
	err := proto.Unmarshal(vec, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
