package extract

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TOML (.toml)
// ---------------------------------------------------------------------------

func TestExtractTOML(t *testing.T) {
	t.Parallel()

	// Valid TOML content passes through.
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		input := []byte(`title = "Example"
[owner]
name = "Tom"
`)
		got, err := extractTOML(input)
		if err != nil {
			t.Fatalf("extractTOML: %v", err)
		}
		if !strings.Contains(got, "Example") {
			t.Errorf("extractTOML missing title, got: %q", got)
		}
	})

	// Non-UTF-8 content.
	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractTOML([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractTOML err = %v, want ErrNotText", err)
		}
	})

	// Empty content.
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		got, err := extractTOML([]byte{})
		if err != nil {
			t.Fatalf("extractTOML empty: %v", err)
		}
		if got != "" {
			t.Errorf("extractTOML empty = %q, want ''", got)
		}
	})

	// Verify registration via Extract.
	t.Run("via_extract", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("config.toml", []byte("key = \"val\"\n"))
		if err != nil {
			t.Fatalf("Extract toml: %v", err)
		}
		if !strings.Contains(got, "key = \"val\"") {
			t.Errorf("Extract toml = %q, want key-val", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Legal files (LICENSE, NOTICE, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY)
// ---------------------------------------------------------------------------

func TestExtractLegal(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		input := []byte("MIT License\n\nCopyright (c) 2024\n")
		got, err := extractLegal(input)
		if err != nil {
			t.Fatalf("extractLegal: %v", err)
		}
		if !strings.Contains(got, "MIT License") {
			t.Errorf("extractLegal = %q, want MIT License", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractLegal([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractLegal err = %v, want ErrNotText", err)
		}
	})

	// Verify registration for each known base name.
	t.Run("via_extract_license", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("LICENSE", []byte("Apache-2.0"))
		if err != nil {
			t.Fatalf("Extract LICENSE: %v", err)
		}
		if got != "Apache-2.0" {
			t.Errorf("Extract LICENSE = %q, want 'Apache-2.0'", got)
		}
	})

	t.Run("via_extract_changelog", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("CHANGELOG.md", []byte("# Changelog\n"))
		if err != nil {
			t.Fatalf("Extract CHANGELOG.md: %v", err)
		}
		if !strings.Contains(got, "Changelog") {
			t.Errorf("Extract CHANGELOG.md = %q, want Changelog", got)
		}
	})

	t.Run("via_extract_notice", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("NOTICE", []byte("Copyright notice"))
		if err != nil {
			t.Fatalf("Extract NOTICE: %v", err)
		}
		if got != "Copyright notice" {
			t.Errorf("Extract NOTICE = %q, want 'Copyright notice'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Dockerfile (Dockerfile / Containerfile)
// ---------------------------------------------------------------------------

func TestExtractDockerfile(t *testing.T) {
	t.Parallel()

	t.Run("with_instructions", func(t *testing.T) {
		t.Parallel()
		input := []byte("FROM golang:1.21\nRUN go build\nCMD [\"./app\"]\n")
		got, err := extractDockerfile(input)
		if err != nil {
			t.Fatalf("extractDockerfile: %v", err)
		}
		// Should have instruction annotations prepended.
		if !strings.Contains(got, "FROM\n") {
			t.Errorf("extractDockerfile missing FROM annotation: %q", got)
		}
		if !strings.Contains(got, "RUN\n") {
			t.Errorf("extractDockerfile missing RUN annotation: %q", got)
		}
		if !strings.Contains(got, "CMD\n") {
			t.Errorf("extractDockerfile missing CMD annotation: %q", got)
		}
		// Should also contain the original content.
		if !strings.Contains(got, "golang:1.21") {
			t.Errorf("extractDockerfile missing original content: %q", got)
		}
	})

	t.Run("no_instructions", func(t *testing.T) {
		t.Parallel()
		input := []byte("# comment only\n")
		got, err := extractDockerfile(input)
		if err != nil {
			t.Fatalf("extractDockerfile: %v", err)
		}
		if !strings.Contains(got, "# comment only") {
			t.Errorf("extractDockerfile = %q, want comment", got)
		}
	})

	t.Run("duplicate_instructions_deduped", func(t *testing.T) {
		t.Parallel()
		input := []byte("FROM alpine\nFROM ubuntu\nRUN echo hi\nRUN echo bye\n")
		got, err := extractDockerfile(input)
		if err != nil {
			t.Fatalf("extractDockerfile: %v", err)
		}
		// "FROM" should only appear once in annotations.
		// Check count of "FROM\n" (the annotation line): should be exactly 1.
		if strings.Count(got, "FROM\n") != 1 {
			t.Errorf("extractDockerfile should deduplicate FROM: %q", got)
		}
		if strings.Count(got, "RUN\n") != 1 {
			t.Errorf("extractDockerfile should deduplicate RUN: %q", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractDockerfile([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractDockerfile err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_dockerfile", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("Dockerfile", []byte("FROM scratch\n"))
		if err != nil {
			t.Fatalf("Extract Dockerfile: %v", err)
		}
		if !strings.Contains(got, "FROM") {
			t.Errorf("Extract Dockerfile = %q, want FROM", got)
		}
	})

	t.Run("via_extract_containerfile", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("Containerfile", []byte("FROM alpine\n"))
		if err != nil {
			t.Fatalf("Extract Containerfile: %v", err)
		}
		if !strings.Contains(got, "FROM") {
			t.Errorf("Extract Containerfile = %q, want FROM", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Makefile (Makefile / makefile / GNUmakefile)
// ---------------------------------------------------------------------------

func TestExtractMakefile(t *testing.T) {
	t.Parallel()

	t.Run("with_targets", func(t *testing.T) {
		t.Parallel()
		input := []byte("build:\n\tgo build\n\nclean:\n\trm -rf bin\n")
		got, err := extractMakefile(input)
		if err != nil {
			t.Fatalf("extractMakefile: %v", err)
		}
		if !strings.Contains(got, "target build") {
			t.Errorf("extractMakefile missing target build annotation: %q", got)
		}
		if !strings.Contains(got, "target clean") {
			t.Errorf("extractMakefile missing target clean annotation: %q", got)
		}
		if !strings.Contains(got, "go build") {
			t.Errorf("extractMakefile missing original content: %q", got)
		}
	})

	t.Run("no_targets", func(t *testing.T) {
		t.Parallel()
		input := []byte("# just a comment\nVAR = value\n")
		got, err := extractMakefile(input)
		if err != nil {
			t.Fatalf("extractMakefile: %v", err)
		}
		if !strings.Contains(got, "# just a comment") {
			t.Errorf("extractMakefile = %q, want comment", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractMakefile([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractMakefile err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_makefile", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("Makefile", []byte("all:\n\techo done\n"))
		if err != nil {
			t.Fatalf("Extract Makefile: %v", err)
		}
		if !strings.Contains(got, "target all") {
			t.Errorf("Extract Makefile = %q, want target all", got)
		}
	})
}

// ---------------------------------------------------------------------------
// AGENTS.md / CLAUDE.md
// ---------------------------------------------------------------------------

func TestExtractAgentsMD(t *testing.T) {
	t.Parallel()

	t.Run("headers_tools_skills", func(t *testing.T) {
		t.Parallel()
		input := []byte("# Title\n\n## Section\n\n- `tool_name`\n- [another_tool]\n\nThis uses the skill: build\n")
		got, err := extractAgentsMD(input)
		if err != nil {
			t.Fatalf("extractAgentsMD: %v", err)
		}
		if !strings.Contains(got, "header h1: Title") {
			t.Errorf("extractAgentsMD missing h1 annotation: %q", got)
		}
		if !strings.Contains(got, "header h2: Section") {
			t.Errorf("extractAgentsMD missing h2 annotation: %q", got)
		}
		if !strings.Contains(got, "tool tool_name") {
			t.Errorf("extractAgentsMD missing tool annotation: %q", got)
		}
		if !strings.Contains(got, "tool another_tool") {
			t.Errorf("extractAgentsMD missing another_tool: %q", got)
		}
		if !strings.Contains(got, "ref build") {
			t.Errorf("extractAgentsMD missing skill ref: %q", got)
		}
		if !strings.Contains(got, "# Title") {
			t.Errorf("extractAgentsMD missing original content: %q", got)
		}
	})

	t.Run("no_annotations", func(t *testing.T) {
		t.Parallel()
		input := []byte("plain text without headers or tools\n")
		got, err := extractAgentsMD(input)
		if err != nil {
			t.Fatalf("extractAgentsMD: %v", err)
		}
		if got != "plain text without headers or tools\n" {
			t.Errorf("extractAgentsMD = %q, want plain text", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractAgentsMD([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractAgentsMD err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_agentsmd", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("AGENTS.md", []byte("# Overview\n- `my-tool`\n"))
		if err != nil {
			t.Fatalf("Extract AGENTS.md: %v", err)
		}
		if !strings.Contains(got, "header h1: Overview") {
			t.Errorf("Extract AGENTS.md = %q, want header annotation", got)
		}
		if !strings.Contains(got, "tool my-tool") {
			t.Errorf("Extract AGENTS.md missing tool: %q", got)
		}
	})

	t.Run("via_extract_claudemd", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("CLAUDE.md", []byte("# Instructions\n"))
		if err != nil {
			t.Fatalf("Extract CLAUDE.md: %v", err)
		}
		if !strings.Contains(got, "header h1: Instructions") {
			t.Errorf("Extract CLAUDE.md = %q, want header annotation", got)
		}
	})
}

// ---------------------------------------------------------------------------
// reStructuredText (.rst)
// ---------------------------------------------------------------------------

func TestExtractRST(t *testing.T) {
	t.Parallel()

	t.Run("with_directives", func(t *testing.T) {
		t.Parallel()
		input := []byte("Heading\n=======\n\n.. code-block:: go\n\n    fmt.Println(\"hi\")\n\nBody text\n")
		got, err := extractRST(input)
		if err != nil {
			t.Fatalf("extractRST: %v", err)
		}
		if !strings.Contains(got, "Heading") {
			t.Errorf("extractRST missing heading: %q", got)
		}
		if strings.Contains(got, "code-block") {
			t.Errorf("extractRST should strip directives: %q", got)
		}
		if !strings.Contains(got, "Body text") {
			t.Errorf("extractRST missing body: %q", got)
		}
	})

	t.Run("no_directives", func(t *testing.T) {
		t.Parallel()
		input := []byte("Just a paragraph.\n\nAnother one.\n")
		got, err := extractRST(input)
		if err != nil {
			t.Fatalf("extractRST: %v", err)
		}
		if !strings.Contains(got, "Just a paragraph") {
			t.Errorf("extractRST = %q, want paragraph", got)
		}
	})

	t.Run("all_stripped_fallback", func(t *testing.T) {
		t.Parallel()
		// If everything is stripped, it falls back to raw text.
		input := []byte(".. comment\n\n  continuation\n")
		got, err := extractRST(input)
		if err != nil {
			t.Fatalf("extractRST: %v", err)
		}
		if !strings.Contains(got, ".. comment") {
			t.Errorf("extractRST fallback should keep raw text: %q", got)
		}
	})

	t.Run("bare_comment", func(t *testing.T) {
		t.Parallel()
		input := []byte("..\n  indented continuation\n\nReal text\n")
		got, err := extractRST(input)
		if err != nil {
			t.Fatalf("extractRST: %v", err)
		}
		if strings.Contains(got, "indented continuation") {
			t.Errorf("extractRST should strip bare comment continuation: %q", got)
		}
		if !strings.Contains(got, "Real text") {
			t.Errorf("extractRST missing real text: %q", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractRST([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractRST err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("doc.rst", []byte("Hello\n=====\n\nWorld\n"))
		if err != nil {
			t.Fatalf("Extract rst: %v", err)
		}
		if !strings.Contains(got, "Hello") {
			t.Errorf("Extract rst = %q, want Hello", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Vue Single File Component (.vue)
// ---------------------------------------------------------------------------

func TestExtractVue(t *testing.T) {
	t.Parallel()

	t.Run("script_and_template", func(t *testing.T) {
		t.Parallel()
		input := []byte("<template>\n  <div>Hello World</div>\n</template>\n\n<script>\nexport default {\n  name: 'Test'\n}\n</script>\n")
		got, err := extractVue(input)
		if err != nil {
			t.Fatalf("extractVue: %v", err)
		}
		if !strings.Contains(got, "Hello World") {
			t.Errorf("extractVue missing template content: %q", got)
		}
		if !strings.Contains(got, "name: 'Test'") {
			t.Errorf("extractVue missing script content: %q", got)
		}
	})

	t.Run("script_setup", func(t *testing.T) {
		t.Parallel()
		input := []byte("<script setup lang=\"ts\">\nconst msg = ref('hi')\n</script>\n")
		got, err := extractVue(input)
		if err != nil {
			t.Fatalf("extractVue: %v", err)
		}
		if !strings.Contains(got, "const msg") {
			t.Errorf("extractVue missing setup script: %q", got)
		}
	})

	t.Run("template_only", func(t *testing.T) {
		t.Parallel()
		input := []byte("<template><p>Just template</p></template>\n")
		got, err := extractVue(input)
		if err != nil {
			t.Fatalf("extractVue: %v", err)
		}
		if !strings.Contains(got, "Just template") {
			t.Errorf("extractVue missing template: %q", got)
		}
	})

	t.Run("no_matches_fallback", func(t *testing.T) {
		t.Parallel()
		input := []byte("<style>\nbody { color: red; }\n</style>\n")
		got, err := extractVue(input)
		if err != nil {
			t.Fatalf("extractVue: %v", err)
		}
		if !strings.Contains(got, "color: red") {
			t.Errorf("extractVue fallback should keep raw: %q", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractVue([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractVue err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("component.vue", []byte("<template><span>hi</span></template>\n"))
		if err != nil {
			t.Fatalf("Extract vue: %v", err)
		}
		if !strings.Contains(got, "hi") {
			t.Errorf("Extract vue = %q, want 'hi'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Protocol Buffers (.proto)
// ---------------------------------------------------------------------------

func TestExtractProto(t *testing.T) {
	t.Parallel()

	t.Run("service_and_message", func(t *testing.T) {
		t.Parallel()
		input := []byte("service Greeter {\n  rpc SayHello (HelloRequest) returns (HelloReply);\n}\n\nmessage HelloRequest {\n  string name = 1;\n}\n\nmessage HelloReply {\n  string message = 1;\n}\n")
		got, err := extractProto(input)
		if err != nil {
			t.Fatalf("extractProto: %v", err)
		}
		if !strings.Contains(got, "service Greeter") {
			t.Errorf("extractProto missing service annotation: %q", got)
		}
		if !strings.Contains(got, "message HelloRequest") {
			t.Errorf("extractProto missing message annotation: %q", got)
		}
		if !strings.Contains(got, "message HelloReply") {
			t.Errorf("extractProto missing message HelloReply: %q", got)
		}
		if !strings.Contains(got, "rpc SayHello") {
			t.Errorf("extractProto missing original content: %q", got)
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		t.Parallel()
		input := []byte("syntax = \"proto3\";\n")
		got, err := extractProto(input)
		if err != nil {
			t.Fatalf("extractProto: %v", err)
		}
		if !strings.Contains(got, "syntax") {
			t.Errorf("extractProto = %q, want syntax line", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractProto([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractProto err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("api.proto", []byte("service Svc {}\n"))
		if err != nil {
			t.Fatalf("Extract proto: %v", err)
		}
		if !strings.Contains(got, "service Svc") {
			t.Errorf("Extract proto = %q, want service annotation", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Jinja templates (.jinja / .jinja2 / .j2)
// ---------------------------------------------------------------------------

func TestExtractJinja(t *testing.T) {
	t.Parallel()

	t.Run("all_syntax_types", func(t *testing.T) {
		t.Parallel()
		input := []byte("Hello {{ name }},\n{% if show %}\n\n{# comment #}\n\nVisible\n{% endif %}\n")
		got, err := extractJinja(input)
		if err != nil {
			t.Fatalf("extractJinja: %v", err)
		}
		if strings.Contains(got, "{{") {
			t.Errorf("extractJinja should strip var syntax: %q", got)
		}
		if strings.Contains(got, "{%") {
			t.Errorf("extractJinja should strip stmt syntax: %q", got)
		}
		if strings.Contains(got, "{#") {
			t.Errorf("extractJinja should strip comment syntax: %q", got)
		}
		if !strings.Contains(got, "Hello") {
			t.Errorf("extractJinja missing text: %q", got)
		}
		if !strings.Contains(got, "Visible") {
			t.Errorf("extractJinja missing Visible: %q", got)
		}
	})

	t.Run("no_jinja_syntax", func(t *testing.T) {
		t.Parallel()
		input := []byte("plain text content\n")
		got, err := extractJinja(input)
		if err != nil {
			t.Fatalf("extractJinja: %v", err)
		}
		if got != "plain text content" {
			t.Errorf("extractJinja = %q, want 'plain text content'", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractJinja([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractJinja err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_jinja", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("template.jinja", []byte("{{ var }}\n"))
		if err != nil {
			t.Fatalf("Extract jinja: %v", err)
		}
		if strings.Contains(got, "{{") {
			t.Errorf("Extract jinja should strip syntax: %q", got)
		}
	})

	t.Run("via_extract_jinja2", func(t *testing.T) {
		t.Parallel()
		_, err := Extract("template.jinja2", []byte("hi"))
		if err != nil {
			t.Fatalf("Extract jinja2: %v", err)
		}
	})

	t.Run("via_extract_j2", func(t *testing.T) {
		t.Parallel()
		_, err := Extract("template.j2", []byte("hi"))
		if err != nil {
			t.Fatalf("Extract j2: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Handlebars (.hbs / .handlebars)
// ---------------------------------------------------------------------------

func TestExtractHandlebars(t *testing.T) {
	t.Parallel()

	t.Run("triple_and_double", func(t *testing.T) {
		t.Parallel()
		input := []byte("Hello {{{ name }}}!\n{{#if show}}\n  Visible\n{{/if}}\n{{> partial}}\n")
		got, err := extractHandlebars(input)
		if err != nil {
			t.Fatalf("extractHandlebars: %v", err)
		}
		if strings.Contains(got, "{{{") {
			t.Errorf("extractHandlebars should strip triple: %q", got)
		}
		if strings.Contains(got, "{{#if") {
			t.Errorf("extractHandlebars should strip block: %q", got)
		}
		if strings.Contains(got, "{{/if") {
			t.Errorf("extractHandlebars should strip end block: %q", got)
		}
		if strings.Contains(got, "{{>") {
			t.Errorf("extractHandlebars should strip partial: %q", got)
		}
		if !strings.Contains(got, "Hello") {
			t.Errorf("extractHandlebars missing Hello: %q", got)
		}
		if !strings.Contains(got, "Visible") {
			t.Errorf("extractHandlebars missing Visible: %q", got)
		}
	})

	t.Run("no_handlebars", func(t *testing.T) {
		t.Parallel()
		input := []byte("plain text\n")
		got, err := extractHandlebars(input)
		if err != nil {
			t.Fatalf("extractHandlebars: %v", err)
		}
		if got != "plain text" {
			t.Errorf("extractHandlebars = %q, want 'plain text'", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractHandlebars([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractHandlebars err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_hbs", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("template.hbs", []byte("{{var}} text\n"))
		if err != nil {
			t.Fatalf("Extract hbs: %v", err)
		}
		if strings.Contains(got, "{{") {
			t.Errorf("Extract hbs should strip syntax: %q", got)
		}
		if !strings.Contains(got, "text") {
			t.Errorf("Extract hbs missing text: %q", got)
		}
	})

	t.Run("via_extract_handlebars", func(t *testing.T) {
		t.Parallel()
		_, err := Extract("template.handlebars", []byte("hi"))
		if err != nil {
			t.Fatalf("Extract handlebars: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// GraphQL (.graphql / .gql)
// ---------------------------------------------------------------------------

func TestExtractGraphQL(t *testing.T) {
	t.Parallel()

	t.Run("type_definitions", func(t *testing.T) {
		t.Parallel()
		input := []byte("type Query {\n  users: [User!]!\n}\n\ntype User {\n  id: ID!\n  name: String!\n}\n\ninput UserInput {\n  name: String!\n}\n\nenum Role {\n  ADMIN\n  USER\n}\n")
		got, err := extractGraphQL(input)
		if err != nil {
			t.Fatalf("extractGraphQL: %v", err)
		}
		if !strings.Contains(got, "type Query") {
			t.Errorf("extractGraphQL missing type Query: %q", got)
		}
		if !strings.Contains(got, "type User") {
			t.Errorf("extractGraphQL missing type User: %q", got)
		}
		if !strings.Contains(got, "input UserInput") {
			t.Errorf("extractGraphQL missing input: %q", got)
		}
		if !strings.Contains(got, "enum Role") {
			t.Errorf("extractGraphQL missing enum: %q", got)
		}
		if !strings.Contains(got, "id: ID!") {
			t.Errorf("extractGraphQL missing original content: %q", got)
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		t.Parallel()
		input := []byte("# just a comment\n")
		got, err := extractGraphQL(input)
		if err != nil {
			t.Fatalf("extractGraphQL: %v", err)
		}
		if !strings.Contains(got, "# just a comment") {
			t.Errorf("extractGraphQL = %q, want comment", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractGraphQL([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractGraphQL err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_graphql", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("schema.graphql", []byte("type Query {}\n"))
		if err != nil {
			t.Fatalf("Extract graphql: %v", err)
		}
		if !strings.Contains(got, "type Query") {
			t.Errorf("Extract graphql = %q, want type Query", got)
		}
	})

	t.Run("via_extract_gql", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("schema.gql", []byte("type Mutation {}\n"))
		if err != nil {
			t.Fatalf("Extract gql: %v", err)
		}
		if !strings.Contains(got, "type Mutation") {
			t.Errorf("Extract gql = %q, want type Mutation", got)
		}
	})
}

// ---------------------------------------------------------------------------
// extractKeyword helper (graphql.go)
// ---------------------------------------------------------------------------

func TestExtractKeyword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		match string
		name  string
		want  string
	}{
		{"type Foo", "Foo", "type"},
		{"input Bar", "Bar", "input"},
		{"enum Baz", "Baz", "enum"},
		{"query Q", "Q", "query"},
		{"mutation M", "M", "mutation"},
		{"unknown X", "X", "type"}, // fallback to "type"
	}

	for _, tc := range cases {
		t.Run(tc.match, func(t *testing.T) {
			got := extractKeyword(tc.match, tc.name)
			if got != tc.want {
				t.Errorf("extractKeyword(%q, %q) = %q, want %q", tc.match, tc.name, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// XML (.xml)
// ---------------------------------------------------------------------------

func TestExtractXML(t *testing.T) {
	t.Parallel()

	t.Run("well_formed", func(t *testing.T) {
		t.Parallel()
		input := []byte("<root>\n  <item>Hello</item>\n  <item>World</item>\n</root>\n")
		got, err := extractXML(input)
		if err != nil {
			t.Fatalf("extractXML: %v", err)
		}
		if !strings.Contains(got, "Hello") {
			t.Errorf("extractXML missing Hello: %q", got)
		}
		if !strings.Contains(got, "World") {
			t.Errorf("extractXML missing World: %q", got)
		}
	})

	t.Run("non_well_formed", func(t *testing.T) {
		t.Parallel()
		// Bad XML that falls back to regex tag-stripping.
		input := []byte("<root><item>Hello</item></splat>\n")
		got, err := extractXML(input)
		if err != nil {
			t.Fatalf("extractXML: %v", err)
		}
		if !strings.Contains(got, "Hello") {
			t.Errorf("extractXML regex fallback missing Hello: %q", got)
		}
	})

	t.Run("comments_stripped", func(t *testing.T) {
		t.Parallel()
		input := []byte("<doc><!-- comment --><p>text</p></doc>")
		got, err := extractXML(input)
		if err != nil {
			t.Fatalf("extractXML: %v", err)
		}
		if strings.Contains(got, "comment") {
			t.Errorf("extractXML should strip comments: %q", got)
		}
		if !strings.Contains(got, "text") {
			t.Errorf("extractXML missing text: %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		got, err := extractXML([]byte{})
		if err != nil {
			t.Fatalf("extractXML empty: %v", err)
		}
		if got != "" {
			t.Errorf("extractXML empty = %q, want ''", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractXML([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractXML err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("data.xml", []byte("<doc>test</doc>"))
		if err != nil {
			t.Fatalf("Extract xml: %v", err)
		}
		if !strings.Contains(got, "test") {
			t.Errorf("Extract xml = %q, want 'test'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Lockfiles (package-lock.json, yarn.lock, poetry.lock, Gemfile.lock, go.sum, Cargo.lock)
// ---------------------------------------------------------------------------

func TestExtractLockfile(t *testing.T) {
	t.Parallel()

	// JSON lockfile (package-lock.json)
	t.Run("json_lock", func(t *testing.T) {
		t.Parallel()
		input := []byte(`{
  "name": "my-pkg",
  "version": "1.0.0",
  "packages": {
    "": {
      "name": "my-pkg",
      "version": "1.0.0"
    },
    "node_modules/dep": {
      "name": "dep",
      "version": "2.0.0"
    }
  }
}
`)
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile json: %v", err)
		}
		if !strings.Contains(got, "my-pkg@1.0.0") {
			t.Errorf("extractLockfile json missing my-pkg: %q", got)
		}
		if !strings.Contains(got, "dep@2.0.0") {
			t.Errorf("extractLockfile json missing dep: %q", got)
		}
	})

	// Yarn lockfile
	t.Run("yarn_lock", func(t *testing.T) {
		t.Parallel()
		input := []byte("# THIS IS AN AUTOGENERATED FILE\n\nfoo@^1.0.0:\n  version 1.0.0\n\nbar@^2.0.0:\n  version 2.0.0\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile yarn: %v", err)
		}
		if !strings.Contains(got, "foo@^1.0.0") {
			t.Errorf("extractLockfile yarn missing foo: %q", got)
		}
		if !strings.Contains(got, "bar@^2.0.0") {
			t.Errorf("extractLockfile yarn missing bar: %q", got)
		}
	})

	// go.sum
	t.Run("gosum", func(t *testing.T) {
		t.Parallel()
		input := []byte("github.com/foo/bar v1.0.0 h1:abc=\ngithub.com/baz/qux v2.0.0 h2:def=\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile gosum: %v", err)
		}
		if !strings.Contains(got, "github.com/foo/bar@v1.0.0") {
			t.Errorf("extractLockfile gosum missing foo/bar: %q", got)
		}
		if !strings.Contains(got, "github.com/baz/qux@v2.0.0") {
			t.Errorf("extractLockfile gosum missing baz/qux: %q", got)
		}
	})

	// Cargo.lock
	t.Run("cargo_lock", func(t *testing.T) {
		t.Parallel()
		input := []byte("[[package]]\nname = \"serde\"\nversion = \"1.0.0\"\n\n[[package]]\nname = \"tokio\"\nversion = \"1.35.0\"\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile cargo: %v", err)
		}
		if !strings.Contains(got, "serde@1.0.0") {
			t.Errorf("extractLockfile cargo missing serde: %q", got)
		}
		if !strings.Contains(got, "tokio@1.35.0") {
			t.Errorf("extractLockfile cargo missing tokio: %q", got)
		}
	})

	// Poetry.lock
	t.Run("poetry_lock", func(t *testing.T) {
		t.Parallel()
		input := []byte("# Poetry lockfile\n[[package]]\nname = \"requests\"\nversion = \"2.31.0\"\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile poetry: %v", err)
		}
		if !strings.Contains(got, "requests@2.31.0") {
			t.Errorf("extractLockfile poetry missing requests: %q", got)
		}
	})

	// Gemfile.lock
	t.Run("gemfile_lock", func(t *testing.T) {
		t.Parallel()
		input := []byte("GEM\n  remote: https://rubygems.org/\n  specs:\n    rake (13.0.6)\n    rails (7.0.0)\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n  rails\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile gemfile: %v", err)
		}
		if !strings.Contains(got, "rake@13.0.6") {
			t.Errorf("extractLockfile gemfile missing rake: %q", got)
		}
		if !strings.Contains(got, "rails@7.0.0") {
			t.Errorf("extractLockfile gemfile missing rails: %q", got)
		}
	})

	// Unknown format falls back to raw text.
	t.Run("unknown_format", func(t *testing.T) {
		t.Parallel()
		input := []byte("some random text\n")
		got, err := extractLockfile(input)
		if err != nil {
			t.Fatalf("extractLockfile unknown: %v", err)
		}
		if !strings.Contains(got, "some random text") {
			t.Errorf("extractLockfile unknown = %q, want raw", got)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		_, err := extractLockfile([]byte{0xff, 0xfe, 0xfd})
		if !errors.Is(err, ErrNotText) {
			t.Errorf("extractLockfile err = %v, want ErrNotText", err)
		}
	})

	t.Run("via_extract_package_lock_json", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("package-lock.json", []byte("{\n  \"name\": \"pkg\",\n  \"version\": \"1.0.0\"\n}\n"))
		if err != nil {
			t.Fatalf("Extract package-lock.json: %v", err)
		}
		if !strings.Contains(got, "pkg@1.0.0") {
			t.Errorf("Extract package-lock.json = %q, want pkg@1.0.0", got)
		}
	})

	t.Run("via_extract_yarn_lock", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("yarn.lock", []byte("# yarn\nfoo@1.0.0:\n  version 1.0.0\n"))
		if err != nil {
			t.Fatalf("Extract yarn.lock: %v", err)
		}
		if !strings.Contains(got, "foo@1.0.0") {
			t.Errorf("Extract yarn.lock = %q, want foo@1.0.0", got)
		}
	})

	t.Run("via_extract_go_sum", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("go.sum", []byte("mod v1.0.0 h1:abc=\n"))
		if err != nil {
			t.Fatalf("Extract go.sum: %v", err)
		}
		if !strings.Contains(got, "mod@v1.0.0") {
			t.Errorf("Extract go.sum = %q, want mod@v1.0.0", got)
		}
	})

	t.Run("via_extract_cargo_lock", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("Cargo.lock", []byte("[[package]]\nname = \"crate\"\nversion = \"0.1.0\"\n"))
		if err != nil {
			t.Fatalf("Extract Cargo.lock: %v", err)
		}
		if !strings.Contains(got, "crate@0.1.0") {
			t.Errorf("Extract Cargo.lock = %q, want crate@0.1.0", got)
		}
	})

	t.Run("via_extract_poetry_lock", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("poetry.lock", []byte("[[package]]\nname = \"lib\"\nversion = \"1.0.0\"\n"))
		if err != nil {
			t.Fatalf("Extract poetry.lock: %v", err)
		}
		if !strings.Contains(got, "lib@1.0.0") {
			t.Errorf("Extract poetry.lock = %q, want lib@1.0.0", got)
		}
	})

	t.Run("via_extract_gemfile_lock", func(t *testing.T) {
		t.Parallel()
		got, err := Extract("Gemfile.lock", []byte("GEM\n  specs:\n    gem (1.0.0)\n"))
		if err != nil {
			t.Fatalf("Extract Gemfile.lock: %v", err)
		}
		if !strings.Contains(got, "gem@1.0.0") {
			t.Errorf("Extract Gemfile.lock = %q, want gem@1.0.0", got)
		}
	})
}

// ---------------------------------------------------------------------------
// detectLockFormat (lockfiles.go)
// ---------------------------------------------------------------------------

func TestDetectLockFormat(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat(""); got != "" {
			t.Errorf("detectLockFormat('') = %q, want ''", got)
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("{"); got != "json" {
			t.Errorf("detectLockFormat('{') = %q, want 'json'", got)
		}
		if got := detectLockFormat("{ \"name\": \"x\" }"); got != "json" {
			t.Errorf("detectLockFormat(json) = %q, want 'json'", got)
		}
	})

	t.Run("yarn_hash", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("# yarn.lock"); got != "yarn" {
			t.Errorf("detectLockFormat('# yarn') = %q, want 'yarn'", got)
		}
	})

	t.Run("yarn_entry", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("foo@^1.0.0:"); got != "yarn" {
			t.Errorf("detectLockFormat('foo@^1.0.0:') = %q, want 'yarn'", got)
		}
	})

	t.Run("gosum_version", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("github.com/foo v1.0.0 h1:abc"); got != "gosum" {
			t.Errorf("detectLockFormat(gosum ver) = %q, want 'gosum'", got)
		}
	})

	t.Run("gosum_two_fields", func(t *testing.T) {
		t.Parallel()
		// When version doesn't start with 'v', needs >=2 spaces (3 fields)
		if got := detectLockFormat("github.com/foo v1.0.0"); got != "gosum" {
			t.Errorf("detectLockFormat(gosum 3 fields) = %q, want 'gosum'", got)
		}
	})

	t.Run("cargo", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("[[package]]\nname = \"crate\""); got != "cargo" {
			t.Errorf("detectLockFormat(cargo) = %q, want 'cargo'", got)
		}
	})

	t.Run("poetry", func(t *testing.T) {
		t.Parallel()
		// Detection requires [[package]] on first line AND "poetry" in the
		// first 5 non-blank lines.
		inputWithPoetry := "[[package]]\nname = \"pkg\"\nversion = \"1.0\"\nPoetry lockfile"
		if got := detectLockFormat(inputWithPoetry); got != "poetry" {
			t.Errorf("detectLockFormat(poetry) = %q, want 'poetry'", got)
		}
	})

	t.Run("gemfile", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("GEM"); got != "gemfile" {
			t.Errorf("detectLockFormat('GEM') = %q, want 'gemfile'", got)
		}
		if got := detectLockFormat("PATH"); got != "gemfile" {
			t.Errorf("detectLockFormat('PATH') = %q, want 'gemfile'", got)
		}
		if got := detectLockFormat("PLATFORMS"); got != "gemfile" {
			t.Errorf("detectLockFormat('PLATFORMS') = %q, want 'gemfile'", got)
		}
		if got := detectLockFormat("DEPENDENCIES"); got != "gemfile" {
			t.Errorf("detectLockFormat('DEPENDENCIES') = %q, want 'gemfile'", got)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		if got := detectLockFormat("some random content"); got != "" {
			t.Errorf("detectLockFormat(random) = %q, want ''", got)
		}
	})
}
