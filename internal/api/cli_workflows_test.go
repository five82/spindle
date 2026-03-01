package api

import (
	"testing"

	"spindle/internal/queue"
)

func TestAssessIdentifyDiscSuccess(t *testing.T) {
	item := &queue.Item{
		MetadataJSON: `{"title":"The Matrix","release_date":"1999-03-31","filename":"The Matrix (1999)"}`,
	}

	assessment := AssessIdentifyDisc(item)
	if assessment.Outcome != "success" {
		t.Fatalf("Outcome = %q, want success", assessment.Outcome)
	}
	if assessment.LibraryFilename != "The Matrix (1999).mkv" {
		t.Fatalf("LibraryFilename = %q, want The Matrix (1999).mkv", assessment.LibraryFilename)
	}
}

func TestAssessIdentifyDiscReview(t *testing.T) {
	item := &queue.Item{
		NeedsReview:  true,
		ReviewReason: "low confidence",
	}

	assessment := AssessIdentifyDisc(item)
	if assessment.Outcome != "review" {
		t.Fatalf("Outcome = %q, want review", assessment.Outcome)
	}
	if !assessment.ReviewRequired {
		t.Fatalf("ReviewRequired = false, want true")
	}
	if assessment.ReviewReason != "low confidence" {
		t.Fatalf("ReviewReason = %q, want low confidence", assessment.ReviewReason)
	}
}

func TestAssessIdentifyDiscFallbackFilename(t *testing.T) {
	item := &queue.Item{
		MetadataJSON: `{"title":"Alien","release_date":"1979-05-25"}`,
	}

	assessment := AssessIdentifyDisc(item)
	if assessment.LibraryFilename != "Alien (1979).mkv" {
		t.Fatalf("LibraryFilename = %q, want Alien (1979).mkv", assessment.LibraryFilename)
	}
}
