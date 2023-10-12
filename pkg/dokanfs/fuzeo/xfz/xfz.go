package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
type directivesModule struct {
	nodes      map[string]*dokan.FileInfo
	nodesMutex sync.Mutex

	inBuffer   chan Directive
	outBuffer  chan Answer
	idSequence uint64

	directives      map[DirectiveID]Directive
	directivesMutex sync.Mutex
}

var diesm = &directivesModule{
	nodes:      make(map[string]*dokan.FileInfo),
	inBuffer:   make(chan Directive, 20),
	outBuffer:  make(chan Answer, 20),
	idSequence: 0,
	directives: make(map[DirectiveID]Directive),
}

func (m *directivesModule) putDirective(req Directive) DirectiveID {
	rid := atomic.AddUint64(&(m.idSequence), 1)
	id := DirectiveID(rid)
	m.directivesMutex.Lock()
	defer m.directivesMutex.Unlock()
	m.directives[id] = req
	return id
}

func (m *directivesModule) getDirective(id DirectiveID) Directive {
	m.directivesMutex.Lock()
	defer m.directivesMutex.Unlock()
	return m.directives[id]
}

func (m *directivesModule) putNode(req Directive) *dokan.FileInfo {
	var resultFi *dokan.FileInfo = nil
	onFileInfo := func(req *Directive, fi *dokan.FileInfo) {
		path := fi.Path()

		m.nodesMutex.Lock()
		defer m.nodesMutex.Unlock()
		fix, ok := m.nodes[path]
		resultFi = fix
		if !ok {
			m.nodes[path] = fi
			resultFi = fi
		}
	}
	mapDirectiveType(&req, onFileInfo)

	if resultFi == nil {
		panic("No value for FileInfo.")
	}
	return resultFi
}

func (m *directivesModule) PostDirective(ctx context.Context, directive Directive) (Answer, error) {
	id := m.putDirective(directive)
	node := m.putNode(directive)

	directive.putHdr(&DirectiveHeader{
		ID:       id,
		FileInfo: node,
	})
	m.inBuffer <- directive
	var resp Answer
	for {
		//TODO(ORC): How long should we wait/loop
		// and what error should be produced.
		resp = <-m.outBuffer
		h := resp.Hdr()
		if DirectiveID(h.ID) == id {
			break
		}
		m.outBuffer <- resp
	}

	return resp, nil
}

func (m *directivesModule) PostAnswer(fd *os.File, answer Answer) error {

	m.outBuffer <- answer

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

type requestsModule struct {
	nodes      map[string]NodeID
	nodesMutex sync.Mutex

	// inBuffer   chan Request
	// outBuffer  chan Response
	idSequence uint64

	requests      map[RequestID]Request
	requestsMutex sync.Mutex
}

// var reqBuffer = make(chan Request, 20)
// var respBuffer = make(chan Response, 20)
// var requestIdSequence uint64 = 0

// var requests = make(map[RequestID]Request)
// var requestsMutex sync.Mutex

var retsm = &requestsModule{
	nodes: make(map[string]NodeID),
	// inBuffer:   make(chan Request, 20),
	// outBuffer:  make(chan Response, 20),
	idSequence: 0,
	requests:   make(map[RequestID]Request),
}

// func (m *requestsModule) WriteRequest(ctx context.Context, req Request) (Response, error) {
// 	var resp Response
// 	requestId := m.putRequest(req)
// 	nodeId := m.putNodeId(req)
// 	h := req.Hdr()
// 	h.ID = requestId
// 	h.Node = nodeId

// 	m.inBuffer <- req
// 	for {
// 		resp = <-m.outBuffer
// 		id := resp.GetId()
// 		if RequestID(id) == requestId {
// 			break
// 		}
// 		m.outBuffer <- resp
// 	}

// 	return resp, nil
// }

// func (m *requestModule) WriteRespond(fd *os.File, resp Response) error {

// 	switch r := (resp).(type) {
// 	case *OpenResponse:
// 		a := r.makeAnswer()
// 		directivem.WriteAnswer(fd, a)
// 		// // case *CreateFileAnswer:
// 		// 	// ri := getRequest(resp.Hdr().ID)
// 		// 	// req := ri.(*CreateFileRequest)
// 		// 	if err != nil {
// 		// 		// return emptyFile{}, dokan.CreateStatus(dokan.ErrNotSupported), err
// 		// 		return emptyFile{}, r.CreateStatus, err
// 		// 	}

// 		// 	if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
// 		// 		cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

// 		// 		return emptyFile{}, dokan.CreateStatus(dokan.ErrAccessDenied), nil
// 		// 	}

// 		// 	return emptyFile{}, dokan.ExistingDir, nil

// 		// 	f := emptyFile{}

// 		// 	saveHandle(r.Handle, f)

// 		// case *FindFilesResponse:

// 		// 	f := emptyFile{}

// 		// 	saveHandle(r.Handle, f)
// 	}

// 	// respBuffer <- resp

// 	return nil
// }

type requestResponseModule struct{}

var Requests = &requestResponseModule{}

func (rrm *requestResponseModule) ReadRequest(fd *os.File) (Request, error) {

	select {
	// case r := <-retsm.inBuffer:
	// 	return r, nil
	case d := <-diesm.inBuffer:
		r := d.makeRequest()
		return r, nil
	}

	return nil, nil
}

func (rrm *requestResponseModule) WriteRespond(fd *os.File, resp Response) error {

	a := resp.makeAnswer()

	switch r := (resp).(type) {
	case *OpenResponse:
		// Need to get the request to know if it's open or opendir?
		// fuse_operations::opendir

		// fuse_operations::open
		if r.Handle != 0 {
			f := emptyFile{}
			saveHandle(r.Handle, f)
		}

		// // case *CreateFileAnswer:
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
	}
	diesm.PostAnswer(fd, a)

	// respBuffer <- resp

	return nil
}

func (m *requestsModule) putRequest(req Request) RequestID {
	rid := atomic.AddUint64(&(m.idSequence), 1)
	id := RequestID(rid)
	m.requestsMutex.Lock()
	defer m.requestsMutex.Unlock()
	m.requests[id] = req
	return id
}

func (m *requestsModule) getRequest(id RequestID) Request {
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
	makeAnswer() Answer
}

// A Header describes the basic information sent in every request.
type DirectiveHeader struct {
	ID       DirectiveID     // unique ID for request
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

	makeRequest() Request
}

func mkHeaderFromResponse(response Response) Header {
	id := response.GetId()
	// r := requestm.getRequest(RequestID(id))
	d := diesm.getDirective(DirectiveID(id))
	h := d.Hdr()
	node := supplyNodeIdWithFileInfo(h.FileInfo)
	return Header{
		ID:   RequestID(h.ID),
		Node: NodeID(node),
		// Uid:  uint32(h.ID),
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
}

type RequestHeaderInfo interface{}

func mapDirectiveType(
	directive *Directive,
	fileInfoCallBack func(directive *Directive, fi *dokan.FileInfo),
) {

	switch d := (*directive).(type) {
	case *CreateFileDirective:
		fileInfoCallBack(directive, d.FileInfo)
	case *FindFilesDirective:
		fileInfoCallBack(directive, d.FileInfo)
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

func supplyNodeIdWithFileInfo(fi *dokan.FileInfo) NodeID {
	var nid *NodeID = nil
	path := fi.Path()

	retsm.nodesMutex.Lock()
	defer retsm.nodesMutex.Unlock()
	nodeId, ok := retsm.nodes[path]
	if !ok {
		nodeId = NodeID(len(retsm.nodes) + 1)
		retsm.nodes[path] = nodeId
	}
	nid = &nodeId

	if nid == nil {
		panic("No value for NodeID.")
	}
	return *nid
}

func (m *requestsModule) putNodeId(req Request) NodeID {
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
	// // openIn
	// Flags  uint32
	// Unused uint32
}

var _ Directive = (*CreateFileDirective)(nil)

func (d *CreateFileDirective) putHdr(header *DirectiveHeader) { d.hdr = header }
func (d *CreateFileDirective) Hdr() *DirectiveHeader          { return d.hdr }
func (d *CreateFileDirective) RespondError(error)             {}
func (d *CreateFileDirective) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", d.FileInfo.Path())
}

func (d *CreateFileDirective) IsDirectiveType() {}

func (d *CreateFileDirective) makeRequest() Request {
	var result Request
	fmt.Printf("CreateFileDirective.makeRequest %v", d)
	// fuse_operations::mknod
	// fuse_operations::create
	// fuse_operations::open
	// fuse_operations::mkdir
	// fuse_operations::opendir
	cd := d.CreateData
	fmt.Printf("FileInfo %v\n", d.FileInfo)
	fmt.Printf("FileInfo Path %v\n", d.FileInfo.Path())
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
		// fuse_operations::opendir

		req := &OpenRequest{
			Header: mkHeaderFromDirective(d),
			// Dir:    m.hdr.Opcode == opOpendir,
			Dir: cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory,
			// Flags:  openFlags(in.Flags),
			Flags: OpenReadOnly,
		}

		result = req
	} else if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
		// fuse_operations::open
		path := d.FileInfo.Path()
		isDir := len(path) != 0 && path[len(path)-1] == filepath.Separator
		// r.FileInfo = req.FileInfo
		// r.CreateData = req.CreateData
		req := &OpenRequest{
			Header:    mkHeaderFromDirective(d),
			Dir:       isDir,
			Flags:     OpenDirectory,
			OpenFlags: OpenRequestFlags(0),
		}

		result = req
	}

	return result
}

type CreateFileAnswer struct {
	Header
	dokan.File
	dokan.CreateStatus
	//error
}

var _ Answer = (*CreateFileAnswer)(nil)

func (r *CreateFileAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *CreateFileAnswer) IsAnswerType() {}

type FindFilesDirective struct {
	hdr              *DirectiveHeader
	FileInfo         *dokan.FileInfo
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
	// // readIn
	// Fh        uint64
	// Offset    uint64
	// Size      uint32
	// ReadFlags uint32
	// LockOwner uint64
	// Flags     uint32
	// _         uint32
}

var _ Directive = (*FindFilesDirective)(nil)

func (r *FindFilesDirective) putHdr(header *DirectiveHeader) { r.hdr = header }
func (r *FindFilesDirective) Hdr() *DirectiveHeader          { return r.hdr }
func (r *FindFilesDirective) RespondError(error)             {}
func (r *FindFilesDirective) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}
func (d *FindFilesDirective) IsDirectiveType() {}
func (d *FindFilesDirective) makeRequest() Request {
	// fuse_operations::readdir
	req := &ReadRequest{
		Header: mkHeaderFromDirective(d),
		Dir:    true,
		// Handle: ,
		Offset: 0,
		Size:   0,
		// Flags:  OpenReadOnly,
		FileFlags: OpenReadOnly,
	}

	return req
}

type FindFilesAnswer struct{ Header }

var _ Answer = (*FindFilesAnswer)(nil)

func (r *FindFilesAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *FindFilesAnswer) IsAnswerType() {}

func convertToDirective() {
}
