package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"container/ring"
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

type postDirectiveWithArg struct {
	ctx               context.Context
	directive         Directive
	initWithDirective func(directive Directive) DirectiveID
	sync              bool
}

func (m *directivesModule) postDirectiveWith(arg postDirectiveWithArg) (Answer, error) {
	id := arg.initWithDirective(arg.directive)
	directive := arg.directive

	m.inBuffer <- directive
	if arg.sync == false {
		return nil, nil
	}
	var resp Answer
	for {
		time.Sleep(20 * time.Millisecond)
		//TODO(ORC): How long should we wait/loop
		// and what error should be produced.
		if m == nil {
			debug(m)
		}
		resp = <-m.outBuffer
		if resp == nil {
			debug(m)
		}
		h := resp.Hdr()
		if h == nil {
			debug(m)
		}
		if h.id == id {
			break
		}
		m.outBuffer <- resp
	}

	return resp, nil
}

func (m *directivesModule) initWithDirective(directive Directive) DirectiveID {
	id := m.putDirective(directive)
	directive.putDirectiveId(id)
	directive.initProcessor()
	return id
}

func (m *directivesModule) PostDirective(ctx context.Context, directive Directive) (Answer, error) {
	arg := postDirectiveWithArg{
		ctx:               ctx,
		directive:         directive,
		initWithDirective: m.initWithDirective,
		sync:              true}
	return m.postDirectiveWith(arg)
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

func makeHeaderWith(node NodeID, directive Directive) Header {
	rid := retsm.requestId()
	pid := directive.principalDirectiveId()
	retsm.saveRequestId(rid, pid)
	return Header{
		ID:   RequestID(rid),
		Node: node,
		// Uid:  uint32(h.ID),
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
}
func makeHeaderWithDirective(directive Directive) Header {
	h := directive.Hdr()
	return makeHeaderWith(h.node, directive)
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

type Dispatch = func(note any)

type Effect = func(dispatch Dispatch)

type Cmd = []Effect

func execCmd(dispatch Dispatch, cmd Cmd) {
	if cmd == nil {
		return
	}
	for _, call := range cmd {
		call(dispatch)
	}
}

type process[Tdata any] struct {
	init       func(processArg) (Tdata, Cmd)
	putData    func(Tdata)
	getData    func() Tdata
	getState   func() processState
	update     func(note any, data Tdata) (Tdata, Cmd)
	updateWith func(processArg)
}

type processor[Tdata any] struct {
	process *process[Tdata]
}

type ringBuffer struct {
	ring.Ring
}

func (x ringBuffer) push(v any) {
	it := ring.New(1)
	it.Value = v
	x.Link(it)
}

func (x ringBuffer) pop() any {
	if x.Len() > 0 {
		return x.Unlink(1).Value
	}
	return nil
}

func (p *processor[Tdata]) init(arg processArg) {
	var (
		dispatch    func(note any)
		processNote func()
	)
	rb := ringBuffer{}

	data, cmd := p.process.init(arg)
	reentered := false
	state := data

	dispatch = func(note any) {
		rb.push(note)
		if !reentered {
			reentered = true
			processNote()
			reentered = false
		} else {
			debug(reentered)
		}
	}
	processNote = func() {
		nextNote := rb.pop()
		for {
			if nextNote == nil {
				break
			}
			note := nextNote
			data, cmd := p.process.update(note, state)
			p.process.putData(data)

			execCmd(dispatch, cmd)

			state = data
			nextNote = rb.pop()
		}
	}
	reentered = true
	p.process.putData(data)
	execCmd(dispatch, cmd)
	processNote()
	reentered = false
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

type findFilesNoteKind = int

const (
	findFilesNoteKindReaddirReq  = findFilesNoteKind(0b00010001)
	findFilesNoteKindReaddirResp = findFilesNoteKind(0b00010010)
	findFilesNoteKindOpenReq     = findFilesNoteKind(0b00100001)
	findFilesNoteKindOpenResp    = findFilesNoteKind(0b00100010)
	findFilesNoteKindGetattrReq  = findFilesNoteKind(0b00110001)
	findFilesNoteKindGetattrResp = findFilesNoteKind(0b00110010)
	findFilesNoteKindEnqueuReq   = findFilesNoteKind(0b10000001)
)

type findFilesProcessData struct {
	processState     processState
	directive        *FindFilesDirective
	readRequest      *ReadRequest
	readResponse     *ReadResponse
	openRequests     []*OpenRequest
	openResponses    []*OpenResponse
	getattrRequests  []*GetattrRequest
	getattrResponses []*GetattrResponse
	reqR             *ring.Ring
}

func (x *findFilesProcessData) enqueueReq(req Request) {
	it := ring.New(1)
	it.Value = req
	x.reqR.Link(it)
}

func (x *findFilesProcessData) dequeueReq() Request {
	if x.reqR.Len() > 0 {
		req := x.reqR.Unlink(1).Value.(Request)
		return req
	}
	return nil
}

// type findFilesProcessGetattrReq struct {
// 	directiveHeader
// 	_principalDirectiveId DirectiveID
// }

// var _ Directive = (*findFilesProcessGetattrReq)(nil)

// func (d *findFilesProcessGetattrReq) principalDirectiveId() DirectiveID {
// 	return d._principalDirectiveId
// }
// func (d *findFilesProcessGetattrReq) putDirectiveId(id DirectiveID) {
// 	d.id = id
// }
// func (d *findFilesProcessGetattrReq) String() string {
// 	return fmt.Sprintf("findFilesCompoundGetattrReq [%s]", d.Hdr())
// }
// func (d *findFilesProcessGetattrReq) IsDirectiveType() {}

// Information on Windows API map to FUSE API > Step 4: Implementing FUSE Core
// https://winfsp.dev/doc/SSHFS-Port-Case-Study/
// https://winfsp.dev/doc/Native-API-vs-FUSE/
// https://github.com/dokan-dev/dokany/wiki/FUSE
// https://github.com/winfsp/winfsp/blob/master/doc/WinFsp-Tutorial.asciidoc#readdirectory
// FindFiles maps to readdir, and getattr per file/directory.
func makefindFilesCompound(ctx context.Context) *findFilesCompound {

	type enqueueReq struct {
		req Request
	}

	type phaseReaddir struct {
		req  *ReadRequest
		resp *ReadResponse
	}

	type open struct {
		req  *OpenRequest
		resp *OpenResponse
	}

	type getattr struct {
		req  *GetattrRequest
		resp *GetattrResponse
	}

	const (
		readdirReq   = findFilesNoteKindReaddirReq
		readdirResp  = findFilesNoteKindReaddirResp
		openReq      = findFilesNoteKindOpenReq
		openResp     = findFilesNoteKindOpenResp
		getattrReq   = findFilesNoteKindGetattrReq
		getattrResp  = findFilesNoteKindGetattrResp
		nkEnqueueReq = findFilesNoteKindEnqueuReq
	)

	type note struct {
		kind       findFilesNoteKind
		enqueueReq *enqueueReq
		readdir    *phaseReaddir
		open       *open
		getattr    *getattr
	}

	cmdOfNote := func(n note) Cmd {
		f := func(dispatch Dispatch) {
			dispatch(n)
		}
		return []Effect{f}
	}

	batchCmd := func(cmds []Cmd) Cmd {
		var result Cmd = make([]Effect, len(cmds))
		for _, cmd := range cmds {
			result = append(result, cmd...)
		}
		return result
	}

	init := func(arg processArg) (data *findFilesProcessData, cmd Cmd) {
		switch d := arg.(type) {
		case *FindFilesDirective:
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
			data = &findFilesProcessData{
				processState: processStatePending,
				reqR:         ring.New(0),
				directive:    d,
				readRequest:  readRequest,
			}
			// Enqueue and set ReadRequest
			cmd = batchCmd([]Cmd{
				cmdOfNote(note{kind: nkEnqueueReq,
					enqueueReq: &enqueueReq{req: readRequest},
				}),
				cmdOfNote(note{kind: readdirReq,
					readdir: &phaseReaddir{req: readRequest},
				}),
			})
		}
		return
	}

	noteWith := func(arg processArg) (r note) {
		switch arg := arg.(type) {
		case *ReadRequest:
			r = note{
				kind: findFilesNoteKindReaddirReq,
				readdir: &phaseReaddir{
					req: arg,
				},
			}
		case *ReadResponse:
			r = note{
				kind: findFilesNoteKindReaddirResp,
				readdir: &phaseReaddir{
					resp: arg,
				},
			}
		case *OpenRequest:
			r = note{
				kind: findFilesNoteKindOpenReq,
				open: &open{
					req: arg,
				},
			}
		case *OpenResponse:
			r = note{
				kind: findFilesNoteKindOpenReq,
				open: &open{
					resp: arg,
				},
			}
		case *GetattrRequest:
			r = note{
				kind: findFilesNoteKindGetattrReq,
				getattr: &getattr{
					req: arg,
				},
			}
		case *GetattrResponse:
			r = note{
				kind: findFilesNoteKindGetattrResp,
				getattr: &getattr{
					resp: arg,
				},
			}
		}
		return
	}

	openReqCmd := func(d *FindFilesDirective, dirent Dirent) Cmd {
		fxi := func(dispatch Dispatch) {
			fPath := filepath.Join(d.fileInfo.Path(), dirent.Name)
			isDir := fileInfos.isDir(d.Hdr().fileInfo)
			node := supplyNodeIdWithPath(fPath)
			openReq := &OpenRequest{
				Header:    makeHeaderWith(node, d),
				Dir:       isDir,
				Flags:     OpenDirectory,
				OpenFlags: OpenRequestFlags(0),
			}
			dispatch(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: openReq},
			})
		}
		fxj := func(dispatch Dispatch) {
			arg := postDirectiveWithArg{
				ctx:       ctx,
				directive: d,
				sync:      false,
			}
			diesm.postDirectiveWith(arg)
			dispatch(nil)
		}
		return batchCmd([]Cmd{[]Effect{fxi}, []Effect{fxj}})
	}
	// postOpenReqCmd := func(d *FindFilesDirective, dirent Dirent) Cmd {
	// 	f := func(dispatch Dispatch) {
	// 		fPath := filepath.Join(d.fileInfo.Path(), dirent.Name)
	// 		isDir := fileInfos.isDir(d.Hdr().fileInfo)
	// 		node := supplyNodeIdWithPath(fPath)
	// 		openReq := &OpenRequest{
	// 			Header:    makeHeaderWith(node, d),
	// 			Dir:       isDir,
	// 			Flags:     OpenDirectory,
	// 			OpenFlags: OpenRequestFlags(0),
	// 		}

	// 		di := &findFilesProcessGetattrReq{
	// 			directiveHeader: directiveHeader{
	// 				node: supplyNodeIdWithPath(fPath),
	// 			},
	// 			_principalDirectiveId: d.id,
	// 		}
	// 		arg := postDirectiveWithArg{
	// 			ctx:       ctx,
	// 			directive: d,
	// 			sync:      false,
	// 		}
	// 		argi := postDirectiveWithArg{
	// 			ctx:       ctx,
	// 			directive: di,
	// 			sync:      false,
	// 		}
	// 		diesm.postDirectiveWith(arg)

	// 		dispatch(note{kind: nkEnqueueReq,
	// 			enqueueReq: &enqueueReq{req: openReq},
	// 		})

	// 		// cmd = batchCmd([]Cmd{
	// 		// 	cmdOfNote(note{kind: nkEnqueueReq,
	// 		// 		enqueueReq: &enqueueReq{req: readRequest},
	// 		// 	}),
	// 		// 	cmdOfNote(note{kind: readdirReq,
	// 		// 		readdir: &phaseReaddir{req: readRequest},
	// 		// 	}),
	// 		// })
	// 	}
	// 	return []Effect{f}
	// }

	getattrReqCmd := func(d *FindFilesDirective, dirent Dirent) Cmd {
		fxi := func(dispatch Dispatch) {
			// TODO(OR): Is there a file/handle
			// file := d.file.(emptyFile)
			// handle := file.handle
			req := &GetattrRequest{
				Header: makeHeaderWithDirective(d),
				Handle: 0,
				Flags:  0, // No handle GetattrFh,
			}
			dispatch(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			})
		}
		fxj := func(dispatch Dispatch) {
			arg := postDirectiveWithArg{
				ctx:       ctx,
				directive: d,
				sync:      false,
			}
			diesm.postDirectiveWith(arg)
			dispatch(nil)
		}
		return batchCmd([]Cmd{[]Effect{fxi}, []Effect{fxj}})
	}
	// Amend amendment
	// Workflow
	update := func(note note, data findFilesProcessData) (updated findFilesProcessData, cmd Cmd) {
		updated = data
		cmd = nil
		switch note.kind {
		case nkEnqueueReq:
			fmt.Printf("findFilesProcess enqueueReq: %v\n", note.enqueueReq.req)
			updated.enqueueReq(note.enqueueReq.req)
		case readdirReq:
			fmt.Printf("findFilesProcess readdir (req): %v\n", note.readdir.req)
			updated.readRequest = note.readdir.req
		case readdirResp:
			fmt.Printf("findFilesProcess readdir (resp): %v\n", note.readdir.resp)
			updated.readResponse = note.readdir.resp
			dirent := updated.readResponse.Entries[0]
			cmd = openReqCmd(data.directive, dirent)

		case openReq:
			fmt.Printf("findFilesProcess open (req): %v\n", note.open.req)
			updated.openRequests = append(updated.openRequests, note.open.req)
		case openResp:
			fmt.Printf("findFilesProcess open (resp): %v\n", note.open.resp)
			updated.readResponse = note.readdir.resp
			dirent := updated.readResponse.Entries[0]
			cmd = getattrReqCmd(data.directive, dirent)

			updated.openResponses = append(updated.openResponses, note.open.resp)
			if len(data.openResponses) == len(data.readResponse.Entries) {
				updated.processState = processStateSettled
			} else {
				idx := len(data.openResponses) + 1
				dirent := updated.readResponse.Entries[idx]
				cmd = getattrReqCmd(data.directive, dirent)
			}
		case getattrReq:
			fmt.Printf("findFilesProcess getattr (req): %v\n", note.getattr.req)
			updated.getattrRequests = append(updated.getattrRequests, note.getattr.req)
		case getattrResp:
			fmt.Printf("findFilesProcess getattr (resp): %v\n", note.getattr.resp)
			updated.getattrResponses = append(updated.getattrResponses, note.getattr.resp)
			if len(data.getattrResponses) == len(data.readResponse.Entries) {
				updated.processState = processStateSettled
			} else {
				idx := len(data.getattrResponses) + 1
				dirent := updated.readResponse.Entries[idx]
				cmd = getattrReqCmd(data.directive, dirent)
			}
		}
		return
	}

	var _data *findFilesProcessData
	putData := func(data *findFilesProcessData) {
		_data = data
	}

	getData := func() *findFilesProcessData {
		return _data
	}

	updateWith := func(arg processArg) {
		var (
			dispatch    func(note any)
			processNote func()
		)
		note := noteWith(arg)
		updated, cmd := update(note, *getData())
		putData(&updated)

		rb := ringBuffer{}
		data, cmd := p.process.init(arg)
		reentered := false
		state := data

		dispatch = func(note any) {
			rb.push(note)
			if !reentered {
				reentered = true
				processNote()
				reentered = false
			} else {
				debug(reentered)
			}
		}
		processNote = func() {
			nextNote := rb.pop()
			for {
				if nextNote == nil {
					break
				}
				note := nextNote
				data, cmd := p.process.update(note, state)
				p.process.putData(data)

				execCmd(dispatch, cmd)

				state = data
				nextNote = rb.pop()
			}
		}
		reentered = true
		p.process.putData(data)
		execCmd(dispatch, cmd)
		processNote()
		reentered = false
	}

	getState := func() processState {
		return _data.processState
	}

	update_ := func(an any, data *findFilesProcessData) (*findFilesProcessData, Cmd) {
		n := an.(note)
		updated, cmd := update(n, *data)
		return &updated, cmd
	}

	findFilesProcess := &process[*findFilesProcessData]{
		init:       init,
		updateWith: updateWith,
		update:     update_,
		putData:    putData,
		getData:    getData,
		getState:   getState,
	}

	findFilesProcessor := &processor[*findFilesProcessData]{
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
	//#region Compound related requests
	// case *findFilesProcessOpenReq:
	// 	req = &OpenRequest{
	// 		Header:    makeHeaderWithDirective(d),
	// 		Dir:       isDir,
	// 		Flags:     OpenDirectory,
	// 		OpenFlags: OpenRequestFlags(0),
	// 	}
	// 	err = nil
	// 	return
	// case *findFilesProcessGetattrReq:
	// 	req = &GetattrRequest{
	// 		Header: makeHeaderWithDirective(d),
	// 		Flags:  0, // no handle GetattrFh
	// 	}
	// 	err = nil
	// 	return
	//#endregion
	case *CreateFileDirective:
		// fuse_operations::mknod
		// fuse_operations::create
		// fuse_operations::mkdir
		// fuse_operations::opendir
		// fuse_operations::open
		cd := d.CreateData
		isDir := fileInfos.isDir(d.Hdr().fileInfo)
		if isDir {
			if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
				debugf("ReadRequest fuse_operations::opendir")
				req = &OpenRequest{
					Header:    makeHeaderWithDirective(d),
					Dir:       isDir,
					Flags:     OpenDirectory,
					OpenFlags: OpenRequestFlags(0),
				}
				err = nil
				return
			} else if cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

			}
		} else {
			if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
				// CreateData.DesiredAccess=100100000000010001001
				// CreateData.FileAttributes=0
				// CreateData.ShareAccess=111
				// CreateData.CreateDisposition=1
				// CreateData.CreateOptions=1100100
				debugf("ReadRequest fuse_operations::open")
				req = &OpenRequest{
					Header:    makeHeaderWithDirective(d),
					Dir:       isDir,
					Flags:     OpenDirectory,
					OpenFlags: OpenRequestFlags(0),
				}
				err = nil
				return
			}
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
		req = d.compound.nextRequest()
		if req == nil {
			debug(req)
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
	case *ErrorResponse:
		rid := RequestID(r.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		debugf("WriteRespond ErrorResponse %s \n> %s", r, directive)
		// TODO(OR): Complete directive
		return r.Error
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
			answerHeader: mkAnswerHeader(r),
			File:         file,
			CreateStatus: createStatus,
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
				answerHeader: mkAnswerHeader(readResp),
				Items:        make([]dokan.NamedStat, len(readResp.Entries)),
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
				answerHeader: mkAnswerHeader(r),
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
				answerHeader: mkAnswerHeader(r),
				Items:        make([]dokan.NamedStat, len(r.Entries)),
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

	if answer == nil {
		r := resp
		rid := RequestID(r.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		debug(directive)
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
	Hdr() *answerHeader
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

type ErrorResponse struct {
	ResponseHeader
	Error error
	Errno int32
}

func (r *ErrorResponse) IsResponseType() {}
func (r *ErrorResponse) PutId(id uint64) { r.Id = RequestID(id) }
func (r *ErrorResponse) GetId() uint64   { return uint64(r.Id) }
func (d *ErrorResponse) isProcessArg()   {}

// A Header describes the basic information sent in every request.
type directiveHeader struct {
	id       DirectiveID     // unique ID for directive
	fileInfo *dokan.FileInfo // file or directory the request is about
	node     NodeID          // file or directory the request is about
}

type answerHeader struct {
	id DirectiveID // unique ID for request
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
	principalDirectiveId() DirectiveID
	putDirectiveId(id DirectiveID)
	initProcessor()
	// Hdr returns the Header associated with this directive.
	Hdr() *directiveHeader

	String() string

	IsDirectiveType()
}

func mkAnswerHeader(response Response) answerHeader {
	rid := RequestID(response.GetId())
	id := retsm.getDirectiveId(rid)
	d := diesm.getDirective(id)
	h := d.Hdr()
	return answerHeader{
		id: h.id,
	}
}

func supplyNodeIdWithFileInfo(fi *dokan.FileInfo) NodeID {
	return supplyNodeIdWithPath(fi.Path())
}

func supplyNodeIdWithPath(path string) NodeID {
	var nid *NodeID = nil

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

func (d *CreateFileDirective) principalDirectiveId() DirectiveID {
	return d.id
}
func (d *CreateFileDirective) putDirectiveId(id DirectiveID) { d.id = id }
func (d *CreateFileDirective) initProcessor()                {}

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
	answerHeader
	dokan.File
	dokan.CreateStatus
	//error
}

var _ Answer = (*CreateFileAnswer)(nil)

func (r *CreateFileAnswer) Hdr() *answerHeader { return &r.answerHeader }
func (r *CreateFileAnswer) IsAnswerType()      {}

type findFilesCompound struct {
	processor *processor[*findFilesProcessData]
}

func (c *findFilesCompound) nextRequest() Request {
	state := c.processor.fetch()
	req := state.dequeueReq()
	switch r := req.(type) {
	case *ReadRequest:
		// fuse_operations::readdir
		debug("ReadRequest nextRequest fuse_operations::readdir")
		return r
	}
	return req
}

func (c *findFilesCompound) putRequest(req processArg) {
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

func (d *FindFilesDirective) principalDirectiveId() DirectiveID {
	return d.id
}
func (d *FindFilesDirective) putDirectiveId(id DirectiveID) {
	d.id = id
}
func (d *FindFilesDirective) initProcessor() {
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
	answerHeader
	Items []dokan.NamedStat
}

var _ Answer = (*FindFilesAnswer)(nil)

func (r *FindFilesAnswer) Hdr() *answerHeader { return &r.answerHeader }
func (r *FindFilesAnswer) IsAnswerType()      {}

type GetFileInformationDirective struct {
	directiveHeader
	file dokan.File
}

var _ Directive = (*GetFileInformationDirective)(nil)

func (d *GetFileInformationDirective) principalDirectiveId() DirectiveID {
	return d.id
}
func (r *GetFileInformationDirective) putDirectiveId(id DirectiveID) { r.id = id }
func (d *GetFileInformationDirective) initProcessor()                {}
func (r *GetFileInformationDirective) RespondError(error)            {}
func (r *GetFileInformationDirective) String() string {
	f := r.file.(emptyFile)
	return fmt.Sprintf("GetFileInformationDirective [%s] file.handle=%s", r.Hdr(), f.handle)
}
func (d *GetFileInformationDirective) IsDirectiveType() {}

type GetFileInformationAnswer struct {
	answerHeader
	Stat *dokan.Stat
}

var _ Answer = (*GetFileInformationAnswer)(nil)

func (r *GetFileInformationAnswer) Hdr() *answerHeader { return &r.answerHeader }
func (r *GetFileInformationAnswer) IsAnswerType()      {}

// bazil.org/fuse. option https://github.com/jacobsa/fuse  (No Windows support)
