package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaultsThenReadsThem(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c, created, err := Load(p)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	c2, created, err := Load(p)
	if err != nil || created || c2 != c {
		t.Fatalf("reload differs: %+v %v %v", c2, created, err)
	}
}

func TestRefusesNonLocalListen(t *testing.T) {
	for _, l := range []string{"0.0.0.0:8470", "192.168.1.5:8470", ":8470"} {
		c := Defaults()
		c.Listen = l
		if c.Validate() == nil {
			t.Errorf("%s accepted", l)
		}
	}
	for _, l := range []string{"127.0.0.1:1", "localhost:8470", "[::1]:8470"} {
		c := Defaults()
		c.Listen = l
		if err := c.Validate(); err != nil {
			t.Errorf("%s refused: %v", l, err)
		}
	}
}

func TestBadFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte("{nope"), 0o600)
	if _, _, err := Load(p); err == nil {
		t.Fatal("damaged config accepted")
	}
}

func TestBudgetsMatchDesign(t *testing.T) {
	c := Defaults()
	if got := c.BufferBudgetBytes(64 << 30); got != 128<<20 {
		t.Errorf("laptop budget %d", got)
	}
	if got := c.BufferBudgetBytes(1 << 30); got != 64<<20 {
		t.Errorf("Pi budget %d", got)
	}
	c.DedicatedMachine = true
	if got := c.MemoryCeilingBytes(48 << 30); got != 1<<30 {
		t.Errorf("mini ceiling %d, want 1 GB", got)
	}
	if c.OpenFiles(10240) != 5120 {
		t.Error("open-file cap not half the limit")
	}
}
