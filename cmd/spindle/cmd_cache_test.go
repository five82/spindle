package main

import (
	"bytes"
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

func TestSelectCacheEntriesByMixedSelectors(t *testing.T) {
	entries := []ripcache.EntryMetadata{
		{Fingerprint: "abcdef123456", DiscTitle: "First"},
		{Fingerprint: "123456abcdef", DiscTitle: "Second"},
		{Fingerprint: "fedcba654321", DiscTitle: "Third"},
	}

	selected, err := selectCacheEntries(entries, []string{"2", "fedcba", "ABCDEF", "123456abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 {
		t.Fatalf("selected %d entries, want 3: %#v", len(selected), selected)
	}
	for i, want := range []string{"Second", "Third", "First"} {
		if selected[i].DiscTitle != want {
			t.Errorf("selected[%d] = %q, want %q", i, selected[i].DiscTitle, want)
		}
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

func TestSelectCacheEntryRejectsInvalidSelectors(t *testing.T) {
	entries := []ripcache.EntryMetadata{{Fingerprint: "abcd1234"}}
	for _, tc := range []struct{ selector, want string }{
		{"  ", "required"}, {"0", "invalid entry number"}, {"-2", "invalid entry number"}, {"9", "not found"}, {"no-match", "not found"},
	} {
		if _, err := selectCacheEntry(entries, tc.selector); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("select %q: %v", tc.selector, err)
		}
	}
	if _, err := selectCacheEntries(entries, []string{"1", "bad"}); err == nil {
		t.Fatal("invalid selector in batch accepted")
	}
}

func TestCacheRemoveHelpDocumentsSelectors(t *testing.T) {
	cmd := newCacheRemoveCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Help(); err != nil {
		t.Fatal(err)
	}

	help := output.String()
	for _, want := range []string{"<number-or-fingerprint>...", "one or more", "unique", "fingerprint prefix"} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not contain %q:\n%s", want, help)
		}
	}
}
