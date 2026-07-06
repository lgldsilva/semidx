package imports

import (
	"testing"
)

func TestAnalyze_Go(t *testing.T) {
	t.Parallel()

	t.Run("local imports", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/pkg/client"
)

func main() {}
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		want := []string{"internal/chunker/", "pkg/client/"}
		assertSlice(t, got, want)
	})

	t.Run("stdlib and third-party excluded", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	"fmt"
	"os"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/spf13/cobra"
)
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		want := []string{"internal/chunker/"}
		assertSlice(t, got, want)
	})

	t.Run("dot import skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	. "github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/pkg/client"
)
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		want := []string{"pkg/client/"}
		assertSlice(t, got, want)
	})

	t.Run("self-reference root skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	"github.com/lgldsilva/semidx"
	"github.com/lgldsilva/semidx/internal/chunker"
)
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		want := []string{"internal/chunker/"}
		assertSlice(t, got, want)
	})

	t.Run("malformed Go returns nil", func(t *testing.T) {
		t.Parallel()
		src := []byte(`this is not valid Go code at all`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("only stdlib returns nil", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	"fmt"
	"strings"
)
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestAnalyze_Java(t *testing.T) {
	t.Parallel()

	t.Run("simple import", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import com.foo.bar.Baz;`)
		got := Analyze("Test.java", src, "")
		want := []string{"com/foo/bar/"}
		assertSlice(t, got, want)
	})

	t.Run("multiple imports", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import com.foo.bar.Baz;
import com.foo.baz.Quux;
import java.util.List;
import javax.servlet.http.HttpServlet;
import org.junit.Test;
`)
		got := Analyze("Test.java", src, "")
		want := []string{"com/foo/bar/", "com/foo/baz/"}
		assertSlice(t, got, want)
	})

	t.Run("static import", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import static com.foo.bar.Constants.MAX_SIZE;`)
		got := Analyze("Test.java", src, "")
		want := []string{"com/foo/bar/"}
		assertSlice(t, got, want)
	})

	t.Run("Kotlin extension", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import com.foo.bar.Baz`)
		got := Analyze("Test.kt", src, "")
		want := []string{"com/foo/bar/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_TypeScript(t *testing.T) {
	t.Parallel()

	t.Run("relative import from same directory", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import { x } from './utils';`)
		got := Analyze("src/foo/bar.ts", src, "")
		want := []string{"src/foo/utils/"}
		assertSlice(t, got, want)
	})

	t.Run("relative import from parent directory", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import { x } from '../other/helper';`)
		got := Analyze("src/foo/bar.ts", src, "")
		want := []string{"src/other/helper/"}
		assertSlice(t, got, want)
	})

	t.Run("require with relative path", func(t *testing.T) {
		t.Parallel()
		src := []byte(`const x = require('./utils');`)
		got := Analyze("src/foo/bar.ts", src, "")
		want := []string{"src/foo/utils/"}
		assertSlice(t, got, want)
	})

	t.Run("dynamic import", func(t *testing.T) {
		t.Parallel()
		src := []byte(`const x = import('./utils');`)
		got := Analyze("src/foo/bar.ts", src, "")
		want := []string{"src/foo/utils/"}
		assertSlice(t, got, want)
	})

	t.Run("@ alias", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import {x} from '@/components/Foo';`)
		got := Analyze("src/app/page.tsx", src, "")
		want := []string{"components/"}
		assertSlice(t, got, want)
	})

	t.Run("relative import resolves to target directory", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import {x} from './utils';`)
		got := Analyze("src/app/page.tsx", src, "")
		want := []string{"src/app/utils/"}
		assertSlice(t, got, want)
	})

	t.Run("parent relative import resolves correctly", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import {x} from '../lib';`)
		got := Analyze("src/app/page.tsx", src, "")
		want := []string{"src/lib/"}
		assertSlice(t, got, want)
	})

	t.Run("npm packages and scoped packages excluded", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import React from 'react';
import { foo } from '@scope/package';
import { readFile } from 'node:fs';
`)
		got := Analyze("src/foo/bar.ts", src, "")
		if got != nil {
			t.Errorf("expected nil for external packages, got %v", got)
		}
	})

	t.Run("JSX extension", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import { x } from './utils';`)
		got := Analyze("src/foo/bar.jsx", src, "")
		want := []string{"src/foo/utils/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_Markdown(t *testing.T) {
	t.Parallel()

	t.Run("local link", func(t *testing.T) {
		t.Parallel()
		src := []byte(`[link](docs/api.md)`)
		got := Analyze("README.md", src, "")
		want := []string{"docs/"}
		assertSlice(t, got, want)
	})

	t.Run("external URL skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`[link](https://example.com)`)
		got := Analyze("README.md", src, "")
		if got != nil {
			t.Errorf("expected nil for external URL, got %v", got)
		}
	})

	t.Run("anchor-only link skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`[link](#anchor)`)
		got := Analyze("README.md", src, "")
		if got != nil {
			t.Errorf("expected nil for anchor link, got %v", got)
		}
	})

	t.Run("MDX extension", func(t *testing.T) {
		t.Parallel()
		src := []byte(`[link](docs/api.md)`)
		got := Analyze("README.mdx", src, "")
		want := []string{"docs/"}
		assertSlice(t, got, want)
	})

	t.Run("RST extension", func(t *testing.T) {
		t.Parallel()
		src := []byte(`[link](docs/api.md)`)
		got := Analyze("README.rst", src, "")
		want := []string{"docs/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_Python(t *testing.T) {
	t.Parallel()

	t.Run("from import", func(t *testing.T) {
		t.Parallel()
		src := []byte(`from foo.bar import Baz`)
		got := Analyze("module.py", src, "")
		want := []string{"foo/bar/"}
		assertSlice(t, got, want)
	})

	t.Run("direct import", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import foo.bar`)
		got := Analyze("module.py", src, "")
		want := []string{"foo/bar/"}
		assertSlice(t, got, want)
	})

	t.Run("stdlib filtered", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import os
import sys
from collections import OrderedDict
from foo.bar import Baz
`)
		got := Analyze("module.py", src, "")
		want := []string{"foo/bar/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_Rust(t *testing.T) {
	t.Parallel()

	t.Run("use with module path", func(t *testing.T) {
		t.Parallel()
		src := []byte(`use my_crate::module::Type;`)
		got := Analyze("lib.rs", src, "")
		want := []string{"my_crate/module/"}
		assertSlice(t, got, want)
	})

	t.Run("extern crate", func(t *testing.T) {
		t.Parallel()
		src := []byte(`extern crate my_crate;`)
		got := Analyze("lib.rs", src, "")
		want := []string{"my_crate/"}
		assertSlice(t, got, want)
	})

	t.Run("extern crate std skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`extern crate std;
extern crate core;
extern crate alloc;
extern crate my_crate;
`)
		got := Analyze("lib.rs", src, "")
		want := []string{"my_crate/"}
		assertSlice(t, got, want)
	})

	t.Run("stdlib skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`use std::collections::HashMap;
use core::fmt;
use alloc::vec;
use proc_macro::TokenStream;
use my_crate::module::Type;
`)
		got := Analyze("lib.rs", src, "")
		want := []string{"my_crate/module/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_C(t *testing.T) {
	t.Parallel()

	t.Run("quoted include", func(t *testing.T) {
		t.Parallel()
		src := []byte(`#include "foo/bar.h"`)
		got := Analyze("main.c", src, "")
		want := []string{"foo/"}
		assertSlice(t, got, want)
	})

	t.Run("angle-bracket include skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte("#include <stdio.h>\n#include \"foo/bar.h\"")
		got := Analyze("main.c", src, "")
		want := []string{"foo/"}
		assertSlice(t, got, want)
	})

	t.Run("header in same directory skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`#include "local.h"`)
		got := Analyze("main.c", src, "")
		if got != nil {
			t.Errorf("expected nil for same-directory header, got %v", got)
		}
	})

	t.Run("CPP extension", func(t *testing.T) {
		t.Parallel()
		src := []byte(`#include "project/config.hpp"`)
		got := Analyze("main.cpp", src, "")
		want := []string{"project/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_Ruby(t *testing.T) {
	t.Parallel()

	t.Run("require_relative resolves to same directory", func(t *testing.T) {
		t.Parallel()
		src := []byte(`require_relative 'foo'`)
		got := Analyze("lib/myapp.rb", src, "")
		want := []string{"lib/"}
		assertSlice(t, got, want)
	})

	t.Run("require with relative path", func(t *testing.T) {
		t.Parallel()
		src := []byte(`require './helper'`)
		got := Analyze("lib/myapp.rb", src, "")
		want := []string{"lib/"}
		assertSlice(t, got, want)
	})

	t.Run("bare gem name skipped", func(t *testing.T) {
		t.Parallel()
		src := []byte(`require 'rails'
require 'json'
`)
		got := Analyze("lib/myapp.rb", src, "")
		if got != nil {
			t.Errorf("expected nil for gem names, got %v", got)
		}
	})
}

func TestAnalyze_CSharp(t *testing.T) {
	t.Parallel()

	t.Run("simple using", func(t *testing.T) {
		t.Parallel()
		src := []byte(`using Foo.Bar;`)
		got := Analyze("Test.cs", src, "")
		want := []string{"Foo/Bar/"}
		assertSlice(t, got, want)
	})

	t.Run("framework namespaces filtered", func(t *testing.T) {
		t.Parallel()
		src := []byte(`using System;
using System.Collections.Generic;
using Microsoft.AspNetCore.Builder;
using Windows.Storage;
using MyApp.Models;
`)
		got := Analyze("Test.cs", src, "")
		want := []string{"MyApp/Models/"}
		assertSlice(t, got, want)
	})

	t.Run("using statement not confused with using directive", func(t *testing.T) {
		t.Parallel()
		src := []byte(`using System;
using (var conn = new DbConnection()) { }
using MyApp.Data;
`)
		// The using statement `using (var conn ...)` should not match.
		got := Analyze("Test.cs", src, "")
		want := []string{"MyApp/Data/"}
		assertSlice(t, got, want)
	})
}

func TestAnalyze_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	src := []byte(`some random content`)
	got := Analyze("file.txt", src, "")
	if got != nil {
		t.Errorf("expected nil for .txt, got %v", got)
	}
}

func TestAnalyze_EmptyContent(t *testing.T) {
	t.Parallel()

	t.Run("nil content", func(t *testing.T) {
		t.Parallel()
		got := Analyze("main.go", nil, "")
		if got != nil {
			t.Errorf("expected nil for nil content, got %v", got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		got := Analyze("main.go", []byte{}, "")
		if got != nil {
			t.Errorf("expected nil for empty content, got %v", got)
		}
	})
}

func TestAnalyze_Deduplication(t *testing.T) {
	t.Parallel()

	t.Run("Go dedup", func(t *testing.T) {
		t.Parallel()
		src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/chunker"
)
`)
		got := Analyze("main.go", src, "github.com/lgldsilva/semidx")
		want := []string{"internal/chunker/"}
		assertSlice(t, got, want)
	})

	t.Run("Java dedup", func(t *testing.T) {
		t.Parallel()
		src := []byte(`import com.foo.bar.Baz;
import com.foo.bar.Quux;
`)
		got := Analyze("Test.java", src, "")
		want := []string{"com/foo/bar/"}
		assertSlice(t, got, want)
	})
}

// assertSlice compares two string slices (order-sensitive).
func assertSlice(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, len(want)=%d\ngot:  %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at index %d:\ngot:  %#v\nwant: %#v", i, got, want)
		}
	}
}
