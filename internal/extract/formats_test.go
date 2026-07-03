package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// zipFile builds an in-memory zip from name->content entries (deterministic).
func zipFile(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPassthroughDataFormats(t *testing.T) {
	body := "id,name\n1,alpha\n2,beta\n"
	for _, name := range []string{"data.csv", "data.tsv", "app.log", "cfg.ini", "x.cfg", "y.conf"} {
		if !Supported(name) {
			t.Errorf("Supported(%q) = false", name)
		}
		got, err := Extract(name, []byte(body))
		if err != nil {
			t.Errorf("Extract(%q): %v", name, err)
		}
		if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
			t.Errorf("Extract(%q) lost content: %q", name, got)
		}
	}
	// A binary blob mislabelled as .csv is rejected (passthrough validates UTF-8).
	if _, err := Extract("bad.csv", []byte{0xff, 0xfe, 0x00}); err == nil {
		t.Error("non-UTF-8 .csv should error")
	}
}

func TestExtractPPTX(t *testing.T) {
	slide := func(text string) string {
		return `<?xml version="1.0"?><p:sld xmlns:p="p" xmlns:a="a"><p:cSld><p:spTree>` +
			`<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:spTree></p:cSld></p:sld>`
	}
	// slide10 must come after slide2 (numeric ordering, not lexical).
	data := zipFile(t, map[string]string{
		"ppt/slides/slide2.xml":  slide("Second quarter revenue"),
		"ppt/slides/slide10.xml": slide("Closing remarks"),
		"[Content_Types].xml":    "<Types/>",
	})
	got, err := Extract("deck.pptx", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Second quarter revenue") || !strings.Contains(got, "Closing remarks") {
		t.Fatalf("pptx text missing: %q", got)
	}
	if strings.Index(got, "Second quarter") > strings.Index(got, "Closing remarks") {
		t.Errorf("slides out of order: %q", got)
	}
	if isEncrypted := func() bool { _, e := Extract("x.pptx", oleMagic); return e == ErrEncrypted }(); !isEncrypted {
		t.Error("OLE-container .pptx should be reported encrypted")
	}
}

func TestExtractOpenDocument(t *testing.T) {
	content := `<?xml version="1.0"?><office:document-content xmlns:office="o" xmlns:text="t">` +
		`<office:body><office:text>` +
		`<text:h>Project Plan</text:h><text:p>Deliver the report by Friday.</text:p>` +
		`</office:text></office:body></office:document-content>`
	for _, name := range []string{"doc.odt", "sheet.ods", "deck.odp"} {
		data := zipFile(t, map[string]string{"content.xml": content, "mimetype": "application/vnd.oasis"})
		got, err := Extract(name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(got, "Project Plan") || !strings.Contains(got, "Deliver the report by Friday") {
			t.Errorf("%s text missing: %q", name, got)
		}
	}
	// Missing content.xml → clear error, no panic.
	if _, err := Extract("empty.odt", zipFile(t, map[string]string{"mimetype": "x"})); err == nil {
		t.Error("odt without content.xml should error")
	}
}

func TestExtractEPUB(t *testing.T) {
	data := zipFile(t, map[string]string{
		"mimetype":               "application/epub+zip",
		"OEBPS/ch1.xhtml":        "<html><body><h1>Chapter One</h1><p>It was a dark night.</p></body></html>",
		"OEBPS/ch2.xhtml":        "<html><body><p>The end.</p></body></html>",
		"META-INF/container.xml": "<container/>",
	})
	got, err := Extract("book.epub", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Chapter One", "dark night", "The end"} {
		if !strings.Contains(got, want) {
			t.Errorf("epub missing %q in %q", want, got)
		}
	}
}

func TestExtractIPYNB(t *testing.T) {
	nb := `{"cells":[
	  {"cell_type":"markdown","source":["# Title\n","some **text**"]},
	  {"cell_type":"code","source":"import os\nprint(os.getcwd())","outputs":[{"text":"SHOULD-NOT-APPEAR"}]},
	  {"cell_type":"raw","source":["ignored raw"]}
	]}`
	got, err := Extract("nb.ipynb", []byte(nb))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "# Title") || !strings.Contains(got, "import os") {
		t.Errorf("ipynb missing cell source: %q", got)
	}
	if strings.Contains(got, "SHOULD-NOT-APPEAR") {
		t.Errorf("ipynb must not include cell outputs: %q", got)
	}
	if _, err := Extract("bad.ipynb", []byte("not json")); err == nil {
		t.Error("invalid ipynb json should error")
	}
}

func TestExtractRTF(t *testing.T) {
	rtf := `{\rtf1\ansi{\fonttbl{\f0 Arial;}}{\colortbl;\red0\green0\blue0;}` +
		`{\*\generator Riched20}\f0\fs24 Caf\'e9 meeting\par Second line\tab tabbed}`
	got, err := Extract("note.rtf", []byte(rtf))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Café meeting") {
		t.Errorf("rtf text/hex-escape wrong: %q", got)
	}
	if !strings.Contains(got, "Second line") {
		t.Errorf("rtf \\par handling wrong: %q", got)
	}
	// Font/color tables and the ignorable \* destination must not leak.
	for _, junk := range []string{"Arial", "fonttbl", "colortbl", "Riched20", "red0"} {
		if strings.Contains(got, junk) {
			t.Errorf("rtf leaked non-content %q: %q", junk, got)
		}
	}
	if _, err := Extract("bad.rtf", []byte("plain, not rtf")); err == nil {
		t.Error("non-rtf should error")
	}
}

func TestExtractAllWrapsNewSingleDocs(t *testing.T) {
	// A non-archive document flows through ExtractAll as exactly one Doc.
	docs, err := ExtractAll("nb.ipynb", []byte(`{"cells":[{"cell_type":"code","source":"x=1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || !strings.Contains(docs[0].Text, "x=1") {
		t.Errorf("ExtractAll(ipynb) = %+v", docs)
	}
}
