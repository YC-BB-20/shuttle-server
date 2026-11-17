package shuttle

import (
	"os"
	"strings"
	"testing"
)

// Mac releases are signed with one fixed certificate so macOS keeps the
// drive permissions across updates. The key and password reach the signer
// only as files, never on a command line.
func TestCISignsMacBinariesWithoutSecretsOnCommandLine(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"matrix.goos == 'darwin'", "secrets.MAC_SIGN_P12_BASE64", "secrets.MAC_SIGN_P12_PASSWORD",
		"--p12-file", "--p12-password-file", "--binary-identifier com.yc-bb-20.shuttle-server",
		"tar -czf shuttle-server-${GOOS}-${GOARCH}.tar.gz",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ci.yml lacks %q", want)
		}
	}
	if strings.Contains(s, "--p12-password ") || strings.Contains(s, "${{ secrets.MAC_SIGN_P12_PASSWORD }}\"") {
		t.Error("the signing password must not be on a command line")
	}
	// the archive step must come after signing, or the tar holds an unsigned file
	if strings.Index(s, "name: sign (macOS binaries)") > strings.Index(s, "name: archive") {
		t.Error("signing must happen before the archive is made")
	}
}
