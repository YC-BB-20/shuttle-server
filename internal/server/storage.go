package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/speedtest"
	"github.com/YC-BB-20/shuttle-server/internal/storage"
)

// Disk is one card on the Storage page: a place from the picker plus what
// the system says about how it is connected.
type Disk struct {
	Place
	storage.Info
}

// storageList lists the computer's own disk and every connected disk, with
// connection, speed and kind (v0.2.32). It only reads. The whole answer is
// bounded in time; a slow hardware report is finished in the background and
// Pending tells the page to ask again.
func (s *Server) storageList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 11*time.Second)
	defer cancel()
	if r.URL.Query().Get("fresh") == "1" {
		storage.ForgetHardware()
		infoCache.Lock()
		infoCache.m = nil
		infoCache.Unlock()
	}
	out := []Disk{}
	for _, p := range places() {
		d := Disk{Place: p}
		if p.Note == "" && ctx.Err() == nil {
			at := p.Path
			if p.Kind == "internal" {
				at = "/"
			}
			d.Info = cachedDetails(ctx, at)
		}
		out = append(out, d)
	}
	markShared(out)
	writeJSON(w, http.StatusOK, map[string]any{"disks": out, "pending": storage.Pending()})
}

// The page asks every few seconds while Storage is open (v0.2.47): free
// space is read fresh every time, the slower details are kept for a minute
// unless the set of disks changes. At most one entry per mounted disk.
var infoCache struct {
	sync.Mutex
	m   map[string]cachedInfo
	sig string
}

type cachedInfo struct {
	in storage.Info
	at time.Time
}

func cachedDetails(ctx context.Context, at string) storage.Info {
	sig := mountSig()
	infoCache.Lock()
	if infoCache.sig != sig || infoCache.m == nil {
		infoCache.m, infoCache.sig = map[string]cachedInfo{}, sig
	}
	c, ok := infoCache.m[at]
	infoCache.Unlock()
	if ok && time.Since(c.at) < time.Minute && !storage.Pending() {
		c.in.Trash = storage.TrashHasItems(at, c.in.Internal || at == "/")
		c.in.Shares = nil
		return c.in
	}
	in := storage.Details(ctx, at)
	if in.Device != "" || in.FileSystem != "" {
		infoCache.Lock()
		if infoCache.sig == sig && len(infoCache.m) < 200 {
			infoCache.m[at] = cachedInfo{in, time.Now()}
		}
		infoCache.Unlock()
	}
	return in
}

func mountSig() string {
	var b strings.Builder
	for _, d := range mountDirs() {
		ents, _ := os.ReadDir(d)
		for i, e := range ents {
			if i > 100 {
				break
			}
			b.WriteString(e.Name())
			b.WriteByte('/')
		}
	}
	return b.String()
}

// markShared names, for each disk, the other disks on the same port or
// controller: they split its bandwidth when used at the same time.
func markShared(ds []Disk) {
	for i := range ds {
		if ds[i].BusKey == "" {
			continue
		}
		for j := range ds {
			if i != j && ds[j].BusKey == ds[i].BusKey && (ds[i].Unit == "" || ds[j].Unit != ds[i].Unit) {
				ds[i].Shares = append(ds[i].Shares, ds[j].Name)
			}
		}
	}
}

// speedStatus is the running or last speed test, the kept results of every
// disk, and the sizes offered (v0.2.33).
func (s *Server) speedStatus(w http.ResponseWriter, r *http.Request) {
	if s.Speed == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": speedtest.Status{}, "results": []speedtest.Result{}, "sizes": speedtest.Sizes})
		return
	}
	res := s.Speed.Results()
	if res == nil {
		res = []speedtest.Result{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Speed.Status(), "results": res, "sizes": speedtest.Sizes})
}

func (s *Server) speedAction(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.Speed == nil {
		return nil, errors.New("the speed test is not available in safe mode")
	}
	switch r.PathValue("action") {
	case "start":
		var req struct {
			Path     string `json:"path"`
			SizeGB   int    `json:"size_gb"`
			ReadOnly bool   `json:"read_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, errors.New("could not read the request")
		}
		if err := s.Speed.Start(req.Path, req.SizeGB, req.ReadOnly); err != nil {
			return nil, err
		}
	case "stop":
		s.Speed.Stop()
	default:
		return nil, errors.New("unknown action")
	}
	return map[string]bool{"ok": true}, nil
}
