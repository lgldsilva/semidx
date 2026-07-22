package depcatalog

import "testing"

func TestParseManifestMatrix(t *testing.T) {
	tests := []struct {
		path      string
		body      string
		ecosystem Ecosystem
		want      map[string]string
	}{
		{"go.mod", "module example.com/app\nrequire (\n\tgithub.com/acme/log v1.2.3\n\tgithub.com/acme/old v0.4.0 // indirect\n)\n", EcosystemGo, map[string]string{"github.com/acme/log": "v1.2.3", "github.com/acme/old": "v0.4.0"}},
		{"pom.xml", `<project><dependencies><dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.13</version></dependency></dependencies></project>`, EcosystemMaven, map[string]string{"org.slf4j:slf4j-api": "2.0.13"}},
		{"build.gradle.kts", `dependencies { implementation("org.slf4j:slf4j-api:2.0.13"); testImplementation("junit:junit:4.13.2") }`, EcosystemGradle, map[string]string{"org.slf4j:slf4j-api": "2.0.13", "junit:junit": "4.13.2"}},
		{"Package.swift", `dependencies: [.package(url: "https://github.com/apple/swift-log.git", from: "1.5.0")]`, EcosystemSwift, map[string]string{"swift-log": "1.5.0"}},
		{"Podfile", `pod 'Alamofire', '~> 5.8'`, EcosystemCocoaPods, map[string]string{"Alamofire": "~> 5.8"}},
	}
	for _, tt := range tests {
		deps, err := ParseManifest(tt.path, []byte(tt.body))
		if err != nil {
			t.Fatalf("%s: %v", tt.path, err)
		}
		if len(deps) != len(tt.want) {
			t.Fatalf("%s: got %d deps, want %d: %+v", tt.path, len(deps), len(tt.want), deps)
		}
		for _, dep := range deps {
			if dep.Ecosystem != tt.ecosystem {
				t.Errorf("%s ecosystem=%q", tt.path, dep.Ecosystem)
			}
			if got := tt.want[dep.Name]; got != dep.Constraint {
				t.Errorf("%s %s constraint=%q want %q", tt.path, dep.Name, dep.Constraint, got)
			}
		}
	}
}

func TestParseResolvedSwiftAndPods(t *testing.T) {
	deps, err := ParseManifests(map[string][]byte{
		"Package.resolved": []byte(`{"pins":[{"identity":"swift-log","location":"https://github.com/apple/swift-log.git","state":{"version":"1.5.0"}}]}`),
		"Podfile.lock":     []byte("PODS:\n  - Alamofire (5.9.0)\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %+v", deps)
	}
	for _, dep := range deps {
		if dep.ResolvedVersion == "" {
			t.Errorf("unresolved dependency: %+v", dep)
		}
	}
}

func TestParseUnknownAndNormalize(t *testing.T) {
	deps, err := ParseManifest("README.md", []byte("hello"))
	if err != nil || deps != nil {
		t.Fatalf("unknown manifest = %+v, %v", deps, err)
	}
	if got := NormalizeName(EcosystemMaven, " Org.SLF4J:slf4j-api "); got != "org.slf4j:slf4j-api" {
		t.Errorf("normalized=%q", got)
	}
}
