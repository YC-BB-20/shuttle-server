package copyjob

import (
	"context"
	"time"
)

// LucidClient is what copies need from LucidLink's client (package lucid).
type LucidClient interface {
	// Waiting is the bytes written on this computer not yet uploaded.
	Waiting(ctx context.Context) (int64, error)
	// Drop clears the cache of data already uploaded.
	Drop(ctx context.Context) error
}

// Tunables; tests make them small.
var (
	lucidPoll  = 15 * time.Second // how often LucidLink is asked while waiting
	lucidEvery = 10 * time.Second // how often a copy asks before its next file
)

// lucidReadFor is how a new copy or Verify reads back: only on LucidLink,
// as asked, or else as Settings say.
func lucidReadFor(lucid bool, asked, def string) string {
	if !lucid {
		return ""
	}
	if asked == "" {
		asked = def
	}
	return normLucidRead(asked)
}

// fromCache: the copy is read back from LucidLink's cache on this computer
// (Spec.LucidRead "cache"), and lucidUploaded is waited for afterwards.
func (s Spec) fromCache() bool { return s.Lucid && s.LucidRead == LucidReadCache }

// lucidReady holds the read-back of copies on LucidLink until LucidLink says
// nothing written on this computer is waiting to upload, then clears its
// cache, so what is read back comes from LucidLink's storage and not from
// this computer (DESIGN.md section 19). A read-back before that would prove
// only this computer's cache, so nothing is called verified meanwhile. If
// LucidLink cannot be asked, the job waits and says why; it never guesses.
func (j *Job) lucidReady(ctx context.Context, e env) error {
	return j.lucidWait(ctx, e, true)
}

// lucidUploaded (v0.2.110, LucidRead "cache") waits, after the copies were
// read back from the cache, until LucidLink says nothing written on this
// computer is left to upload. The cache is not cleared. Until then the job
// is not finished.
func (j *Job) lucidUploaded(ctx context.Context, e env) error {
	return j.lucidWait(ctx, e, false)
}

func (j *Job) lucidWait(ctx context.Context, e env, drop bool) error {
	why := "before reading the copies back"
	if !drop {
		why = "after checking the copies on this Mac"
	}
	logged := ""
	say := func(reason string) {
		j.setLive(func(l *live) { l.reason = reason })
		if reason != logged && e.log != nil {
			e.log.Info("a check on LucidLink waits", "job", j.st.ID, "reason", reason)
		}
		logged = reason
	}
	for {
		if e.lucid == nil {
			say("Waiting for LucidLink: Shuttle cannot ask LucidLink what is left to upload")
		} else if n, err := e.lucid.Waiting(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			say("Waiting for LucidLink: " + err.Error())
		} else if n > 0 {
			// The page splits this text into three lines (waitLines, v0.2.106): keep its shape.
			say("Waiting for LucidLink to finish uploading (" + humanBytes(n) + " left) " + why)
		} else if !drop {
			j.setLive(func(l *live) { l.reason = "" })
			if e.log != nil {
				e.log.Info("LucidLink has uploaded everything; the copies were read back from its cache on this computer", "job", j.st.ID)
			}
			return nil
		} else if err := e.lucid.Drop(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			say("Waiting to check the copies: " + err.Error())
		} else {
			j.setLive(func(l *live) { l.reason = "" })
			if e.log != nil {
				e.log.Info("LucidLink has uploaded everything and its cache is cleared; reading the copies back", "job", j.st.ID)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lucidPoll):
		}
	}
}

// lucidRoom keeps a copy to LucidLink from writing faster than LucidLink
// uploads: above lucidMax waiting, it pauses between files until three
// quarters of that is left. If LucidLink cannot be asked, copying goes on;
// the client slows writes itself when its cache is full, and the check
// still waits for the upload (lucidReady).
type lucidRoom struct {
	asked  time.Time
	warned bool
}

func (r *lucidRoom) wait(ctx context.Context, j *Job, e env) error {
	if e.lucid == nil || e.lucidMax <= 0 || time.Since(r.asked) < lucidEvery {
		return nil
	}
	held := false
	for {
		r.asked = time.Now()
		n, err := e.lucid.Waiting(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		limit := e.lucidMax
		if held {
			limit = e.lucidMax / 4 * 3
		}
		if err != nil || n <= limit {
			if err != nil && !r.warned && e.log != nil {
				e.log.Warn("LucidLink cannot say what is left to upload; copying goes on without pausing for it", "job", j.st.ID, "err", err)
				r.warned = true
			}
			if held {
				j.setLive(func(l *live) { l.reason = "" })
			}
			return nil
		}
		held = true
		j.setLive(func(l *live) {
			l.reason = "Waiting for LucidLink to upload: " + humanBytes(n) + " waiting; copying goes on below " + humanBytes(e.lucidMax/4*3)
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lucidPoll):
		}
	}
}
