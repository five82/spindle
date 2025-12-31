// Package ripcache stores optional copies of ripped artifacts so failed or repeated
// encodes can reuse existing rips without rerunning MakeMKV. Each entry also
// carries identification metadata so cached rips can be re-queued without
// reinserting a disc. The cache enforces a size budget and a free-space floor
// (20% by default), pruning oldest entries first.
package ripcache
