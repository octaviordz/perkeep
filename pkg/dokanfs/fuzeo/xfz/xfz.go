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
	fileInfos      map[string]*dokan.FileInfo
	fileInfosMutex sync.Mutex

	inBuffer   chan Directive
	outBuffer  chan Answer
	idSequence uint64

	directives      map[DirectiveID]Directive
	directivesMutex sync.Mutex
}

var diesm = &directivesModule{
	fileInfos:  make(map[string]*dokan.FileInfo),
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

func (m *directivesModule) putFileInfo(directive Directive) *dokan.FileInfo {
	supplyFileInfo := func(directive Directive) *dokan.FileInfo {
		fi := directive.Hdr().FileInfo
		path := fi.Path()

		m.fileInfosMutex.Lock()
		defer m.fileInfosMutex.Unlock()
		x, ok := m.fileInfos[path]
		if !ok {
			m.fileInfos[path] = fi
			return fi
		}
		return x
	}
	result := supplyFileInfo(directive)

	if result == nil {
		panic("No value for FileInfo.")
	}
	return result
}

func (m *directivesModule) PostDirective(ctx context.Context, directive Directive) (Answer, error) {
	id := m.putDirective(directive)
	node := m.putFileInfo(directive)

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

	handlesMutex sync.Mutex
	handles      map[HandleID]dokan.File
	files        map[dokan.File]HandleID
	filePaths    map[string][]HandleID
}

var retsm = &requestsModule{
	nodes:   make(map[string]NodeID),
	handles: make(map[HandleID]dokan.File),
}

func (m *requestsModule) saveHandle(handle HandleID, path string, file dokan.File) {
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	m.handles[handle] = file
	m.files[file] = handle
	m.filePaths[path] = append(m.filePaths[path], handle)
}

func (m *requestsModule) getHandleByPath(path string) []HandleID {
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	h, ok := m.filePaths[path]
	if !ok {
		return make([]HandleID, 0)
	}
	return h
}

func (m *requestsModule) getHandleByFile(file dokan.File) HandleID {
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	h, ok := m.files[file]
	if !ok {
		return HandleID(0)
	}
	return h
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

func (rrm *requestResponseModule) ReadRequest(fd *os.File) (req Request, err error) {
	d := <-diesm.inBuffer

	switch d := d.(type) {
	case *GetFileInformationDirective:
		// retsm.saveHandle()
		retsm.getHandleByPath(d.hdr.FileInfo.Path())
		req = &GetattrRequest{
			Header: mkHeaderFromDirective(d),
			// Handle: ,
			Flags: GetattrFh,
		}
		err = nil
		return
	}
	r := d.makeRequest()

	return r, nil
}

func (rrm *requestResponseModule) WriteRespond(fd *os.File, resp Response) error {
	var answer Answer

	switch r := (resp).(type) {
	case *OpenResponse:
		// fuse_operations::opendir
		// fuse_operations::open

		// OpenDirectIO    OpenResponseFlags = 1 << 0 // bypass page cache for this open file
		// OpenKeepCache   OpenResponseFlags = 1 << 1 // don't invalidate the data cache on open
		// OpenNonSeekable OpenResponseFlags = 1 << 2 // mark the file as non-seekable (not supported on FreeBSD)
		// OpenCacheDir    OpenResponseFlags = 1 << 3 // allow caching directory contents
		// Need to get the request to know if it's open or opendir?
		id := r.GetId()
		directive := diesm.getDirective(DirectiveID(id))
		d := directive.(*CreateFileDirective)
		createStatus := dokan.ExistingFile
		if fileInfos.isDir(d.Hdr().FileInfo) {
			createStatus = dokan.ExistingDir
		}
		// DOKAN_OPERATIONS::ZwCreateFile
		a := &CreateFileAnswer{
			Header:       mkHeaderFromResponse(r),
			File:         emptyFile{},
			CreateStatus: createStatus,
		}
		answer = a
		// dokan.CreateStatus(dokan.ErrAccessDenied)

		if r.Handle != 0 {
			retsm.saveHandle(r.Handle, d.Hdr().FileInfo.Path(), a.File)
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
	case *GetattrResponse:
		// fuse_operations::getattr
		// DOKAN_OPERATIONS::GetFileInformation
		// Valid time.Duration // how long Attr can be cached
		// Inode     uint64      // inode number
		// Size      uint64      // size in bytes
		// Blocks    uint64      // size in 512-byte units
		// Atime     time.Time   // time of last access
		// Mtime     time.Time   // time of last modification
		// Ctime     time.Time   // time of last inode change
		// Crtime    time.Time   // time of creation (OS X only)
		// Mode      os.FileMode // file mode
		// Nlink     uint32      // number of links (usually 1)
		// Uid       uint32      // owner uid
		// Gid       uint32      // group gid
		// Rdev      uint32      // device numbers
		// Flags     uint32      // chflags(2) flags (OS X only)
		// BlockSize uint32      // preferred blocksize for filesystem I/O
		a := &GetFileInformationAnswer{
			Header: mkHeaderFromResponse(r),
			Stat: &dokan.Stat{
				Creation:       r.Attr.Crtime,
				LastAccess:     r.Attr.Atime,
				LastWrite:      r.Attr.Mtime,
				FileSize:       int64(r.Attr.Size),
				FileIndex:      r.Attr.Inode,
				FileAttributes: mkFileAttributesWithAttr(r.Attr),
			},
		}
		answer = a
		debug(a)
	}

	return diesm.PostAnswer(fd, answer)
}

// func (m *requestsModule) putRequest(req Request) RequestID {
// 	rid := atomic.AddUint64(&(m.idSequence), 1)
// 	id := RequestID(rid)
// 	m.requestsMutex.Lock()
// 	defer m.requestsMutex.Unlock()
// 	m.requests[id] = req
// 	return id
// }

// func (m *requestsModule) getRequest(id RequestID) Request {
// 	m.requestsMutex.Lock()
// 	defer m.requestsMutex.Unlock()
// 	return m.requests[id]
// }

// answer reaction
type Answer interface {
	IsAnswerType()
	Hdr() *Header
}

type ResponseHeader struct {
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

// func mapDirectiveType(
// 	directive *Directive,
// 	fileInfoCallBack func(directive *Directive, fi *dokan.FileInfo),
// ) {

// 	switch d := (*directive).(type) {
// 	case *CreateFileDirective:
// 		fileInfoCallBack(directive, d.FileInfo)
// 	case *GetFileInformationDirective:
// 		fileInfoCallBack(directive, d.FileInfo)
// 	case *FindFilesDirective:
// 		fileInfoCallBack(directive, d.FileInfo)
// 	}
// }

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

// func (m *requestsModule) putNodeId(req Request) NodeID {
// 	var nid *NodeID = nil
// 	onFileInfo := func(req *Request, fi *dokan.FileInfo) {
// 		path := fi.Path()

// 		m.nodesMutex.Lock()
// 		defer m.nodesMutex.Unlock()
// 		nodeId, ok := m.nodes[path]
// 		if !ok {
// 			nodeId = NodeID(len(m.nodes) + 1)
// 			m.nodes[path] = nodeId
// 		}
// 		nid = &nodeId
// 	}
// 	mapRequestType(&req, onFileInfo)

// 	if nid == nil {
// 		panic("No value for NodeID.")
// 	}
// 	return *nid
// }

type FileInfoModule struct{}

var fileInfos = FileInfoModule{}

func (m *FileInfoModule) isDir(fi *dokan.FileInfo) (isDir bool) {
	path := fi.Path()
	isDir = len(path) != 0 && path[len(path)-1] == filepath.Separator
	return
}

type CreateFileDirective struct {
	hdr        *DirectiveHeader
	CreateData *dokan.CreateData
}

var _ Directive = (*CreateFileDirective)(nil)

func (d *CreateFileDirective) putHdr(header *DirectiveHeader) { d.hdr = header }
func (d *CreateFileDirective) Hdr() *DirectiveHeader          { return d.hdr }
func (d *CreateFileDirective) RespondError(error)             {}
func (d *CreateFileDirective) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", d.Hdr().FileInfo.Path())
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
	fmt.Printf("FileInfo %v\n", d.Hdr().FileInfo)
	fmt.Printf("FileInfo Path %v\n", d.Hdr().FileInfo.Path())
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
		isDir := fileInfos.isDir(d.Hdr().FileInfo)
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

type GetFileInformationDirective struct {
	hdr *DirectiveHeader
}

var _ Directive = (*GetFileInformationDirective)(nil)

func (r *GetFileInformationDirective) putHdr(header *DirectiveHeader) { r.hdr = header }
func (r *GetFileInformationDirective) Hdr() *DirectiveHeader          { return r.hdr }
func (r *GetFileInformationDirective) RespondError(error)             {}
func (r *GetFileInformationDirective) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.Hdr().FileInfo.Path())
}
func (d *GetFileInformationDirective) IsDirectiveType() {}
func (d *GetFileInformationDirective) makeRequest() Request {
	// fuse_operations::getattr
	req := &GetattrRequest{
		Header: mkHeaderFromDirective(d),
		Flags:  GetattrFh,
	}
	return req

}

type GetFileInformationAnswer struct {
	Header
	Stat *dokan.Stat
}

var _ Answer = (*GetFileInformationAnswer)(nil)

func (r *GetFileInformationAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *GetFileInformationAnswer) IsAnswerType() {}
