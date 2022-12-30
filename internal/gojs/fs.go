package gojs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/internal/gojs/custom"
	"github.com/tetratelabs/wazero/internal/gojs/goos"
	"github.com/tetratelabs/wazero/internal/platform"
	internalsys "github.com/tetratelabs/wazero/internal/sys"
	"github.com/tetratelabs/wazero/internal/wasm"
)

var (
	// jsfs = js.Global().Get("fs") // fs_js.go init
	//
	// js.fsCall conventions:
	// * funcWrapper callback is the last parameter
	//   * arg0 is error and up to one result in arg1
	jsfs = newJsVal(goos.RefJsfs, custom.NameFs).
		addProperties(map[string]interface{}{
			"constants": jsfsConstants, // = jsfs.Get("constants") // init
		}).
		addFunction(custom.NameFsOpen, &jsfsOpen{}).
		addFunction(custom.NameFsStat, &jsfsStat{}).
		addFunction(custom.NameFsFstat, &jsfsFstat{}).
		addFunction(custom.NameFsLstat, &jsfsStat{}). // because fs.FS doesn't support symlink
		addFunction(custom.NameFsClose, &jsfsClose{}).
		addFunction(custom.NameFsRead, &jsfsRead{}).
		addFunction(custom.NameFsWrite, &jsfsWrite{}).
		addFunction(custom.NameFsReaddir, &jsfsReaddir{}).
		addFunction(custom.NameFsMkdir, &jsfsMkdir{}).
		addFunction(custom.NameFsRmdir, &jsfsRmdir{}).
		addFunction(custom.NameFsUnlink, &jsfsUnlink{}).
		addFunction(custom.NameFsUtimes, &jsfsUtimes{})

	// TODO: stub all these with syscall.ENOSYS
	//	* _, err := fsCall("chmod", path, mode) // syscall.Chmod
	//	* _, err := fsCall("fchmod", fd, mode) // syscall.Fchmod
	//	* _, err := fsCall("chown", path, uint32(uid), uint32(gid)) // syscall.Chown
	//	* _, err := fsCall("fchown", fd, uint32(uid), uint32(gid)) // syscall.Fchown
	//	* _, err := fsCall("lchown", path, uint32(uid), uint32(gid)) // syscall.Lchown
	//	* _, err := fsCall("rename", from, to) // syscall.Rename
	//	* _, err := fsCall("truncate", path, length) // syscall.Truncate
	//	* _, err := fsCall("ftruncate", fd, length) // syscall.Ftruncate
	//	* dst, err := fsCall("readlink", path) // syscall.Readlink
	//	* _, err := fsCall("link", path, link) // syscall.Link
	//	* _, err := fsCall("symlink", path, link) // syscall.Symlink
	//	* _, err := fsCall("fsync", fd) // syscall.Fsync

	// jsfsConstants = jsfs Get("constants") // fs_js.go init
	jsfsConstants = newJsVal(goos.RefJsfsConstants, "constants").
			addProperties(map[string]interface{}{
			"O_WRONLY": oWRONLY,
			"O_RDWR":   oRDWR,
			"O_CREAT":  oCREAT,
			"O_TRUNC":  oTRUNC,
			"O_APPEND": oAPPEND,
			"O_EXCL":   oEXCL,
		})

	// oWRONLY = jsfsConstants Get("O_WRONLY").Int() // fs_js.go init
	oWRONLY = float64(os.O_WRONLY)

	// oRDWR = jsfsConstants Get("O_RDWR").Int() // fs_js.go init
	oRDWR = float64(os.O_RDWR)

	// o CREAT = jsfsConstants Get("O_CREAT").Int() // fs_js.go init
	oCREAT = float64(os.O_CREATE)

	// oTRUNC = jsfsConstants Get("O_TRUNC").Int() // fs_js.go init
	oTRUNC = float64(os.O_TRUNC)

	// oAPPEND = jsfsConstants Get("O_APPEND").Int() // fs_js.go init
	oAPPEND = float64(os.O_APPEND)

	// oEXCL = jsfsConstants Get("O_EXCL").Int() // fs_js.go init
	oEXCL = float64(os.O_EXCL)
)

// jsfsOpen implements fs.Open
//
//	jsFD /* Int */, err := fsCall("open", path, flags, perm)
type jsfsOpen struct{}

// invoke implements jsFn.invoke
func (*jsfsOpen) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	flags := toUint64(args[1]) // flags are derived from constants like oWRONLY
	perm := toUint32(args[2])
	callback := args[3].(funcWrapper)

	fd, err := syscallOpen(mod, name, flags, perm)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, fd) // note: error first
}

// jsfsStat is used for syscall.Stat
//
//	jsSt, err := fsCall("stat", path)
type jsfsStat struct{}

// invoke implements jsFn.invoke
func (*jsfsStat) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	callback := args[1].(funcWrapper)

	stat, err := syscallStat(mod, name)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, stat) // note: error first
}

// syscallStat is like syscall.Stat
func syscallStat(mod api.Module, name string) (*jsSt, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()
	if fd, err := fsc.OpenFile(name, os.O_RDONLY, 0); err != nil {
		return nil, err
	} else {
		defer fsc.CloseFile(fd)
		return syscallFstat(fsc, fd)
	}
}

// jsfsStat is used for syscall.Open
//
//	stat, err := fsCall("fstat", fd); err == nil && stat.Call("isDirectory").Bool()
type jsfsFstat struct{}

// invoke implements jsFn.invoke
func (*jsfsFstat) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	fd := toUint32(args[0])
	callback := args[1].(funcWrapper)

	fstat, err := syscallFstat(fsc, fd)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, fstat) // note: error first
}

// mode constants from syscall_js.go
const (
	S_IFSOCK = uint32(0o000140000)
	S_IFLNK  = uint32(0o000120000)
	S_IFREG  = uint32(0o000100000)
	S_IFBLK  = uint32(0o000060000)
	S_IFDIR  = uint32(0o000040000)
	S_IFCHR  = uint32(0o000020000)
	S_IFIFO  = uint32(0o000010000)

	S_ISUID = uint32(0o004000)
	S_ISGID = uint32(0o002000)
	S_ISVTX = uint32(0o001000)
)

// syscallFstat is like syscall.Fstat
func syscallFstat(fsc *internalsys.FSContext, fd uint32) (*jsSt, error) {
	if f, ok := fsc.OpenedFile(fd); !ok {
		return nil, syscall.EBADF
	} else if stat, err := f.File.Stat(); err != nil {
		return nil, err
	} else {
		ret := &jsSt{}
		ret.isDir = stat.IsDir()
		ret.mode = getJsMode(stat.Mode())
		ret.size = stat.Size()
		atimeSec, atimeNsec, mtimeSec, mtimeNsec, ctimeSec, ctimeNsec := platform.StatTimes(stat)
		ret.atimeMs = atimeSec*1e3 + atimeNsec/1e6
		ret.mtimeMs = mtimeSec*1e3 + mtimeNsec/1e6
		ret.ctimeMs = ctimeSec*1e3 + ctimeNsec/1e6
		return ret, nil
	}
}

// getJsMode is required because the mode property read in `GOOS=js` is
// incompatible with normal go. Particularly the directory flag isn't the same.
func getJsMode(mode fs.FileMode) (jsMode uint32) {
	jsMode = uint32(mode & fs.ModePerm)
	switch mode & fs.ModeType {
	case fs.ModeDir:
		jsMode |= S_IFDIR
	case fs.ModeSymlink:
		jsMode |= S_IFLNK
	case fs.ModeNamedPipe:
		jsMode |= S_IFIFO
	case fs.ModeSocket:
		jsMode |= S_IFSOCK
	case fs.ModeDevice:
		jsMode |= S_IFBLK
	case fs.ModeCharDevice:
		jsMode |= S_IFCHR
	case fs.ModeIrregular:
		// unmapped to js
	}

	if mode&fs.ModeType == 0 {
		jsMode |= S_IFREG
	}
	if mode&fs.ModeSetgid != 0 {
		jsMode |= S_ISGID
	}
	if mode&fs.ModeSetuid != 0 {
		jsMode |= S_ISUID
	}
	if mode&fs.ModeSticky != 0 {
		jsMode |= S_ISVTX
	}
	return
}

// jsfsClose is used for syscall.Close
//
//	_, err := fsCall("close", fd)
type jsfsClose struct{}

// invoke implements jsFn.invoke
func (*jsfsClose) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	fd := toUint32(args[0])
	callback := args[1].(funcWrapper)

	err := syscallClose(fsc, fd)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, true) // note: error first
}

// syscallClose is like syscall.Close
func syscallClose(fsc *internalsys.FSContext, fd uint32) (err error) {
	if ok := fsc.CloseFile(fd); !ok {
		err = syscall.EBADF // already closed
	}
	return
}

// jsfsRead is used in syscall.Read and syscall.Pread, called by
// src/internal/poll/fd_unix.go poll.Read.
//
//	n, err := fsCall("read", fd, buf, 0, len(b), nil)
type jsfsRead struct{}

// invoke implements jsFn.invoke
func (*jsfsRead) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	fd := toUint32(args[0])
	buf, ok := args[1].(*byteArray)
	if !ok {
		return nil, fmt.Errorf("arg[1] is %v not a []byte", args[1])
	}
	offset := toUint32(args[2])
	byteCount := toUint32(args[3])
	fOffset := args[4] // nil unless Pread
	callback := args[5].(funcWrapper)

	n, err := syscallRead(mod, fd, fOffset, buf.slice[offset:offset+byteCount])
	return callback.invoke(ctx, mod, goos.RefJsfs, err, n) // note: error first
}

// syscallRead is like syscall.Read
func syscallRead(mod api.Module, fd uint32, offset interface{}, p []byte) (n uint32, err error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	r := fsc.FdReader(fd)
	if r == nil {
		err = syscall.EBADF
	}

	if offset != nil {
		if s, ok := r.(io.Seeker); ok {
			if _, err := s.Seek(toInt64(offset), io.SeekStart); err != nil {
				return 0, err
			}
		} else {
			return 0, syscall.ENOTSUP
		}
	}

	if nRead, e := r.Read(p); e == nil || e == io.EOF {
		// fs_js.go cannot parse io.EOF so coerce it to nil.
		// See https://github.com/golang/go/issues/43913
		n = uint32(nRead)
	} else {
		err = e
	}
	return
}

// jsfsWrite is used in syscall.Write and syscall.Pwrite.
//
// Notably, offset is non-nil in Pwrite.
//
//	n, err := fsCall("write", fd, buf, 0, len(b), nil)
type jsfsWrite struct{}

// invoke implements jsFn.invoke
func (*jsfsWrite) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	fd := toUint32(args[0])
	buf, ok := args[1].(*byteArray)
	if !ok {
		return nil, fmt.Errorf("arg[1] is %v not a []byte", args[1])
	}
	offset := toUint32(args[2])
	byteCount := toUint32(args[3])
	fOffset := args[4] // nil unless Pread
	callback := args[5].(funcWrapper)

	if byteCount > 0 { // empty is possible on EOF
		n, err := syscallWrite(mod, fd, fOffset, buf.slice[offset:offset+byteCount])
		return callback.invoke(ctx, mod, goos.RefJsfs, err, n) // note: error first
	}
	return callback.invoke(ctx, mod, goos.RefJsfs, nil, goos.RefValueZero)
}

// syscallWrite is like syscall.Write
func syscallWrite(mod api.Module, fd uint32, offset interface{}, p []byte) (n uint32, err error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	if writer := fsc.FdWriter(fd); writer == nil {
		err = syscall.EBADF
	} else if nWritten, e := writer.Write(p); e == nil || e == io.EOF {
		// fs_js.go cannot parse io.EOF so coerce it to nil.
		// See https://github.com/golang/go/issues/43913
		n = uint32(nWritten)
	} else {
		err = e
	}
	return
}

// jsfsReaddir is used in syscall.Open
//
//	dir, err := fsCall("readdir", path)
//		dir.Length(), dir.Index(i).String()
type jsfsReaddir struct{}

// invoke implements jsFn.invoke
func (*jsfsReaddir) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	callback := args[1].(funcWrapper)

	stat, err := syscallReaddir(ctx, mod, name)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, stat) // note: error first
}

func syscallReaddir(_ context.Context, mod api.Module, name string) (*objectArray, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	fd, err := fsc.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer fsc.CloseFile(fd)

	if f, ok := fsc.OpenedFile(fd); !ok {
		return nil, syscall.EBADF
	} else if d, ok := f.File.(fs.ReadDirFile); !ok {
		return nil, syscall.ENOTDIR
	} else if l, err := d.ReadDir(-1); err != nil {
		return nil, err
	} else {
		entries := make([]interface{}, 0, len(l))
		for _, e := range l {
			entries = append(entries, e.Name())
		}
		return &objectArray{entries}, nil
	}
}

type returnZero struct{}

// invoke implements jsFn.invoke
func (*returnZero) invoke(context.Context, api.Module, ...interface{}) (interface{}, error) {
	return goos.RefValueZero, nil
}

type returnSliceOfZero struct{}

// invoke implements jsFn.invoke
func (*returnSliceOfZero) invoke(context.Context, api.Module, ...interface{}) (interface{}, error) {
	return &objectArray{slice: []interface{}{goos.RefValueZero}}, nil
}

type returnArg0 struct{}

// invoke implements jsFn.invoke
func (*returnArg0) invoke(_ context.Context, _ api.Module, args ...interface{}) (interface{}, error) {
	return args[0], nil
}

// cwd for fs.Open syscall.Getcwd in fs_js.go
type cwd struct{}

// invoke implements jsFn.invoke
func (*cwd) invoke(ctx context.Context, _ api.Module, _ ...interface{}) (interface{}, error) {
	return getState(ctx).cwd, nil
}

// chdir for fs.Open syscall.Chdir in fs_js.go
type chdir struct{}

// invoke implements jsFn.invoke
func (*chdir) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()

	path := args[0].(string)

	// TODO: refactor so that sys has path-based ops, also needed in WASI.
	if fd, err := fsc.OpenFile(path, os.O_RDONLY, 0); err != nil {
		return nil, syscall.ENOENT
	} else if f, ok := fsc.OpenedFile(fd); !ok {
		return nil, syscall.ENOENT
	} else if s, err := f.File.Stat(); err != nil {
		fsc.CloseFile(fd)
		return nil, syscall.ENOENT
	} else if !s.IsDir() {
		fsc.CloseFile(fd)
		return nil, syscall.ENOTDIR
	} else {
		getState(ctx).cwd = path
		return nil, nil
	}
}

// jsfsMkdir implements fs.Mkdir
//
//	jsFD /* Int */, err := fsCall("mkdir", path, perm)
type jsfsMkdir struct{}

// invoke implements jsFn.invoke
func (*jsfsMkdir) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	perm := toUint32(args[1])
	callback := args[2].(funcWrapper)

	fd, err := syscallMkdir(mod, name, perm)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, fd) // note: error first
}

// syscallMkdir is like syscall.Mkdir
func syscallMkdir(mod api.Module, name string, perm uint32) (uint32, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()
	return fsc.Mkdir(name, fs.FileMode(perm))
}

// jsfsRmdir implements the following
//
//	_, err := fsCall("rmdir", path) // syscall.Rmdir
type jsfsRmdir struct{}

// invoke implements jsFn.invoke
func (*jsfsRmdir) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	callback := args[1].(funcWrapper)

	ok, err := syscallRmdir(mod, name)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, ok) // note: error first
}

// syscallRmdir is like syscall.Rmdir
func syscallRmdir(mod api.Module, name string) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()
	err := fsc.Rmdir(name)
	return err != nil, err
}

// jsfsUnlink implements the following
//
//	_, err := fsCall("unlink", path) // syscall.Unlink
type jsfsUnlink struct{}

// invoke implements jsFn.invoke
func (*jsfsUnlink) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	callback := args[1].(funcWrapper)

	ok, err := syscallUnlink(mod, name)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, ok) // note: error first
}

// syscallUnlink is like syscall.Unlink
func syscallUnlink(mod api.Module, name string) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()
	err := fsc.Unlink(name)
	return err != nil, err
}

// jsfsUtimes implements the following
//
//	_, err := fsCall("utimes", path, atime, mtime) // syscall.UtimesNano
type jsfsUtimes struct{}

// invoke implements jsFn.invoke
func (*jsfsUtimes) invoke(ctx context.Context, mod api.Module, args ...interface{}) (interface{}, error) {
	name := args[0].(string)
	atimeSec := toInt64(args[1])
	mtimeSec := toInt64(args[2])
	callback := args[3].(funcWrapper)

	ok, err := syscallUtimes(mod, name, atimeSec, mtimeSec)
	return callback.invoke(ctx, mod, goos.RefJsfs, err, ok) // note: error first
}

// syscallUtimes is like syscall.Utimes
func syscallUtimes(mod api.Module, name string, atimeSec, mtimeSec int64) (interface{}, error) {
	fsc := mod.(*wasm.CallContext).Sys.FS()
	err := fsc.Utimes(name, atimeSec, 0, mtimeSec, 0)
	return err != nil, err
}

// jsSt is pre-parsed from fs_js.go setStat to avoid thrashing
type jsSt struct {
	isDir   bool
	dev     int64
	ino     uint64
	mode    uint32
	nlink   uint32
	uid     uint32
	gid     uint32
	rdev    int64
	size    int64
	blksize int32
	blocks  int32
	atimeMs int64
	mtimeMs int64
	ctimeMs int64
}

// String implements fmt.Stringer
func (s *jsSt) String() string {
	return fmt.Sprintf("{isDir=%v,mode=%s,size=%d,mtimeMs=%d}", s.isDir, fs.FileMode(s.mode), s.size, s.mtimeMs)
}

// get implements jsGet.get
func (s *jsSt) get(_ context.Context, propertyKey string) interface{} {
	switch propertyKey {
	case "dev":
		return s.dev
	case "ino":
		return s.ino
	case "mode":
		return s.mode
	case "nlink":
		return s.nlink
	case "uid":
		return s.uid
	case "gid":
		return s.gid
	case "rdev":
		return s.rdev
	case "size":
		return s.size
	case "blksize":
		return s.blksize
	case "blocks":
		return s.blocks
	case "atimeMs":
		return s.atimeMs
	case "mtimeMs":
		return s.mtimeMs
	case "ctimeMs":
		return s.ctimeMs
	}
	panic(fmt.Sprintf("TODO: stat.%s", propertyKey))
}

// call implements jsCall.call
func (s *jsSt) call(_ context.Context, _ api.Module, _ goos.Ref, method string, _ ...interface{}) (interface{}, error) {
	if method == "isDirectory" {
		return s.isDir, nil
	}
	panic(fmt.Sprintf("TODO: stat.%s", method))
}
