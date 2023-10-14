package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keybase/client/go/kbfs/dokan"
)

type mountHandleEntry struct {
	Fd  *os.File
	Dir string
	// MountConfig *mountConfig
}

type directivesModule struct {
	inBuffer   chan Directive
	outBuffer  chan Answer
	idSequence uint64

	directives      map[DirectiveID]Directive
	directivesMutex sync.Mutex
}

var diesm = &directivesModule{
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

// func (m *directivesModule) putFileInfo(directive Directive) *dokan.FileInfo {
// 	supplyFileInfo := func(directive Directive) *dokan.FileInfo {
// 		fi := directive.Hdr().FileInfo
// 		path := fi.Path()

// 		m.fileInfosMutex.Lock()
// 		defer m.fileInfosMutex.Unlock()
// 		x, ok := m.fileInfos[path]
// 		if !ok {
// 			m.fileInfos[path] = fi
// 			return fi
// 		}
// 		return x
// 	}
// 	result := supplyFileInfo(directive)

// 	if result == nil {
// 		panic("No value for FileInfo.")
// 	}
// 	return result
// }

func (m *directivesModule) PostDirective(ctx context.Context, directive Directive) (Answer, error) {
	id := m.putDirective(directive)
	// node := m.putFileInfo(directive)

	directive.putDirectiveId(id)
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

type HandleSet = map[HandleID]struct{}

var haetsm HandleSet
var handleSetDefaultEntry = struct{}{}

// func (s *HandleSet) default() struct{} {
// 	return handleSetDefault
// }

type requestsModule struct {
	nodes      map[string]NodeID
	nodesMutex sync.Mutex

	handlesMutex sync.Mutex
	handles      map[HandleID]dokan.File
	files        map[dokan.File]HandleID
	filePaths    map[string]HandleSet
}

var retsm = &requestsModule{
	nodes:     make(map[string]NodeID),
	handles:   make(map[HandleID]dokan.File),
	files:     make(map[dokan.File]HandleID),
	filePaths: make(map[string]HandleSet),
}

func (m *requestsModule) saveHandle(handle HandleID, path string, file dokan.File) {
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	m.handles[handle] = file
	m.files[file] = handle
	set, ok := m.filePaths[path]
	if !ok {
		set = make(map[HandleID]struct{})
	}
	set[handle] = handleSetDefaultEntry
	m.filePaths[path] = set
}

func (m *requestsModule) getHandleByPath(path string) []HandleID {
	result := make([]HandleID, 0)
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	hset, ok := m.filePaths[path]
	if !ok {
		return result
	}
	for h := range hset {
		result = append(result, h)
	}
	return result
}

func (m *requestsModule) getHandleByFile(file emptyFile) HandleID {
	m.handlesMutex.Lock()
	defer m.handlesMutex.Unlock()
	h, ok := m.files[file]
	if !ok {
		return HandleID(0)
	}
	return h
}

//#region Workflows / Products

// Information on Windows API map to FUSE API > Step 4: Implementing FUSE Core
// https://winfsp.dev/doc/SSHFS-Port-Case-Study/
// https://github.com/dokan-dev/dokany/wiki/FUSE
type product[Arg any, Phase any, State any, Cmd any] struct {
	init   func(Arg) (State, Cmd)
	update func(Phase, State) (State, Cmd)
}

func makefindFilesProduct() {
	type Cmd interface{}
	// type Cmd[T any] struct{}

	type State struct {
		directive       *FindFilesDirective
		readRequest     *ReadRequest
		readResponse    *ReadResponse
		getattrRequest  *GetattrRequest
		getattrResponse *GetattrResponse
	}

	type readdir struct {
		kind string
		req  *ReadRequest
		resp *ReadResponse
	}

	type getattr struct {
		kind string
		req  *GetattrRequest
		resp *GetattrResponse
	}

	type Phase struct {
		kind    string
		readdir *readdir
		getattr *getattr
	}
	type initArg struct {
		d *FindFilesDirective
	}

	makeInitCmd := func(arg initArg) Cmd {
		// fuse_operations::readdir
		d := arg.d
		file := d.file.(emptyFile)
		handle := file.handle
		debug("ReadRequest fuse_operations::readdir")
		return Phase{
			kind: "readdir",
			readdir: &readdir{
				kind: "request",
				req: &ReadRequest{
					Header:    makeHeaderWithDirective(d),
					Dir:       true,
					Handle:    handle,
					Offset:    0,
					Size:      maxRead,
					Flags:     0,
					FileFlags: OpenReadOnly,
				},
			},
		}
	}

	// Workflow
	update := func(phase Phase, state State) (updated State, cmd Cmd) {
		switch phase.kind {
		case "readdir":
			switch phase.readdir.kind {
			case "request":
				fmt.Printf("Request readdir: %d\n", phase.readdir.req)
				updated = state
				updated.readRequest = phase.readdir.req
				return
			case "response":
				fmt.Printf("Response readdir: %d\n", phase.readdir.resp)
				updated = state
				updated.readResponse = phase.readdir.resp
				return
			}
		case "getattr":
			switch phase.getattr.kind {
			case "request":
				fmt.Printf("Request readdir: %d\n", phase.getattr.req)
			case "response":
				fmt.Printf("Response readdir: %d\n", phase.getattr.resp)
			}
			// case "complete":
		}
	}

	init := func(arg initArg) (State, Cmd) {
		state := State{
			directive: arg.d,
		}
		cmd := makeInitCmd(arg)
		return state, cmd
	}

	findFilesProduct := product[initArg, Phase, State, Cmd]{
		init:   init,
		update: update,
	}

	return findFilesProduct
}

//#endregion

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
	debugf("## ReadRequest\n > %v\n", d)

	switch d := d.(type) {
	default:
		return nil, fmt.Errorf("not implemented")
	case *CreateFileDirective:
		// fuse_operations::mknod
		// fuse_operations::create
		// fuse_operations::mkdir
		// fuse_operations::opendir
		// fuse_operations::open
		cd := d.CreateData
		if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
			isDir := fileInfos.isDir(d.Hdr().fileInfo)
			if isDir {
				debugf("ReadRequest fuse_operations::opendir")
			} else {
				debugf("ReadRequest fuse_operations::open")
			}
			req = &OpenRequest{
				Header:    makeHeaderWithDirective(d),
				Dir:       isDir,
				Flags:     OpenDirectory,
				OpenFlags: OpenRequestFlags(0),
			}
			err = nil
			return
		}
		err = fmt.Errorf("not implemented")
		return
	case *GetFileInformationDirective:
		handles := retsm.getHandleByPath(d.fileInfo.Path())
		if len(handles) <= 0 {
			debugf("ReadRequest len(handles) <= 0; %v", handles)
		} else if len(handles) > 1 {
			debugf("ReadRequest len(handles) > 1; %v", handles)
		}
		file := d.file.(emptyFile)
		handle := file.handle
		debug("ReadRequest fuse_operations::getattr")
		req = &GetattrRequest{
			Header: makeHeaderWithDirective(d),
			Handle: handle,
			Flags:  GetattrFh,
		}
		err = nil
		return
	case *FindFilesDirective:

		// p := FindFilesProduct{}
		// arg:= {directive: d}
		// Products.perform(arg, p)

		// fuse_operations::readdir
		file := d.file.(emptyFile)
		handle := file.handle
		debug("ReadRequest fuse_operations::readdir")
		req = &ReadRequest{
			Header:    makeHeaderWithDirective(d),
			Dir:       true,
			Handle:    handle,
			Offset:    0,
			Size:      maxRead,
			Flags:     0,
			FileFlags: OpenReadOnly,
		}
		return
	}
}

func makeFileWithHandle(handle HandleID) emptyFile {
	return emptyFile{handle: handle}
}

func (rrm *requestResponseModule) WriteRespond(fd *os.File, resp Response) error {
	var answer Answer

	debugf("## WriteRespond\n > %v\n", resp)

	switch r := (resp).(type) {
	case *OpenResponse:
		// fuse_operations::mknod	DOKAN_OPERATIONS::ZwCreateFile
		// fuse_operations::create	DOKAN_OPERATIONS::ZwCreateFile
		// fuse_operations::open	DOKAN_OPERATIONS::ZwCreateFile
		// fuse_operations::mkdir	DOKAN_OPERATIONS::ZwCreateFile
		// fuse_operations::opendir	DOKAN_OPERATIONS::ZwCreateFile

		// OpenDirectIO    OpenResponseFlags = 1 << 0 // bypass page cache for this open file
		// OpenKeepCache   OpenResponseFlags = 1 << 1 // don't invalidate the data cache on open
		// OpenNonSeekable OpenResponseFlags = 1 << 2 // mark the file as non-seekable (not supported on FreeBSD)
		// OpenCacheDir    OpenResponseFlags = 1 << 3 // allow caching directory contents
		// Need to get the request to know if it's open or opendir?
		id := r.GetId()
		directive := diesm.getDirective(DirectiveID(id))
		d := directive.(*CreateFileDirective)
		createStatus := dokan.ExistingFile
		isDir := fileInfos.isDir(d.fileInfo)
		if isDir {
			debugf("WriteRespond fuse_operations::opendir")
			createStatus = dokan.ExistingDir
		} else {
			debugf("WriteRespond fuse_operations::open")
		}
		file := makeFileWithHandle(r.Handle)

		a := &CreateFileAnswer{
			Header:       mkHeaderFromResponse(r),
			File:         file,
			CreateStatus: createStatus,
		}
		answer = a
		// dokan.CreateStatus(dokan.ErrAccessDenied)

		if r.Handle != 0 {
			retsm.saveHandle(r.Handle, d.fileInfo.Path(), a.File)
		}

	case *GetattrResponse:
		// fuse_operations::getattr 	DOKAN_OPERATIONS::GetFileInformation
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
		debugf("WriteRespond fuse_operations::getattr")
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
	case *ReadResponse:
		// fuse_operations::readdir 	DOKAN_OPERATIONS::FindFiles
		debugf("WriteRespond fuse_operations::readdir")
		a := &FindFilesAnswer{
			Header: mkHeaderFromResponse(r),
			Items:  make([]dokan.NamedStat, len(r.Entries)),
		}
		answer = a
		namedStat := dokan.NamedStat{
			Name:      "",
			ShortName: "",
			Stat: dokan.Stat{
				Creation:   time.Now(),
				LastAccess: time.Now(),
				LastWrite:  time.Now(),
				FileSize:   0,
			},
		}
		for _, it := range r.Entries {
			namedStat.Name = it.Name
			if it.Type&DT_File == DT_File {
				namedStat.Stat.FileAttributes = dokan.FileAttributeNormal
			} else if it.Type&DT_Dir == DT_Dir {
				namedStat.Stat.FileAttributes = dokan.FileAttributeDirectory
			}
			a.Items = append(a.Items, namedStat)
		}
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

func (h *ResponseHeader) String() string {
	return fmt.Sprintf("Id=%v", h.Id)
}

type Response interface {
	IsResponseType()
	PutId(uint64)
	GetId() uint64
	// Hdr returns the Header associated with this request.
	// Hdr() *Header

	// RespondError responds to the request with the given error.
	// RespondError(error)

	String() string
}

// A Header describes the basic information sent in every request.
type directiveHeader struct {
	id       DirectiveID     // unique ID for request
	fileInfo *dokan.FileInfo // file or directory the request is about
}

func (h *directiveHeader) Hdr() *directiveHeader {
	return h
}

func (h *directiveHeader) String() string {
	return fmt.Sprintf("Id=%v FileInfo.Path=%v FileInfo.NumberOfFileHandles=%v", h.id, h.fileInfo.Path(), h.fileInfo.NumberOfFileHandles())
}

type Directive interface {
	putDirectiveId(id DirectiveID)
	// Hdr returns the Header associated with this request.
	Hdr() *directiveHeader

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	IsDirectiveType()
}

func mkHeaderFromResponse(response Response) Header {
	id := response.GetId()
	d := diesm.getDirective(DirectiveID(id))
	h := d.Hdr()
	node := supplyNodeIdWithFileInfo(h.fileInfo)
	return Header{
		ID:   RequestID(h.id),
		Node: NodeID(node),
		// Uid:  uint32(h.ID),
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
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
	directiveHeader
	CreateData *dokan.CreateData
}

var _ Directive = (*CreateFileDirective)(nil)

func (d *CreateFileDirective) putDirectiveId(id DirectiveID) { d.id = id }
func (d *CreateFileDirective) RespondError(error)            {}
func (d *CreateFileDirective) String() string {
	return fmt.Sprintf(
		"CreateFileDirective [%s] CreateData.DesiredAccess=%b CreateData.FileAttributes=%b CreateData.ShareAccess=%b CreateData.CreateDisposition=%b CreateData.CreateOptions=%b",
		d.Hdr(),
		d.CreateData.DesiredAccess,
		d.CreateData.FileAttributes,
		d.CreateData.ShareAccess,
		d.CreateData.CreateDisposition,
		d.CreateData.CreateOptions)
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
	fmt.Printf("FileInfo %v\n", d.Hdr().fileInfo)
	fmt.Printf("FileInfo Path %v\n", d.Hdr().fileInfo.Path())
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
			Header: makeHeaderWithDirective(d),
			// Dir:    m.hdr.Opcode == opOpendir,
			Dir: cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory,
			// Flags:  openFlags(in.Flags),
			Flags: OpenReadOnly,
		}

		result = req
	} else if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
		// fuse_operations::open
		isDir := fileInfos.isDir(d.Hdr().fileInfo)
		// r.FileInfo = req.FileInfo
		// r.CreateData = req.CreateData
		req := &OpenRequest{
			Header:    makeHeaderWithDirective(d),
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
	directiveHeader
	file             dokan.File
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
}

var _ Directive = (*FindFilesDirective)(nil)

func (r *FindFilesDirective) putDirectiveId(id DirectiveID) { r.id = id }
func (r *FindFilesDirective) RespondError(error)            {}
func (r *FindFilesDirective) String() string {
	f := r.file.(emptyFile)
	return fmt.Sprintf("FindFilesDirective [%s] file.handle=%v", r.Hdr(), f.handle)
}
func (d *FindFilesDirective) IsDirectiveType() {}

type FindFilesAnswer struct {
	Header
	Items []dokan.NamedStat
}

var _ Answer = (*FindFilesAnswer)(nil)

func (r *FindFilesAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *FindFilesAnswer) IsAnswerType() {}

type GetFileInformationDirective struct {
	directiveHeader
	file dokan.File
}

var _ Directive = (*GetFileInformationDirective)(nil)

func (r *GetFileInformationDirective) putDirectiveId(id DirectiveID) { r.id = id }
func (r *GetFileInformationDirective) RespondError(error)            {}
func (r *GetFileInformationDirective) String() string {
	f := r.file.(emptyFile)
	return fmt.Sprintf("GetFileInformationDirective [%s] file.handle=%s", r.Hdr(), f.handle)
}
func (d *GetFileInformationDirective) IsDirectiveType() {}

type GetFileInformationAnswer struct {
	Header
	Stat *dokan.Stat
}

var _ Answer = (*GetFileInformationAnswer)(nil)

func (r *GetFileInformationAnswer) Hdr() *Header  { return r.Header.Hdr() }
func (r *GetFileInformationAnswer) IsAnswerType() {}
