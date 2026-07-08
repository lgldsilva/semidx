package main

import "testing"

func TestUpdateAlertHash(t *testing.T) {
	alerts := []Alert{
		{Name: "a1", Project: "p1", LastHash: "old1"},
		{Name: "a2", Project: "p2", LastHash: "old2"},
	}
	updateAlertHash(alerts, "a1", "p1", "new1")
	if alerts[0].LastHash != "new1" {
		t.Fatalf("expected a1 hash updated, got %q", alerts[0].LastHash)
	}
	if alerts[1].LastHash != "old2" {
		t.Fatalf("expected a2 hash unchanged, got %q", alerts[1].LastHash)
	}
}
