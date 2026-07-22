package privacy

import "testing"

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		value string
		want  Mode
	}{
		{"cloud", Cloud},
		{" HYBRID ", Hybrid},
		{"", Hybrid},
		{"edge", Edge},
	}
	for _, tt := range tests {
		got, err := NormalizeMode(tt.value)
		if err != nil || got != tt.want {
			t.Errorf("NormalizeMode(%q) = %q, %v; want %q", tt.value, got, err, tt.want)
		}
	}
	if _, err := NormalizeMode("local"); err == nil {
		t.Error("NormalizeMode(local) should reject unknown modes")
	}
}
