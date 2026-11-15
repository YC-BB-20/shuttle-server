package copyjob

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
	"github.com/YC-BB-20/shuttle-server/internal/xxh64"
)

// v0.2.58: the hash chosen in Settings is what a copy makes and proves with,
// the file info shows it, and a Verify of the copy reads with the kind the
// copy's records hold, even after the setting changed.
func TestHashChoiceCopiesAndVerifies(t *testing.T) {
	for _, algo := range []string{AlgoXXH128, AlgoXXH64, AlgoBoth} {
		t.Run(algo, func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			makeTree(t, src, 1<<20+3)
			m := newManager(t, filepath.Join(root, "state"))
			if s, err := m.SetSettings(Settings{SkipSystem: true, Hash: algo}); err != nil || s.Hash != algo {
				t.Fatal(s, err)
			}
			startRun(t, m)
			c, err := m.Add(Spec{Source: src, Dest: dst, S2: "now"})
			if err != nil {
				t.Fatal(err)
			}
			v := waitState(t, m, c.st.ID, "finished")
			if v.Strong != v.Files || v.Failed != 0 || v.Algo != algo {
				t.Fatalf("%+v", v)
			}
			fi, err := m.FileInfo(c.st.ID, 0)
			if err != nil || fi.Algo != algo {
				t.Fatal(fi.Algo, err)
			}
			data, _ := os.ReadFile(filepath.Join(src, filepath.FromSlash(fi.Path)))
			h128, h64 := xxh3.Sum128(data).String(), fmt.Sprintf("%016x", xxh64.Sum(data))
			want := map[string]string{AlgoXXH128: h128, AlgoXXH64: h64, AlgoBoth: h128 + "+" + h64}[algo]
			if fi.Hash.C != want || fi.Hash.D1 != want || fi.Hash.S2 != want {
				t.Fatalf("hashes %+v, want %s", fi.Hash, want)
			}
			// A Verify after the setting changed still reads with the copy's kind.
			m.SetSettings(Settings{SkipSystem: true, Hash: AlgoXXH128})
			if algo == AlgoXXH128 {
				m.SetSettings(Settings{SkipSystem: true, Hash: AlgoXXH64})
			}
			d, err := m.Add(Spec{Kind: KindVerify, Source: dst, From: []string{c.st.ID}})
			if err != nil {
				t.Fatal(err)
			}
			dv := waitState(t, m, d.st.ID, "finished")
			if dv.Failed != 0 || dv.Verified == 0 {
				t.Fatalf("verify %+v", dv)
			}
			fi, _ = m.FileInfo(c.st.ID, 0)
			if len(fi.Verifies) != 1 || fi.Verifies[0].D1 != want {
				t.Fatalf("verify reads %+v", fi.Verifies)
			}
		})
	}
}

func TestAlgoOfAndOldSettings(t *testing.T) {
	if normAlgo("") != AlgoXXH128 || normAlgo("sha") != AlgoXXH128 || normAlgo(AlgoBoth) != AlgoBoth {
		t.Fatal("normAlgo")
	}
	if algoOf("", AlgoXXH64) != AlgoXXH64 || algoOf("0123456789abcdef", "") != AlgoXXH64 {
		t.Fatal("algoOf")
	}
	root := t.TempDir()
	m := newManager(t, filepath.Join(root, "state"))
	// Settings saved by v0.2.57 have no hash choice: XXH128.
	os.WriteFile(m.settingsPath(), []byte(`{"format_version":1,"skip_system_files":true,"xxh64_lists":true}`), 0o600)
	if s := m.Settings(); s.Hash != AlgoXXH128 || !s.SkipSystem {
		t.Fatalf("%+v", s)
	}
}
