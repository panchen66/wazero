package wazevo

import (
	"context"
	"reflect"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/internal/engine/wazevo/wazevoapi"
	"github.com/tetratelabs/wazero/internal/internalapi"
	"github.com/tetratelabs/wazero/internal/wasm"
	"github.com/tetratelabs/wazero/internal/wasmruntime"
)

type (
	// callEngine implements api.Function.
	callEngine struct {
		internalapi.WazeroOnly
		stack []byte
		// stackTop is the pointer to the *aligned* top of the stack. This must be updated
		// whenever the stack is changed. This is passed to the assembly function
		// at the very beginning of api.Function Call/CallWithStack.
		stackTop uintptr
		// executable is the pointer to the executable code for this function.
		executable *byte
		// parent is the *moduleEngine from which this callEngine is created.
		parent *moduleEngine
		// indexInModule is the index of the function in the module.
		indexInModule wasm.Index
		// sizeOfParamResultSlice is the size of the parameter/result slice.
		sizeOfParamResultSlice int
		// execCtx holds various information to be read/written by assembly functions.
		execCtx executionContext
		// execCtxPtr holds the pointer to the executionContext which doesn't change after callEngine is created.
		execCtxPtr uintptr
	}

	// executionContext is the struct to be read/written by assembly functions.
	executionContext struct {
		// exitCode holds the wazevoapi.ExitCode describing the state of the function execution.
		exitCode wazevoapi.ExitCode
		// callerModuleContextPtr holds the moduleContextOpaque for Go function calls.
		callerModuleContextPtr *byte
		// originalFramePointer holds the original frame pointer of the caller of the assembly function.
		originalFramePointer uintptr
		// originalStackPointer holds the original stack pointer of the caller of the assembly function.
		originalStackPointer uintptr
		// goReturnAddress holds the return address to go back to the caller of the assembly function.
		goReturnAddress uintptr
		// stackBottomPtr holds the pointer to the bottom of the stack.
		stackBottomPtr *byte
		// goCallReturnAddress holds the return address to go back to the caller of the Go function.
		goCallReturnAddress *byte
		// stackPointerBeforeGrow holds the stack pointer before stack grow.
		stackPointerBeforeGrow uintptr
		// stackGrowRequiredSize holds the required size of stack grow.
		stackGrowRequiredSize uintptr
		// _ is needed to align .savedRegisters at 16 bytes boundary.
		_ uint64
		// savedRegisters is the opaque spaces for save/restore registers.
		// We want to align 16 bytes for each register, so we use [64][2]uint64.
		savedRegisters [64][2]uint64
	}
)

var initialStackSize uint64 = 512

func (c *callEngine) init() {
	stackSize := initialStackSize
	if c.sizeOfParamResultSlice > int(stackSize) {
		stackSize = uint64(c.sizeOfParamResultSlice)
	}

	c.stack = make([]byte, stackSize)
	c.stackTop = alignedStackTop(c.stack)
	c.execCtx.stackBottomPtr = &c.stack[0]
	c.execCtxPtr = uintptr(unsafe.Pointer(&c.execCtx))
}

// alignedStackTop returns 16-bytes aligned stack top of given stack.
// 16 bytes should be good for all platform (arm64/amd64).
func alignedStackTop(s []byte) uintptr {
	stackAddr := uintptr(unsafe.Pointer(&s[len(s)-1]))
	return stackAddr - (stackAddr & (16 - 1))
}

// Definition implements api.Function.
func (c *callEngine) Definition() api.FunctionDefinition {
	return &c.parent.module.Source.FunctionDefinitionSection[c.indexInModule]
}

// Call implements api.Function.
func (c *callEngine) Call(ctx context.Context, params ...uint64) ([]uint64, error) {
	paramResultSlice := make([]uint64, c.sizeOfParamResultSlice)
	copy(paramResultSlice, params)
	if err := c.CallWithStack(ctx, paramResultSlice); err != nil {
		return nil, err
	}
	return paramResultSlice, nil
}

// CallWithStack implements api.Function.
func (c *callEngine) CallWithStack(ctx context.Context, paramResultStack []uint64) error {
	var paramResultPtr *uint64
	if len(paramResultStack) > 0 {
		paramResultPtr = &paramResultStack[0]
	}

	entrypoint(c.executable, c.execCtxPtr, c.parent.opaquePtr, paramResultPtr, c.stackTop)
	for {
		switch c.execCtx.exitCode {
		case wazevoapi.ExitCodeOK:
			return nil
		case wazevoapi.ExitCodeGrowStack:
			newsp, err := c.growStack()
			if err != nil {
				return err
			}
			c.execCtx.exitCode = wazevoapi.ExitCodeOK
			afterStackGrowEntrypoint(c.execCtx.goCallReturnAddress, c.execCtxPtr, newsp)
		case wazevoapi.ExitCodeUnreachable:
			return wasmruntime.ErrRuntimeUnreachable
		case wazevoapi.ExitCodeMemoryOutOfBounds:
			return wasmruntime.ErrRuntimeOutOfBoundsMemoryAccess
		default:
			panic("BUG")
		}
	}
}

const callStackCeiling = uintptr(5000000) // in uint64 (8 bytes) == 40000000 bytes in total == 40mb.

// growStack grows the stack, and returns the new stack pointer.
func (c *callEngine) growStack() (newSP uintptr, err error) {
	currentLen := uintptr(len(c.stack))
	if callStackCeiling < currentLen {
		err = wasmruntime.ErrRuntimeStackOverflow
		return
	}

	newLen := 2*currentLen + c.execCtx.stackGrowRequiredSize
	newStack := make([]byte, newLen)

	relSp := c.stackTop - c.execCtx.stackPointerBeforeGrow

	// Copy the existing contents in the previous Go-allocated stack into the new one.
	var prevStackAligned, newStackAligned []byte
	{
		sh := (*reflect.SliceHeader)(unsafe.Pointer(&prevStackAligned))
		sh.Data = c.stackTop - relSp
		sh.Len = int(relSp)
		sh.Cap = int(relSp)
	}
	newTop := alignedStackTop(newStack)
	{
		newSP = newTop - relSp
		sh := (*reflect.SliceHeader)(unsafe.Pointer(&newStackAligned))
		sh.Data = newSP
		sh.Len = int(relSp)
		sh.Cap = int(relSp)
	}
	copy(newStackAligned, prevStackAligned)

	c.stack = newStack
	c.stackTop = newTop
	c.execCtx.stackBottomPtr = &newStack[0]
	return
}
