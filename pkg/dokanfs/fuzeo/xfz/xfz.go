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

func (m *directivesModule) putDirective(directive Directive) DirectiveID {
	did := atomic.AddUint64(&(m.idSequence), 1)
	id := DirectiveID(did)
	m.directivesMutex.Lock()
	defer m.directivesMutex.Unlock()
	m.directives[id] = directive
	return id
}

func (m *directivesModule) getDirective(id DirectiveID) Directive {
	m.directivesMutex.Lock()
	defer m.directivesMutex.Unlock()
	return m.directives[id]
}

func (m *directivesModule) PostDirective(ctx context.Context, directive Directive) (Answer, error) {
	id := m.putDirective(directive)
	directive.putDirectiveId(id)
	m.inBuffer <- directive
	var resp Answer
	for {
		time.Sleep(20 * time.Millisecond)
		//TODO(ORC): How long should we wait/loop
		// and what error should be produced.
		resp = <-m.outBuffer
		h := resp.Hdr()
		if h.id == id {
			break
		}
		m.outBuffer <- resp
	}

	return resp, nil
}

func (m *directivesModule) PostAnswer(fd *os.File, answer Answer) error {

	m.outBuffer <- answer

	return nil
}

type HandleSet = map[HandleID]struct{}

var haetsm HandleSet
var handleSetDefaultEntry = struct{}{}

// func (s *HandleSet) default() struct{} {
// 	return handleSetDefault
// }

type requestsModule struct {
	idSequence    uint64
	requests      map[RequestID]DirectiveID
	requestsMutex sync.Mutex

	nodes      map[string]NodeID
	nodesMutex sync.Mutex

	handlesMutex sync.Mutex
	handles      map[HandleID]dokan.File
	files        map[dokan.File]HandleID
	filePaths    map[string]HandleSet
}

var retsm = &requestsModule{
	requests:  make(map[RequestID]DirectiveID),
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

func (m *requestsModule) saveRequestId(rid RequestID, id DirectiveID) {
	m.requestsMutex.Lock()
	defer m.requestsMutex.Unlock()
	m.requests[rid] = id
}

func (m *requestsModule) getDirectiveId(rid RequestID) DirectiveID {
	m.requestsMutex.Lock()
	defer m.requestsMutex.Unlock()

	return m.requests[rid]
}

func (m *requestsModule) requestId() RequestID {
	rid := atomic.AddUint64(&(m.idSequence), 1)
	id := RequestID(rid)
	return id
}

func makeHeaderWithDirective(directive Directive) Header {
	h := directive.Hdr()
	node := supplyNodeIdWithFileInfo(h.fileInfo)
	rid := retsm.requestId()
	retsm.saveRequestId(rid, h.id)
	return Header{
		ID:   RequestID(rid),
		Node: NodeID(node),
		// Uid:  uint32(h.ID),
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
}

const maxRead = 128 * 1024

// #region Workflows / Processes

type processArg interface {
	isProcessArg()
}

type processState = uint8

const (
	processStatePending = iota
	processStateSettled
)

type process[Tdata any] struct {
	init       func(processArg) Tdata
	putData    func(Tdata)
	getData    func() Tdata
	getState   func() processState
	updateWith func(processArg)
}

type processor[Tdata any] struct {
	process *process[Tdata]
}

func (p *processor[Tdata]) init(arg processArg) {
	data := p.process.init(arg)
	p.process.putData(data)
}

func (p *processor[Tdata]) step(arg processArg) {
	p.process.updateWith(arg)
}
func (p *processor[Tdata]) fetch() Tdata {
	return p.process.getData()
}
func (p *processor[Tdata]) state() processState {
	return p.process.getState()
}

type findFilesProcessData struct {
	processState     processState
	directive        *FindFilesDirective
	readRequest      *ReadRequest
	readResponse     *ReadResponse
	getattrRequests  []*GetattrRequest
	getattrResponses []*GetattrResponse
}

// Information on Windows API map to FUSE API > Step 4: Implementing FUSE Core
// https://winfsp.dev/doc/SSHFS-Port-Case-Study/
// https://github.com/dokan-dev/dokany/wiki/FUSE

// FindFiles maps to readdir, and getattr per file/directory.
func makefindFilesCompound() *findFilesCompound {
	type data = findFilesProcessData

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

	type phase struct {
		kind    string
		readdir *readdir
		getattr *getattr
	}

	// Workflow
	update := func(phase phase, data data) (updated data) {
		updated = data
		switch phase.kind {
		case "readdir":
			switch phase.readdir.kind {
			case "request":
				fmt.Printf("findFilesProcess readdir (req): %v\n", phase.readdir.req)
				updated.readRequest = phase.readdir.req
			case "response":
				fmt.Printf("findFilesProcess readdir (resp): %v\n", phase.readdir.resp)
				updated.readResponse = phase.readdir.resp
			}
		case "getattr":
			switch phase.getattr.kind {
			case "request":
				fmt.Printf("findFilesProcess readdir (req): %v\n", phase.getattr.req)
				updated.getattrRequests = append(updated.getattrRequests, phase.getattr.req)
			case "response":
				fmt.Printf("findFilesProcess readdir (resp): %v\n", phase.getattr.resp)
				updated.getattrResponses = append(updated.getattrResponses, phase.getattr.resp)
				if len(data.getattrResponses) == len(data.readResponse.Entries) {
					updated.processState = processStateSettled
				}
			}
		}
		return
	}

	init := func(arg processArg) (_data *data) {
		switch arg := arg.(type) {
		case *FindFilesDirective:
			_data = &data{
				processState: processStatePending,
				directive:    arg,
			}
		}
		return
	}

	phaseWith := func(arg processArg) (_phase phase) {
		switch arg := arg.(type) {
		case *ReadRequest:
			_phase = phase{
				kind: "readdir",
				readdir: &readdir{
					kind: "request",
					req:  arg,
				},
			}
		case *ReadResponse:
			_phase = phase{
				kind: "readdir",
				readdir: &readdir{
					kind: "response",
					resp: arg,
				},
			}
		case *GetattrRequest:
			_phase = phase{
				kind: "getattr",
				getattr: &getattr{
					kind: "response",
					req:  arg,
				},
			}
		case *GetattrResponse:
			_phase = phase{
				kind: "getattr",
				getattr: &getattr{
					kind: "response",
					resp: arg,
				},
			}
		}
		return
	}

	var _data *data
	putData := func(data *data) {
		_data = data
	}
	getData := func() *data {
		return _data
	}

	updateWith := func(arg processArg) {
		phase := phaseWith(arg)
		updated := update(phase, *getData())
		putData(&updated)
	}

	getState := func() processState {
		return _data.processState
	}

	findFilesProcess := &process[*data]{
		init:       init,
		updateWith: updateWith,
		putData:    putData,
		getData:    getData,
		getState:   getState,
	}

	findFilesProcessor := &processor[*data]{
		process: findFilesProcess,
	}

	return &findFilesCompound{
		processor: findFilesProcessor,
	}
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
		if d.compound == nil {
			return nil, fmt.Errorf("FindFilesDirective has no compound")
		}
		// fuse_operations::readdir
		debug("ReadRequest fuse_operations::readdir")
		file := d.file.(emptyFile)
		handle := file.handle
		readRequest := &ReadRequest{
			Header:    makeHeaderWithDirective(d),
			Dir:       true,
			Handle:    handle,
			Offset:    0,
			Size:      maxRead,
			Flags:     0,
			FileFlags: OpenReadOnly,
		}
		d.compound.putReadRequest(readRequest)
		req = readRequest
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
			directiveHeader: mkAnswerHeader(r),
			File:            file,
			CreateStatus:    createStatus,
		}
		answer = a
		// dokan.CreateStatus(dokan.ErrAccessDenied)

		if r.Handle != 0 {
			retsm.saveHandle(r.Handle, d.fileInfo.Path(), a.File)
		}

	case *GetattrResponse:
		debugf("WriteRespond fuse_operations::getattr")
		rid := RequestID(r.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		switch d := directive.(type) {
		case *FindFilesDirective:
			if d.compound == nil {
				return fmt.Errorf("FindFilesDirective has no compound")
			}
			// p := FindFilesProduct{}
			// arg:= {directive: d}
			// Products.perform(arg, p)
			d.compound.putGetattrResponse(r)
			if !d.compound.isComplete() {
				// No complete answer to post.
				return nil
			}
			readResp := d.compound.getReadResponse()
			a := &FindFilesAnswer{
				directiveHeader: mkAnswerHeader(readResp),
				Items:           make([]dokan.NamedStat, len(readResp.Entries)),
			}
			answer = a
			namedStat := dokan.NamedStat{
				Name: "",
				Stat: dokan.Stat{},
			}
			for _, it := range readResp.Entries {
				attrResp := d.compound.getGetattrResponseByInode(it.Inode)
				if attrResp == nil {
					continue
				}
				namedStat.Name = it.Name
				if it.Type&DT_File == DT_File {
					namedStat.Stat.FileAttributes = dokan.FileAttributeNormal
				} else if it.Type&DT_Dir == DT_Dir {
					namedStat.Stat.FileAttributes = dokan.FileAttributeDirectory
				}
				namedStat.Stat.Creation = attrResp.Attr.Crtime
				namedStat.Stat.LastAccess = attrResp.Attr.Atime
				namedStat.Stat.LastWrite = attrResp.Attr.Mtime
				namedStat.Stat.FileSize = int64(attrResp.Attr.Size)
				namedStat.Stat.FileIndex = attrResp.Attr.Inode
				namedStat.Stat.FileAttributes = mkFileAttributesWithAttr(r.Attr)
				a.Items = append(a.Items, namedStat)
			}
		case *GetFileInformationDirective:
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
			a := &GetFileInformationAnswer{
				directiveHeader: mkAnswerHeader(r),
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
		}
	case *ReadResponse:
		rid := RequestID(r.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		switch d := directive.(type) {
		case *FindFilesDirective:
			if d.compound == nil {
				return fmt.Errorf("FindFilesDirective has no compound")
			}
			// p := FindFilesProduct{}
			// arg:= {directive: d}
			// Products.perform(arg, p)

			d.compound.putReadResponse(r)

			if !d.compound.isComplete() {
				// No complete answer to post.
				return nil
			}

			debugf("WriteRespond fuse_operations::readdir")
			a := &FindFilesAnswer{
				directiveHeader: mkAnswerHeader(r),
				Items:           make([]dokan.NamedStat, len(r.Entries)),
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
	Hdr() *directiveHeader
}

type ResponseHeader struct {
	Id RequestID
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

type compound interface {
	isCompound()
}

type Directive interface {
	putDirectiveId(id DirectiveID)
	// Hdr returns the Header associated with this request.
	Hdr() *directiveHeader

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string

	IsDirectiveType()

	// compound() compound
}

func mkAnswerHeader(response Response) directiveHeader {
	rid := RequestID(response.GetId())
	id := retsm.getDirectiveId(rid)
	d := diesm.getDirective(id)
	h := d.Hdr()
	return directiveHeader{
		id:       h.id,
		fileInfo: h.fileInfo,
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
	directiveHeader
	dokan.File
	dokan.CreateStatus
	//error
}

var _ Answer = (*CreateFileAnswer)(nil)

func (r *CreateFileAnswer) Hdr() *directiveHeader { return &r.directiveHeader }
func (r *CreateFileAnswer) IsAnswerType()         {}

type findFilesCompound struct {
	processor *processor[*findFilesProcessData]
}

func (c *findFilesCompound) putReadRequest(req *ReadRequest) {
	c.processor.step(req)
}

func (c *findFilesCompound) getReadRequest() *ReadRequest {
	state := c.processor.fetch()
	return state.readRequest
}

func (c *findFilesCompound) putReadResponse(resp *ReadResponse) {
	c.processor.step(resp)
}
func (c *findFilesCompound) getReadResponse() *ReadResponse {
	state := c.processor.fetch()
	return state.readResponse
}

func (c *findFilesCompound) putGetattrResponse(resp *GetattrResponse) {
	c.processor.step(resp)
}

func (c *findFilesCompound) getGetattrResponses() []*GetattrResponse {
	state := c.processor.fetch()
	return state.getattrResponses
}

func (c *findFilesCompound) getGetattrResponseByInode(inode uint64) *GetattrResponse {
	state := c.processor.fetch()
	for _, resp := range state.getattrResponses {
		if resp.Attr.Inode == inode {
			return resp
		}
	}
	return nil
}

func (c *findFilesCompound) isComplete() bool {
	return c.processor.state() == processStateSettled
}

func (c *findFilesCompound) isCompound() {}

var _ compound = (*findFilesCompound)(nil)

type FindFilesDirective struct {
	directiveHeader
	file             dokan.File
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
	compound         *findFilesCompound
}

var _ Directive = (*FindFilesDirective)(nil)

func (d *FindFilesDirective) putDirectiveId(id DirectiveID) {
	d.id = id
	d.compound.processor.init(d)
}
func (d *FindFilesDirective) RespondError(error) {}
func (d *FindFilesDirective) String() string {
	f := d.file.(emptyFile)
	return fmt.Sprintf("FindFilesDirective [%s] file.handle=%v", d.Hdr(), f.handle)
}
func (d *FindFilesDirective) IsDirectiveType()      {}
func (d *FindFilesDirective) getCompound() compound { return d.compound }
func (d *FindFilesDirective) isProcessArg()         {}

type FindFilesAnswer struct {
	directiveHeader
	Items []dokan.NamedStat
}

var _ Answer = (*FindFilesAnswer)(nil)

func (r *FindFilesAnswer) Hdr() *directiveHeader { return &r.directiveHeader }
func (r *FindFilesAnswer) IsAnswerType()         {}

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
	directiveHeader
	Stat *dokan.Stat
}

var _ Answer = (*GetFileInformationAnswer)(nil)

func (r *GetFileInformationAnswer) Hdr() *directiveHeader { return &r.directiveHeader }
func (r *GetFileInformationAnswer) IsAnswerType()         {}
