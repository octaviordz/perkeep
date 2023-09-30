// Adapted from github.com/bazil/fuse

package fuzeo // import "perkeep.org/pkg/dokanfs/fuzeo"

import (
	"fmt"

	"github.com/keybase/client/go/kbfs/dokan"
	"perkeep.org/pkg/dokanfs/fuzeo/xfz"
)

type RequestCreateFile struct {
	hdr        *Header
	FileInfo   *dokan.FileInfo
	CreateData *dokan.CreateData
}

func (r RequestCreateFile) Hdr() *Header       { return r.hdr }
func (r RequestCreateFile) RespondError(error) {}
func (r RequestCreateFile) String() string {
	return fmt.Sprintf("RequestCreateFile [%s]", r.FileInfo.Path())
}
func (r *RequestCreateFile) BuildFrom(conn *Conn, req xfz.RequestCreateFile) {
	h := req.Hdr()
	r.hdr = &Header{
		Conn: conn,
		ID:   RequestID(h.ID),
		// Node: NodeID(h.NodeID),
		// Uid:  h.Uid,
		// Gid:  h.Gid,
		// Pid:  h.Pid,
	}
	r.FileInfo = req.FileInfo
	r.CreateData = req.CreateData
}

var _ Request = RequestCreateFile{}

type RequestFindFiles struct {
	hdr              *Header
	FileInfo         *dokan.FileInfo
	Pattern          string
	FillStatCallback func(*dokan.NamedStat) error
}

func (r RequestFindFiles) Hdr() *Header       { return r.hdr }
func (r RequestFindFiles) RespondError(error) {}
func (r RequestFindFiles) String() string {
	return fmt.Sprintf("RequestFindFiles [%s]", r.FileInfo.Path())
}
func (r *RequestFindFiles) BuildFrom(conn *Conn, req xfz.RequestFindFiles) {
	h := req.Hdr()
	r.hdr = &Header{
		Conn: conn,
		ID:   RequestID(h.ID),
	}
	r.FileInfo = req.FileInfo
	r.Pattern = req.Pattern
	r.FillStatCallback = req.FillStatCallback
}

var _ Request = RequestFindFiles{}

// fuzeo
// fjuzo
