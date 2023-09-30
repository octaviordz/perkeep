package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"

	"github.com/keybase/client/go/kbfs/dokan"
)

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

type handleEntry struct {
	Fd  *os.File
	Dir string
	// MountConfig *mountConfig
}

var mountHandleMap = make(map[*os.File]handleEntry)

func MountHandleMap() map[*os.File]handleEntry {
	return mountHandleMap
}

func MountHandle(dir string) (handle *os.File, err error) {
	//dir, err = os.MkdirTemp("", "pk-dokanfs-mount")
	fd, err := os.CreateTemp("", "pk-dokanfs-mount")
	if err != nil {
		return nil, err
	}
	mountHandleMap[fd] = handleEntry{
		Fd:  fd,
		Dir: dir,
	}

	return fd, nil
}

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

var reqBuffer = make(chan Request, 400)
var respBuffer = make(chan Response, 400)
var reqId uint64 = 0

func WriteRequest(ctx context.Context, req Request, requestIdCallback func(RequestID)) (Response, error) {
	reqId++
	requestID := RequestID(reqId)
	requestIdCallback(requestID)
	reqBuffer <- req
	resp := <-respBuffer

	return resp, nil
}

func ReadRequest(fd *os.File) (Request, error) {
	req := <-reqBuffer

	return req, nil
}

// // FindFiles
// func (c *Conn) ReadRequest(fillStatCallback func(*dokan.NamedStat) error) (Request, error) {
// }

type Response interface {
	IsResponseType()
}

func WriteRespond(fd *os.File, resp Response) {
	respBuffer <- resp
}

// A RequestID identifies an active FUSE request.
type RequestID uint64

func (r RequestID) String() string {
	return fmt.Sprintf("%#x", uint64(r))
}

const (
	rootID = 1
)

// A HandleID is a number identifying an open directory or file.
// It only needs to be unique while the directory or file is open.
type HandleID uint64

func (h HandleID) String() string {
	return fmt.Sprintf("%#x", uint64(h))
}

// The RootID identifies the root directory of a FUSE file system.
const RootID uint64 = rootID

// A Header describes the basic information sent in every request.
type Header struct {
	ID RequestID // unique ID for request
}

func (h *Header) String() string {
	return fmt.Sprintf("ID=%v", h.ID)
}

func (h *Header) Hdr() *Header {
	return h
}

type Request interface {
	// Hdr returns the Header associated with this request.
	Hdr() *Header

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	IsRequestType()
}

type RequestCreateFile struct {
	hdr        *Header
	FileInfo   *dokan.FileInfo
	CreateData *dokan.CreateData
}

var _ Request = RequestCreateFile{}

func (r RequestCreateFile) Hdr() *Header       { return r.hdr }
func (r RequestCreateFile) RespondError(error) {}
func (r RequestCreateFile) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", r.FileInfo.Path())
}
func (r RequestCreateFile) IsRequestType() {}

type RequestFindFiles struct {
	hdr              *Header
	FileInfo         *dokan.FileInfo
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
}

var _ Request = RequestFindFiles{}

func (r RequestFindFiles) Hdr() *Header       { return r.hdr }
func (r RequestFindFiles) RespondError(error) {}
func (r RequestFindFiles) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}
func (r RequestFindFiles) IsRequestType() {}
