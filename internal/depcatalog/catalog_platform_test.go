package depcatalog

import "testing"

func TestValidEcosystemAndAdapterNames(t *testing.T) {
	known := []Ecosystem{EcosystemGo, EcosystemNPM, EcosystemMaven, EcosystemGradle, EcosystemSwift, EcosystemCocoaPods}
	for _, ecosystem := range known {
		if !ValidEcosystem(ecosystem) {
			t.Errorf("ValidEcosystem(%q) = false", ecosystem)
		}
	}
	if ValidEcosystem("rust") {
		t.Error("ValidEcosystem(rust) = true")
	}

	want := []string{"go.mod", "package.json", "pom.xml", "Gradle build script", "Package.swift", "Package.resolved", "Podfile", "Podfile.lock"}
	for i, adapter := range adapters {
		if got := adapter.Name(); got != want[i] {
			t.Errorf("adapter %d Name() = %q, want %q", i, got, want[i])
		}
	}
}
