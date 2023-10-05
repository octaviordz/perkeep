// Adapted from github.com/bazil/fuse

package xfz

import (
	"fmt"

	"github.com/keybase/client/go/kbfs/dokan"
)

type OpenRequestFlags uint32

// An OpenRequest asks to open a file or directory
type OpenRequest struct {
	Header    `json:"-"`
	Dir       bool // is this Opendir?
	Flags     OpenFlags
	OpenFlags OpenRequestFlags
}

var _ Request = (*OpenRequest)(nil)

func (r *OpenRequest) String() string {
	return fmt.Sprintf("Open [%s] dir=%v fl=%v", &r.Header, r.Dir, r.Flags)
}

// Respond replies to the request with the given response.
func (r *OpenRequest) Respond(resp *OpenResponse) {
	// buf := newBuffer(unsafe.Sizeof(openOut{}))
	// out := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
	// out.Fh = uint64(resp.Handle)
	// out.OpenFlags = uint32(resp.Flags)

	var outResp xfz.Response = &xfz.CreateFileResponse{
		Header: xfz.Header{
			ID:   xfz.RequestID(uint64(r.ID)),
			Node: xfz.NodeID(uint64(r.Node)),
		},
		Handle: xfz.HandleID(resp.Handle),
	}

	r.respond(outResp)
}

// A OpenResponse is the response to a OpenRequest.
type OpenResponse struct {
	Handle HandleID
	Flags  OpenResponseFlags
}

func (r *OpenResponse) string() string {
	return fmt.Sprintf("%v fl=%v", r.Handle, r.Flags)
}

func (r *OpenResponse) String() string {
	return fmt.Sprintf("Open %s", r.string())
}

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

type FindFilesRequest struct {
	hdr              *Header
	FileInfo         *dokan.FileInfo
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
}

func (r FindFilesRequest) Hdr() *Header       { return r.hdr }
func (r FindFilesRequest) RespondError(error) {}
func (r FindFilesRequest) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}

var _ Request = FindFilesRequest{}

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

// fuzeo
// fjuzo
