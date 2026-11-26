package gitmeta

import (
	"context"
	"errors"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

type fake struct {
	rev, status execx.Result
	revErr      error
	statusErr   error
}

func (f fake) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if args[2] == "rev-parse" {
		return f.rev, f.revErr
	}
	return f.status, f.statusErr
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	got, err := Read(ctx, fake{rev: execx.Result{Stdout: "3cb19d0\n"}}, "/p")
	if err != nil || got != (Info{IsRepo: true, Commit: "3cb19d0"}) {
		t.Fatalf("clean: %+v %v", got, err)
	}
	got, _ = Read(ctx, fake{rev: execx.Result{Stdout: "abc1234\n"}, status: execx.Result{Stdout: " M a.go\n"}}, "/p")
	if !got.Dirty {
		t.Fatalf("dirty: %+v", got)
	}
	got, err = Read(ctx, fake{revErr: errors.New("not a repo")}, "/p")
	if err != nil || got.IsRepo {
		t.Fatalf("no repo: %+v %v", got, err)
	}
	if _, err = Read(ctx, fake{rev: execx.Result{Stdout: "abc1234"}, statusErr: errors.New("boom")}, "/p"); err == nil {
		t.Fatal("status error should propagate")
	}
}
