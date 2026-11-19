package server

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/ytdlp"
)

// YouTube's tools in Settings (v0.2.123): whether yt-dlp and Deno are there,
// their versions, and installing or updating them into Shuttle's tools folder
// (GET /api/youtube, POST /api/youtube/update). Only the official builds,
// each checked against its release's checksum.

var ytJob struct {
	mu      sync.Mutex
	working bool
	step    string
	err     string
	changed []string
	at      time.Time
}

func (s *Server) youtubeStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	ytJob.mu.Lock() // first, so a finished install is never reported with the programs looked for before it ended
	out["working"], out["step"], out["error"], out["changed"] = ytJob.working, ytJob.step, ytJob.err, ytJob.changed
	if !ytJob.at.IsZero() {
		out["finished"] = ytJob.at
	}
	ytJob.mu.Unlock()
	t := ytdlp.Find(s.ToolsDir)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	out["yt_dlp"], out["deno"], out["own"], out["missing"] = t.YTDLP != "", t.Deno != "", t.Own, t.Missing()
	if t.YTDLP != "" {
		out["yt_dlp_version"] = ytdlp.Version(ctx, t.YTDLP)
	}
	if t.Deno != "" {
		if f := strings.Fields(strings.TrimPrefix(ytdlp.Version(ctx, t.Deno), "deno ")); len(f) > 0 {
			out["deno_version"] = f[0] // "2.9.8", without the build details
		}
	}
	if c := ytdlp.LastChecked(s.ToolsDir); !c.IsZero() {
		out["checked"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) youtubeUpdate(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.ToolsDir == "" {
		return nil, errors.New("the tools folder is not set")
	}
	ytJob.mu.Lock()
	defer ytJob.mu.Unlock()
	if ytJob.working {
		return map[string]bool{"started": false}, nil
	}
	ytJob.working, ytJob.step, ytJob.err, ytJob.changed = true, "Starting", "", nil
	go func() {
		changed, err := ytdlp.Update(context.Background(), ytdlp.Client(), s.ToolsDir, runtime.GOOS, runtime.GOARCH, func(step string) {
			ytJob.mu.Lock()
			ytJob.step = step
			ytJob.mu.Unlock()
		})
		ytJob.mu.Lock()
		ytJob.working, ytJob.step, ytJob.changed, ytJob.at = false, "", changed, time.Now()
		if err != nil {
			ytJob.err = err.Error()
		}
		ytJob.mu.Unlock()
		if s.Log != nil {
			if err != nil {
				s.Log.Warn("YouTube tools: not updated", "err", err)
			} else {
				s.Log.Info("YouTube tools checked", "changed", strings.Join(changed, ", "))
			}
		}
	}()
	return map[string]bool{"started": true}, nil
}
