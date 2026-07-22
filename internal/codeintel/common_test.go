package codeintel

import (
	"testing"
)

func TestParseFileLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    FileLine
		wantErr bool
	}{
		{
			name:  "simple file:line",
			input: "main.go:42",
			want:  FileLine{File: "main.go", Line: 42},
		},
		{
			name:  "path with directory",
			input: "internal/auth/token.go:100",
			want:  FileLine{File: "internal/auth/token.go", Line: 100},
		},
		{
			name:  "path with colon in directory name",
			input: "some:dir/file.go:10",
			want:  FileLine{File: "some:dir/file.go", Line: 10},
		},
		{
			name:    "no colon",
			input:   "main.go",
			wantErr: true,
		},
		{
			name:    "invalid line number",
			input:   "main.go:abc",
			wantErr: true,
		},
		{
			name:    "zero line number",
			input:   "main.go:0",
			wantErr: true,
		},
		{
			name:    "negative line number",
			input:   "main.go:-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFileLine(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFileLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseFileLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
