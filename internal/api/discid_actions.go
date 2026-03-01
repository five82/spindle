package api

import (
	"fmt"

	"spindle/internal/discidcache"
)

// RemoveDiscIDEntryByNumber removes a cache entry using the 1-based numbering from cache list output.
func RemoveDiscIDEntryByNumber(cache *discidcache.Cache, entryNum int) (discidcache.Entry, error) {
	if cache == nil {
		return discidcache.Entry{}, fmt.Errorf("disc ID cache is not available")
	}
	if entryNum < 1 {
		return discidcache.Entry{}, fmt.Errorf("invalid entry number: %d (must be a positive integer)", entryNum)
	}

	entries := cache.List()
	if entryNum > len(entries) {
		return discidcache.Entry{}, fmt.Errorf("entry number %d out of range (only %d entries exist)", entryNum, len(entries))
	}

	entry := entries[entryNum-1]
	if err := cache.Remove(entry.DiscID); err != nil {
		return discidcache.Entry{}, fmt.Errorf("remove cache entry: %w", err)
	}
	return entry, nil
}
