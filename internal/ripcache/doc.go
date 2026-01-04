// Package ripcache stores optional copies of ripped artifacts so failed or repeated
// encodes can reuse existing rips without rerunning MakeMKV. Each entry also
// carries identification metadata so cached rips can be re-queued without
// reinserting a disc.
//
// # Size Management
//
// The cache enforces two constraints: a configurable size budget (rip_cache_max_gib)
// and a 20% free-space floor on the underlying volume. When either limit is
// approached, the manager auto-prunes oldest entries first until headroom is
// restored. Manual pruning is available via `spindle cache prune`.
//
// Use `spindle cache stats` to inspect current usage before adjusting limits.
package ripcache
