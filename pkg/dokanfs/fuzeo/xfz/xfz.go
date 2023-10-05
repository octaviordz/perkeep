package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

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

type mountHandleEntry struct {
	Fd  *os.File
	Dir string
	// MountConfig *mountConfig
}

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

var reqBuffer = make(chan Request, 20)
var respBuffer = make(chan Response, 20)
var reqId uint64 = 0

var requests = make(map[RequestID]Request)
var requestsMutex sync.Mutex

func putRequest(req Request) RequestID {
	rid := atomic.AddUint64(&reqId, 1)
	id := RequestID(rid)
	requestsMutex.Lock()
	defer requestsMutex.Unlock()
	requests[id] = req
	return id
}

func getRequest(id RequestID) Request {
	requestsMutex.Lock()
	defer requestsMutex.Unlock()
	return requests[id]
}

func ReadRequest(fd *os.File) (Request, error) {
	req := <-reqBuffer

	return req, nil
}

type Response interface {
	IsResponseType()
	// Hdr returns the Header associated with this request.
	Hdr() *Header

	// RespondError responds to the request with the given error.
	// RespondError(error)

	// String() string
}

// A RequestID identifies an active FUSE request.
type RequestID uint64

func (r RequestID) String() string {
	return fmt.Sprintf("%#x", uint64(r))
}

const (
	rootID = 1
)

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
	ID   RequestID // unique ID for request
	Node NodeID    // file or directory the request is about
}

// type ResponseHeader struct {
// 	ID    RequestID // unique ID for request
// 	Node  NodeID    // file or directory the request is about
// 	Error error
// }

func (h *Header) String() string {
	return fmt.Sprintf("ID=%v", h.ID)
}

func (h *Header) Hdr() *Header {
	return h
}

type Request interface {
	putHdr(*Header)
	// Hdr returns the Header associated with this request.
	Hdr() *Header

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	IsRequestType()
}

type RequestHeaderInfo interface{}

func mapRequestType(
	req *Request,
	fileInfoCallBack func(req *Request, fi *dokan.FileInfo),
) {

	switch r := (*req).(type) {
	case *CreateFileRequest:
		fileInfoCallBack(req, r.FileInfo)
	case *FindFilesRequest:
		fileInfoCallBack(req, r.FileInfo)
	}
}

var nodes = make(map[string]NodeID)
var nodesMutex sync.Mutex

func putNodeId(req Request) NodeID {
	var nid *NodeID = nil
	onFileInfo := func(req *Request, fi *dokan.FileInfo) {
		path := fi.Path()

		nodesMutex.Lock()
		defer nodesMutex.Unlock()
		nodeId, ok := nodes[path]
		if !ok {
			nodeId = NodeID(len(nodes) + 1)
			nodes[path] = nodeId
		}
		nid = &nodeId
	}
	mapRequestType(&req, onFileInfo)

	if nid == nil {
		panic("No value for NodeID.")
	}
	return *nid
}

var handles = make(map[HandleID]dokan.File)
var handlesMutex sync.Mutex

func saveHandle(handle HandleID, file dokan.File) {
	handlesMutex.Lock()
	defer handlesMutex.Unlock()
	handles[handle] = file
}

type CreateFileRequest struct {
	hdr        *Header
	FileInfo   *dokan.FileInfo
	CreateData *dokan.CreateData
	// openIn
	Flags  uint32
	Unused uint32
}

var _ Request = (*CreateFileRequest)(nil)

func (r *CreateFileRequest) putHdr(header *Header) { r.hdr = header }
func (r *CreateFileRequest) Hdr() *Header          { return r.hdr }
func (r *CreateFileRequest) RespondError(error)    {}
func (r *CreateFileRequest) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", r.FileInfo.Path())
}
func (r *CreateFileRequest) IsRequestType() {}

type CreateFileResponse struct {
	dokan.File
	dokan.CreateStatus
	//error

	Header
	Handle HandleID
	Flags  OpenResponseFlags
}

var _ Response = (*CreateFileResponse)(nil)

func (r *CreateFileResponse) Hdr() *Header    { return r.Header.Hdr() }
func (r *CreateFileResponse) IsResponseType() {}

type FindFilesRequest struct {
	hdr              *Header
	FileInfo         *dokan.FileInfo
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
	// readIn
	Fh        uint64
	Offset    uint64
	Size      uint32
	ReadFlags uint32
	LockOwner uint64
	Flags     uint32
	_         uint32
}

var _ Request = (*FindFilesRequest)(nil)

func (r *FindFilesRequest) putHdr(header *Header) { r.hdr = header }
func (r *FindFilesRequest) Hdr() *Header          { return r.hdr }
func (r *FindFilesRequest) RespondError(error)    {}
func (r *FindFilesRequest) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}
func (r FindFilesRequest) IsRequestType() {}

type FindFilesResponse struct{ Header }

var _ Response = (*FindFilesResponse)(nil)

func (r *FindFilesResponse) Hdr() *Header    { return r.Header.Hdr() }
func (r *FindFilesResponse) IsResponseType() {}

func WriteRequest(ctx context.Context, req Request) (Response, error) {
	requestId := putRequest(req)
	nodeId := putNodeId(req)

	req.putHdr(&Header{
		ID:   requestId,
		Node: nodeId,
	})
	reqBuffer <- req
	var resp Response
	for {
		resp = <-respBuffer
		h := resp.Hdr()
		if h.ID == requestId {
			break
		}
		respBuffer <- resp
	}

	return resp, nil
}

func WriteRespond(fd *os.File, resp Response) error {

	switch r := (resp).(type) {
	case *CreateFileResponse:
		// ri := getRequest(resp.Hdr().ID)
		// req := ri.(*CreateFileRequest)
		r.

		if err != nil {
			// return emptyFile{}, dokan.CreateStatus(dokan.ErrNotSupported), err
			return emptyFile{}, r.CreateStatus, err
		}

		if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
			cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

			return emptyFile{}, dokan.CreateStatus(dokan.ErrAccessDenied), nil
		}

		return emptyFile{}, dokan.ExistingDir, nil

		f := emptyFile{}


		saveHandle(r.Handle, f)

	case *FindFilesResponse:

		f := emptyFile{}

		saveHandle(r.Handle, f)
	}

	respBuffer <- resp

	return nil
}
