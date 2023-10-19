//go:build windows
// +build windows

package xfz

import (
	"context"
	"fmt"
	"time"

	"github.com/keybase/client/go/kbfs/dokan"
	"github.com/keybase/client/go/kbfs/dokan/winacl"
)

var _ dokan.FileSystem = fileSystemInter{}

type fileSystemInter struct{}

func (t emptyFile) GetFileSecurity(ctx context.Context, fi *dokan.FileInfo, si winacl.SecurityInformation, sd *winacl.SecurityDescriptor) error {
	debug("RFS.GetFileSecurity")
	return nil
}

func (t emptyFile) SetFileSecurity(ctx context.Context, fi *dokan.FileInfo, si winacl.SecurityInformation, sd *winacl.SecurityDescriptor) error {
	debug("RFS.SetFileSecurity")
	return nil
}

func (t emptyFile) Cleanup(ctx context.Context, fi *dokan.FileInfo) {
	debug("RFS.Cleanup")
}

func (t emptyFile) CloseFile(ctx context.Context, fi *dokan.FileInfo) {
	debug("RFS.CloseFile")
}

func (t fileSystemInter) WithContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, nil
}

func (t fileSystemInter) GetVolumeInformation(ctx context.Context) (dokan.VolumeInformation, error) {
	debug("RFS.GetVolumeInformation")
	// 	rations::statfs 	DOKAN_OPERATIONS::GetDiskFreeSpace
	// DOKAN_OPERATIONS::GetVolumeInformation
	return dokan.VolumeInformation{
		VolumeName:             "Pk",
		FileSystemName:         "fileSystem",
		MaximumComponentLength: 256,
	}, nil
}

func (t fileSystemInter) GetDiskFreeSpace(ctx context.Context) (dokan.FreeSpace, error) {
	debug("RFS.GetDiskFreeSpace")
	return dokan.FreeSpace{
		FreeBytesAvailable:     512 * 1024 * 1024,
		TotalNumberOfBytes:     1024 * 1024 * 1024,
		TotalNumberOfFreeBytes: 512 * 1024 * 1024,
	}, nil
}

func (t fileSystemInter) ErrorPrint(err error) {
	debug(err)
}

func (t fileSystemInter) Printf(string, ...interface{}) {
}

func (t fileSystemInter) CreateFile(ctx context.Context, fi *dokan.FileInfo, cd *dokan.CreateData) (dokan.File, dokan.CreateStatus, error) {
	debug("RFS.CreateFile")
	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
			node: supplyNodeIdWithPath(fi.Path()),
		},
		CreateData: cd,
	}
	// openRequest := fuzeo.OpenRequest{
	// 	Header: header,
	// 	Dir:    cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory,
	// 	Flags:  fuzeo.OpenReadOnly,
	// }
	// // Return true if OpenReadOnly is set.
	// func (fl OpenFlags) IsReadOnly() bool {
	// 	return fl&OpenAccessModeMask == OpenReadOnly
	// }

	// // Return true if OpenWriteOnly is set.
	// func (fl OpenFlags) IsWriteOnly() bool {
	// 	return fl&OpenAccessModeMask == OpenWriteOnly
	// }

	// // Return true if OpenReadWrite is set.
	// func (fl OpenFlags) IsReadWrite() bool {
	// 	return fl&OpenAccessModeMask == OpenReadWrite
	// }

	// case opOpendir, opOpen:
	// 	in := (*openIn)(m.data())
	// 	if m.len() < unsafe.Sizeof(*in) {
	// 		goto corrupt
	// 	}
	// 	req = &OpenRequest{
	// 		Header: m.Header(),
	// 		Dir:    m.hdr.Opcode == opOpendir,
	// 		Flags:  openFlags(in.Flags),
	// 	}

	//TODO(ORC): Process response.
	answer, err := diesm.PostDirective(ctx, directive)
	debug(answer)

	a := answer.(*CreateFileAnswer)

	if err != nil {
		return nil, dokan.CreateStatus(dokan.ErrNotSupported), err
	}

	if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
		cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

		return a.File, a.CreateStatus, nil
	}

	return a.File, a.CreateStatus, nil
}

func (t emptyFile) CanDeleteFile(ctx context.Context, fi *dokan.FileInfo) error {
	return dokan.ErrAccessDenied
}

func (t emptyFile) CanDeleteDirectory(ctx context.Context, fi *dokan.FileInfo) error {
	return dokan.ErrAccessDenied
}

func (t emptyFile) SetEndOfFile(ctx context.Context, fi *dokan.FileInfo, length int64) error {
	debug("emptyFile.SetEndOfFile")
	return nil
}

func (t emptyFile) SetAllocationSize(ctx context.Context, fi *dokan.FileInfo, length int64) error {
	debug("emptyFile.SetAllocationSize")
	return nil
}

func (t fileSystemInter) MoveFile(ctx context.Context, src dokan.File, sourceFI *dokan.FileInfo, targetPath string, replaceExisting bool) error {
	debug("RFS.MoveFile")
	return nil
}

func (t emptyFile) ReadFile(ctx context.Context, fi *dokan.FileInfo, bs []byte, offset int64) (int, error) {
	return len(bs), nil
}

func (t emptyFile) WriteFile(ctx context.Context, fi *dokan.FileInfo, bs []byte, offset int64) (int, error) {
	return len(bs), nil
}

func (t emptyFile) FlushFileBuffers(ctx context.Context, fi *dokan.FileInfo) error {
	debug("RFS.FlushFileBuffers")
	return nil
}

type emptyFile struct {
	handle HandleID
}

func (t emptyFile) GetFileInformation(ctx context.Context, fi *dokan.FileInfo) (*dokan.Stat, error) {
	debug("emptyFile.GetFileInformation")
	directive := &GetFileInformationDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
			node: supplyNodeIdWithPath(fi.Path()),
		},
		file: t,
	}
	answer, err := diesm.PostDirective(ctx, directive)
	debug(answer)
	debug(err)
	if err != nil {
		return nil, err
	}
	a := answer.(*GetFileInformationAnswer)
	// if fi.Path() == "\\" {
	// 	st.FileAttributes = dokan.FileAttributeDirectory
	// 	return &st, nil
	// }
	return a.Stat, nil
}

// FindFiles is the readdir. The function is a callback that should be called
// with each file. The same NamedStat may be reused for subsequent calls.
//
// Pattern will be an empty string unless UseFindFilesWithPattern is enabled - then
// it may be a pattern like `*.png` to match. All implementations must be prepared
// to handle empty strings as patterns.
func (t emptyFile) FindFiles(ctx context.Context, fi *dokan.FileInfo, pattern string, fillStatCallback func(*dokan.NamedStat) error) error {
	debug("emptyFile.FindFiles")
	fmt.Printf("FindFiles fi.Path(): %s\n", fi.Path())
	compound := makefindFilesCompound(ctx)
	// fuse_operations::readdir 	DOKAN_OPERATIONS::FindFiles
	directive := &FindFilesDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
			node: supplyNodeIdWithPath(fi.Path()),
		},
		file:             t,
		Pattern:          pattern,
		FillStatCallback: fillStatCallback,
		compound:         compound,
	}

	answer, err := diesm.PostDirective(ctx, directive)
	if err != nil {
		return err
	}
	a := answer.(*FindFilesAnswer)
	for _, namedStat := range a.Items {
		if err := fillStatCallback(&namedStat); err != nil {
			fmt.Println("fillStatCallback Error:", err)
		}
	}
	// namedStat := dokan.NamedStat{
	// 	Name:      "",
	// 	ShortName: "",
	// 	Stat: dokan.Stat{
	// 		Creation:       time.Now(),
	// 		LastAccess:     time.Now(),
	// 		LastWrite:      time.Now(),
	// 		FileSize:       0,
	// 		FileAttributes: dokan.FileAttributeDirectory,
	// 	},
	// }

	return nil
}

func (t emptyFile) SetFileTime(context.Context, *dokan.FileInfo, time.Time, time.Time, time.Time) error {
	debug("emptyFile.SetFileTime")
	return nil
}

func (t emptyFile) SetFileAttributes(ctx context.Context, fi *dokan.FileInfo, fileAttributes dokan.FileAttribute) error {
	debug("emptyFile.SetFileAttributes")
	return nil
}

func (t emptyFile) LockFile(ctx context.Context, fi *dokan.FileInfo, offset int64, length int64) error {
	debug("emptyFile.LockFile")
	return nil
}

func (t emptyFile) UnlockFile(ctx context.Context, fi *dokan.FileInfo, offset int64, length int64) error {
	debug("emptyFile.UnlockFile")
	return nil
}
