package main

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripcache"
)

func TestSelectCacheEntryByNumberOrFingerprint(t *testing.T) {
	entries := []ripcache.EntryMetadata{
		{Fingerprint: "abcdef123456", DiscTitle: "First"},
		{Fingerprint: "123456abcdef", DiscTitle: "Second"},
	}

	byNumber, err := selectCacheEntry(entries, "2")
	if err != nil || byNumber.DiscTitle != "Second" {
		t.Fatalf("select number = %#v, %v", byNumber, err)
	}
	byFingerprint, err := selectCacheEntry(entries, "ABCDEF")
	if err != nil || byFingerprint.DiscTitle != "First" {
		t.Fatalf("select fingerprint = %#v, %v", byFingerprint, err)
	}
	byNumericFingerprint, err := selectCacheEntry(entries, "123456")
	if err != nil || byNumericFingerprint.DiscTitle != "Second" {
		t.Fatalf("select numeric fingerprint = %#v, %v", byNumericFingerprint, err)
	}
}

func TestSelectCacheEntryRejectsAmbiguousFingerprint(t *testing.T) {
	entries := []ripcache.EntryMetadata{
		{Fingerprint: "abcdef123456"},
		{Fingerprint: "abcdef654321"},
	}
	_, err := selectCacheEntry(entries, "abcdef")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("select ambiguous error = %v", err)
	}
}

func TestSelectCacheEntryRejectsUnknownFingerprint(t *testing.T) {
	_, err := selectCacheEntry([]ripcache.EntryMetadata{{Fingerprint: "abcdef"}}, "fedcba")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("select unknown error = %v", err)
	}
}
