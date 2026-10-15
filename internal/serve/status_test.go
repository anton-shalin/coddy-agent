package serve

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func TestBuildOf(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "-tags", Value: "http,ui,metrics"},
		{Key: "vcs.revision", Value: "7b566ae3"},
		{Key: "CGO_ENABLED", Value: "0"},
	}}
	got := buildOf(info)
	want := Build{
		Version: version.Get(), Revision: "7b566ae3", GoVersion: runtime.Version(),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Tags: []string{"http", "ui", "metrics"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildOf = %+v, want %+v", got, want)
	}

	// A binary without version control data or tags, or without build info at
	// all, still says what it is.
	for _, info := range []*debug.BuildInfo{nil, {}} {
		got := buildOf(info)
		if got.Revision != "" || got.Tags != nil || got.Version != version.Get() || got.GoVersion != runtime.Version() {
			t.Errorf("buildOf(%v) = %+v", info, got)
		}
	}
}
