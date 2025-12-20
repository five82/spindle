package encodingstate

import "testing"

func TestSnapshotMarshalUnmarshal(t *testing.T) {
	empty := Snapshot{}
	if !empty.IsZero() {
		t.Fatal("expected zero snapshot to be IsZero")
	}
	data, err := empty.Marshal()
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if data != "" {
		t.Fatalf("expected empty marshal output, got %q", data)
	}
	decoded, err := Unmarshal("")
	if err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if !decoded.IsZero() {
		t.Fatalf("expected zero snapshot after empty unmarshal")
	}

	snapshot := Snapshot{
		JobLabel: "encode-1",
		Stage:    "encoding",
		Percent:  12.5,
		Warning:  "low bitrate",
	}
	data, err = snapshot.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	roundTrip, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTrip.JobLabel != snapshot.JobLabel || roundTrip.Stage != snapshot.Stage || roundTrip.Percent != snapshot.Percent {
		t.Fatalf("unexpected round trip snapshot: %+v", roundTrip)
	}
}
