//go:build windows
// +build windows

package xfz

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/keybase/client/go/kbfs/dokan"
	"github.com/keybase/client/go/kbfs/dokan/winacl"
	"golang.org/x/sys/windows/registry"
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
	// fuzeo.Request{}
	request := &CreateFileRequest{
		FileInfo:   fi,
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
	resp, err := WriteRequest(ctx, request)
	debug(resp)

	r := resp.(*CreateFileResponse)

	if err != nil {
		// return emptyFile{}, dokan.CreateStatus(dokan.ErrNotSupported), err
		return emptyFile{}, r.CreateStatus, err
	}

	if cd.FileAttributes&dokan.FileAttributeDirectory == dokan.FileAttributeDirectory &&
		cd.CreateDisposition&dokan.FileCreate == dokan.FileCreate {

		return emptyFile{}, dokan.CreateStatus(dokan.ErrAccessDenied), nil
	}

	return emptyFile{}, dokan.ExistingDir, nil
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

type emptyFile struct{}

func (t emptyFile) GetFileInformation(ctx context.Context, fi *dokan.FileInfo) (*dokan.Stat, error) {
	debug("emptyFile.Getdokan.FileInformation")
	var emptyStat dokan.Stat

	st := dokan.Stat{
		Creation:       time.Now(),
		LastAccess:     time.Now(),
		LastWrite:      time.Now(),
		FileSize:       0,
		FileAttributes: dokan.FileAttributeDirectory,
	}
	if fi.Path() == "\\" {
		st.FileAttributes = dokan.FileAttributeDirectory
		return &st, nil
	}

	_, err := getRegistoryEntry(fi.Path())
	if err != nil {
		return &emptyStat, err
	}
	st.FileAttributes = dokan.FileAttributeDirectory

	return &st, nil
}

var topDirectory = map[string]registry.Key{
	"ClassesRoot":   registry.CLASSES_ROOT,
	"CurrentUser":   registry.CURRENT_USER,
	"CurrentConfig": registry.CURRENT_CONFIG,
	"LocalMachine":  registry.LOCAL_MACHINE,
	"Users":         registry.USERS,
}

func getRegistoryEntry(name string) (registry.Key, error) {
	fmt.Printf("getRegistoryEntry : %s\n", name)
	var emptyKey registry.Key

	xi := name[1:]
	fmt.Printf("name[1:] : %s\n", xi)
	top := strings.Index(name[1:], "\\") + 1
	fmt.Printf("top : %d\n", top)
	if top <= 0 {
		top = len(name)
	}
	fmt.Printf("top : %d\n", top)

	topname := name[1:top]
	fmt.Printf("topname : %s\n", topname)
	sub := strings.Index(name[1:], "\\")
	fmt.Printf("sub : %d\n", sub)

	vkey, exists := topDirectory[topname]
	fmt.Printf("vkey: %v, exists: %t\n", vkey, exists)
	if exists {
		if sub == -1 {
			return vkey, nil
		} else {
			fmt.Printf("name[sub+2:] : %s\n", name[sub+2:])
			return registry.OpenKey(vkey, name[sub+2:], registry.READ)
		}
	}
	return emptyKey, fmt.Errorf("enty outside scope")
}

// FindFiles is the readdir. The function is a callback that should be called
// with each file. The same NamedStat may be reused for subsequent calls.
//
// Pattern will be an empty string unless UseFindFilesWithPattern is enabled - then
// it may be a pattern like `*.png` to match. All implementations must be prepared
// to handle empty strings as patterns.
func (t emptyFile) FindFiles(ctx context.Context, fi *dokan.FileInfo, pattern string, fillStatCallback func(*dokan.NamedStat) error) error {
	debug("emptyFile.FindFiles")
	fmt.Printf("FindFiles fi.Path() : %s\n", fi.Path())
	request := &FindFilesRequest{
		FileInfo:         fi,
		Pattern:          pattern,
		FillStatCallback: fillStatCallback,
	}
	_, err := WriteRequest(ctx, request)
	if err != nil {
		return err
	}
	// readResp := resp.(fuzeo.ReadResponse)
	// debug(readResp)
	// debug(readResp.Data)

	namedStat := dokan.NamedStat{
		Name:      "",
		ShortName: "",
		Stat: dokan.Stat{
			Creation:       time.Now(),
			LastAccess:     time.Now(),
			LastWrite:      time.Now(),
			FileSize:       0,
			FileAttributes: dokan.FileAttributeDirectory,
		},
	}
	if fi.Path() == "\\" {
		for key := range topDirectory {
			namedStat.Name = key
			namedStat.Stat.FileAttributes = dokan.FileAttributeDirectory
			if err := fillStatCallback(&namedStat); err != nil {
				fmt.Println("fillStatCallback Error:", err)
			}
		}
	} else {
		key, err := getRegistoryEntry(fi.Path())
		if err != nil {
			return err
		}
		subkeys, err := key.ReadSubKeyNames(4000)
		if err != nil && err != io.EOF {
			return err
		}
		for _, subkey := range subkeys {
			namedStat.Name = subkey
			namedStat.Stat.FileAttributes = dokan.FileAttributeDirectory
			if err := fillStatCallback(&namedStat); err != nil {
				fmt.Println("fillStatCallback Error:", err)
			}
		}
		valueNames, err := key.ReadValueNames(4000)
		if err != nil && err != io.EOF {
			return err
		}
		for _, valueName := range valueNames {
			namedStat.Name = valueName
			namedStat.Stat.FileAttributes = dokan.FileAttributeNormal
			if err := fillStatCallback(&namedStat); err != nil {
				fmt.Println("fillStatCallback Error:", err)
			}
		}
	}

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