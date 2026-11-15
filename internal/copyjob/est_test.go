package copyjob

import (
	"encoding/json"
	"testing"
)

func TestSpecEstimateRoundTrip(t *testing.T) {
	var s Spec
	if err := json.Unmarshal([]byte(`{"source":"/a","dest":"/b","est_bytes":1234,"est_files":7}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.EstBytes != 1234 || s.EstFiles != 7 {
		t.Fatalf("got %d %d", s.EstBytes, s.EstFiles)
	}
	b, _ := json.Marshal(s)
	var back Spec
	json.Unmarshal(b, &back)
	if back.EstBytes != 1234 {
		t.Fatal("estimate lost on save")
	}
}
