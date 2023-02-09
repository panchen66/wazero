package gojs

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// Errno is a (GOARCH=wasm) error, which must match a key in mapJSError.
//
// See https://github.com/golang/go/blob/go1.20/src/syscall/tables_js.go#L371-L494
type Errno struct {
	s string
}

// Error implements error.
func (e *Errno) Error() string {
	return e.s
}

// This order match constants from wasi_snapshot_preview1.ErrnoSuccess for
// easier maintenance.
var (
	// ErrnoBadf Bad file descriptor.
	ErrnoBadf = &Errno{"EBADF"}
	// ErrnoExist File exists.
	ErrnoExist = &Errno{"EEXIST"}
	// ErrnoInval Invalid argument.
	ErrnoInval = &Errno{"EINVAL"}
	// ErrnoIo I/O error.
	ErrnoIo = &Errno{"EIO"}
	// ErrnoIsdir Is a directory.
	ErrnoIsdir = &Errno{"EISDIR"}
	// ErrnoLoop Too many levels of symbolic links.
	ErrnoLoop = &Errno{"ELOOP"}
	// ErrnoNametoolong Filename too long.
	ErrnoNametoolong = &Errno{"ENAMETOOLONG"}
	// ErrnoNoent No such file or directory.
	ErrnoNoent = &Errno{"ENOENT"}
	// ErrnoNosys function not supported.
	ErrnoNosys = &Errno{"ENOSYS"}
	// ErrnoNotdir Not a directory or a symbolic link to a directory.
	ErrnoNotdir = &Errno{"ENOTDIR"}
	// ErrnoNotempty Directory not empty.
	ErrnoNotempty = &Errno{"ENOTEMPTY"}
	// ErrnoNotsup Not supported, or operation not supported on socket.
	ErrnoNotsup = &Errno{"ENOTSUP"}
	// ErrnoPerm Operation not permitted.
	ErrnoPerm = &Errno{"EPERM"}
)

// ToErrno maps I/O errors as the message must be the code, ex. "EINVAL", not
// the message, e.g. "invalid argument".
//
// This should match wasi_snapshot_preview1.ToErrno for maintenance ease.
func ToErrno(err error) *Errno {
	if pe, ok := err.(*os.PathError); ok {
		err = pe.Unwrap()
	}
	if se, ok := err.(syscall.Errno); ok {
		return errnoFromSyscall(se)
	}
	// Below are all the fs.ErrXXX in fs.go. errors.Is is more expensive, so
	// try it last. Note: Once we have our own file type, we should never see
	// these.
	switch {
	case errors.Is(err, fs.ErrInvalid):
		return ErrnoInval
	case errors.Is(err, fs.ErrPermission):
		return ErrnoPerm
	case errors.Is(err, fs.ErrExist):
		return ErrnoExist
	case errors.Is(err, fs.ErrNotExist):
		return ErrnoNoent
	case errors.Is(err, fs.ErrClosed):
		return ErrnoBadf
	default:
		return ErrnoIo
	}
}

func errnoFromSyscall(errno syscall.Errno) *Errno {
	switch errno {
	case syscall.EBADF:
		return ErrnoBadf
	case syscall.EEXIST:
		return ErrnoExist
	case syscall.EINVAL:
		return ErrnoInval
	case syscall.EIO:
		return ErrnoIo
	case syscall.EISDIR:
		return ErrnoIsdir
	case syscall.ELOOP:
		return ErrnoLoop
	case syscall.ENAMETOOLONG:
		return ErrnoNametoolong
	case syscall.ENOENT:
		return ErrnoNoent
	case syscall.ENOSYS:
		return ErrnoNosys
	case syscall.ENOTDIR:
		return ErrnoNotdir
	case syscall.ENOTEMPTY:
		return ErrnoNotempty
	case syscall.ENOTSUP:
		return ErrnoNotsup
	case syscall.EPERM:
		return ErrnoPerm
	default:
		return ErrnoIo
	}
}
