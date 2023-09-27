package fuzeoutil // import "perkeep.org/pkg/dokanfs/fuzeo/fuzeoutil"

import (
	"perkeep.org/pkg/dokanfs/fuzeo"
)

// HandleRead handles a read request assuming that data is the entire file content.
// It adjusts the amount returned in resp according to req.Offset and req.Size.
func HandleRead(req *fuzeo.ReadRequest, resp *fuzeo.ReadResponse, data []byte) {
	if req.Offset >= int64(len(data)) {
		data = nil
	} else {
		data = data[req.Offset:]
	}
	if len(data) > req.Size {
		data = data[:req.Size]
	}
	n := copy(resp.Data[:req.Size], data)
	resp.Data = resp.Data[:n]
}
