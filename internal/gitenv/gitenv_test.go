package gitenv

import (
	"slices"
	"testing"
)

func TestClean(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/repo/.git",
		"HOME=/home/u",
		"GIT_WORK_TREE=/repo",
		"GIT_INDEX_FILE=/repo/.git/index",
		"GIT_COMMON_DIR=/repo/.git",
		"GIT_OBJECT_DIRECTORY=/repo/.git/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/alt",
		"GIT_NAMESPACE=ns",
		"GIT_PREFIX=sub/",
		"GIT_AUTHOR_NAME=keep me",     // identity, not a location var — must stay
		"GIT_CONFIG_GLOBAL=/dev/null", // config path, not location — must stay
		"MALFORMED",                   // no '=' — kept verbatim
	}
	want := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"GIT_AUTHOR_NAME=keep me",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"MALFORMED",
	}
	got := Clean(in)
	if !slices.Equal(got, want) {
		t.Errorf("Clean() = %v, want %v", got, want)
	}
	if len(in) != 13 {
		t.Errorf("Clean mutated its input: len=%d, want 13", len(in))
	}
}

func TestCleanEmpty(t *testing.T) {
	if got := Clean(nil); len(got) != 0 {
		t.Errorf("Clean(nil) = %v, want empty", got)
	}
}
