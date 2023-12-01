package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/keybase/client/go/kbfs/dokan"

	// "perkeep.org/pkg/dokanfs/fuzeo/psor"
	"perkeep.org/pkg/dokanfs/fuzeo/psor"

	"sync"
	"sync/atomic"
	"time"
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

func (m *directivesModule) postDirectiveWith(arg postDirectiveWithArg) (result Answer, err error) {
	id := arg.initWithDirective(arg.directive)
	directive := arg.directive

	m.inBuffer <- directive
	if arg.sync == false {
		return nil, nil
	}

	for {
		time.Sleep(20 * time.Millisecond)
		//TODO(ORC): How long should we wait/loop
		// and what error should be produced.
		if m == nil {
			debug(m)
		}
		resp := <-m.outBuffer
		if resp == nil {
			debug(m)
		}
		h := resp.Hdr()
		if h == nil {
			debug(m)
		}
		if h.id == id {
			switch r := resp.(type) {
			default:
				result = resp
			case *ErrorAnswer:
				err = r.errResp.Error
			}
			break
		}
		m.outBuffer <- resp
	}

	return
}

func (m *directivesModule) noInitWithDirective(directive Directive) DirectiveID {
	return directive.Hdr().id
}

func (m *directivesModule) publishDirective(ctx context.Context, directive Directive) error {
	arg := postDirectiveWithArg{
		ctx:               ctx,
		directive:         directive,
		initWithDirective: m.noInitWithDirective,
		sync:              false}
	_, err := m.postDirectiveWith(arg)
	return err
}

func (m *directivesModule) initWithDirective(directive Directive) DirectiveID {
	id := m.putDirective(directive)
	directive.putDirectiveId(id)
	directive.startProcessor()
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

func filepathSplit(path string) []string {
	parts := strings.Split(path, string(filepath.Separator))
	parts_ := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			parts_ = append(parts_, part)
		}
	}
	return parts_
}

const maxRead = 128 * 1024

// #region Workflows / Processes

type processArg interface {
	isProcessArg()
}

const (
	processStatePending = iota
	processStateSettled
)

func makeCreateFileProcess(ctx context.Context) *psor.Processor[*createFileProcessData] {

	type enqueueReq struct {
		req Request
	}

	type access struct {
		req  *AccessRequest
		resp *AccessResponse
	}

	type open struct {
		isDirent bool
		req      *OpenRequest
		resp     *OpenResponse
	}

	type getattr struct {
		isDirent bool
		req      *GetattrRequest
		resp     *GetattrResponse
	}

	type lookup struct {
		req  *LookupRequest
		resp *LookupResponse
	}

	const (
		getattrReq   = createFileNoteKindGetattrReq
		getattrResp  = createFileNoteKindGetattrResp
		lookupReq    = createFileNoteKindLookupReq
		lookupResp   = createFileNoteKindLookupResp
		accessReq    = createFileNoteKindAccessReq
		accessResp   = createFileNoteKindAccessResp
		openReq      = createFileNoteKindOpenReq
		openResp     = createFileNoteKindOpenResp
		nkEnqueueReq = createFileNoteKindEnqueuReq
	)

	type note struct {
		kind       createFileNoteKind
		enqueueReq *enqueueReq
		getattr    *getattr
		lookup     *lookup
		access     *access
		open       *open
	}

	init := func(arg psor.ProcessArg) (data *createFileProcessData, cmd psor.Cmd) {
		switch d := arg.(type) {
		case *CreateFileDirective:
			node := supplyNodeIdWithPath(d.fileInfo.Path())
			req := &GetattrRequest{
				Header: makeHeaderWith(node, d),
				Handle: HandleID(0),
				Flags:  0,
			}
			data = &createFileProcessData{
				processState: processStatePending,
				directive:    d,
				reqR:         psor.NewRingBuffer(),
			}
			// Set and enqueue request.
			cmd = psor.BatchCmd([]psor.Cmd{
				psor.CmdOfNote(note{kind: getattrReq,
					getattr: &getattr{req: req},
				}),
				psor.CmdOfNote(note{kind: nkEnqueueReq,
					enqueueReq: &enqueueReq{req: req},
				}),
			})
		}
		return
	}

	noteWith := func(arg psor.ProcessArg) (r note) {
		switch arg := arg.(type) {
		case *GetattrRequest:
			r = note{
				kind: createFileNoteKindGetattrReq,
				getattr: &getattr{
					req: arg,
				},
			}
		case *GetattrResponse:
			r = note{
				kind: createFileNoteKindGetattrResp,
				getattr: &getattr{
					resp: arg,
				},
			}
		case *LookupRequest:
			r = note{
				kind: createFileNoteKindLookupReq,
				lookup: &lookup{
					req: arg,
				},
			}
		case *LookupResponse:
			r = note{
				kind: createFileNoteKindLookupResp,
				lookup: &lookup{
					resp: arg,
				},
			}
		case *AccessRequest:
			r = note{
				kind: createFileNoteKindAccessReq,
				access: &access{
					req: arg,
				},
			}
		case *AccessResponse:
			r = note{
				kind: createFileNoteKindAccessResp,
				access: &access{
					resp: arg,
				},
			}
		case *OpenRequest:
			r = note{
				kind: createFileNoteKindOpenReq,
				open: &open{
					req: arg,
				},
			}
		case *OpenResponse:
			r = note{
				kind: createFileNoteKindOpenReq,
				open: &open{
					resp: arg,
				},
			}
		}
		return
	}

	reqReadyCmd := func(data *createFileProcessData) psor.Cmd {
		v := data.reqR.Peek()
		isInit := data.accessRequest == nil || v == data.accessRequest
		fxj := func(dispatch psor.Dispatch) {
			if isInit {
				// Must not publish during Init.
				// Initial request is read by conn.ReadRequest.
				return
			}
			diesm.publishDirective(ctx, data.directive)
		}
		return []psor.Effect{fxj}
	}

	// Lookup for each part/section of path
	lookupReqCmd := func(data *createFileProcessData) psor.Cmd {
		d := data.directive
		idx := len(data.lookupRequests)
		fPath := d.fileInfo.Path()
		parts := filepathSplit(fPath)
		name := parts[idx]
		nPath := filepath.Join(fPath, name)
		node := supplyNodeIdWithPath(nPath)
		req := &LookupRequest{
			Header: makeHeaderWith(node, d),
			Name:   name,
		}
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: lookupReq,
				lookup: &lookup{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	accessReqCmd := func(data *createFileProcessData) psor.Cmd {
		d := data.directive
		fPath := d.fileInfo.Path()
		node := supplyNodeIdWithPath(fPath)
		req := &AccessRequest{
			Header: makeHeaderWith(node, d),
			Mask:   0x1,
		}
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: accessReq,
				access: &access{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	openReqCmd := func(data *createFileProcessData) psor.Cmd {
		d := data.directive
		fPath := d.fileInfo.Path()
		isDir := fileInfos.isDir(fPath)
		node := supplyNodeIdWithPath(fPath)
		req := &OpenRequest{
			Header:    makeHeaderWith(node, d),
			Dir:       isDir,
			Flags:     OpenDirectory,
			OpenFlags: OpenRequestFlags(0),
		}
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: openReq,
				open: &open{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	update := func(note note, data createFileProcessData) (updated createFileProcessData, cmd psor.Cmd) {
		updated = data
		cmd = nil
		switch note.kind {
		case nkEnqueueReq:
			updated.reqR.Push(note.enqueueReq.req)
			cmd = reqReadyCmd(&updated)
		case getattrReq:
			updated.getattrRequest = note.getattr.req
		case getattrResp:
			updated.getattrResponse = note.getattr.resp
			fpath := data.directive.fileInfo.Path()
			parts := filepathSplit(fpath)
			if len(updated.lookupResponses) >= len(parts) {
				cmd = accessReqCmd(&updated)
			} else {
				cmd = lookupReqCmd(&updated)
			}
		case lookupReq:
			updated.lookupRequests = append(updated.lookupRequests, note.lookup.req)
		case lookupResp:
			fpath := data.directive.fileInfo.Path()
			parts := filepathSplit(fpath)
			updated.lookupResponses = append(updated.lookupResponses, note.lookup.resp)
			if len(updated.lookupResponses) >= len(parts) {
				cmd = accessReqCmd(&updated)
			} else {
				cmd = lookupReqCmd(&updated)
			}
		case accessReq:
			updated.accessRequest = note.access.req
		case accessResp:
			updated.accessResponse = note.access.resp
			cmd = openReqCmd(&updated)
		case openReq:
			updated.openRequest = note.open.req
		case openResp:
			updated.openResponse = note.open.resp
			updated.processState = processStateSettled
		}
		return
	}

	var __dispatch psor.Dispatch
	_dispatch := func(note note) {
		if __dispatch == nil {
			return
		}
		__dispatch(note)
	}

	subWith := func() psor.Subscribe {
		start := func(dispatch psor.Dispatch) psor.Unsubscriber {
			__dispatch = dispatch
			unsub := func() {
				__dispatch = nil
			}
			return unsub
		}
		return start
	}

	subscribe := func(data *createFileProcessData) psor.Sub {
		subent := psor.Subent{SubId: 1, Subscribe: subWith()}
		r := []psor.Subent{subent}
		return r
	}

	var _data *createFileProcessData
	putData := func(data *createFileProcessData) {
		_data = data
	}

	getData := func() *createFileProcessData {
		return _data
	}

	updateWith := func(arg psor.ProcessArg) {
		note := noteWith(arg)
		_dispatch(note)
	}

	getProcessState := func() psor.ProcessState {
		return _data.processState
	}

	update_ := func(anote any, data *createFileProcessData) (*createFileProcessData, psor.Cmd) {
		n := anote.(note)
		updated, cmd := update(n, *data)
		return &updated, cmd
	}

	createFileProcess := &psor.Process[*createFileProcessData]{
		Init:            init,
		UpdateWith:      updateWith,
		Update:          update_,
		Subscribe:       subscribe,
		PutData:         putData,
		GetData:         getData,
		GetProcessState: getProcessState,
	}

	return &psor.Processor[*createFileProcessData]{
		Process: createFileProcess,
	}
}

type findFilesNoteKind = int

const (
	findFilesNoteKindRespBitMask = findFilesNoteKind(0b0001_00000000)
	findFilesNoteKindAccessReq   = findFilesNoteKind(opAccess)
	findFilesNoteKindAccessResp  = findFilesNoteKind(findFilesNoteKindRespBitMask | opAccess)
	findFilesNoteKindLookupReq   = findFilesNoteKind(opLookup)
	findFilesNoteKindLookupResp  = findFilesNoteKind(findFilesNoteKindRespBitMask | opLookup)
	findFilesNoteKindReaddirReq  = findFilesNoteKind(opReaddir)
	findFilesNoteKindReaddirResp = findFilesNoteKind(findFilesNoteKindRespBitMask | opReaddir)
	findFilesNoteKindOpenReq     = findFilesNoteKind(opOpen) // opOpendir
	findFilesNoteKindOpenResp    = findFilesNoteKind(findFilesNoteKindRespBitMask | opOpen)
	findFilesNoteKindGetattrReq  = findFilesNoteKind(opGetattr)
	findFilesNoteKindGetattrResp = findFilesNoteKind(findFilesNoteKindRespBitMask | opGetattr)
	findFilesNoteKindReleaseReq  = findFilesNoteKind(opRelease)
	findFilesNoteKindReleaseResp = findFilesNoteKind(findFilesNoteKindRespBitMask | opRelease)
	findFilesNoteKindPush        = findFilesNoteKind(0b1000_1000_0000)
	findFilesNoteKindEnqueuReq   = findFilesNoteKind(0b1000_1111_0000)
)

type findFilesProcessData struct {
	processState        psor.ProcessState
	directive           *FindFilesDirective
	accessRequest       *AccessRequest
	accessResponse      *AccessResponse
	openRequest         *OpenRequest
	openResponse        *OpenResponse
	getattrRequest      *GetattrRequest
	getattrResponse     *GetattrResponse
	readRequest         *ReadRequest
	readResponse        *ReadResponse
	pathLookupRequests  []*LookupRequest
	pathLookupResponses []*LookupResponse
	lookupRequests      []*LookupRequest
	lookupResponses     []*LookupResponse
	getattrRequests     []*GetattrRequest
	getattrResponses    []*GetattrResponse
	reqR                *psor.RingBuffer
}

func (x *findFilesProcessData) dequeueReq() Request {
	v := x.reqR.Pop()
	if v == nil {
		debug(v)
	}
	switch req := v.(type) {
	case Request:
		return req
	}
	return nil
}

// Information on Windows API map to FUSE API > Step 4: Implementing FUSE Core
// https://winfsp.dev/doc/SSHFS-Port-Case-Study/
// https://winfsp.dev/doc/Native-API-vs-FUSE/
// https://github.com/dokan-dev/dokany/wiki/FUSE
// https://github.com/winfsp/winfsp/blob/master/doc/WinFsp-Tutorial.asciidoc#readdirectory
// FindFiles maps to readdir, and getattr per file/directory.
func makefindFilesProcessor(ctx context.Context) *psor.Processor[*findFilesProcessData] {

	type enqueueReq struct {
		req Request
	}

	type access struct {
		req  *AccessRequest
		resp *AccessResponse
	}

	type readdir struct {
		req  *ReadRequest
		resp *ReadResponse
	}

	type open struct {
		isDirent bool
		req      *OpenRequest
		resp     *OpenResponse
	}

	type getattr struct {
		isDirent bool
		req      *GetattrRequest
		resp     *GetattrResponse
	}

	type lookup struct {
		req  *LookupRequest
		resp *LookupResponse
	}

	type release struct {
		req  *ReleaseRequest
		resp *ReleaseResponse
	}

	const (
		accessReq    = findFilesNoteKindAccessReq
		accessResp   = findFilesNoteKindAccessResp
		readdirReq   = findFilesNoteKindReaddirReq
		readdirResp  = findFilesNoteKindReaddirResp
		openReq      = findFilesNoteKindOpenReq
		openResp     = findFilesNoteKindOpenResp
		getattrReq   = findFilesNoteKindGetattrReq
		getattrResp  = findFilesNoteKindGetattrResp
		lookupReq    = findFilesNoteKindLookupReq
		lookupResp   = findFilesNoteKindLookupResp
		releaseReq   = findFilesNoteKindReleaseReq
		releaseResp  = findFilesNoteKindReleaseResp
		nkEnqueueReq = findFilesNoteKindEnqueuReq
	)

	type note struct {
		kind       findFilesNoteKind
		enqueueReq *enqueueReq
		access     *access
		getattr    *getattr
		open       *open
		readdir    *readdir
		lookup     *lookup
		release    *release
	}

	init := func(arg psor.ProcessArg) (data *findFilesProcessData, cmd psor.Cmd) {
		switch d := arg.(type) {
		case *FindFilesDirective:
			fpath := d.fileInfo.Path()
			parts := filepathSplit(fpath)
			dname := parts[0]
			path := string(filepath.Separator)
			node := supplyNodeIdWithPath(path)
			lookupRequest := &LookupRequest{
				Header: makeHeaderWith(node, d),
				Name:   dname,
			}
			data = &findFilesProcessData{
				processState: processStatePending,
				directive:    d,
				reqR:         psor.NewRingBuffer(),
			}
			// Set and enqueue request.
			cmd = psor.BatchCmd([]psor.Cmd{
				psor.CmdOfNote(note{kind: lookupReq,
					lookup: &lookup{req: lookupRequest},
				}),
				psor.CmdOfNote(note{kind: nkEnqueueReq,
					enqueueReq: &enqueueReq{req: lookupRequest},
				}),
			})
		}
		return
	}

	noteWith := func(arg psor.ProcessArg) (r note) {
		switch arg := arg.(type) {
		case *ReadRequest:
			r = note{
				kind: findFilesNoteKindReaddirReq,
				readdir: &readdir{
					req: arg,
				},
			}
		case *ReadResponse:
			r = note{
				kind: findFilesNoteKindReaddirResp,
				readdir: &readdir{
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
				kind: findFilesNoteKindOpenResp,
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
		case *LookupRequest:
			r = note{
				kind: findFilesNoteKindLookupReq,
				lookup: &lookup{
					req: arg,
				},
			}
		case *LookupResponse:
			r = note{
				kind: findFilesNoteKindLookupResp,
				lookup: &lookup{
					resp: arg,
				},
			}
		}
		return
	}

	reqReadyCmd := func(data *findFilesProcessData) psor.Cmd {
		v := data.reqR.Peek()
		isInit := data.accessRequest == nil || v == data.accessRequest
		fxj := func(dispatch psor.Dispatch) {
			if isInit {
				// Must not publish when init.
				// Initial request is read by serve > conn.ReadRequest.
				return
			}
			diesm.publishDirective(ctx, data.directive)
		}
		return []psor.Effect{fxj}
	}

	readdirReqCmd := func(data *findFilesProcessData) psor.Cmd {
		d := data.directive
		fxi := func(dispatch psor.Dispatch) {
			node := supplyNodeIdWithPath(d.fileInfo.Path())
			handle := data.openResponse.Handle
			readRequest := &ReadRequest{
				Header:    makeHeaderWith(node, d),
				Dir:       true,
				Handle:    handle,
				Offset:    0,
				Size:      maxRead,
				Flags:     0,
				FileFlags: OpenReadOnly,
			}
			dispatch(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: readRequest},
			})
		}
		return psor.Cmd{fxi}
	}

	openReqCmd := func(data *findFilesProcessData) psor.Cmd {
		d := data.directive
		fPath := d.fileInfo.Path()
		isDir := fileInfos.isDir(fPath)
		node := supplyNodeIdWithPath(fPath)
		req := &OpenRequest{
			Header:    makeHeaderWith(node, d),
			Dir:       isDir,
			Flags:     OpenDirectory,
			OpenFlags: OpenRequestFlags(0),
		}
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: openReq,
				open: &open{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	// getattrReqRootCmd := func(data *findFilesProcessData) psor.Cmd {
	// 	d := data.directive
	// 	fPath := d.fileInfo.Path()
	// 	node := supplyNodeIdWithPath(fPath)
	// 	req := &GetattrRequest{
	// 		Header: makeHeaderWith(node, d),
	// 		Handle: HandleID(0),
	// 		Flags:  0,
	// 	}
	// 	// Set and enqueue request
	// 	return psor.BatchCmd([]psor.Cmd{
	// 		psor.CmdOfNote(note{kind: getattrReq,
	// 			getattr: &getattr{req: req},
	// 		}),
	// 		psor.CmdOfNote(note{kind: nkEnqueueReq,
	// 			enqueueReq: &enqueueReq{req: req},
	// 		}),
	// 	})
	// }

	pathLookupReqCmd := func(data *findFilesProcessData) psor.Cmd {
		d := data.directive
		fpath := d.fileInfo.Path()
		idx := len(data.pathLookupRequests)
		parts := filepathSplit(fpath)
		dname := parts[idx]
		path := filepath.Join(string(filepath.Separator), filepath.Join(parts[:idx]...))
		node := supplyNodeIdWithPath(path)
		req := &LookupRequest{
			Header: makeHeaderWith(node, d),
			Name:   dname,
		}
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: lookupReq,
				lookup: &lookup{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	getattrReqCmd := func(data *findFilesProcessData) psor.Cmd {
		d := data.directive
		idx := len(data.getattrRequests)
		dirent := data.readResponse.Entries[idx]
		fPath := filepath.Join(d.fileInfo.Path(), dirent.Name)
		node := supplyNodeIdWithPath(fPath)
		req := &GetattrRequest{
			Header: makeHeaderWith(node, d),
			Handle: HandleID(0),
			Flags:  0,
		}
		// req := &GetattrRequest{
		// 	Header: makeHeaderWith(node, d),
		// 	Handle: handle,
		// 	Flags:  GetattrFh,
		// }
		// Set and enqueue request
		return psor.BatchCmd([]psor.Cmd{
			psor.CmdOfNote(note{kind: getattrReq,
				getattr: &getattr{req: req},
			}),
			psor.CmdOfNote(note{kind: nkEnqueueReq,
				enqueueReq: &enqueueReq{req: req},
			}),
		})
	}

	update := func(note note, data findFilesProcessData) (updated findFilesProcessData, cmd psor.Cmd) {
		updated = data
		cmd = nil
		switch note.kind {
		case nkEnqueueReq:
			updated.reqR.Push(note.enqueueReq.req)
			cmd = reqReadyCmd(&updated)
		case lookupReq:
			updated.pathLookupRequests = append(updated.pathLookupRequests, note.lookup.req)
		case lookupResp:
			updated.pathLookupResponses = append(updated.pathLookupResponses, note.lookup.resp)
			fpath := data.directive.fileInfo.Path()
			parts := filepathSplit(fpath)
			if len(updated.pathLookupResponses) == len(parts) {
				cmd = openReqCmd(&updated)
			} else {
				cmd = pathLookupReqCmd(&data)
			}
		// case accessReq:
		// 	updated.accessRequest = note.access.req
		// case accessResp:
		// 	updated.accessResponse = note.access.resp
		// 	cmd = openReqCmd(&updated)
		case openReq:
			updated.openRequest = note.open.req
		case openResp:
			updated.openResponse = note.open.resp
			cmd = readdirReqCmd(&updated)
		case readdirReq:
			updated.readRequest = note.readdir.req
		case readdirResp:
			updated.readResponse = note.readdir.resp
			cmd = getattrReqCmd(&updated)
		case getattrReq:
			updated.getattrRequest = note.getattr.req
		case getattrResp:
			updated.getattrResponse = note.getattr.resp
			cmd = getattrReqCmd(&updated)
		}
		return
	}

	var __dispatch psor.Dispatch
	_dispatch := func(note note) {
		if __dispatch == nil {
			return
		}
		__dispatch(note)
	}

	subWith := func() psor.Subscribe {
		start := func(dispatch psor.Dispatch) psor.Unsubscriber {
			__dispatch = dispatch
			unsub := func() {
				__dispatch = nil
			}
			return unsub
		}
		return start
	}

	subscribe := func(data *findFilesProcessData) psor.Sub {
		subent := psor.Subent{1, subWith()}
		r := []psor.Subent{subent}
		return r
	}

	var _data *findFilesProcessData
	putData := func(data *findFilesProcessData) {
		_data = data
	}

	getData := func() *findFilesProcessData {
		return _data
	}

	updateWith := func(arg psor.ProcessArg) {
		note := noteWith(arg)
		_dispatch(note)
	}

	getProcessState := func() psor.ProcessState {
		return _data.processState
	}

	update_ := func(anote any, data *findFilesProcessData) (*findFilesProcessData, psor.Cmd) {
		n := anote.(note)
		updated, cmd := update(n, *data)
		return &updated, cmd
	}

	findFilesProcess := &psor.Process[*findFilesProcessData]{
		Init:            init,
		UpdateWith:      updateWith,
		Update:          update_,
		Subscribe:       subscribe,
		PutData:         putData,
		GetData:         getData,
		GetProcessState: getProcessState,
	}

	return &psor.Processor[*findFilesProcessData]{
		Process: findFilesProcess,
	}
}

//#endregion

type requestResponseModule struct{}

var Requests = &requestResponseModule{}

func (rrm *requestResponseModule) ReadRequest(fd *os.File) (req Request, err error) {
	directive := <-diesm.inBuffer
	debugf("## ReadRequest\n > %v\n", directive)

	req = directive.nextReq()
	if req == nil {
		debug(req)
		err = fmt.Errorf("not implemented")
	}
	return
}

func makeFileWithHandle(handle HandleID) emptyFile {
	return emptyFile{handle: handle}
}

func (rrm *requestResponseModule) WriteRespond(fd *os.File, resp Response) error {
	var answer Answer
	switch r := (resp).(type) {
	default:
		debugf("## WriteRespond\n > %v\n", resp)
		rid := RequestID(resp.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		directive.putResp(resp)
		if !directive.isComplete() {
			// No complete answer to post.
			return nil
		}
		answer = directive.answer()

	case *ErrorResponse:
		rid := RequestID(r.GetId())
		id := retsm.getDirectiveId(rid)
		directive := diesm.getDirective(DirectiveID(id))
		debugf("WriteRespond ErrorResponse %s \n> %s", r, directive)
		// TODO(OR): Complete directive
		a := &ErrorAnswer{
			answerHeader: mkAnswerHeader(r),
			errResp:      r,
		}
		answer = a
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

type ErrorAnswer struct {
	answerHeader
	errResp *ErrorResponse
}

var _ Answer = (*ErrorAnswer)(nil)

func (r *ErrorAnswer) Hdr() *answerHeader { return &r.answerHeader }
func (r *ErrorAnswer) IsAnswerType()      {}

type Response interface {
	IsResponseType()
	PutId(uint64)
	GetId() uint64
	// Hdr returns the Header associated with this request.
	// Hdr() *Header

	// RespondError responds to the request with the given error.
	// RespondError(error)

	String() string
	IsProcessArg()
}

type ErrorResponse struct {
	ResponseHeader
	Error error
	Errno int32
}

func (r *ErrorResponse) IsResponseType() {}
func (r *ErrorResponse) PutId(id uint64) { r.Id = RequestID(id) }
func (r *ErrorResponse) GetId() uint64   { return uint64(r.Id) }
func (d *ErrorResponse) IsProcessArg()   {}

type fileInfo interface {
	Path() string
	NumberOfFileHandles() uint32
}

type fileInfoImp struct {
	path string
}

var _ fileInfo = (*fileInfoImp)(nil)

func (fi *fileInfoImp) Path() string {
	return fi.path
}

func (fi *fileInfoImp) NumberOfFileHandles() uint32 { return 0 }

// A Header describes the basic information sent in every request.
type directiveHeader struct {
	id       DirectiveID // unique ID for directive
	fileInfo fileInfo    // file or directory the request is about
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
	startProcessor()
	nextReq() Request
	putResp(r Response)
	isComplete() bool
	answer() Answer
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

func (m *FileInfoModule) isDir(path string) (isDir bool) {
	isDir = len(path) != 0 && path[len(path)-1] == filepath.Separator
	return
}

func (m *FileInfoModule) isDirWithFileInfo(fi fileInfo) (isDir bool) {
	path := fi.Path()
	isDir = m.isDir(path)
	return
}

type createFileNoteKind = int

const (
	createFileNoteKindRespBitMask = createFileNoteKind(0b0001_00000000)
	createFileNoteKindGetattrReq  = createFileNoteKind(opGetattr)
	createFileNoteKindGetattrResp = createFileNoteKind(createFileNoteKindRespBitMask | opGetattr)
	createFileNoteKindLookupReq   = createFileNoteKind(opLookup)
	createFileNoteKindLookupResp  = createFileNoteKind(createFileNoteKindRespBitMask | opLookup)
	createFileNoteKindAccessReq   = createFileNoteKind(opAccess)
	createFileNoteKindAccessResp  = createFileNoteKind(createFileNoteKindRespBitMask | opAccess)
	createFileNoteKindOpenReq     = createFileNoteKind(opOpen) // opOpendir
	createFileNoteKindOpenResp    = createFileNoteKind(createFileNoteKindRespBitMask | opOpen)
	createFileNoteKindPush        = createFileNoteKind(0b1000_1000_0000)
	createFileNoteKindEnqueuReq   = createFileNoteKind(0b1000_1111_0000)
)

type createFileProcessData struct {
	processState    psor.ProcessState
	directive       *CreateFileDirective
	getattrRequest  *GetattrRequest
	getattrResponse *GetattrResponse
	fPathPartLen    int
	lookupRequests  []*LookupRequest
	lookupResponses []*LookupResponse
	accessRequest   *AccessRequest
	accessResponse  *AccessResponse
	openRequest     *OpenRequest
	openResponse    *OpenResponse
	reqR            *psor.RingBuffer
}

type CreateFileDirective struct {
	directiveHeader
	a          Answer
	CreateData *dokan.CreateData
	processor  *psor.Processor[*createFileProcessData]
}

var _ Directive = (*CreateFileDirective)(nil)

func (d *CreateFileDirective) principalDirectiveId() DirectiveID {
	return d.id
}
func (d *CreateFileDirective) putDirectiveId(id DirectiveID) { d.id = id }
func (d *CreateFileDirective) startProcessor() {
	d.processor.Start(d)
}

func (d *CreateFileDirective) nextReq() (req Request) {
	// fuse_operations::mknod
	// fuse_operations::create
	// fuse_operations::mkdir
	// fuse_operations::opendir
	// fuse_operations::open
	cd := d.CreateData
	isDir := fileInfos.isDirWithFileInfo(d.Hdr().fileInfo)
	data := d.processor.Fetch()
	req = data.reqR.Pop().(Request)
	switch req.(type) {
	case *OpenRequest:
		if isDir {
			if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
				debugf("ReadRequest fuse_operations::opendir")
				// req = &OpenRequest{
				// 	Header:    makeHeaderWithDirective(d),
				// 	Dir:       isDir,
				// 	Flags:     OpenDirectory,
				// 	OpenFlags: OpenRequestFlags(0),
				// }
			} else if cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

			}
		} else {
			if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
				// https://learn.microsoft.com/en-us/windows-hardware/drivers/ifs/access-mask
				// https://learn.microsoft.com/en-us/windows-hardware/drivers/kernel/access-mask?redirectedfrom=MSDN
				// CreateData.DesiredAccess=100100000000010001001
				// CreateData.FileAttributes=0
				// CreateData.ShareAccess=111
				// CreateData.CreateDisposition=1
				// CreateData.CreateOptions=1100100
				debugf("ReadRequest fuse_operations::open")
				// req = &OpenRequest{
				// 	Header:    makeHeaderWithDirective(d),
				// 	Dir:       isDir,
				// 	Flags:     OpenDirectory,
				// 	OpenFlags: OpenRequestFlags(0),
				// }
			}
		}
	}
	return
}

func (d *CreateFileDirective) putResp(resp Response) {
	// fuse_operations::mknod	DOKAN_OPERATIONS::ZwCreateFile
	// fuse_operations::create	DOKAN_OPERATIONS::ZwCreateFile
	// fuse_operations::open	DOKAN_OPERATIONS::ZwCreateFile
	// fuse_operations::mkdir	DOKAN_OPERATIONS::ZwCreateFile
	// fuse_operations::opendir	DOKAN_OPERATIONS::ZwCreateFile

	// OpenDirectIO    OpenResponseFlags = 1 << 0 // bypass page cache for this open file
	// OpenKeepCache   OpenResponseFlags = 1 << 1 // don't invalidate the data cache on open
	// OpenNonSeekable OpenResponseFlags = 1 << 2 // mark the file as non-seekable (not supported on FreeBSD)
	// OpenCacheDir    OpenResponseFlags = 1 << 3 // allow caching directory contents
	processor := d.processor
	p := processor
	p.Step(resp)

	if d.isComplete() {
		switch r := (resp).(type) {
		case *OpenResponse:
			// Need to get the request to know if it's open or opendir?
			// id := r.GetId()
			// directive := diesm.getDirective(DirectiveID(id))
			// d := directive.(*CreateFileDirective)
			createStatus := dokan.ExistingFile
			isDir := fileInfos.isDirWithFileInfo(d.fileInfo)
			if isDir {
				debugf("WriteRespond fuse_operations::opendir")
				createStatus = dokan.ExistingDir
			} else {
				debugf("WriteRespond fuse_operations::open")
			}
			file := makeFileWithHandle(r.Handle)

			// dokan.CreateStatus(dokan.ErrAccessDenied)
			if r.Handle != 0 {
				retsm.saveHandle(r.Handle, d.fileInfo.Path(), file)
			}
			d.a = &CreateFileAnswer{
				answerHeader: mkAnswerHeader(r),
				File:         file,
				CreateStatus: createStatus,
			}
		}
	}
}

func (d *CreateFileDirective) isComplete() bool {
	return d.a != nil
}

func (d *CreateFileDirective) answer() Answer {
	return d.a
}

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
func (d *CreateFileDirective) IsProcessArg()    {}

func (d *CreateFileDirective) makeRequest() Request {
	var result Request
	fmt.Printf("CreateFileDirective.makeRequest %v", d)
	// fuse_operations::mknod
	// fuse_operations::create
	// fuse_operations::open
	// fuse_operations::mkdir
	// fuse_operations::opendir
	node := supplyNodeIdWithPath(d.fileInfo.Path())
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
			Header: makeHeaderWith(node, d),
			// Dir:    m.hdr.Opcode == opOpendir,
			Dir: cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory,
			// Flags:  openFlags(in.Flags),
			Flags: OpenReadOnly,
		}

		result = req
	} else if cd.CreateDisposition&dokan.FileOpen == dokan.FileOpen {
		// fuse_operations::open
		isDir := fileInfos.isDirWithFileInfo(d.Hdr().fileInfo)
		// r.FileInfo = req.FileInfo
		// r.CreateData = req.CreateData
		req := &OpenRequest{
			Header:    makeHeaderWith(node, d),
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
	processor *psor.Processor[*findFilesProcessData]
}

func (c *findFilesCompound) nextReq() Request {
	data := c.processor.Fetch()
	req := data.dequeueReq()
	switch r := req.(type) {
	case *ReadRequest:
		// fuse_operations::readdir
		debug("ReadRequest nextRequest fuse_operations::readdir")
		return r
	}
	return req
}

func (c *findFilesCompound) putReq(req psor.ProcessArg) {
	c.processor.Step(req)
}

func (c *findFilesCompound) putResp(resp psor.ProcessArg) {
	c.processor.Step(resp)
}

func (c *findFilesCompound) getReadRequest() *ReadRequest {
	data := c.processor.Fetch()
	return data.readRequest
}

func (c *findFilesCompound) putReadResponse(resp *ReadResponse) {
	c.processor.Step(resp)
}

func (c *findFilesCompound) getReadResponse() *ReadResponse {
	data := c.processor.Fetch()
	return data.readResponse
}

func (c *findFilesCompound) putGetattrResponse(resp *GetattrResponse) {
	c.processor.Step(resp)
}

func (c *findFilesCompound) getGetLookupResponses() []*LookupResponse {
	data := c.processor.Fetch()
	return data.lookupResponses
}

func (c *findFilesCompound) getGetattrResponseByInode(inode uint64) *LookupResponse {
	data := c.processor.Fetch()
	for _, resp := range data.lookupResponses {
		if resp.Attr.Inode == inode {
			return resp
		}
	}
	return nil
}

func (c *findFilesCompound) isComplete() bool {
	return c.processor.State() == processStateSettled
}

func (c *findFilesCompound) isCompound() {}

var _ compound = (*findFilesCompound)(nil)

type FindFilesDirective struct {
	directiveHeader
	file             dokan.File
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
	processor        *psor.Processor[*findFilesProcessData]
}

var _ Directive = (*FindFilesDirective)(nil)
var _ psor.ProcessArg = (*FindFilesDirective)(nil)

func (d *FindFilesDirective) principalDirectiveId() DirectiveID {
	return d.id
}

func (d *FindFilesDirective) putDirectiveId(id DirectiveID) {
	d.id = id
}

func (d *FindFilesDirective) startProcessor() {
	// processor := d.compound.processor.WithConsoleLog()
	// processor.Start(d)
	d.processor.Start(d)
}

func (d *FindFilesDirective) nextReq() Request {
	data := d.processor.Fetch()
	req := data.reqR.Pop().(Request)
	return req
}

func (d *FindFilesDirective) putResp(r Response) {
	d.processor.Step(r)
}

func (d *FindFilesDirective) isComplete() bool {
	return d.processor.State() == processStateSettled
}

func (d *FindFilesDirective) getReadResponse() *ReadResponse {
	data := d.processor.Fetch()
	return data.readResponse
}

func (d *FindFilesDirective) getGetattrResponseByInode(inode uint64) *LookupResponse {
	data := d.processor.Fetch()
	for _, resp := range data.lookupResponses {
		if resp.Attr.Inode == inode {
			return resp
		}
	}
	return nil
}

func (d *FindFilesDirective) answer() Answer {
	// p := FindFilesProduct{}
	// arg:= {directive: d}
	// Products.perform(arg, p)
	if !d.isComplete() {
		// No complete answer to post.
		return nil
	}
	readResp := d.getReadResponse()
	a := &FindFilesAnswer{
		answerHeader: mkAnswerHeader(readResp),
		Items:        make([]dokan.NamedStat, len(readResp.Entries)),
	}
	namedStat := dokan.NamedStat{
		Name: "",
		Stat: dokan.Stat{},
	}
	for _, it := range readResp.Entries {
		attrResp := d.getGetattrResponseByInode(it.Inode)
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
		namedStat.Stat.FileAttributes = mkFileAttributesWithAttr(attrResp.Attr)
		a.Items = append(a.Items, namedStat)
	}
	return a
}

func (d *FindFilesDirective) RespondError(error) {}
func (d *FindFilesDirective) String() string {
	f := d.file.(emptyFile)
	return fmt.Sprintf("FindFilesDirective [%s] file.handle=%v", d.Hdr(), f.handle)
}

func (d *FindFilesDirective) IsDirectiveType() {}
func (d *FindFilesDirective) IsProcessArg()    {}

type FindFilesAnswer struct {
	answerHeader
	Items []dokan.NamedStat
}

var _ Answer = (*FindFilesAnswer)(nil)

func (r *FindFilesAnswer) Hdr() *answerHeader { return &r.answerHeader }
func (r *FindFilesAnswer) IsAnswerType()      {}

type GetFileInformationDirective struct {
	directiveHeader
	a    Answer
	file dokan.File
}

var _ Directive = (*GetFileInformationDirective)(nil)

func (d *GetFileInformationDirective) principalDirectiveId() DirectiveID {
	return d.id
}
func (r *GetFileInformationDirective) putDirectiveId(id DirectiveID) { r.id = id }
func (d *GetFileInformationDirective) startProcessor()               {}

func (d *GetFileInformationDirective) nextReq() (req Request) {
	node := supplyNodeIdWithPath(d.fileInfo.Path())
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
		Header: makeHeaderWith(node, d),
		Handle: handle,
		Flags:  GetattrFh,
	}
	return
}

func (d *GetFileInformationDirective) putResp(resp Response) {
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
	switch r := (resp).(type) {
	case *GetattrResponse:
		d.a = &GetFileInformationAnswer{
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
	}
}

func (d *GetFileInformationDirective) isComplete() bool {
	return d.a != nil
}

func (d *GetFileInformationDirective) answer() Answer {
	return d.a
}

func (r *GetFileInformationDirective) RespondError(error) {}
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
