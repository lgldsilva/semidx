package extract

import "testing"

func TestNewDecompilerDisabledByDefault(t *testing.T) {
	t.Setenv("SEMIDX_JAVA_DECOMPILER", "")
	if newDecompiler() != nil {
		t.Error("decompiler should be nil when SEMIDX_JAVA_DECOMPILER is unset")
	}
}

func TestDecompilerRunsConfiguredCommand(t *testing.T) {
	// `echo` ignores the class bytes and prints the temp file path — enough to
	// prove the configured command runs and its stdout is captured.
	t.Setenv("SEMIDX_JAVA_DECOMPILER", "echo decompiled:")
	d := newDecompiler()
	if d == nil {
		t.Fatal("expected a decompiler")
	}
	out, ok := d.decompile([]byte{0xCA, 0xFE, 0xBA, 0xBE})
	if !ok || out == "" {
		t.Fatalf("decompile = %q, ok=%v; want captured stdout", out, ok)
	}
}

func TestDecompilerFailsGracefully(t *testing.T) {
	// A command that does not exist must degrade to (\"\", false), not error out.
	t.Setenv("SEMIDX_JAVA_DECOMPILER", "/nonexistent/decompiler-binary-xyz")
	d := newDecompiler()
	if d == nil {
		t.Fatal("expected a decompiler")
	}
	if out, ok := d.decompile([]byte{0xCA, 0xFE, 0xBA, 0xBE}); ok || out != "" {
		t.Errorf("missing tool should degrade to empty/false, got %q/%v", out, ok)
	}
}
