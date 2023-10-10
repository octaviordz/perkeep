// Adapted from github.com/bazil/fuse

package xfz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/keybase/client/go/kbfs/dokan"
)

// The FUSE version implemented by the package.
const (
	protoVersionMinMajor = 7
	protoVersionMinMinor = 17
	protoVersionMaxMajor = 7
	protoVersionMaxMinor = 33
)

const (
	rootID = 1
)

// AttrFlags are bit flags that can be seen in Attr.Flags.
type AttrFlags uint32

const (
	// Node is a submount root.
	//
	// Don't use unless `Conn.Features` includes `InitSubMounts`.
	//
	// This doesn't seem to be usable outside of `virtio_fs``.
	attrSubMount AttrFlags = 1 << 0
)

var attrFlagsNames = []flagName{
	{uint32(attrSubMount), "AttrSubMount"},
}

func (fl AttrFlags) String() string {
	return flagString(uint32(fl), attrFlagsNames)
}

type attr struct {
	Ino       uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Nlink     uint32
	Uid       uint32
	Gid       uint32
	Rdev      uint32
	Blksize   uint32
	Flags     uint32
}

type kstatfs struct {
	Blocks  uint64
	Bfree   uint64
	Bavail  uint64
	Files   uint64
	Ffree   uint64
	Bsize   uint32
	Namelen uint32
	Frsize  uint32
	_       uint32
	Spare   [6]uint32
}

// GetattrFlags are bit flags that can be seen in GetattrRequest.
type GetattrFlags uint32

const (
	// Indicates the handle is valid.
	GetattrFh GetattrFlags = 1 << 0
)

var getattrFlagsNames = []flagName{
	{uint32(GetattrFh), "GetattrFh"},
}

func (fl GetattrFlags) String() string {
	return flagString(uint32(fl), getattrFlagsNames)
}

// The SetattrValid are bit flags describing which fields in the SetattrRequest
// are included in the change.
type SetattrValid uint32

const (
	SetattrMode        SetattrValid = 1 << 0
	SetattrUid         SetattrValid = 1 << 1
	SetattrGid         SetattrValid = 1 << 2
	SetattrSize        SetattrValid = 1 << 3
	SetattrAtime       SetattrValid = 1 << 4
	SetattrMtime       SetattrValid = 1 << 5
	SetattrHandle      SetattrValid = 1 << 6
	SetattrAtimeNow    SetattrValid = 1 << 7
	SetattrMtimeNow    SetattrValid = 1 << 8
	SetattrLockOwner   SetattrValid = 1 << 9 // http://www.mail-archive.com/git-commits-head@vger.kernel.org/msg27852.html
	SetattrCTime       SetattrValid = 1 << 10
	SetattrKillSUIDGID SetattrValid = 1 << 11
)

func (fl SetattrValid) Mode() bool               { return fl&SetattrMode != 0 }
func (fl SetattrValid) Uid() bool                { return fl&SetattrUid != 0 }
func (fl SetattrValid) Gid() bool                { return fl&SetattrGid != 0 }
func (fl SetattrValid) Size() bool               { return fl&SetattrSize != 0 }
func (fl SetattrValid) Atime() bool              { return fl&SetattrAtime != 0 }
func (fl SetattrValid) Mtime() bool              { return fl&SetattrMtime != 0 }
func (fl SetattrValid) Handle() bool             { return fl&SetattrHandle != 0 }
func (fl SetattrValid) AtimeNow() bool           { return fl&SetattrAtimeNow != 0 }
func (fl SetattrValid) MtimeNow() bool           { return fl&SetattrMtimeNow != 0 }
func (fl SetattrValid) LockOwner() bool          { return fl&SetattrLockOwner != 0 }
func (fl SetattrValid) SetattrCTime() bool       { return fl&SetattrCTime != 0 }
func (fl SetattrValid) SetattrKillSUIDGID() bool { return fl&SetattrKillSUIDGID != 0 }

func (fl SetattrValid) String() string {
	return flagString(uint32(fl), setattrValidNames)
}

var setattrValidNames = []flagName{
	{uint32(SetattrMode), "SetattrMode"},
	{uint32(SetattrUid), "SetattrUid"},
	{uint32(SetattrGid), "SetattrGid"},
	{uint32(SetattrSize), "SetattrSize"},
	{uint32(SetattrAtime), "SetattrAtime"},
	{uint32(SetattrMtime), "SetattrMtime"},
	{uint32(SetattrHandle), "SetattrHandle"},
	{uint32(SetattrAtimeNow), "SetattrAtimeNow"},
	{uint32(SetattrMtimeNow), "SetattrMtimeNow"},
	{uint32(SetattrLockOwner), "SetattrLockOwner"},
	{uint32(SetattrCTime), "SetattrCTime"},
	{uint32(SetattrKillSUIDGID), "SetattrKillSUIDGID"},
}

// Flags that can be seen in OpenRequest.Flags.
const (
	// Access modes. These are not 1-bit flags, but alternatives where
	// only one can be chosen. See the IsReadOnly etc convenience
	// methods.
	OpenReadOnly  OpenFlags = syscall.O_RDONLY
	OpenWriteOnly OpenFlags = syscall.O_WRONLY
	OpenReadWrite OpenFlags = syscall.O_RDWR

	// File was opened in append-only mode, all writes will go to end
	// of file. FreeBSD does not provide this information.
	OpenAppend    OpenFlags = syscall.O_APPEND
	OpenCreate    OpenFlags = syscall.O_CREAT
	OpenDirectory OpenFlags = 0x10000 //syscall.O_DIRECTORY
	OpenExclusive OpenFlags = syscall.O_EXCL
	OpenNonblock  OpenFlags = syscall.O_NONBLOCK
	OpenSync      OpenFlags = syscall.O_SYNC
	OpenTruncate  OpenFlags = syscall.O_TRUNC
)

// OpenAccessModeMask is a bitmask that separates the access mode
// from the other flags in OpenFlags.
const OpenAccessModeMask OpenFlags = 0x3 //syscall.O_ACCMODE

// OpenFlags are the O_FOO flags passed to open/create/etc calls. For
// example, os.O_WRONLY | os.O_APPEND.
type OpenFlags uint32

func (fl OpenFlags) String() string {
	// O_RDONLY, O_RWONLY, O_RDWR are not flags
	s := accModeName(fl & OpenAccessModeMask)
	flags := uint32(fl &^ OpenAccessModeMask)
	if flags != 0 {
		s = s + "+" + flagString(flags, openFlagNames)
	}
	return s
}

// Return true if OpenReadOnly is set.
func (fl OpenFlags) IsReadOnly() bool {
	return fl&OpenAccessModeMask == OpenReadOnly
}

// Return true if OpenWriteOnly is set.
func (fl OpenFlags) IsWriteOnly() bool {
	return fl&OpenAccessModeMask == OpenWriteOnly
}

// Return true if OpenReadWrite is set.
func (fl OpenFlags) IsReadWrite() bool {
	return fl&OpenAccessModeMask == OpenReadWrite
}

func accModeName(flags OpenFlags) string {
	switch flags {
	case OpenReadOnly:
		return "OpenReadOnly"
	case OpenWriteOnly:
		return "OpenWriteOnly"
	case OpenReadWrite:
		return "OpenReadWrite"
	default:
		return ""
	}
}

var openFlagNames = []flagName{
	{uint32(OpenAppend), "OpenAppend"},
	{uint32(OpenCreate), "OpenCreate"},
	// {uint32(OpenDirectory), "OpenDirectory"},
	{uint32(OpenExclusive), "OpenExclusive"},
	{uint32(OpenNonblock), "OpenNonblock"},
	{uint32(OpenSync), "OpenSync"},
	{uint32(OpenTruncate), "OpenTruncate"},
}

// OpenRequestFlags are the FUSE-specific flags in an OpenRequest (as opposed to the flags from filesystem client `open(2)` flags argument).
type OpenRequestFlags uint32

const (
	OpenKillSUIDGID OpenRequestFlags = 1 << 0
)

func (fl OpenRequestFlags) String() string {
	return flagString(uint32(fl), openRequestFlagNames)
}

var openRequestFlagNames = []flagName{
	{uint32(OpenKillSUIDGID), "OpenKillSUIDGID"},
}

// The OpenResponseFlags are returned in the OpenResponse.
type OpenResponseFlags uint32

const (
	OpenDirectIO    OpenResponseFlags = 1 << 0 // bypass page cache for this open file
	OpenKeepCache   OpenResponseFlags = 1 << 1 // don't invalidate the data cache on open
	OpenNonSeekable OpenResponseFlags = 1 << 2 // mark the file as non-seekable (not supported on FreeBSD)
	OpenCacheDir    OpenResponseFlags = 1 << 3 // allow caching directory contents
)

func (fl OpenResponseFlags) String() string {
	return flagString(uint32(fl), openResponseFlagNames)
}

var openResponseFlagNames = []flagName{
	{uint32(OpenDirectIO), "OpenDirectIO"},
	{uint32(OpenKeepCache), "OpenKeepCache"},
	{uint32(OpenNonSeekable), "OpenNonSeekable"},
	{uint32(OpenCacheDir), "OpenCacheDir"},
}

// The InitFlags are used in the Init exchange.
type InitFlags uint32

const (
	InitAsyncRead     InitFlags = 1 << 0
	InitPOSIXLocks    InitFlags = 1 << 1
	InitFileOps       InitFlags = 1 << 2
	InitAtomicTrunc   InitFlags = 1 << 3
	InitExportSupport InitFlags = 1 << 4
	InitBigWrites     InitFlags = 1 << 5
	// Do not mask file access modes with umask.
	InitDontMask        InitFlags = 1 << 6
	InitSpliceWrite     InitFlags = 1 << 7
	InitSpliceMove      InitFlags = 1 << 8
	InitSpliceRead      InitFlags = 1 << 9
	InitFlockLocks      InitFlags = 1 << 10
	InitHasIoctlDir     InitFlags = 1 << 11
	InitAutoInvalData   InitFlags = 1 << 12
	InitDoReaddirplus   InitFlags = 1 << 13
	InitReaddirplusAuto InitFlags = 1 << 14
	InitAsyncDIO        InitFlags = 1 << 15
	InitWritebackCache  InitFlags = 1 << 16
	InitNoOpenSupport   InitFlags = 1 << 17
	InitParallelDirOps  InitFlags = 1 << 18
	// Deprecated: Use `InitHandleKillPriv2`.
	InitHandleKillPriv InitFlags = 1 << 19
	InitPosixACL       InitFlags = 1 << 20
	InitAbortError     InitFlags = 1 << 21
	InitMaxPages       InitFlags = 1 << 22
	InitCacheSymlinks  InitFlags = 1 << 23
	// Kernel supports zero-message OpenDir.
	InitNoOpenDirSupport InitFlags = 1 << 24
	// Only invalidate cached pages on explicit request, instead of e.g. at every file size change.
	InitExplicitInvalidateData InitFlags = 1 << 25
	InitMapAlignment           InitFlags = 1 << 26
	InitSubMounts              InitFlags = 1 << 27
	// Filesystem promises to remove SUID/SGID/cap on writes and `chown`.
	InitHandleKillPrivV2 InitFlags = 1 << 28
	InitSetxattrExt      InitFlags = 1 << 29
)

type flagName struct {
	bit  uint32
	name string
}

var initFlagNames = []flagName{
	{uint32(InitAsyncRead), "InitAsyncRead"},
	{uint32(InitPOSIXLocks), "InitPOSIXLocks"},
	{uint32(InitFileOps), "InitFileOps"},
	{uint32(InitAtomicTrunc), "InitAtomicTrunc"},
	{uint32(InitExportSupport), "InitExportSupport"},
	{uint32(InitBigWrites), "InitBigWrites"},
	{uint32(InitDontMask), "InitDontMask"},
	{uint32(InitSpliceWrite), "InitSpliceWrite"},
	{uint32(InitSpliceMove), "InitSpliceMove"},
	{uint32(InitSpliceRead), "InitSpliceRead"},
	{uint32(InitFlockLocks), "InitFlockLocks"},
	{uint32(InitHasIoctlDir), "InitHasIoctlDir"},
	{uint32(InitAutoInvalData), "InitAutoInvalData"},
	{uint32(InitDoReaddirplus), "InitDoReaddirplus"},
	{uint32(InitReaddirplusAuto), "InitReaddirplusAuto"},
	{uint32(InitAsyncDIO), "InitAsyncDIO"},
	{uint32(InitWritebackCache), "InitWritebackCache"},
	{uint32(InitNoOpenSupport), "InitNoOpenSupport"},
	{uint32(InitParallelDirOps), "InitParallelDirOps"},
	{uint32(InitHandleKillPriv), "InitHandleKillPriv"},
	{uint32(InitPosixACL), "InitPosixACL"},
	{uint32(InitAbortError), "InitAbortError"},
	{uint32(InitMaxPages), "InitMaxPages"},
	{uint32(InitCacheSymlinks), "InitCacheSymlinks"},
	{uint32(InitNoOpenDirSupport), "InitNoOpenDirSupport"},
	{uint32(InitExplicitInvalidateData), "InitExplicitInvalidateData"},
	{uint32(InitMapAlignment), "InitMapAlignment"},
	{uint32(InitSubMounts), "InitSubMounts"},
	{uint32(InitHandleKillPrivV2), "InitHandleKillPrivV2"},
	{uint32(InitSetxattrExt), "InitSetxattrExt"},
}

func (fl InitFlags) String() string {
	return flagString(uint32(fl), initFlagNames)
}

func flagString(f uint32, names []flagName) string {
	var s string

	if f == 0 {
		return "0"
	}

	for _, n := range names {
		if f&n.bit != 0 {
			s += "+" + n.name
			f &^= n.bit
		}
	}
	if f != 0 {
		s += fmt.Sprintf("%+#x", f)
	}
	return s[1:]
}

// The ReleaseFlags are used in the Release exchange.
type ReleaseFlags uint32

const (
	ReleaseFlush       ReleaseFlags = 1 << 0
	ReleaseFlockUnlock ReleaseFlags = 1 << 1
)

func (fl ReleaseFlags) String() string {
	return flagString(uint32(fl), releaseFlagNames)
}

var releaseFlagNames = []flagName{
	{uint32(ReleaseFlush), "ReleaseFlush"},
	{uint32(ReleaseFlockUnlock), "ReleaseFlockUnlock"},
}

// Opcodes
const (
	opLookup        = 1
	opForget        = 2 // no reply
	opGetattr       = 3
	opSetattr       = 4
	opReadlink      = 5
	opSymlink       = 6
	opMknod         = 8
	opMkdir         = 9
	opUnlink        = 10
	opRmdir         = 11
	opRename        = 12
	opLink          = 13
	opOpen          = 14
	opRead          = 15
	opWrite         = 16
	opStatfs        = 17
	opRelease       = 18
	opFsync         = 20
	opSetxattr      = 21
	opGetxattr      = 22
	opListxattr     = 23
	opRemovexattr   = 24
	opFlush         = 25
	opInit          = 26
	opOpendir       = 27
	opReaddir       = 28
	opReleasedir    = 29
	opFsyncdir      = 30
	opGetlk         = 31
	opSetlk         = 32
	opSetlkw        = 33
	opAccess        = 34
	opCreate        = 35
	opInterrupt     = 36
	opBmap          = 37
	opDestroy       = 38
	opIoctl         = 39
	opPoll          = 40
	opNotifyReply   = 41
	opBatchForget   = 42
	opFAllocate     = 43
	opReadDirPlus   = 44
	opRename2       = 45
	opLSeek         = 46
	opCopyFileRange = 47
	opSetupMapping  = 48
	opRemoveMapping = 49
)

type entryOut struct {
	Nodeid         uint64 // Inode ID
	Generation     uint64 // Inode generation
	EntryValid     uint64 // Cache timeout for the name
	AttrValid      uint64 // Cache timeout for the attributes
	EntryValidNsec uint32
	AttrValidNsec  uint32
	Attr           attr
}

type forgetIn struct {
	Nlookup uint64
}

type forgetOne struct {
	NodeID  uint64
	Nlookup uint64
}

type batchForgetIn struct {
	Count uint32
	_     uint32
}

type getattrIn struct {
	GetattrFlags uint32
	_            uint32
	Fh           uint64
}

type attrOut struct {
	AttrValid     uint64 // Cache timeout for the attributes
	AttrValidNsec uint32
	_             uint32
	Attr          attr
}

type mknodIn struct {
	Mode  uint32
	Rdev  uint32
	Umask uint32
	_     uint32
	// "filename\x00" follows.
}

type mkdirIn struct {
	Mode  uint32
	Umask uint32
	// filename follows
}

type renameIn struct {
	Newdir uint64
	// "oldname\x00newname\x00" follows
}

type linkIn struct {
	Oldnodeid uint64
}

type setattrIn struct {
	Valid     uint32
	_         uint32
	Fh        uint64
	Size      uint64
	LockOwner uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Unused4   uint32
	Uid       uint32
	Gid       uint32
	Unused5   uint32
}

type openIn struct {
	Flags     uint32
	OpenFlags uint32
}

type openOut struct {
	Fh        uint64
	OpenFlags uint32
	_         uint32
}

type createIn struct {
	Flags uint32
	Mode  uint32
	Umask uint32
	_     uint32
}

type releaseIn struct {
	Fh           uint64
	Flags        uint32
	ReleaseFlags uint32
	LockOwner    uint64
}

type flushIn struct {
	Fh        uint64
	_         uint32
	_         uint32
	LockOwner uint64
}

type readIn struct {
	Fh        uint64
	Offset    uint64
	Size      uint32
	ReadFlags uint32
	LockOwner uint64
	Flags     uint32
	_         uint32
}

// The ReadFlags are passed in ReadRequest.
type ReadFlags uint32

const (
	// LockOwner field is valid.
	ReadLockOwner ReadFlags = 1 << 1
)

var readFlagNames = []flagName{
	{uint32(ReadLockOwner), "ReadLockOwner"},
}

func (fl ReadFlags) String() string {
	return flagString(uint32(fl), readFlagNames)
}

type writeIn struct {
	Fh         uint64
	Offset     uint64
	Size       uint32
	WriteFlags uint32
	LockOwner  uint64
	Flags      uint32
	_          uint32
}

type writeOut struct {
	Size uint32
	_    uint32
}

// The WriteFlags are passed in WriteRequest.
type WriteFlags uint32

const (
	WriteCache WriteFlags = 1 << 0
	// LockOwner field is valid.
	WriteLockOwner WriteFlags = 1 << 1
	// Remove SUID and GID bits.
	WriteKillSUIDGID WriteFlags = 1 << 2
)

var writeFlagNames = []flagName{
	{uint32(WriteCache), "WriteCache"},
	{uint32(WriteLockOwner), "WriteLockOwner"},
	{uint32(WriteKillSUIDGID), "WriteKillSUIDGID"},
}

func (fl WriteFlags) String() string {
	return flagString(uint32(fl), writeFlagNames)
}

type statfsOut struct {
	St kstatfs
}

type fsyncIn struct {
	Fh         uint64
	FsyncFlags uint32
	_          uint32
}

// SetxattrFlags re passed in SetxattrRequest.SetxattrFlags.
type SetxattrFlags uint32

const (
	SetxattrACLKillSGID SetxattrFlags = 1 << 0
)

var setxattrFlagNames = []flagName{
	{uint32(SetxattrACLKillSGID), "SetxattrACLKillSGID"},
}

func (fl SetxattrFlags) String() string {
	return flagString(uint32(fl), setxattrFlagNames)
}

type setxattrIn struct {
	Size          uint32
	Flags         uint32
	SetxattrFlags SetxattrFlags
	_             uint32
}

func setxattrInSize(fl InitFlags) uintptr {
	if fl&InitSetxattrExt == 0 {
		return unsafe.Offsetof(setxattrIn{}.SetxattrFlags)
	}
	return unsafe.Sizeof(setxattrIn{})
}

type getxattrIn struct {
	Size uint32
	_    uint32
}

type getxattrOut struct {
	Size uint32
	_    uint32
}

// The LockFlags are passed in LockRequest or LockWaitRequest.
type LockFlags uint32

const (
	// BSD-style flock lock (not POSIX lock)
	LockFlock LockFlags = 1 << 0
)

var lockFlagNames = []flagName{
	{uint32(LockFlock), "LockFlock"},
}

func (fl LockFlags) String() string {
	return flagString(uint32(fl), lockFlagNames)
}

type LockType uint32

const (
	// It seems FreeBSD FUSE passes these through using its local
	// values, not whatever Linux enshrined into the protocol. It's
	// unclear what the intended behavior is.

	LockRead   LockType = 0x0 // unix.F_RDLCK
	LockWrite  LockType = 0x1 // unix.F_WRLCK
	LockUnlock LockType = 0x2 // unix.F_UNLCK
)

var lockTypeNames = map[LockType]string{
	LockRead:   "LockRead",
	LockWrite:  "LockWrite",
	LockUnlock: "LockUnlock",
}

func (l LockType) String() string {
	s, ok := lockTypeNames[l]
	if ok {
		return s
	}
	return fmt.Sprintf("LockType(%d)", l)
}

type fileLock struct {
	Start uint64
	End   uint64
	Type  uint32
	PID   uint32
}

type lkIn struct {
	Fh      uint64
	Owner   uint64
	Lk      fileLock
	LkFlags uint32
	_       uint32
}

type lkOut struct {
	Lk fileLock
}

type accessIn struct {
	Mask uint32
	_    uint32
}

type initIn struct {
	Major        uint32
	Minor        uint32
	MaxReadahead uint32
	Flags        uint32
}

const initInSize = int(unsafe.Sizeof(initIn{}))

type initOut struct {
	Major               uint32
	Minor               uint32
	MaxReadahead        uint32
	Flags               uint32
	MaxBackground       uint16
	CongestionThreshold uint16
	MaxWrite            uint32

	// end of protocol 7.22 fields

	// Granularity of timestamps, in nanoseconds.
	// Maximum value 1e9 (one second).
	TimeGran uint32
	// Maximum number of pages of data in one read or write request.
	// Set initOut.Flags.InitMaxPages when valid.
	MaxPages     uint16
	MapAlignment uint16
	_            [8]uint32
}

type interruptIn struct {
	Unique uint64
}

type bmapIn struct {
	Block     uint64
	BlockSize uint32
	_         uint32
}

type bmapOut struct {
	Block uint64
}

type inHeader struct {
	Len    uint32
	Opcode uint32
	Unique uint64
	Nodeid uint64
	Uid    uint32
	Gid    uint32
	Pid    uint32
	_      uint32
}

const inHeaderSize = int(unsafe.Sizeof(inHeader{}))

type outHeader struct {
	Len    uint32
	Error  int32
	Unique uint64
}

type dirent struct {
	Ino     uint64
	Off     uint64
	Namelen uint32
	Type    uint32
}

const direntSize = 8 + 8 + 4 + 4

const (
	notifyCodePoll       int32 = 1
	notifyCodeInvalInode int32 = 2
	notifyCodeInvalEntry int32 = 3
	notifyCodeStore      int32 = 4
	notifyCodeRetrieve   int32 = 5
	notifyCodeDelete     int32 = 6
)

type notifyInvalInodeOut struct {
	Ino uint64
	Off int64
	Len int64
}

type notifyInvalEntryOut struct {
	Parent  uint64
	Namelen uint32
	_       uint32
}

type notifyDeleteOut struct {
	Parent  uint64
	Child   uint64
	Namelen uint32
	_       uint32
}

type notifyStoreOut struct {
	Nodeid uint64
	Offset uint64
	Size   uint32
	_      uint32
}

type notifyRetrieveOut struct {
	NotifyUnique uint64
	Nodeid       uint64
	Offset       uint64
	Size         uint32
	_            uint32
}

type notifyRetrieveIn struct {
	// matches writeIn

	_      uint64
	Offset uint64
	Size   uint32
	_      uint32
	_      uint64
	_      uint64
}

// PollFlags are passed in PollRequest.Flags
type PollFlags uint32

const (
	// PollScheduleNotify requests that a poll notification is done
	// once the node is ready.
	PollScheduleNotify PollFlags = 1 << 0
)

var pollFlagNames = []flagName{
	{uint32(PollScheduleNotify), "PollScheduleNotify"},
}

func (fl PollFlags) String() string {
	return flagString(uint32(fl), pollFlagNames)
}

type PollEvents uint32

const (
	PollIn       PollEvents = 0x0000_0001
	PollPriority PollEvents = 0x0000_0002
	PollOut      PollEvents = 0x0000_0004
	PollError    PollEvents = 0x0000_0008
	PollHangup   PollEvents = 0x0000_0010
	// PollInvalid doesn't seem to be used in the FUSE protocol.
	PollInvalid        PollEvents = 0x0000_0020
	PollReadNormal     PollEvents = 0x0000_0040
	PollReadOutOfBand  PollEvents = 0x0000_0080
	PollWriteNormal    PollEvents = 0x0000_0100
	PollWriteOutOfBand PollEvents = 0x0000_0200
	PollMessage        PollEvents = 0x0000_0400
	PollReadHangup     PollEvents = 0x0000_2000

	DefaultPollMask = PollIn | PollOut | PollReadNormal | PollWriteNormal
)

var pollEventNames = []flagName{
	{uint32(PollIn), "PollIn"},
	{uint32(PollPriority), "PollPriority"},
	{uint32(PollOut), "PollOut"},
	{uint32(PollError), "PollError"},
	{uint32(PollHangup), "PollHangup"},
	{uint32(PollInvalid), "PollInvalid"},
	{uint32(PollReadNormal), "PollReadNormal"},
	{uint32(PollReadOutOfBand), "PollReadOutOfBand"},
	{uint32(PollWriteNormal), "PollWriteNormal"},
	{uint32(PollWriteOutOfBand), "PollWriteOutOfBand"},
	{uint32(PollMessage), "PollMessage"},
	{uint32(PollReadHangup), "PollReadHangup"},
}

func (fl PollEvents) String() string {
	return flagString(uint32(fl), pollEventNames)
}

type pollIn struct {
	Fh     uint64
	Kh     uint64
	Flags  uint32
	Events uint32
}

type pollOut struct {
	REvents uint32
	_       uint32
}

type notifyPollWakeupOut struct {
	Kh uint64
}

type FAllocateFlags uint32

const (
	FAllocateKeepSize  FAllocateFlags = 0x1 // unix.FALLOC_FL_KEEP_SIZE
	FAllocatePunchHole FAllocateFlags = 0x2 // unix.FALLOC_FL_PUNCH_HOLE

	// Only including constants supported by FUSE kernel implementation.
	//
	// FAllocateCollapseRange FAllocateFlags = unix.FALLOC_FL_COLLAPSE_RANGE
	// FAllocateInsertRange   FAllocateFlags = unix.FALLOC_FL_INSERT_RANGE
	// FAllocateNoHideStale   FAllocateFlags = unix.FALLOC_FL_NO_HIDE_STALE
	// FAllocateUnshareRange  FAllocateFlags = unix.FALLOC_FL_UNSHARE_RANGE
	// FAllocateZeroRange     FAllocateFlags = unix.FALLOC_FL_ZERO_RANGE
)

var fAllocateFlagsNames = []flagName{
	{uint32(FAllocatePunchHole), "FAllocatePunchHole"},
	{uint32(FAllocateKeepSize), "FAllocateKeepSize"},

	// Only including constants supported by FUSE kernel implementation.
	//
	// {uint32(FAllocateCollapseRange), "FAllocateCollapseRange"},
	// {uint32(FAllocateInsertRange), "FAllocateInsertRange"},
	// {uint32(FAllocateNoHideStale), "FAllocateNoHideStale"},
	// {uint32(FAllocateUnshareRange), "FAllocateUnshareRange"},
	// {uint32(FAllocateZeroRange), "FAllocateZeroRange"},
}

func (fl FAllocateFlags) String() string {
	return flagString(uint32(fl), fAllocateFlagsNames)
}

type fAllocateIn struct {
	Fh     uint64
	Offset uint64
	Length uint64
	Mode   uint32
	_      uint32
}

///////////////////////////////
///////////////////////////////
///////////////////////////////

var mountHandleMap = make(map[*os.File]mountHandleEntry)

func MountHandleMap() map[*os.File]mountHandleEntry {
	return mountHandleMap
}

func MountHandle(dir string) (handle *os.File, err error) {
	//dir, err = os.MkdirTemp("", "pk-dokanfs-mount")
	fd, err := os.CreateTemp("", "pk-dokanfs-mount")
	if err != nil {
		return nil, err
	}
	mountHandleMap[fd] = mountHandleEntry{
		Fd:  fd,
		Dir: dir,
	}

	return fd, nil
}

///////////////////////////////
///////////////////////////////
///////////////////////////////
///////////////////////////////

// fuzeo

///////////////////////////////
///////////////////////////////
///////////////////////////////

// MountpointDoesNotExistError is an error returned when the
// mountpoint does not exist.
type MountpointDoesNotExistError struct {
	Path string
}

var _ error = (*MountpointDoesNotExistError)(nil)

func (e *MountpointDoesNotExistError) Error() string {
	return fmt.Sprintf("mountpoint does not exist: %v", e.Path)
}

// Mount mounts a new FUSE connection on the named directory
// and returns a connection for reading and writing FUSE messages.
//
// After a successful return, caller must call Close to free
// resources.
//
// Even on successful return, the new mount is not guaranteed to be
// visible until after Conn.Ready is closed. See Conn.MountError for
// possible errors. Incoming requests on Conn must be served to make
// progress.
func Mount(cfg *dokan.Config) (*dokan.MountHandle, error) {
	var fileSystemInter fileSystemInter
	var emptyMh dokan.MountHandle

	mh, err := dokan.Mount(
		&dokan.Config{FileSystem: fileSystemInter, Path: cfg.Path})
	if err != nil {
		return &emptyMh, err
	}
	// return handle.BlockTillDone()
	return mh, err
}

var (
	ErrClosedWithoutInit = errors.New("fuse connection closed without init")
)

// A Request represents a single FUSE request received from the kernel.
// Use a type switch to determine the specific kind.
// A request of unrecognized type will have concrete type *Header.
type Request interface {
	// Hdr returns the Header associated with this request.
	Hdr() *Header

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	// IsRequestType()
}

// A RequestID identifies an active FUSE request.
type RequestID uint64

func (r RequestID) String() string {
	return fmt.Sprintf("%#x", uint64(r))
}

// A NodeID is a number identifying a directory or file.
// It must be unique among IDs returned in LookupResponses
// that have not yet been forgotten by ForgetRequests.
type NodeID uint64

func (n NodeID) String() string {
	return fmt.Sprintf("%#x", uint64(n))
}

// A HandleID is a number identifying an open directory or file.
// It only needs to be unique while the directory or file is open.
type HandleID uint64

func (h HandleID) String() string {
	return fmt.Sprintf("%#x", uint64(h))
}

// The RootID identifies the root directory of a FUSE file system.
const RootID NodeID = rootID

// A Header describes the basic information sent in every request.
type Header struct {
	// Conn *Conn     `json:"-"` // connection this request was received on
	ID   RequestID // unique ID for request
	Node NodeID    // file or directory the request is about
	Uid  uint32    // user ID of process making request
	Gid  uint32    // group ID of process making request
	Pid  uint32    // process ID of process making request

	// for returning to reqPool
	// msg *message
}

func (h *Header) String() string {
	return fmt.Sprintf("ID=%v Node=%v Uid=%d Gid=%d Pid=%d", h.ID, h.Node, h.Uid, h.Gid, h.Pid)
}

func (h *Header) Hdr() *Header {
	return h
}

func (h *Header) noResponse() {
	// putMessage(h.msg)
}

// An ErrorNumber is an error with a specific error number.
//
// Operations may return an error value that implements ErrorNumber to
// control what specific error number (errno) to return.
type ErrorNumber interface {
	// Errno returns the the error number (errno) for this error.
	Errno() Errno
}

// Deprecated: Return a syscall.Errno directly. See ToErrno for exact
// rules.
const (
	// ENOSYS indicates that the call is not supported.
	ENOSYS = Errno(syscall.ENOSYS)

	// ESTALE is used by Serve to respond to violations of the FUSE protocol.
	ESTALE = Errno(syscall.ESTALE)

	ENOENT = Errno(syscall.ENOENT)
	EIO    = Errno(syscall.EIO)
	EPERM  = Errno(syscall.EPERM)

	// EINTR indicates request was interrupted by an InterruptRequest.
	// See also fs.Intr.
	EINTR = Errno(syscall.EINTR)

	ERANGE  = Errno(syscall.ERANGE)
	ENOTSUP = Errno(syscall.ENOTSUP)
	EEXIST  = Errno(syscall.EEXIST)
)

// DefaultErrno is the errno used when error returned does not
// implement ErrorNumber.
const DefaultErrno = EIO

var errnoNames = map[Errno]string{
	ENOSYS:                      "ENOSYS",
	ESTALE:                      "ESTALE",
	ENOENT:                      "ENOENT",
	EIO:                         "EIO",
	EPERM:                       "EPERM",
	EINTR:                       "EINTR",
	EEXIST:                      "EEXIST",
	Errno(syscall.ENAMETOOLONG): "ENAMETOOLONG",
}

// Errno implements Error and ErrorNumber using a syscall.Errno.
type Errno syscall.Errno

var _ = ErrorNumber(Errno(0))
var _ = error(Errno(0))

func (e Errno) Errno() Errno {
	return e
}

func (e Errno) String() string {
	return syscall.Errno(e).Error()
}

func (e Errno) Error() string {
	return syscall.Errno(e).Error()
}

// ErrnoName returns the short non-numeric identifier for this errno.
// For example, "EIO".
func (e Errno) ErrnoName() string {
	s := errnoNames[e]
	if s == "" {
		s = fmt.Sprint(e.Errno())
	}
	return s
}

func (e Errno) MarshalText() ([]byte, error) {
	s := e.ErrnoName()
	return []byte(s), nil
}

// ToErrno converts arbitrary errors to Errno.
//
// If the underlying type of err is syscall.Errno, it is used
// directly. No unwrapping is done, to prevent wrong errors from
// leaking via e.g. *os.PathError.
//
// If err unwraps to implement ErrorNumber, that is used.
//
// Finally, returns DefaultErrno.
func ToErrno(err error) Errno {
	if err, ok := err.(syscall.Errno); ok {
		return Errno(err)
	}
	var errnum ErrorNumber
	if errors.As(err, &errnum) {
		return Errno(errnum.Errno())
	}
	return DefaultErrno
}

func (h *Header) RespondError(err error) {
	// errno := ToErrno(err)
	// // FUSE uses negative errors!
	// // TODO: File bug report against OSXFUSE: positive error causes kernel panic.
	// buf := newBuffer(0)
	// hOut := (*outHeader)(unsafe.Pointer(&buf[0]))
	// hOut.Error = -int32(errno)
	// h.respond(buf)
}

// Maximum file write size we are prepared to receive.
//
// This number is just a guess.
const maxWrite = 128 * 1024

// All requests read from the kernel, without data, are shorter than
// this.
var maxRequestSize = syscall.Getpagesize()
var bufSize = maxRequestSize + maxWrite

// reqPool is a pool of messages.
//
// Lifetime of a logical message is from getMessage to putMessage.
// getMessage is called by ReadRequest. putMessage is called by
// Conn.ReadRequest, Request.Respond, or Request.RespondError.
//
// Messages in the pool are guaranteed to have conn and off zeroed,
// buf allocated and len==bufSize, and hdr set.
// var reqPool = sync.Pool{
// 	New: allocMessage,
// }

// func allocMessage() interface{} {
// 	m := &message{buf: make([]byte, bufSize)}
// 	m.hdr = (*inHeader)(unsafe.Pointer(&m.buf[0]))
// 	return m
// }

// func getMessage(c *Conn) *message {
// 	m := reqPool.Get().(*message)
// 	m.conn = c
// 	return m
// }

// func putMessage(m *message) {
// 	m.buf = m.buf[:bufSize]
// 	m.conn = nil
// 	m.off = 0
// 	reqPool.Put(m)
// }

// a message represents the bytes of a single FUSE message
// type message struct {
// 	conn *Conn
// 	buf  []byte    // all bytes
// 	hdr  *inHeader // header
// 	off  int       // offset for reading additional fields
// }

// func (m *message) len() uintptr {
// 	return uintptr(len(m.buf) - m.off)
// }

// func (m *message) data() unsafe.Pointer {
// 	var p unsafe.Pointer
// 	if m.off < len(m.buf) {
// 		p = unsafe.Pointer(&m.buf[m.off])
// 	}
// 	return p
// }

// func (m *message) bytes() []byte {
// 	return m.buf[m.off:]
// }

// func (m *message) Header() Header {
// 	h := m.hdr
// 	return Header{
// 		ID:   RequestID(h.Unique),
// 		Node: NodeID(h.Nodeid),
// 		Uid:  h.Uid,
// 		Gid:  h.Gid,
// 		Pid:  h.Pid,
// 	}
// }

// fileMode returns a Go os.FileMode from a Unix mode.
func fileMode(unixMode uint32) os.FileMode {
	mode := os.FileMode(unixMode & 0777)
	switch unixMode & syscall.S_IFMT {
	case syscall.S_IFREG:
		// nothing
	case syscall.S_IFDIR:
		mode |= os.ModeDir
	case syscall.S_IFCHR:
		mode |= os.ModeCharDevice | os.ModeDevice
	case syscall.S_IFBLK:
		mode |= os.ModeDevice
	case syscall.S_IFIFO:
		mode |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= os.ModeSymlink
	case syscall.S_IFSOCK:
		mode |= os.ModeSocket
	case 0:
		// apparently there's plenty of times when the FUSE request
		// does not contain the file type
		mode |= os.ModeIrregular
	default:
		// not just unavailable in the kernel codepath; known to
		// kernel but unrecognized by us
		Debug(fmt.Sprintf("unrecognized file mode type: %04o", unixMode))
		mode |= os.ModeIrregular
	}
	if unixMode&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if unixMode&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	return mode
}

type noOpcode struct {
	Opcode uint32
}

func (m noOpcode) String() string {
	return fmt.Sprintf("No opcode %v", m.Opcode)
}

type malformedMessage struct {
}

func (malformedMessage) String() string {
	return "malformed message"
}

type bugShortKernelWrite struct {
	Written int64
	Length  int64
	Error   string
	Stack   string
}

func (b bugShortKernelWrite) String() string {
	return fmt.Sprintf("short kernel write: written=%d/%d error=%q stack=\n%s", b.Written, b.Length, b.Error, b.Stack)
}

type bugKernelWriteError struct {
	Error string
	Stack string
}

func (b bugKernelWriteError) String() string {
	return fmt.Sprintf("kernel write error: error=%q stack=\n%s", b.Error, b.Stack)
}

// safe to call even with nil error
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// func (c *Conn) writeToKernel(msg []byte) error {
// 	out := (*outHeader)(unsafe.Pointer(&msg[0]))
// 	out.Len = uint32(len(msg))

// 	c.wio.RLock()
// 	defer c.wio.RUnlock()
// 	nn, err := syscall.Write(c.fd(), msg)
// 	if err == nil && nn != len(msg) {
// 		Debug(bugShortKernelWrite{
// 			Written: int64(nn),
// 			Length:  int64(len(msg)),
// 			Error:   errorString(err),
// 			Stack:   stack(),
// 		})
// 	}
// 	return err
// }

// func (c *Conn) respond(msg []byte) {
// 	if err := c.writeToKernel(msg); err != nil {
// 		Debug(bugKernelWriteError{
// 			Error: errorString(err),
// 			Stack: stack(),
// 		})
// 	}
// }

type notCachedError struct{}

func (notCachedError) Error() string {
	return "node not cached"
}

var _ ErrorNumber = notCachedError{}

func (notCachedError) Errno() Errno {
	// Behave just like if the original syscall.ENOENT had been passed
	// straight through.
	return ENOENT
}

var (
	ErrNotCached = notCachedError{}
)

// sendInvalidate sends an invalidate notification to kernel.
//
// A returned ENOENT is translated to a friendlier error.
// func (c *Conn) sendInvalidate(msg []byte) error {
// 	switch err := c.writeToKernel(msg); err {
// 	case syscall.ENOENT:
// 		return ErrNotCached
// 	default:
// 		return err
// 	}
// }

// InvalidateNode invalidates the kernel cache of the attributes and a
// range of the data of a node.
//
// Giving offset 0 and size -1 means all data. To invalidate just the
// attributes, give offset 0 and size 0.
//
// Returns ErrNotCached if the kernel is not currently caching the
// node.
// func (c *Conn) InvalidateNode(nodeID NodeID, off int64, size int64) error {
// 	buf := newBuffer(unsafe.Sizeof(notifyInvalInodeOut{}))
// 	h := (*outHeader)(unsafe.Pointer(&buf[0]))
// 	// h.Unique is 0
// 	h.Error = notifyCodeInvalInode
// 	out := (*notifyInvalInodeOut)(buf.alloc(unsafe.Sizeof(notifyInvalInodeOut{})))
// 	out.Ino = uint64(nodeID)
// 	out.Off = off
// 	out.Len = size
// 	return c.sendInvalidate(buf)
// }

// InvalidateEntry invalidates the kernel cache of the directory entry
// identified by parent directory node ID and entry basename.
//
// Kernel may or may not cache directory listings. To invalidate
// those, use InvalidateNode to invalidate all of the data for a
// directory. (As of 2015-06, Linux FUSE does not cache directory
// listings.)
//
// Returns ErrNotCached if the kernel is not currently caching the
// node.
// func (c *Conn) InvalidateEntry(parent NodeID, name string) error {
// 	const maxUint32 = ^uint32(0)
// 	if uint64(len(name)) > uint64(maxUint32) {
// 		// very unlikely, but we don't want to silently truncate
// 		return syscall.ENAMETOOLONG
// 	}
// 	buf := newBuffer(unsafe.Sizeof(notifyInvalEntryOut{}) + uintptr(len(name)) + 1)
// 	h := (*outHeader)(unsafe.Pointer(&buf[0]))
// 	// h.Unique is 0
// 	h.Error = notifyCodeInvalEntry
// 	out := (*notifyInvalEntryOut)(buf.alloc(unsafe.Sizeof(notifyInvalEntryOut{})))
// 	out.Parent = uint64(parent)
// 	out.Namelen = uint32(len(name))
// 	buf = append(buf, name...)
// 	buf = append(buf, '\x00')
// 	return c.sendInvalidate(buf)
// }

// An InitRequest is the first request sent on a FUSE file system.
type InitRequest struct {
	Header `json:"-"`
	Kernel Protocol
	// Maximum readahead in bytes that the kernel plans to use.
	MaxReadahead uint32
	Flags        InitFlags
}

var _ = Request(&InitRequest{})

func (r *InitRequest) String() string {
	return fmt.Sprintf("Init [%v] %v ra=%d fl=%v", &r.Header, r.Kernel, r.MaxReadahead, r.Flags)
}

// An InitResponse is the response to an InitRequest.
type InitResponse struct {
	Library Protocol
	// Maximum readahead in bytes that the kernel can use. Ignored if
	// greater than InitRequest.MaxReadahead.
	MaxReadahead uint32
	Flags        InitFlags
	// Maximum size of a single write operation.
	// Linux enforces a minimum of 4 KiB.
	MaxWrite uint32
}

func (r *InitResponse) String() string {
	return fmt.Sprintf("Init %v ra=%d fl=%v w=%d", r.Library, r.MaxReadahead, r.Flags, r.MaxWrite)
}

// Respond replies to the request with the given response.
func (r *InitRequest) Respond(resp *InitResponse) {
	// buf := newBuffer(unsafe.Sizeof(initOut{}))
	// out := (*initOut)(buf.alloc(unsafe.Sizeof(initOut{})))
	// out.Major = resp.Library.Major
	// out.Minor = resp.Library.Minor
	// out.MaxReadahead = resp.MaxReadahead
	// out.Flags = uint32(resp.Flags)
	// out.MaxWrite = resp.MaxWrite

	// // MaxWrite larger than our receive buffer would just lead to
	// // errors on large writes.
	// if out.MaxWrite > maxWrite {
	// 	out.MaxWrite = maxWrite
	// }
	// r.respond(buf)
}

// A StatfsRequest requests information about the mounted file system.
type StatfsRequest struct {
	Header `json:"-"`
}

var _ = Request(&StatfsRequest{})

func (r *StatfsRequest) String() string {
	return fmt.Sprintf("Statfs [%s]", &r.Header)
}

// Respond replies to the request with the given response.
func (r *StatfsRequest) Respond(resp *StatfsResponse) {
	// buf := newBuffer(unsafe.Sizeof(statfsOut{}))
	// out := (*statfsOut)(buf.alloc(unsafe.Sizeof(statfsOut{})))
	// out.St = kstatfs{
	// 	Blocks:  resp.Blocks,
	// 	Bfree:   resp.Bfree,
	// 	Bavail:  resp.Bavail,
	// 	Files:   resp.Files,
	// 	Ffree:   resp.Ffree,
	// 	Bsize:   resp.Bsize,
	// 	Namelen: resp.Namelen,
	// 	Frsize:  resp.Frsize,
	// }
	// r.respond(buf)
}

// A StatfsResponse is the response to a StatfsRequest.
type StatfsResponse struct {
	Blocks  uint64 // Total data blocks in file system.
	Bfree   uint64 // Free blocks in file system.
	Bavail  uint64 // Free blocks in file system if you're not root.
	Files   uint64 // Total files in file system.
	Ffree   uint64 // Free files in file system.
	Bsize   uint32 // Block size
	Namelen uint32 // Maximum file name length?
	Frsize  uint32 // Fragment size, smallest addressable data size in the file system.
}

func (r *StatfsResponse) String() string {
	return fmt.Sprintf("Statfs blocks=%d/%d/%d files=%d/%d bsize=%d frsize=%d namelen=%d",
		r.Bavail, r.Bfree, r.Blocks,
		r.Ffree, r.Files,
		r.Bsize,
		r.Frsize,
		r.Namelen,
	)
}

// An AccessRequest asks whether the file can be accessed
// for the purpose specified by the mask.
type AccessRequest struct {
	Header `json:"-"`
	Mask   uint32
}

var _ = Request(&AccessRequest{})

func (r *AccessRequest) String() string {
	return fmt.Sprintf("Access [%s] mask=%#x", &r.Header, r.Mask)
}

// Respond replies to the request indicating that access is allowed.
// To deny access, use RespondError.
func (r *AccessRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// An Attr is the metadata for a single file or directory.
type Attr struct {
	Valid time.Duration // how long Attr can be cached

	Inode     uint64      // inode number
	Size      uint64      // size in bytes
	Blocks    uint64      // size in 512-byte units
	Atime     time.Time   // time of last access
	Mtime     time.Time   // time of last modification
	Ctime     time.Time   // time of last inode change
	Crtime    time.Time   // time of creation (OS X only)
	Mode      os.FileMode // file mode
	Nlink     uint32      // number of links (usually 1)
	Uid       uint32      // owner uid
	Gid       uint32      // group gid
	Rdev      uint32      // device numbers
	Flags     uint32      // chflags(2) flags (OS X only)
	BlockSize uint32      // preferred blocksize for filesystem I/O
}

func (a Attr) String() string {
	return fmt.Sprintf("valid=%v ino=%v size=%d mode=%v", a.Valid, a.Inode, a.Size, a.Mode)
}

func unix(t time.Time) (sec uint64, nsec uint32) {
	nano := t.UnixNano()
	sec = uint64(nano / 1e9)
	nsec = uint32(nano % 1e9)
	return
}

func (a *Attr) attr(out *attr, proto Protocol) {
	// out.Ino = a.Inode
	// out.Size = a.Size
	// out.Blocks = a.Blocks
	// out.Atime, out.AtimeNsec = unix(a.Atime)
	// out.Mtime, out.MtimeNsec = unix(a.Mtime)
	// out.Ctime, out.CtimeNsec = unix(a.Ctime)
	// out.SetCrtime(unix(a.Crtime))
	// out.Mode = uint32(a.Mode) & 0777
	// switch {
	// default:
	// 	out.Mode |= syscall.S_IFREG
	// case a.Mode&os.ModeDir != 0:
	// 	out.Mode |= syscall.S_IFDIR
	// case a.Mode&os.ModeDevice != 0:
	// 	if a.Mode&os.ModeCharDevice != 0 {
	// 		out.Mode |= syscall.S_IFCHR
	// 	} else {
	// 		out.Mode |= syscall.S_IFBLK
	// 	}
	// case a.Mode&os.ModeNamedPipe != 0:
	// 	out.Mode |= syscall.S_IFIFO
	// case a.Mode&os.ModeSymlink != 0:
	// 	out.Mode |= syscall.S_IFLNK
	// case a.Mode&os.ModeSocket != 0:
	// 	out.Mode |= syscall.S_IFSOCK
	// }
	// if a.Mode&os.ModeSetuid != 0 {
	// 	out.Mode |= syscall.S_ISUID
	// }
	// if a.Mode&os.ModeSetgid != 0 {
	// 	out.Mode |= syscall.S_ISGID
	// }
	// out.Nlink = a.Nlink
	// out.Uid = a.Uid
	// out.Gid = a.Gid
	// out.Rdev = a.Rdev
	// out.SetFlags(a.Flags)
	// if proto.GE(Protocol{7, 9}) {
	// 	out.Blksize = a.BlockSize
	// }
}

// A GetattrRequest asks for the metadata for the file denoted by r.Node.
type GetattrRequest struct {
	Header `json:"-"`
	Flags  GetattrFlags
	Handle HandleID
}

var _ = Request(&GetattrRequest{})

func (r *GetattrRequest) String() string {
	return fmt.Sprintf("Getattr [%s] %v fl=%v", &r.Header, r.Handle, r.Flags)
}

// Respond replies to the request with the given response.
func (r *GetattrRequest) Respond(resp *GetattrResponse) {
	// size := attrOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*attrOut)(buf.alloc(size))
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A GetattrResponse is the response to a GetattrRequest.
type GetattrResponse struct {
	Attr Attr // file attributes
}

func (r *GetattrResponse) String() string {
	return fmt.Sprintf("Getattr %v", r.Attr)
}

// A GetxattrRequest asks for the extended attributes associated with r.Node.
type GetxattrRequest struct {
	Header `json:"-"`

	// Maximum size to return.
	Size uint32

	// Name of the attribute requested.
	Name string

	// Offset within extended attributes.
	//
	// Only valid for OS X, and then only with the resource fork
	// attribute.
	Position uint32
}

var _ = Request(&GetxattrRequest{})

func (r *GetxattrRequest) String() string {
	return fmt.Sprintf("Getxattr [%s] %q %d @%d", &r.Header, r.Name, r.Size, r.Position)
}

// Respond replies to the request with the given response.
func (r *GetxattrRequest) Respond(resp *GetxattrResponse) {
	// if r.Size == 0 {
	// 	buf := newBuffer(unsafe.Sizeof(getxattrOut{}))
	// 	out := (*getxattrOut)(buf.alloc(unsafe.Sizeof(getxattrOut{})))
	// 	out.Size = uint32(len(resp.Xattr))
	// 	r.respond(buf)
	// } else {
	// 	buf := newBuffer(uintptr(len(resp.Xattr)))
	// 	buf = append(buf, resp.Xattr...)
	// 	r.respond(buf)
	// }
}

// A GetxattrResponse is the response to a GetxattrRequest.
type GetxattrResponse struct {
	Xattr []byte
}

func (r *GetxattrResponse) String() string {
	return fmt.Sprintf("Getxattr %q", r.Xattr)
}

// A ListxattrRequest asks to list the extended attributes associated with r.Node.
type ListxattrRequest struct {
	Header   `json:"-"`
	Size     uint32 // maximum size to return
	Position uint32 // offset within attribute list
}

var _ = Request(&ListxattrRequest{})

func (r *ListxattrRequest) String() string {
	return fmt.Sprintf("Listxattr [%s] %d @%d", &r.Header, r.Size, r.Position)
}

// Respond replies to the request with the given response.
func (r *ListxattrRequest) Respond(resp *ListxattrResponse) {
	// if r.Size == 0 {
	// 	buf := newBuffer(unsafe.Sizeof(getxattrOut{}))
	// 	out := (*getxattrOut)(buf.alloc(unsafe.Sizeof(getxattrOut{})))
	// 	out.Size = uint32(len(resp.Xattr))
	// 	r.respond(buf)
	// } else {
	// 	buf := newBuffer(uintptr(len(resp.Xattr)))
	// 	buf = append(buf, resp.Xattr...)
	// 	r.respond(buf)
	// }
}

// A ListxattrResponse is the response to a ListxattrRequest.
type ListxattrResponse struct {
	Xattr []byte
}

func (r *ListxattrResponse) String() string {
	return fmt.Sprintf("Listxattr %q", r.Xattr)
}

// Append adds an extended attribute name to the response.
func (r *ListxattrResponse) Append(names ...string) {
	for _, name := range names {
		r.Xattr = append(r.Xattr, name...)
		r.Xattr = append(r.Xattr, '\x00')
	}
}

// A RemovexattrRequest asks to remove an extended attribute associated with r.Node.
type RemovexattrRequest struct {
	Header `json:"-"`
	Name   string // name of extended attribute
}

var _ = Request(&RemovexattrRequest{})

func (r *RemovexattrRequest) String() string {
	return fmt.Sprintf("Removexattr [%s] %q", &r.Header, r.Name)
}

// Respond replies to the request, indicating that the attribute was removed.
func (r *RemovexattrRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A SetxattrRequest asks to set an extended attribute associated with a file.
type SetxattrRequest struct {
	Header `json:"-"`

	// Flags can make the request fail if attribute does/not already
	// exist. Unfortunately, the constants are platform-specific and
	// not exposed by Go1.2. Look for XATTR_CREATE, XATTR_REPLACE.
	//
	// TODO improve this later
	//
	// TODO XATTR_CREATE and exist -> EEXIST
	//
	// TODO XATTR_REPLACE and not exist -> ENODATA
	Flags uint32

	// Offset within extended attributes.
	//
	// Only valid for OS X, and then only with the resource fork
	// attribute.
	Position uint32

	Name  string
	Xattr []byte
}

var _ = Request(&SetxattrRequest{})

func trunc(b []byte, max int) ([]byte, string) {
	if len(b) > max {
		return b[:max], "..."
	}
	return b, ""
}

func (r *SetxattrRequest) String() string {
	xattr, tail := trunc(r.Xattr, 16)
	return fmt.Sprintf("Setxattr [%s] %q %q%s fl=%v @%#x", &r.Header, r.Name, xattr, tail, r.Flags, r.Position)
}

// Respond replies to the request, indicating that the extended attribute was set.
func (r *SetxattrRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A LookupRequest asks to look up the given name in the directory named by r.Node.
type LookupRequest struct {
	Header `json:"-"`
	Name   string
}

var _ = Request(&LookupRequest{})

func (r *LookupRequest) String() string {
	return fmt.Sprintf("Lookup [%s] %q", &r.Header, r.Name)
}

// Respond replies to the request with the given response.
func (r *LookupRequest) Respond(resp *LookupResponse) {
	// size := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*entryOut)(buf.alloc(size))
	// out.Nodeid = uint64(resp.Node)
	// out.Generation = resp.Generation
	// out.EntryValid = uint64(resp.EntryValid / time.Second)
	// out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A LookupResponse is the response to a LookupRequest.
type LookupResponse struct {
	Node       NodeID
	Generation uint64
	EntryValid time.Duration
	Attr       Attr
}

func (r *LookupResponse) string() string {
	return fmt.Sprintf("%v gen=%d valid=%v attr={%v}", r.Node, r.Generation, r.EntryValid, r.Attr)
}

func (r *LookupResponse) String() string {
	return fmt.Sprintf("Lookup %s", r.string())
}

// An OpenRequest asks to open a file or directory
type OpenRequest struct {
	Header    `json:"-"`
	Dir       bool // is this Opendir?
	Flags     OpenFlags
	OpenFlags OpenRequestFlags
}

var _ = Request(&OpenRequest{})

func (r *OpenRequest) String() string {
	return fmt.Sprintf("Open [%s] dir=%v fl=%v", &r.Header, r.Dir, r.Flags)
}

// Respond replies to the request with the given response.
func (r *OpenRequest) Respond(resp *OpenResponse) {
	// buf := newBuffer(unsafe.Sizeof(openOut{}))
	// out := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
	// out.Fh = uint64(resp.Handle)
	// out.OpenFlags = uint32(resp.Flags)
	// r.respond(buf)
}

// A OpenResponse is the response to a OpenRequest.
type OpenResponse struct {
	HeaderResponse
	Handle HandleID
	Flags  OpenResponseFlags
}

func (r *OpenResponse) IsResponseType() {}

func (r *OpenResponse) PutId(id uint64) {
	r.Id = id
}

func (r *OpenResponse) GetId() uint64 {
	return r.Id
}

func (r *OpenResponse) string() string {
	return fmt.Sprintf("%v fl=%v", r.Handle, r.Flags)
}

func (r *OpenResponse) String() string {
	return fmt.Sprintf("Open %s", r.string())
}

// A CreateRequest asks to create and open a file (not a directory).
type CreateRequest struct {
	Header `json:"-"`
	Name   string
	Flags  OpenFlags
	Mode   os.FileMode
	// Umask of the request. Not supported on OS X.
	Umask os.FileMode
}

var _ = Request(&CreateRequest{})

func (r *CreateRequest) String() string {
	return fmt.Sprintf("Create [%s] %q fl=%v mode=%v umask=%v", &r.Header, r.Name, r.Flags, r.Mode, r.Umask)
}

// Respond replies to the request with the given response.
func (r *CreateRequest) Respond(resp *CreateResponse) {
	// eSize := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(eSize + unsafe.Sizeof(openOut{}))

	// e := (*entryOut)(buf.alloc(eSize))
	// e.Nodeid = uint64(resp.Node)
	// e.Generation = resp.Generation
	// e.EntryValid = uint64(resp.EntryValid / time.Second)
	// e.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// e.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// e.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&e.Attr, r.Header.Conn.proto)

	// o := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
	// o.Fh = uint64(resp.Handle)
	// o.OpenFlags = uint32(resp.Flags)

	// r.respond(buf)
}

// A CreateResponse is the response to a CreateRequest.
// It describes the created node and opened handle.
type CreateResponse struct {
	LookupResponse
	OpenResponse
}

func (r *CreateResponse) String() string {
	return fmt.Sprintf("Create {%s} {%s}", r.LookupResponse.string(), r.OpenResponse.string())
}

// A MkdirRequest asks to create (but not open) a directory.
type MkdirRequest struct {
	Header `json:"-"`
	Name   string
	Mode   os.FileMode
	// Umask of the request. Not supported on OS X.
	Umask os.FileMode
}

var _ = Request(&MkdirRequest{})

func (r *MkdirRequest) String() string {
	return fmt.Sprintf("Mkdir [%s] %q mode=%v umask=%v", &r.Header, r.Name, r.Mode, r.Umask)
}

// Respond replies to the request with the given response.
func (r *MkdirRequest) Respond(resp *MkdirResponse) {
	// size := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*entryOut)(buf.alloc(size))
	// out.Nodeid = uint64(resp.Node)
	// out.Generation = resp.Generation
	// out.EntryValid = uint64(resp.EntryValid / time.Second)
	// out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A MkdirResponse is the response to a MkdirRequest.
type MkdirResponse struct {
	LookupResponse
}

func (r *MkdirResponse) String() string {
	return fmt.Sprintf("Mkdir %v", r.LookupResponse.string())
}

// A ReadRequest asks to read from an open file.
type ReadRequest struct {
	Header    `json:"-"`
	Dir       bool // is this Readdir?
	Handle    HandleID
	Offset    int64
	Size      int
	Flags     ReadFlags
	LockOwner uint64
	FileFlags OpenFlags
}

var _ = Request(&ReadRequest{})

func (r *ReadRequest) String() string {
	return fmt.Sprintf("Read [%s] %v %d @%#x dir=%v fl=%v lock=%d ffl=%v", &r.Header, r.Handle, r.Size, r.Offset, r.Dir, r.Flags, r.LockOwner, r.FileFlags)
}

// Respond replies to the request with the given response.
func (r *ReadRequest) Respond(resp *ReadResponse) {
	// buf := newBuffer(uintptr(len(resp.Data)))
	// buf = append(buf, resp.Data...)
	// r.respond(buf)
}

// A ReadResponse is the response to a ReadRequest.
type ReadResponse struct {
	Data []byte
}

func (r *ReadResponse) String() string {
	return fmt.Sprintf("Read %d", len(r.Data))
}

type jsonReadResponse struct {
	Len uint64
}

func (r *ReadResponse) MarshalJSON() ([]byte, error) {
	j := jsonReadResponse{
		Len: uint64(len(r.Data)),
	}
	return json.Marshal(j)
}

// A ReleaseRequest asks to release (close) an open file handle.
type ReleaseRequest struct {
	Header       `json:"-"`
	Dir          bool // is this Releasedir?
	Handle       HandleID
	Flags        OpenFlags // flags from OpenRequest
	ReleaseFlags ReleaseFlags
	LockOwner    uint32
}

var _ = Request(&ReleaseRequest{})

func (r *ReleaseRequest) String() string {
	return fmt.Sprintf("Release [%s] %v fl=%v rfl=%v owner=%#x", &r.Header, r.Handle, r.Flags, r.ReleaseFlags, r.LockOwner)
}

// Respond replies to the request, indicating that the handle has been released.
func (r *ReleaseRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A DestroyRequest is sent by the kernel when unmounting the file system.
// No more requests will be received after this one, but it should still be
// responded to.
type DestroyRequest struct {
	Header `json:"-"`
}

var _ = Request(&DestroyRequest{})

func (r *DestroyRequest) String() string {
	return fmt.Sprintf("Destroy [%s]", &r.Header)
}

// Respond replies to the request.
func (r *DestroyRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A ForgetRequest is sent by the kernel when forgetting about r.Node
// as returned by r.N lookup requests.
type ForgetRequest struct {
	Header `json:"-"`
	N      uint64
}

var _ = Request(&ForgetRequest{})

func (r *ForgetRequest) String() string {
	return fmt.Sprintf("Forget [%s] %d", &r.Header, r.N)
}

// Respond replies to the request, indicating that the forgetfulness has been recorded.
func (r *ForgetRequest) Respond() {
	// Don't reply to forget messages.
	r.noResponse()
}

// A Dirent represents a single directory entry.
type Dirent struct {
	// Inode this entry names.
	Inode uint64

	// Type of the entry, for example DT_File.
	//
	// Setting this is optional. The zero value (DT_Unknown) means
	// callers will just need to do a Getattr when the type is
	// needed. Providing a type can speed up operations
	// significantly.
	Type DirentType

	// Name of the entry
	Name string
}

// Type of an entry in a directory listing.
type DirentType uint32

const (
	// These don't quite match os.FileMode; especially there's an
	// explicit unknown, instead of zero value meaning file. They
	// are also not quite syscall.DT_*; nothing says the FUSE
	// protocol follows those, and even if they were, we don't
	// want each fs to fiddle with syscall.

	// The shift by 12 is hardcoded in the FUSE userspace
	// low-level C library, so it's safe here.

	DT_Unknown DirentType = 0
	DT_Socket  DirentType = syscall.S_IFSOCK >> 12
	DT_Link    DirentType = syscall.S_IFLNK >> 12
	DT_File    DirentType = syscall.S_IFREG >> 12
	DT_Block   DirentType = syscall.S_IFBLK >> 12
	DT_Dir     DirentType = syscall.S_IFDIR >> 12
	DT_Char    DirentType = syscall.S_IFCHR >> 12
	DT_FIFO    DirentType = syscall.S_IFIFO >> 12
)

func (t DirentType) String() string {
	switch t {
	case DT_Unknown:
		return "unknown"
	case DT_Socket:
		return "socket"
	case DT_Link:
		return "link"
	case DT_File:
		return "file"
	case DT_Block:
		return "block"
	case DT_Dir:
		return "dir"
	case DT_Char:
		return "char"
	case DT_FIFO:
		return "fifo"
	}
	return "invalid"
}

// AppendDirent appends the encoded form of a directory entry to data
// and returns the resulting slice.
func AppendDirent(data []byte, dir Dirent) []byte {
	de := dirent{
		Ino:     dir.Inode,
		Namelen: uint32(len(dir.Name)),
		Type:    uint32(dir.Type),
	}
	de.Off = uint64(len(data) + direntSize + (len(dir.Name)+7)&^7)
	data = append(data, (*[direntSize]byte)(unsafe.Pointer(&de))[:]...)
	data = append(data, dir.Name...)
	n := direntSize + uintptr(len(dir.Name))
	if n%8 != 0 {
		var pad [8]byte
		data = append(data, pad[:8-n%8]...)
	}
	return data
}

// A WriteRequest asks to write to an open file.
type WriteRequest struct {
	Header
	Handle    HandleID
	Offset    int64
	Data      []byte
	Flags     WriteFlags
	LockOwner uint64
	FileFlags OpenFlags
}

var _ = Request(&WriteRequest{})

func (r *WriteRequest) String() string {
	return fmt.Sprintf("Write [%s] %v %d @%d fl=%v lock=%d ffl=%v", &r.Header, r.Handle, len(r.Data), r.Offset, r.Flags, r.LockOwner, r.FileFlags)
}

type jsonWriteRequest struct {
	Handle HandleID
	Offset int64
	Len    uint64
	Flags  WriteFlags
}

func (r *WriteRequest) MarshalJSON() ([]byte, error) {
	j := jsonWriteRequest{
		Handle: r.Handle,
		Offset: r.Offset,
		Len:    uint64(len(r.Data)),
		Flags:  r.Flags,
	}
	return json.Marshal(j)
}

// Respond replies to the request with the given response.
func (r *WriteRequest) Respond(resp *WriteResponse) {
	// buf := newBuffer(unsafe.Sizeof(writeOut{}))
	// out := (*writeOut)(buf.alloc(unsafe.Sizeof(writeOut{})))
	// out.Size = uint32(resp.Size)
	// r.respond(buf)
}

// A WriteResponse replies to a write indicating how many bytes were written.
type WriteResponse struct {
	Size int
}

func (r *WriteResponse) String() string {
	return fmt.Sprintf("Write %d", r.Size)
}

// A SetattrRequest asks to change one or more attributes associated with a file,
// as indicated by Valid.
type SetattrRequest struct {
	Header `json:"-"`
	Valid  SetattrValid
	Handle HandleID
	Size   uint64
	Atime  time.Time
	Mtime  time.Time
	// Mode is the file mode to set (when valid).
	//
	// The type of the node (as in os.ModeType, os.ModeDir etc) is not
	// guaranteed to be sent by the kernel, in which case
	// os.ModeIrregular will be set.
	Mode os.FileMode
	Uid  uint32
	Gid  uint32

	// OS X only
	Bkuptime time.Time
	Chgtime  time.Time
	Crtime   time.Time
	Flags    uint32 // see chflags(2)
}

var _ = Request(&SetattrRequest{})

func (r *SetattrRequest) String() string {
	var buf bytes.Buffer
	// fmt.Fprintf(&buf, "Setattr [%s]", &r.Header)
	// if r.Valid.Mode() {
	// 	fmt.Fprintf(&buf, " mode=%v", r.Mode)
	// }
	// if r.Valid.Uid() {
	// 	fmt.Fprintf(&buf, " uid=%d", r.Uid)
	// }
	// if r.Valid.Gid() {
	// 	fmt.Fprintf(&buf, " gid=%d", r.Gid)
	// }
	// if r.Valid.Size() {
	// 	fmt.Fprintf(&buf, " size=%d", r.Size)
	// }
	// if r.Valid.Atime() {
	// 	fmt.Fprintf(&buf, " atime=%v", r.Atime)
	// }
	// if r.Valid.AtimeNow() {
	// 	fmt.Fprintf(&buf, " atime=now")
	// }
	// if r.Valid.Mtime() {
	// 	fmt.Fprintf(&buf, " mtime=%v", r.Mtime)
	// }
	// if r.Valid.MtimeNow() {
	// 	fmt.Fprintf(&buf, " mtime=now")
	// }
	// if r.Valid.Handle() {
	// 	fmt.Fprintf(&buf, " handle=%v", r.Handle)
	// } else {
	// 	fmt.Fprintf(&buf, " handle=INVALID-%v", r.Handle)
	// }
	// if r.Valid.LockOwner() {
	// 	fmt.Fprintf(&buf, " lockowner")
	// }
	// if r.Valid.Crtime() {
	// 	fmt.Fprintf(&buf, " crtime=%v", r.Crtime)
	// }
	// if r.Valid.Chgtime() {
	// 	fmt.Fprintf(&buf, " chgtime=%v", r.Chgtime)
	// }
	// if r.Valid.Bkuptime() {
	// 	fmt.Fprintf(&buf, " bkuptime=%v", r.Bkuptime)
	// }
	// if r.Valid.Flags() {
	// 	fmt.Fprintf(&buf, " flags=%v", r.Flags)
	// }
	return buf.String()
}

// Respond replies to the request with the given response,
// giving the updated attributes.
func (r *SetattrRequest) Respond(resp *SetattrResponse) {
	// size := attrOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*attrOut)(buf.alloc(size))
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A SetattrResponse is the response to a SetattrRequest.
type SetattrResponse struct {
	Attr Attr // file attributes
}

func (r *SetattrResponse) String() string {
	return fmt.Sprintf("Setattr %v", r.Attr)
}

// A FlushRequest asks for the current state of an open file to be flushed
// to storage, as when a file descriptor is being closed.  A single opened Handle
// may receive multiple FlushRequests over its lifetime.
type FlushRequest struct {
	Header    `json:"-"`
	Handle    HandleID
	Flags     uint32
	LockOwner uint64
}

var _ = Request(&FlushRequest{})

func (r *FlushRequest) String() string {
	return fmt.Sprintf("Flush [%s] %v fl=%#x lk=%#x", &r.Header, r.Handle, r.Flags, r.LockOwner)
}

// Respond replies to the request, indicating that the flush succeeded.
func (r *FlushRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A RemoveRequest asks to remove a file or directory from the
// directory r.Node.
type RemoveRequest struct {
	Header `json:"-"`
	Name   string // name of the entry to remove
	Dir    bool   // is this rmdir?
}

var _ = Request(&RemoveRequest{})

func (r *RemoveRequest) String() string {
	return fmt.Sprintf("Remove [%s] %q dir=%v", &r.Header, r.Name, r.Dir)
}

// Respond replies to the request, indicating that the file was removed.
func (r *RemoveRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// A SymlinkRequest is a request to create a symlink making NewName point to Target.
type SymlinkRequest struct {
	Header          `json:"-"`
	NewName, Target string
}

var _ = Request(&SymlinkRequest{})

func (r *SymlinkRequest) String() string {
	return fmt.Sprintf("Symlink [%s] from %q to target %q", &r.Header, r.NewName, r.Target)
}

// Respond replies to the request, indicating that the symlink was created.
func (r *SymlinkRequest) Respond(resp *SymlinkResponse) {
	// size := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*entryOut)(buf.alloc(size))
	// out.Nodeid = uint64(resp.Node)
	// out.Generation = resp.Generation
	// out.EntryValid = uint64(resp.EntryValid / time.Second)
	// out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A SymlinkResponse is the response to a SymlinkRequest.
type SymlinkResponse struct {
	LookupResponse
}

func (r *SymlinkResponse) String() string {
	return fmt.Sprintf("Symlink %v", r.LookupResponse.string())
}

// A ReadlinkRequest is a request to read a symlink's target.
type ReadlinkRequest struct {
	Header `json:"-"`
}

var _ = Request(&ReadlinkRequest{})

func (r *ReadlinkRequest) String() string {
	return fmt.Sprintf("Readlink [%s]", &r.Header)
}

func (r *ReadlinkRequest) Respond(target string) {
	// buf := newBuffer(uintptr(len(target)))
	// buf = append(buf, target...)
	// r.respond(buf)
}

// A LinkRequest is a request to create a hard link.
type LinkRequest struct {
	Header  `json:"-"`
	OldNode NodeID
	NewName string
}

var _ = Request(&LinkRequest{})

func (r *LinkRequest) String() string {
	return fmt.Sprintf("Link [%s] node %d to %q", &r.Header, r.OldNode, r.NewName)
}

func (r *LinkRequest) Respond(resp *LookupResponse) {
	// size := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*entryOut)(buf.alloc(size))
	// out.Nodeid = uint64(resp.Node)
	// out.Generation = resp.Generation
	// out.EntryValid = uint64(resp.EntryValid / time.Second)
	// out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

// A RenameRequest is a request to rename a file.
type RenameRequest struct {
	Header           `json:"-"`
	NewDir           NodeID
	OldName, NewName string
}

var _ = Request(&RenameRequest{})

func (r *RenameRequest) String() string {
	return fmt.Sprintf("Rename [%s] from %q to dirnode %v %q", &r.Header, r.OldName, r.NewDir, r.NewName)
}

func (r *RenameRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

type MknodRequest struct {
	Header `json:"-"`
	Name   string
	Mode   os.FileMode
	Rdev   uint32
	// Umask of the request. Not supported on OS X.
	Umask os.FileMode
}

var _ = Request(&MknodRequest{})

func (r *MknodRequest) String() string {
	return fmt.Sprintf("Mknod [%s] Name %q mode=%v umask=%v rdev=%d", &r.Header, r.Name, r.Mode, r.Umask, r.Rdev)
}

func (r *MknodRequest) Respond(resp *LookupResponse) {
	// size := entryOutSize(r.Header.Conn.proto)
	// buf := newBuffer(size)
	// out := (*entryOut)(buf.alloc(size))
	// out.Nodeid = uint64(resp.Node)
	// out.Generation = resp.Generation
	// out.EntryValid = uint64(resp.EntryValid / time.Second)
	// out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	// out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	// out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	// resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	// r.respond(buf)
}

type FsyncRequest struct {
	Header `json:"-"`
	Handle HandleID
	// TODO bit 1 is datasync, not well documented upstream
	Flags uint32
	Dir   bool
}

var _ = Request(&FsyncRequest{})

func (r *FsyncRequest) String() string {
	return fmt.Sprintf("Fsync [%s] Handle %v Flags %v", &r.Header, r.Handle, r.Flags)
}

func (r *FsyncRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// An InterruptRequest is a request to interrupt another pending request. The
// response to that request should return an error status of EINTR.
type InterruptRequest struct {
	Header `json:"-"`
	IntrID RequestID // ID of the request to be interrupt.
}

var _ = Request(&InterruptRequest{})

func (r *InterruptRequest) Respond() {
	// nothing to do here
	r.noResponse()
}

func (r *InterruptRequest) String() string {
	return fmt.Sprintf("Interrupt [%s] ID %v", &r.Header, r.IntrID)
}

// An ExchangeDataRequest is a request to exchange the contents of two
// files, while leaving most metadata untouched.
//
// This request comes from OS X exchangedata(2) and represents its
// specific semantics. Crucially, it is very different from Linux
// renameat(2) RENAME_EXCHANGE.
//
// https://developer.apple.com/library/mac/documentation/Darwin/Reference/ManPages/man2/exchangedata.2.html
type ExchangeDataRequest struct {
	Header           `json:"-"`
	OldDir, NewDir   NodeID
	OldName, NewName string
	// TODO options
}

var _ = Request(&ExchangeDataRequest{})

func (r *ExchangeDataRequest) String() string {
	// TODO options
	return fmt.Sprintf("ExchangeData [%s] %v %q and %v %q", &r.Header, r.OldDir, r.OldName, r.NewDir, r.NewName)
}

func (r *ExchangeDataRequest) Respond() {
	// buf := newBuffer(0)
	// r.respond(buf)
}

// /////////////////////////////
// /////////////////////////////
// /////////////////////////////
// // An OpenRequest asks to open a file or directory
// type OpenRequest struct {
// 	Header    `json:"-"`
// 	Dir       bool // is this Opendir?
// 	Flags     OpenFlags
// 	OpenFlags OpenRequestFlags
// }

// var _ Request = (*OpenRequest)(nil)

// func (r *OpenRequest) String() string {
// 	return fmt.Sprintf("Open [%s] dir=%v fl=%v", &r.Header, r.Dir, r.Flags)
// }
// func (r *OpenRequest) IsRequestType() {}

// // A OpenResponse is the response to a OpenRequest.
// type OpenResponse struct {
// 	Handle HandleID
// 	Flags  OpenResponseFlags
// }

// func (r *OpenResponse) string() string {
// 	return fmt.Sprintf("%v fl=%v", r.Handle, r.Flags)
// }

// func (r *OpenResponse) String() string {
// 	return fmt.Sprintf("Open %s", r.string())
// }

// // Respond replies to the request with the given response.
// func (r *OpenRequest) Respond(resp *OpenResponse) {
// 	// buf := newBuffer(unsafe.Sizeof(openOut{}))
// 	// out := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
// 	// out.Fh = uint64(resp.Handle)
// 	// out.OpenFlags = uint32(resp.Flags)

// 	var outResp Response = &OpenResponse{
// 		Header: Header{
// 			ID:   RequestID(uint64(r.ID)),
// 			Node: NodeID(uint64(r.Node)),
// 		},
// 		Handle: HandleID(resp.Handle),
// 	}

// 	r.respond(outResp)
// }

// type CreateFileRequest struct {
// 	Header
// 	FileInfo   *dokan.FileInfo
// 	CreateData *dokan.CreateData
// }

// func (r CreateFileRequest) Hdr() *Header       { return r.hdr }
// func (r CreateFileRequest) RespondError(error) {}
//
//	func (r CreateFileRequest) String() string {
//		return fmt.Sprintf("RequestCreateFile [%s]", r.FileInfo.Path())
//	}
// func (r *CreateFileRequest) BuildFrom(conn *Conn, req *xfz.RequestCreateFile) {
// 	h := req.Hdr()
// 	r.hdr = &Header{
// 		Conn: conn,
// 		ID:   RequestID(h.ID),
// 		Node: NodeID(h.Node),
// 		// Uid:  h.Uid,
// 		// Gid:  h.Gid,
// 		// Pid:  h.Pid,
// 	}
// 	r.FileInfo = req.FileInfo
// 	r.CreateData = req.CreateData
// }

// func (r *CreateFileRequest) Respond(resp *CreateFileResponse) {
// 	buf := newBuffer(unsafe.Sizeof(statfsOut{}))
// 	out := (*statfsOut)(buf.alloc(unsafe.Sizeof(statfsOut{})))
// 	out.St = kstatfs{
// 		Blocks:  resp.Blocks,
// 		Bfree:   resp.Bfree,
// 		Bavail:  resp.Bavail,
// 		Files:   resp.Files,
// 		Ffree:   resp.Ffree,
// 		Bsize:   resp.Bsize,
// 		Namelen: resp.Namelen,
// 		Frsize:  resp.Frsize,
// 	}
// 	r.respond(buf)
// }

// type CreateFileResponse struct {
// 	File         dokan.File
// 	CreateStatus dokan.CreateStatus
// }

// var _ Request = CreateFileRequest{}

// type xfzResponse struct {
// 	hdr *xfz.ResponseHeader
// }

// var _ xfz.Response = (*xfzResponse)(nil)

// func (r *xfzResponse) Hdr() *xfz.ResponseHeader {
// 	return r.hdr
// }
// func (r *xfzResponse) RespondError(error) {
// }
// func (r *xfzResponse) String() string {
// 	return fmt.Sprintf("Response [%s]", r.hdr.ID)
// }
// func (r *xfzResponse) IsResponseType() {}

func mkHeaderFromDirective(directive Directive) Header {
	h := directive.Hdr()
	node := makeNodeIdFromFileInfo(h.FileInfo)
	return Header{
		ID:   RequestID(h.ID),
		Node: NodeID(node),
		// Uid:  uint32(h.ID),
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
}

func convertDirectiveToRequest(directive Directive) Request {
	var result Request

	fmt.Printf("%v", result)
	// map dokan -> fuzeo
	// https://github.com/dokan-dev/dokany/wiki/FUSE
	switch r := directive.(type) {
	case *CreateFileDirective:
		fmt.Printf("%v", r)
		// fuse_operations::mknod
		// fuse_operations::create
		// fuse_operations::open
		// fuse_operations::mkdir
		// fuse_operations::opendir
		cd := r.CreateData
		fmt.Printf("FileInfo %v\n", r.FileInfo)
		fmt.Printf("CreateData %v\n", cd)
		fmt.Printf("CreateData FileAttributes %v\n", cd.FileAttributes)
		if cd.FileAttributes&dokan.FileAttributeNormal == dokan.FileAttributeNormal &&
			cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

			// n, ok := node.(NodeCreater)
			// if !ok {
			// 	// If we send back ENOSYS, fuzeo will try mknod+open.
			// 	return syscall.EPERM
			// }
			// s := &fuzeo.CreateFileResponse{
			// 	File:         nil,
			// 	CreateStatus: dokan.ExistingDir,
			// }
			// if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory {
			// 	s = &fuzeo.CreateFileResponse{
			// 		File:         nil,
			// 		CreateStatus: dokan.CreateStatus(dokan.ErrAccessDenied),
			// 	}
			// }
			// initLookupResponse(&s.LookupResponse)
			// n2, h2, err := n.Create(ctx, r, s)
			// if err != nil {
			// 	return err
			// }
			// if err := c.saveLookup(ctx, &s.LookupResponse, snode, r.Name, n2); err != nil {
			// 	return err
			// }
			// s.Handle = c.saveHandle(h2)
			// done(s)
			// r.Respond(s)
		} else if cd.FileAttributes&dokan.FileAttributeNormal == dokan.FileAttributeNormal &&
			cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {

			// // s := &fuzeo.OpenResponse{}
			// s := &fuzeo.CreateFileResponse{
			// 	File:         nil,
			// 	CreateStatus: dokan.ExistingDir,
			// }
			// if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
			// 	cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

			// 	s = &fuzeo.CreateFileResponse{
			// 		File:         nil,
			// 		CreateStatus: dokan.CreateStatus(dokan.ErrAccessDenied),
			// 	}
			// }
			// var h2 Handle
			// if n, ok := node.(NodeOpener); ok {
			// 	hh, err := n.Open(ctx, r, s)
			// 	if err != nil {
			// 		return err
			// 	}
			// 	h2 = hh
			// } else {
			// 	h2 = node
			// }
			// s.Handle = c.saveHandle(h2)
			// done(s)
			// r.Respond(s)
		} else if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
			cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

		} else if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
			cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {

		} else if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
			// r.FileInfo = req.FileInfo
			// r.CreateData = req.CreateData
			req := &OpenRequest{
				Header: mkHeaderFromDirective(directive),
				// Dir:    m.hdr.Opcode == opOpendir,
				Dir: cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory,
				// Flags:  openFlags(in.Flags),
				Flags: OpenReadOnly,
			}

			result = req
		}
	case *FindFilesDirective:
		// fuse_operations::readdir
		fmt.Printf("%v", r)
		fi := r.FileInfo
		fmt.Printf("FileInfo %v\n", fi)
		fmt.Printf("FileInfo Pattern %v\n", r.Pattern)
		in := r
		fmt.Printf("Header (in): %v\n", in)
		req := &ReadRequest{
			Header: mkHeaderFromDirective(directive),
			// Dir:    m.hdr.Opcode == opReaddir,
			Dir:       true,
			Handle:    HandleID(in.Fh),
			Offset:    int64(in.Offset),
			Size:      int(in.Size),
			FileFlags: OpenReadOnly,
		}
		// if c.proto.GE(Protocol{7, 9}) {
		// 	// req.Flags = ReadFlags(in.ReadFlags)
		// 	req.Flags = ReadLockOwner
		// 	// req.LockOwner = in.LockOwner
		// 	req.LockOwner = in.LockOwner
		// 	// req.FileFlags = openFlags(in.Flags)
		// }

		result = req
	}

	// return result, err
	return result
}
