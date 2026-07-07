package license

import (
	"testing"
)

func TestDetect_MIT_File(t *testing.T) {
	content := `MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.
`
	info := Detect("LICENSE", content)
	if info.SPDXID != "MIT" {
		t.Errorf("expected MIT, got %s", info.SPDXID)
	}
	if info.Source != "filename" {
		t.Errorf("expected source filename, got %s", info.Source)
	}
	if info.Confidence < 0.5 {
		t.Errorf("confidence too low: %f", info.Confidence)
	}
}

func TestDetect_Apache_File(t *testing.T) {
	content := `Apache License, Version 2.0

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

   "License" shall mean the terms and conditions for use, reproduction,
   and distribution as defined by Sections 1 through 9 of this document.
`
	info := Detect("LICENSE-2.0", content)
	if info.SPDXID != "Apache-2.0" {
		t.Errorf("expected Apache-2.0, got %s", info.SPDXID)
	}
	if info.Source != "filename" {
		t.Errorf("expected source filename, got %s", info.Source)
	}
}

func TestDetect_GPL_File(t *testing.T) {
	content := `GNU General Public License, version 3

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
`
	info := Detect("COPYING", content)
	if info.SPDXID != "GPL-3.0-only" {
		t.Errorf("expected GPL-3.0-only, got %s", info.SPDXID)
	}
	if info.Source != "filename" {
		t.Errorf("expected source filename, got %s", info.Source)
	}
}

func TestDetect_SPDX_Header(t *testing.T) {
	content := `// SPDX-License-Identifier: BSD-3-Clause
package main
`
	info := Detect("main.go", content)
	if info.SPDXID != "BSD-3-Clause" {
		t.Errorf("expected BSD-3-Clause, got %s", info.SPDXID)
	}
	if info.Source != "header" {
		t.Errorf("expected source header, got %s", info.Source)
	}
	if info.Confidence < 0.9 {
		t.Errorf("confidence too low: %f", info.Confidence)
	}
}

func TestDetect_Declared(t *testing.T) {
	content := `// MIT License
//
// This project is released under the MIT License.
`
	info := Detect("README.md", content)
	if info.SPDXID != "MIT" {
		t.Errorf("expected MIT, got %s", info.SPDXID)
	}
	if info.Source != "declared" {
		t.Errorf("expected source declared, got %s", info.Source)
	}
}

func TestDetect_DeclaredApache(t *testing.T) {
	content := `/*
 * Licensed under the Apache License, Version 2.0 (the "License");
 */
`
	info := Detect("header.go", content)
	if info.SPDXID != "Apache-2.0" {
		t.Errorf("expected Apache-2.0, got %s", info.SPDXID)
	}
	if info.Source != "declared" {
		t.Errorf("expected source declared, got %s", info.Source)
	}
}

func TestDetect_NoContent(t *testing.T) {
	info := Detect("main.go", "")
	if info.SPDXID != "" {
		t.Errorf("expected empty SPDXID, got %s", info.SPDXID)
	}
}

func TestDetect_UnsupportedFile(t *testing.T) {
	info := Detect("data.bin", "some binary content")
	if info.SPDXID != "" {
		t.Errorf("expected empty SPDXID, got %s", info.SPDXID)
	}
}

func TestDetect_HeaderPriority(t *testing.T) {
	// SPDX header should take priority over declaration in a non-LICENSE file.
	content := `// SPDX-License-Identifier: MIT
// Licensed under the Apache License, Version 2.0
`
	info := Detect("source.go", content)
	if info.SPDXID != "MIT" {
		t.Errorf("expected MIT from header priority, got %s", info.SPDXID)
	}
	if info.Source != "header" {
		t.Errorf("expected source header, got %s", info.Source)
	}
}

func TestDetect_BSD3(t *testing.T) {
	content := `Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:
1. Redistributions of source code must retain the above copyright notice,
`
	info := Detect("LICENSE", content)
	if info.SPDXID != "BSD-3-Clause" {
		t.Errorf("expected BSD-3-Clause, got %s", info.SPDXID)
	}
}

func TestIsPermissive(t *testing.T) {
	tests := []struct {
		spdx string
		want bool
	}{
		{"MIT", true},
		{"Apache-2.0", true},
		{"BSD-3-Clause", true},
		{"ISC", true},
		{"Unlicense", true},
		{"GPL-3.0-only", false},
		{"GPL-2.0-only", false},
		{"AGPL-3.0-only", false},
		{"MPL-2.0", false},
		{"unknown", false},
	}
	for _, tc := range tests {
		got := IsPermissive(tc.spdx)
		if got != tc.want {
			t.Errorf("IsPermissive(%q) = %v, want %v", tc.spdx, got, tc.want)
		}
	}
}

func TestNormaliseSPDX(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MIT", "MIT"},
		{"MIT License", "MIT"},
		{"Apache-2.0", "Apache-2.0"},
		{"Apache-2.0 License", "Apache-2.0"},
	}
	for _, tc := range tests {
		got := NormaliseSPDX(tc.input)
		if got != tc.want {
			t.Errorf("NormaliseSPDX(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestInfo_String(t *testing.T) {
	info := Info{SPDXID: "MIT", Confidence: 0.95, Source: "header"}
	s := info.String()
	if s != "MIT (95%, from header)" {
		t.Errorf("unexpected string: %s", s)
	}

	empty := Info{}
	if empty.String() != "unknown" {
		t.Errorf("expected unknown, got %s", empty.String())
	}
}
