package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keybase/client/go/kbfs/dokan"
)

func TestGoi(t *testing.T) {
	// tedigita
	// resp := &GetattrResponse{}
	// fmt.Printf("Response %v", resp)

	idx := 1
	// fpath := filepath.Join("pk")
	// fpath := "\\"
	// fpath := "\\Users\\pk\\folder"
	fpath := filepath.Join(string(filepath.Separator), "root", "pk")
	t.Logf("fpath %v", fpath)
	parts := filepathSplit(fpath)
	t.Logf("parts %v len(parts) %v", parts, len(parts))
	dirname := ""
	path := string(filepath.Separator)
	if idx <= len(parts)-1 {
		dirname = parts[idx]
		path = filepath.Join(string(filepath.Separator), filepath.Join(parts[:idx]...))
	}
	t.Logf("dirname %v", dirname)
	t.Logf("path %v", path)
}

func TestCreateFileProcessOpendir1(t *testing.T) {
	ctx := context.Background()
	cd := &dokan.CreateData{
		DesiredAccess:     0b100000000000000000000,
		FileAttributes:    0,
		ShareAccess:       0b11,
		CreateDisposition: dokan.CreateDisposition(1),
		CreateOptions:     0b100001,
	}

	fi := &fileInfoImp{path: string(filepath.Separator)}
	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
		},
		CreateData: cd,
		processor:  makeCreateFileProcess(ctx),
	}

	processor := directive.processor
	if processor.Fetch() != nil {
		t.Errorf("Expected <nil>, but got %v", processor.Fetch())
	}

	p := processor
	p.Start(directive)
	if p.Fetch().directive == nil {
		t.Errorf("Expected %v, but got %v", directive, processor.Fetch().directive)
	}
	var resp Response

	r0 := p.Fetch().reqR.Pop().(*GetattrRequest)
	t.Logf("GetattrRequest %v", r0)

	resp = &GetattrResponse{}
	p.Step(resp)

	r1 := p.Fetch().reqR.Pop().(*AccessRequest)
	t.Logf("AccessRequest %v", r1)

	resp = &AccessResponse{}
	p.Step(resp)

	r2 := p.Fetch().reqR.Pop().(*OpenRequest)
	t.Logf("OpenRequest %v", r2)

	resp = &OpenResponse{}
	p.Step(resp)

	if directive.isComplete() == true {
		t.Errorf("isComplete() Expected true, but got %v", directive.isComplete())
	}
}

func TestCreateFileProcessOpendir2(t *testing.T) {
	ctx := context.Background()
	cd := &dokan.CreateData{
		DesiredAccess:     0b10000000,
		FileAttributes:    0,
		ShareAccess:       0b111,
		CreateDisposition: dokan.CreateDisposition(1),
		CreateOptions:     0b1000000000000000000000,
	}

	fi := &fileInfoImp{path: string(filepath.Separator)}
	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
		},
		CreateData: cd,
		processor:  makeCreateFileProcess(ctx),
	}

	processor := directive.processor
	if processor.Fetch() != nil {
		t.Errorf("Expected <nil>, but got %v", processor.Fetch())
	}

	p := processor
	p.Start(directive)
	if p.Fetch().directive == nil {
		t.Errorf("Expected %v, but got %v", directive, processor.Fetch().directive)
	}
	var resp Response

	r0 := p.Fetch().reqR.Pop().(*GetattrRequest)
	t.Logf("GetattrRequest %v", r0)

	resp = &GetattrResponse{}
	p.Step(resp)

	r1 := p.Fetch().reqR.Pop().(*AccessRequest)
	t.Logf("AccessRequest %v", r1)

	resp = &AccessResponse{}
	p.Step(resp)

	r2 := p.Fetch().reqR.Pop().(*OpenRequest)
	t.Logf("OpenRequest %v", r2)

	resp = &OpenResponse{}
	p.Step(resp)

	if directive.isComplete() == true {
		t.Errorf("isComplete() Expected true, but got %v", directive.isComplete())
	}
}

func TestCreateFileProcessOpendir(t *testing.T) {
	ctx := context.Background()
	cd := &dokan.CreateData{
		DesiredAccess:     0b100000000000000000000,
		FileAttributes:    0,
		ShareAccess:       0,
		CreateDisposition: dokan.CreateDisposition(1),
		CreateOptions:     0b100000000000000000100001,
	}

	fi := &fileInfoImp{path: filepath.Join("root", "pk", "folder")}
	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
		},
		CreateData: cd,
		processor:  makeCreateFileProcess(ctx),
	}

	processor := directive.processor
	if processor.Fetch() != nil {
		t.Errorf("Expected <nil>, but got %v", processor.Fetch())
	}

	p := processor
	p.Start(directive)
	if p.Fetch().directive == nil {
		t.Errorf("Expected %v, but got %v", directive, processor.Fetch().directive)
	}
	var resp Response

	r0 := p.Fetch().reqR.Pop().(*GetattrRequest)
	t.Logf("GetattrRequest %v", r0)

	resp = &GetattrResponse{}
	p.Step(resp)

	r1 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r1)

	resp = &LookupResponse{}
	p.Step(resp)

	r2 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r2)

	resp = &LookupResponse{}
	p.Step(resp)

	r3 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r3)

	resp = &LookupResponse{}
	p.Step(resp)

	r4 := p.Fetch().reqR.Pop().(*AccessRequest)
	t.Logf("AccessRequest %v", r4)

	resp = &AccessResponse{}
	p.Step(resp)

	r5 := p.Fetch().reqR.Pop().(*OpenRequest)
	t.Logf("OpenRequest %v", r5)

	resp = &OpenResponse{}
	p.Step(resp)

	if directive.isComplete() == true {
		t.Errorf("isComplete() Expected true, but got %v", directive.isComplete())
	}
}

func TestCreateFileProcessOpenfile(t *testing.T) {
	ctx := context.Background()
	cd := &dokan.CreateData{
		DesiredAccess:  0b100100000000010001001,
		FileAttributes: 0,
		// FILE_SHARE_READ (0x00000001)
		// FILE_SHARE_WRITE (0x00000002)
		// FILE_SHARE_DELETE (0x00000004)
		ShareAccess:       0b111,
		CreateDisposition: dokan.CreateDisposition(1),
		// FILE_SEQUENTIAL_ONLY (0x00000004)
		// FILE_SYNCHRONOUS_IO_NONALERT (0x00000020)
		// FILE_NON_DIRECTORY_FILE (0x00000040)
		CreateOptions: 0b1100100, // 0x64
	}

	fi := &fileInfoImp{path: filepath.Join(string(filepath.Separator), "WELCOME.txt")}

	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
		},
		CreateData: cd,
		processor:  makeCreateFileProcess(ctx),
	}

	processor := directive.processor
	if processor.Fetch() != nil {
		t.Errorf("Expected <nil>, but got %v", processor.Fetch())
	}

	p := processor
	p.Start(directive)
	if p.Fetch().directive == nil {
		t.Errorf("Expected %v, but got %v", directive, processor.Fetch().directive)
	}
	var resp Response

	r0 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r0)

	resp = &LookupResponse{}
	p.Step(resp)

	r1 := p.Fetch().reqR.Pop().(*OpenRequest)
	t.Logf("OpenRequest %v", r1)

	resp = &OpenResponse{}
	p.Step(resp)

	r2 := p.Fetch().reqR.Pop().(*ReadRequest)
	t.Logf("ReadRequest %v", r2)

	resp = &ReadResponse{}
	p.Step(resp)

	r3 := p.Fetch().reqR.Pop().(*FlushRequest)
	t.Logf("FlushRequest %v", r3)

	resp = &FlushResponse{}
	p.Step(resp)

	r3 := p.Fetch().reqR.Pop().(*ReleaseRequest)
	t.Logf("ReleaseRequest %v", r3)

	resp = &ReleaseResponse{}
	p.Step(resp)

	if directive.isComplete() == true {
		t.Errorf("isComplete() Expected true, but got %v", directive.isComplete())
	}
}

func reverseSlice(data []string) []string {
	for i := len(data)/2 - 1; i >= 0; i-- {
		opp := len(data) - 1 - i
		data[i], data[opp] = data[opp], data[i]
	}
	return data
}

func filepathSplit_(path string) []string {
	parts := make([]string, 0, 10)
	path_ := path
	for {
		lastBase := filepath.Base(path_)
		path_ = filepath.Dir(path_)
		if lastBase == path_ {
			break
		}
		parts = append(parts, lastBase)
	}
	return reverseSlice(parts)
}

func TestFindFilesProcess(t *testing.T) {
	ctx := context.Background()
	fi := &fileInfoImp{path: filepath.Join("root", "pk", "folder")}
	var f emptyFile
	directive := &FindFilesDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
		},
		file:             f,
		Pattern:          "",
		FillStatCallback: nil, //fillStatCallback,
		processor:        makefindFilesProcessor(ctx),
	}

	processor := directive.processor
	if processor.Fetch() != nil {
		t.Errorf("Expected <nil>, but got %v", processor.Fetch())
	}

	p := processor
	p.Start(directive)
	if p.Fetch().directive == nil {
		t.Errorf("Expected %v, but got %v", directive, processor.Fetch().directive)
	}
	var resp Response

	r0 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r0)

	resp = &LookupResponse{}
	p.Step(resp)

	r1 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r1)

	resp = &LookupResponse{}
	p.Step(resp)

	r2 := p.Fetch().reqR.Pop().(*LookupRequest)
	t.Logf("LookupRequest %v", r2)

	resp = &LookupResponse{}
	p.Step(resp)

	r3 := p.Fetch().reqR.Pop().(*OpenRequest)
	t.Logf("OpenRequest %v", r3)

	resp = &OpenResponse{}
	p.Step(resp)

	r4 := p.Fetch().reqR.Pop().(*ReadRequest)
	t.Logf("ReadRequest %v", r4)

	dirents := []Dirent{
		{
			Inode: 10,
			Type:  DirentType(DT_File),
			Name:  "WELCOME.txt",
		},
	}
	resp = &ReadResponse{
		Entries: dirents,
	}
	p.Step(resp)

	r5 := p.Fetch().reqR.Pop().(*GetattrRequest)
	t.Logf("GetattrRequest %v", r5)

	resp = &GetattrResponse{}
	p.Step(resp)

	if directive.isComplete() == true {
		t.Errorf("isComplete() Expected true, but got %v", directive.isComplete())
	}
}
