package api

// #include <stdlib.h>
// #include "bindings.h"
import "C"

import (
	"fmt"
)

// Value types
type (
	cint   = C.int
	cbool  = C.bool
	cusize = C.size_t
	cu8    = C.uint8_t
	cu64   = C.uint64_t
)

// Pointers
type (
	cu8_ptr = *C.uint8_t
)

/**** To error module ***/

func errorWithMessage(err error, b C.UnmanagedVector) error {
	msg := copyAndDestroyUnmanagedVector(b)
	if msg == nil {
		return err
	}
	return fmt.Errorf("%s", string(msg))
}
