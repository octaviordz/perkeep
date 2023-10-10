package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/keybase/client/go/kbfs/dokan"
)

type mountHandleEntry struct {
	Fd  *os.File
	Dir string
	// MountConfig *mountConfig
}

// reqPool is a pool of messages.
//
// Lifetime of a logical message is from getMessage to putMessage.
// getMessage is called by ReadRequest. putMessage is called by
// Conn.ReadRequest, Request.Respond, or Request.RespondError.
//
// Messages in the pool are guaranteed to have conn and off zeroed,
// buf allocated and len==bufSize, and hdr set.
//
//	var reqPool = sync.Pool{
//		New: allocMessage,
//	}
type directiveModule struct {
	Nodes      map[string]*dokan.FileInfo
	NodesMutex sync.Mutex

	InBuffer   chan Directive
	OutBuffer  chan Answer
	IdSequence uint64

	Directives      map[RequestID]Directive
	DirectivesMutex sync.Mutex
}

var directivem = &directiveModule{
	Nodes:      make(map[string]*dokan.FileInfo),
	InBuffer:   make(chan Directive, 20),
	OutBuffer:  make(chan Answer, 20),
	IdSequence: 0,
	Directives: make(map[RequestID]Directive),
}

func (m *directiveModule) put(req Directive) RequestID {
	rid := atomic.AddUint64(&(m.IdSequence), 1)
	id := RequestID(rid)
	m.DirectivesMutex.Lock()
	defer m.DirectivesMutex.Unlock()
	m.Directives[id] = req
	return id
}

func (m *directiveModule) get(id RequestID) Directive {
	m.DirectivesMutex.Lock()
	defer m.DirectivesMutex.Unlock()
	return m.Directives[id]
}

func (m *directiveModule) putNode(req Directive) *dokan.FileInfo {
	var fi *dokan.FileInfo = nil
	onFileInfo := func(req *Directive, fi *dokan.FileInfo) {
		path := fi.Path()

		m.NodesMutex.Lock()
		defer m.NodesMutex.Unlock()
		fi, ok := m.Nodes[path]
		if !ok {
			m.Nodes[path] = fi
		}
	}
	mapDirectiveType(&req, onFileInfo)

	if fi == nil {
		panic("No value for FileInfo.")
	}
	return fi
}

func (m *directiveModule) PostDirective(ctx context.Context, dir Directive) (Answer, error) {
	requestId := m.put(dir)
	node := m.putNode(dir)

	dir.putHdr(&DirectiveHeader{
		ID:       requestId,
		FileInfo: node,
	})
	m.InBuffer <- dir
	var resp Answer
	for {
		resp = <-m.OutBuffer
		h := resp.Hdr()
		if h.ID == requestId {
			break
		}
		m.OutBuffer <- resp
	}

	return resp, nil
}

func (*directiveModule) WriteAnswer(fd *os.File, answer Answer) error {

	// switch a := (answer).(type) {
	// case *CreateFileAnswer:
	// 	// ri := getRequest(resp.Hdr().ID)
	// 	// req := ri.(*CreateFileRequest)
	// 	if err != nil {
	// 		// return emptyFile{}, dokan.CreateStatus(dokan.ErrNotSupported), err
	// 		return emptyFile{}, a.CreateStatus, err
	// 	}

	// 	if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
	// 		cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

	// 		return emptyFile{}, dokan.CreateStatus(dokan.ErrAccessDenied), nil
	// 	}

	// 	return emptyFile{}, dokan.ExistingDir, nil

	// 	f := emptyFile{}

	// 	saveHandle(a.Handle, f)

	// case *FindFilesResponse:

	// 	f := emptyFile{}

	// 	saveHandle(a.Handle, f)
	// }

	// respBuffer <- answer

	return nil
}

type requestModule struct {
	nodes      map[string]NodeID
	nodesMutex sync.Mutex

	inBuffer   chan Request
	outBuffer  chan Response
	idSequence uint64

	requests      map[RequestID]Request
	requestsMutex sync.Mutex
}

// var reqBuffer = make(chan Request, 20)
// var respBuffer = make(chan Response, 20)
// var requestIdSequence uint64 = 0

// var requests = make(map[RequestID]Request)
// var requestsMutex sync.Mutex

var Requests = &requestModule{
	nodes:      make(map[string]NodeID),
	inBuffer:   make(chan Request, 20),
	outBuffer:  make(chan Response, 20),
	idSequence: 0,
	requests:   make(map[RequestID]Request),
}

func (m *requestModule) WriteRequest(ctx context.Context, req Request) (Response, error) {
	var resp Response
	requestId := m.putRequest(req)
	nodeId := m.putNodeId(req)
	h := req.Hdr()
	h.ID = requestId
	h.Node = nodeId

	m.inBuffer <- req
	for {
		resp = <-m.outBuffer
		id := resp.GetId()
		if RequestID(id) == requestId {
			break
		}
		m.outBuffer <- resp
	}

	return resp, nil
}

func (*requestModule) WriteRespond(fd *os.File, resp Response) error {

	// switch r := (resp).(type) {
	// case *CreateFileAnswer:
	// 	// ri := getRequest(resp.Hdr().ID)
	// 	// req := ri.(*CreateFileRequest)
	// 	if err != nil {
	// 		// return emptyFile{}, dokan.CreateStatus(dokan.ErrNotSupported), err
	// 		return emptyFile{}, r.CreateStatus, err
	// 	}

	// 	if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
	// 		cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

	// 		return emptyFile{}, dokan.CreateStatus(dokan.ErrAccessDenied), nil
	// 	}

	// 	return emptyFile{}, dokan.ExistingDir, nil

	// 	f := emptyFile{}

	// 	saveHandle(r.Handle, f)

	// case *FindFilesResponse:

	// 	f := emptyFile{}

	// 	saveHandle(r.Handle, f)
	// }

	// respBuffer <- resp

	return nil
}

func (m *requestModule) ReadRequest(fd *os.File) (Request, error) {
	req := <-m.inBuffer

	return req, nil
}

func (m *requestModule) putRequest(req Request) RequestID {
	rid := atomic.AddUint64(&(m.idSequence), 1)
	id := RequestID(rid)
	m.requestsMutex.Lock()
	defer m.requestsMutex.Unlock()
	m.requests[id] = req
	return id
}

func (m *requestModule) getRequest(id RequestID) Request {
	m.requestsMutex.Lock()
	defer m.requestsMutex.Unlock()
	return m.requests[id]
}

// answer reaction
type Answer interface {
	IsAnswerType()
	Hdr() *Header
}

type HeaderResponse struct {
	Id uint64
}

type Response interface {
	IsResponseType()
	PutId(uint64)
	GetId() uint64
	// Hdr returns the Header associated with this request.
	// Hdr() *Header

	// RespondError responds to the request with the given error.
	// RespondError(error)

	// String() string
}

// A Header describes the basic information sent in every request.
type DirectiveHeader struct {
	ID       RequestID       // unique ID for request
	FileInfo *dokan.FileInfo // file or directory the request is about
}

type Directive interface {
	putHdr(*DirectiveHeader)
	// Hdr returns the Header associated with this request.
	Hdr() *DirectiveHeader

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	IsDirectiveType()
}

// type ResponseHeader struct {
// 	ID    RequestID // unique ID for request
// 	Node  NodeID    // file or directory the request is about
// 	Error error
// }

type RequestHeaderInfo interface{}

func mapDirectiveType(
	directive *Directive,
	fileInfoCallBack func(directive *Directive, fi *dokan.FileInfo),
) {

	switch r := (*directive).(type) {
	case *CreateFileDirective:
		fileInfoCallBack(directive, r.FileInfo)
	case *FindFilesDirective:
		fileInfoCallBack(directive, r.FileInfo)
	}
}

func mapRequestType(
	req *Request,
	fileInfoCallBack func(req *Request, fi *dokan.FileInfo),
) {

	// switch r := (*req).(type) {
	// case *CreateFileDirective:
	// 	fileInfoCallBack(req, r.FileInfo)
	// case *FindFilesDirective:
	// 	fileInfoCallBack(req, r.FileInfo)
	// }
}

func makeNodeIdFromFileInfo(fi *dokan.FileInfo) NodeID {
	var nid *NodeID = nil
	path := fi.Path()

	Requests.nodesMutex.Lock()
	defer Requests.nodesMutex.Unlock()
	nodeId, ok := Requests.nodes[path]
	if !ok {
		nodeId = NodeID(len(Requests.nodes) + 1)
		Requests.nodes[path] = nodeId
	}
	nid = &nodeId

	if nid == nil {
		panic("No value for NodeID.")
	}
	return *nid
}

func (m *requestModule) putNodeId(req Request) NodeID {
	var nid *NodeID = nil
	onFileInfo := func(req *Request, fi *dokan.FileInfo) {
		path := fi.Path()

		m.nodesMutex.Lock()
		defer m.nodesMutex.Unlock()
		nodeId, ok := m.nodes[path]
		if !ok {
			nodeId = NodeID(len(m.nodes) + 1)
			m.nodes[path] = nodeId
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

type CreateFileDirective struct {
	hdr        *DirectiveHeader
	FileInfo   *dokan.FileInfo
	CreateData *dokan.CreateData
	// openIn
	Flags  uint32
	Unused uint32
}

var _ Directive = (*CreateFileDirective)(nil)

func (r *CreateFileDirective) putHdr(header *DirectiveHeader) { r.hdr = header }
func (r *CreateFileDirective) Hdr() *DirectiveHeader          { return r.hdr }
func (r *CreateFileDirective) RespondError(error)             {}
func (r *CreateFileDirective) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", r.FileInfo.Path())
}
func (r *CreateFileDirective) IsDirectiveType() {}

type CreateFileAnswer struct {
	dokan.File
	dokan.CreateStatus
	//error

	Header
	Handle HandleID
	Flags  OpenResponseFlags
}

var _ Answer = (*CreateFileAnswer)(nil)

func (r *CreateFileAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *CreateFileAnswer) IsAnswerType() {}

type FindFilesDirective struct {
	hdr              *DirectiveHeader
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

var _ Directive = (*FindFilesDirective)(nil)

func (r *FindFilesDirective) putHdr(header *DirectiveHeader) { r.hdr = header }
func (r *FindFilesDirective) Hdr() *DirectiveHeader          { return r.hdr }
func (r *FindFilesDirective) RespondError(error)             {}
func (r *FindFilesDirective) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}
func (r FindFilesDirective) IsDirectiveType() {}

type FindFilesResponse struct{ Header }

var _ Response = (*FindFilesResponse)(nil)

func (r *FindFilesResponse) Hdr() *Header    { return r.Header.Hdr() }
func (r *FindFilesResponse) IsResponseType() {}

func convertToDirective() {
}
