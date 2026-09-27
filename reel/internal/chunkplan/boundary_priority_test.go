package chunkplan

import "testing"

func TestPreferredBoundaryKindRetainsStrongestBoundary(t *testing.T) {
 for _, tc := range []struct{ a, b, want BoundaryKind }{
  {BoundaryKindSyntheticSplit, BoundaryKindStart, BoundaryKindStart},
  {BoundaryKindStart, BoundaryKindNaturalShotCut, BoundaryKindStart},
  {BoundaryKindNaturalShotCut, BoundaryKindSyntheticSplit, BoundaryKindNaturalShotCut},
  {BoundaryKindSyntheticSplit, BoundaryKindNaturalShotCut, BoundaryKindNaturalShotCut},
  {BoundaryKindSyntheticSplit, BoundaryKindSyntheticSplit, BoundaryKindSyntheticSplit},
 } {
  if got := preferredBoundaryKind(tc.a, tc.b); got != tc.want { t.Errorf("%s + %s = %s, want %s", tc.a, tc.b, got, tc.want) }
 }
}
