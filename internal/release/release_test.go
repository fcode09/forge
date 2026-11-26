package release

import (
	"testing"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/gitmeta"
)

func TestNewID(t *testing.T) {
	now := time.Date(2026, 11, 25, 22, 51, 3, 0, time.FixedZone("x", -5*3600))
	cases := []struct {
		g    gitmeta.Info
		want string
	}{
		{gitmeta.Info{IsRepo: true, Commit: "3cb19d0"}, "20261126T035103Z-3cb19d0"},
		{gitmeta.Info{IsRepo: true, Commit: "3cb19d0", Dirty: true}, "20261126T035103Z-3cb19d0-dirty"},
		{gitmeta.Info{}, "20261126T035103Z-nogit"},
	}
	for _, c := range cases {
		got := NewID(now, c.g)
		if got != c.want || !config.ValidReleaseLabel(got) {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []string{"latest", "", "a b", "../x", "x:y"} {
		if Validate(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := Validate("v1.2.3-rc1"); err != nil {
		t.Fatal(err)
	}
}
