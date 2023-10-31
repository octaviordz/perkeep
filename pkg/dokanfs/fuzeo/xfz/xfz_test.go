package xfz // import "perkeep.org/pkg/dokanfs/fuzeo/xfz"

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keybase/client/go/kbfs/dokan"
)

func TestCreateFileProcessOpendir(t *testing.T) {
	ctx := context.Background()
	cd := &dokan.CreateData{
		DesiredAccess:     0b100000000000000000000,
		FileAttributes:    0,
		ShareAccess:       0,
		CreateDisposition: dokan.CreateDisposition(1),
		CreateOptions:     0b100000000000000000100001,
	}

	fi := &fileInfoImp{path: filepath.Join("home", "user", "pk")}
	directive := &CreateFileDirective{
		directiveHeader: directiveHeader{
			fileInfo: fi,
			node:     supplyNodeIdWithPath(fi.Path()),
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
}
