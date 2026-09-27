package quality

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPairedFrameObservation(t *testing.T) {
	var order []string
	original, denoised := make([]byte, 2), make([]byte, 2)
	readRef := func(i int, buf []byte) error {
		order = append(order, "original")
		buf[0] = byte(i)
		return nil
	}
	readDist := func(i int, buf []byte) error {
		order = append(order, "denoised")
		buf[0] = byte(i + 1)
		return nil
	}
	observe := func(i int, src, dst []byte) error {
		order = append(order, "observe")
		if src[0] != byte(i) || dst[0] != byte(i+1) {
			t.Errorf("mismatched pair at %d: %v / %v", i, src, dst)
		}
		return nil
	}
	for i := range 10 {
		order = nil
		if err := readCVVDPPair(context.Background(), i, original, denoised, readRef, readDist, observe); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(order, []string{"original", "denoised", "observe"}) {
			t.Fatalf("pair lifetime/order changed: %v", order)
		}
	}
	order = nil
	if err := readCVVDPPair(context.Background(), 0, original, denoised, readRef, readDist, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"original", "denoised"}) {
		t.Fatalf("normal probe path ran grain analysis: %v", order)
	}
}

func TestPairedFrameFailuresAndCancellation(t *testing.T) {
	failure := errors.New("test failure")
	for _, where := range []string{"original", "denoised", "observer", "canceled", "cancel-after-read"} {
		t.Run(where, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := false
			reads := 0
			read := func(name string) func(int, []byte) error {
				return func(_ int, _ []byte) error {
					reads++
					if name == where {
						return failure
					}
					if name == "denoised" && where == "cancel-after-read" {
						cancel()
					}
					return nil
				}
			}
			if where == "canceled" {
				cancel()
			}
			err := readCVVDPPair(ctx, 0, nil, nil, read("original"), read("denoised"), func(_ int, _, _ []byte) error {
				observed = true
				return failure
			})
			if observed != (where == "observer") {
				t.Fatalf("observed incomplete or canceled pair: %v", observed)
			}
			if where == "canceled" || where == "cancel-after-read" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("lost failure: %v", err)
			}
			if where == "canceled" && reads != 0 {
				t.Fatal("read after cancellation")
			}
		})
	}
}
