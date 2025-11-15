package contentid

import "testing"

func TestResolveEpisodeMatchesOptimalAssignment(t *testing.T) {
	// Construct two episodes with cross-similarity where greedy would swap.
	rip1 := newFingerprint("alpha beta gamma intro theme park")              // intended E1
	rip2 := newFingerprint("delta epsilon zeta magic heroes rescue mission") // intended E2

	ref1 := referenceFingerprint{EpisodeNumber: 1, Vector: newFingerprint("alpha beta gamma park theme opening")}
	ref2 := referenceFingerprint{EpisodeNumber: 2, Vector: newFingerprint("delta epsilon zeta heroes rescue mission magic")}
	refCross := referenceFingerprint{EpisodeNumber: 3, Vector: newFingerprint("alpha beta gamma park magic heroes rescue mission")}

	rips := []ripFingerprint{{EpisodeKey: "s05e01", TitleID: 1, Vector: rip1}, {EpisodeKey: "s05e02", TitleID: 2, Vector: rip2}}
	refs := []referenceFingerprint{ref2, ref1, refCross}

	matches := resolveEpisodeMatches(rips, refs)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	want := map[string]int{"s05e01": 1, "s05e02": 2}
	for _, m := range matches {
		if want[m.EpisodeKey] != m.TargetEpisode {
			t.Fatalf("unexpected match: %+v", m)
		}
	}
}

func TestResolveEpisodeMatchesThreshold(t *testing.T) {
	rip := []ripFingerprint{{EpisodeKey: "s05e01", TitleID: 1, Vector: newFingerprint("barely similar")}}
	ref := []referenceFingerprint{{EpisodeNumber: 1, Vector: newFingerprint("completely different")}}
	matches := resolveEpisodeMatches(rip, ref)
	if len(matches) != 0 {
		t.Fatalf("expected no matches below threshold, got %d", len(matches))
	}
}
