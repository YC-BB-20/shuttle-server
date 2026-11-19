package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// maxServers keeps the list, and the folder it lives in, small.
const maxServers = 20

// Store keeps the servers the owner added, one file each in the secrets
// folder (mode 0600, never backed up, never logged). The page sees only Public.
type Store struct {
	Dir  string
	Opts Options

	mu    sync.Mutex
	tests map[string]Test // last connection test per server, kept in memory
}

// Public is a server as the page may see it: no password or secret key, and
// only the last four characters of an S3 access key.
type Public struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Host      string `json:"host"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	Style     string `json:"style,omitempty"`
	Folder    string `json:"folder,omitempty"`
	User      string `json:"user"`
	Resumable bool   `json:"resumable"`
	Test      *Test  `json:"test,omitempty"`
}

// Test is the result of the last connection test.
type Test struct {
	At   time.Time `json:"at"`
	OK   bool      `json:"ok"`
	Msg  string    `json:"msg"`
	Info *Info     `json:"info,omitempty"`
}

func (s *Store) path(id string) string { return filepath.Join(s.Dir, id+".json") }

func validID(id string) bool {
	if len(id) != 16 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *Store) read(id string) (Config, error) {
	if !validID(id) {
		return Config{}, errors.New("no such server")
	}
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("no such server")
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("the saved server %s could not be read", id)
	}
	c.ID = id
	return c, nil
}

func (s *Store) public(c Config) Public {
	p := Public{ID: c.ID, Name: c.Name, Kind: c.Kind, URL: c.URL, Bucket: c.Bucket, Region: c.Region, Style: c.Style, Folder: c.Folder,
		Resumable: c.Kind == KindS3}
	if u, err := url.Parse(c.URL); err == nil {
		p.Host = u.Host
	}
	if c.Kind == KindS3 {
		k := c.User
		if len(k) > 4 {
			k = k[len(k)-4:]
		}
		p.User = "key ending " + k
	} else {
		p.User = c.User
	}
	if t, ok := s.tests[c.ID]; ok {
		p.Test = &t
	}
	return p
}

// List is every saved server, by name.
func (s *Store) List() ([]Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	des, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Public{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Public{}
	for _, de := range des {
		id, ok := strings.CutSuffix(de.Name(), ".json")
		if !ok || !validID(id) {
			continue
		}
		c, err := s.read(id)
		if err != nil {
			continue
		}
		out = append(out, s.public(c))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Save adds a server (empty ID) or changes one. An empty secret keeps the
// saved one, so the page never needs it back.
func (s *Store) Save(in Config) (Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.Name, in.URL, in.User = strings.TrimSpace(in.Name), NormalizeURL(in.URL), strings.TrimSpace(in.User)
	in.Bucket, in.Region, in.Folder = strings.TrimSpace(in.Bucket), strings.TrimSpace(in.Region), strings.Trim(strings.TrimSpace(in.Folder), "/")
	if in.ID != "" {
		old, err := s.read(in.ID)
		if err != nil {
			return Public{}, err
		}
		if in.Secret == "" {
			if in.User == "" {
				in.User = old.User // an S3 access key is never sent back to the page
			}
			if in.User != old.User || in.Kind != old.Kind || in.URL != old.URL {
				return Public{}, errors.New("type the password or secret key again: the address, kind or user changed")
			}
			in.Secret = old.Secret
		}
		delete(s.tests, in.ID)
	} else {
		if n, _ := os.ReadDir(s.Dir); len(n) >= maxServers {
			return Public{}, fmt.Errorf("at most %d servers can be saved", maxServers)
		}
		b := make([]byte, 8)
		rand.Read(b)
		in.ID = hex.EncodeToString(b)
	}
	if err := in.Valid(); err != nil {
		return Public{}, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Public{}, err
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return Public{}, err
	}
	if err := fsx.WriteFileAtomic(s.path(in.ID), b, 0o600); err != nil {
		return Public{}, err
	}
	return s.public(in), nil
}

// Remove forgets a server: only Shuttle's saved login is removed, nothing on
// the server is touched.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.read(id); err != nil {
		return err
	}
	delete(s.tests, id)
	return os.Remove(s.path(id))
}

// Open connects to a saved server.
func (s *Store) Open(id string) (Remote, Public, error) {
	s.mu.Lock()
	c, err := s.read(id)
	var p Public
	if err == nil {
		p = s.public(c)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, Public{}, err
	}
	r, err := New(c, s.Opts)
	return r, p, err
}

// Check runs a read-only connection test and keeps its result.
func (s *Store) Check(ctx context.Context, id string) (Public, error) {
	s.mu.Lock()
	c, err := s.read(id)
	s.mu.Unlock()
	if err != nil {
		return Public{}, err
	}
	o := s.Opts
	o.Tries = 2 // a test answers within seconds; copies retry longer
	r, err := New(c, o)
	if err != nil {
		return Public{}, err
	}
	in, err := r.Check(ctx)
	t := Test{At: time.Now(), OK: err == nil}
	if err != nil {
		t.Msg = err.Error()
	} else {
		t.Info = &in
		t.Msg = describe(in)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tests == nil {
		s.tests = map[string]Test{}
	}
	s.tests[id] = t
	c, err = s.read(id)
	if err != nil {
		return Public{}, err
	}
	return s.public(c), nil
}

func describe(in Info) string {
	plural := func(n int, one string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %ss", n, one)
	}
	msg := "Connected. The top level holds " + plural(in.Folders, "folder") + " and " + plural(in.Files, "file")
	if in.More {
		msg += ", and more"
	}
	if in.Free >= 0 {
		msg += fmt.Sprintf("; %s free", humanSize(in.Free))
	}
	return msg + fmt.Sprintf(". Answered in %d ms.", in.Ms)
}

// humanSize is decimal, as the page shows sizes.
func humanSize(n int64) string {
	units := []string{"bytes", "KB", "MB", "GB", "TB", "PB"}
	f, i := float64(n), 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
