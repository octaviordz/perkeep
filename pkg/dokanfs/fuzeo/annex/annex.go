package annex // import "perkeep.org/pkg/dokanfs/fuzeo/annex"

import (
	"context"
	"os"

	"github.com/keybase/client/go/kbfs/dokan"
)

type handleEntry struct {
	Fd  *os.File
	Dir string
	// MountConfig *mountConfig
}

var mountHandleMap = make(map[*os.File]handleEntry)

func MountHandleMap() map[*os.File]handleEntry {
	return mountHandleMap
}

func Mount(dir string) (handle *os.File, err error) {
	//dir, err = os.MkdirTemp("", "pk-dokanfs-mount")
	fd, err := os.CreateTemp("", "pk-dokanfs-mount")
	if err != nil {
		return nil, err
	}
	mountHandleMap[fd] = handleEntry{
		Fd:  fd,
		Dir: dir,
	}

	return fd, nil
}

func RequestFindFiles(ctx context.Context, fi *dokan.FileInfo, fillStatCallback func(*dokan.NamedStat) error) error {
	return nil
}

func ReadRequest(ctx context.Context, fillStatCallback func(*dokan.NamedStat) error) error {
	return nil
}

// // FindFiles
// func (c *Conn) ReadRequest(fillStatCallback func(*dokan.NamedStat) error) (Request, error) {

// }
