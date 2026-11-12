package copyjob

import (
	"os"
	"regexp"
	"testing"
)

// Stage names the owner reads say which disk is read, in words, never the
// D1/S1/S2 shorthand; the page's "Verifying" chip keys on "Reading back".
func TestStageNamesArePlainWords(t *testing.T) {
	code := regexp.MustCompile(`l\.stage = [^"]*"([^"]*)"`)
	n := 0
	for _, f := range []string{"run.go", "verify.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range code.FindAllStringSubmatch(string(b), -1) {
			n++
			if regexp.MustCompile(`\b(D1|D2|S1|S2)\b`).MatchString(m[1]) {
				t.Errorf("%s: stage %q uses a code instead of words", f, m[1])
			}
		}
	}
	if n < 10 {
		t.Fatalf("found only %d stage names; the pattern no longer matches the code", n)
	}
}
