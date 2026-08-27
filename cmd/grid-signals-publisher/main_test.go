package main

import "testing"

func TestParseFingerprintsFailsClosed(t *testing.T) {
	if _, err := parseFingerprints(""); err == nil {
		t.Fatal("empty allowlist accepted")
	}
	if _, err := parseFingerprints("not-a-fingerprint"); err == nil {
		t.Fatal("invalid fingerprint accepted")
	}
	value := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := parseFingerprints(value)
	if err != nil || len(got) != 1 {
		t.Fatalf("parseFingerprints = %#v, %v", got, err)
	}
}
