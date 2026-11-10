package projectname

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, ok := range []string{"a", "app", "my-repo.v2", "x_y", strings.Repeat("a", MaxLen)} {
		if err := Validate(ok); err != nil {
			t.Errorf("Validate(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, "-x", ".x", "a b", strings.Repeat("a", MaxLen+1)} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%q) accepted", bad)
		}
	}
}
