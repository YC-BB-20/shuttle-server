package remote

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type webdav struct {
	c    Config
	h    *httpc
	base *url.URL // the server's folder, ending in /
}

func newWebDAV(c Config, h *httpc) (*webdav, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return nil, err
	}
	f := strings.Trim(c.Folder, "/")
	segs := []string{strings.TrimRight(u.EscapedPath(), "/")}
	if f != "" {
		for _, s := range strings.Split(f, "/") {
			segs = append(segs, url.PathEscape(s))
		}
	}
	raw := strings.Join(segs, "/") + "/"
	u.RawPath = raw
	u.Path, _ = url.PathUnescape(raw)
	u.RawQuery, u.Fragment = "", ""
	return &webdav{c: c, h: h, base: u}, nil
}

// Resumable: plain WebDAV has no way to continue a cut-off PUT, so the file
// is sent again from the start (DESIGN.md section 7, rung 3).
func (d *webdav) Resumable() bool { return false }

func (d *webdav) url(p string, dir bool) (*url.URL, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	u := *d.base
	raw := strings.TrimSuffix(u.RawPath, "/")
	if p != "" {
		for _, s := range strings.Split(p, "/") {
			raw += "/" + url.PathEscape(s)
		}
	}
	if dir || p == "" {
		raw += "/"
	}
	u.RawPath = raw
	u.Path, _ = url.PathUnescape(raw)
	return &u, nil
}

func (d *webdav) req(ctx context.Context, method string, u *url.URL, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.SetBasicAuth(d.c.User, d.c.Secret)
	return req, nil
}

const propBody = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop>` +
	`<d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:getetag/><d:quota-available-bytes/></d:prop></d:propfind>`

type multistatus struct {
	Responses []struct {
		Href  string `xml:"href"`
		Props []struct {
			Status string `xml:"status"`
			Prop   struct {
				Type struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length string `xml:"getcontentlength"`
				Mod    string `xml:"getlastmodified"`
				ETag   string `xml:"getetag"`
				Free   string `xml:"quota-available-bytes"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

// propfind lists u (depth 0) or u and what is directly inside (depth 1).
func (d *webdav) propfind(ctx context.Context, u *url.URL, depth string, what string) (*multistatus, error) {
	var ms multistatus
	err := retry(ctx, d.h.o.Tries, func() error {
		req, err := d.req(ctx, "PROPFIND", u, strings.NewReader(propBody))
		if err != nil {
			return err
		}
		req.Header.Set("Depth", depth)
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		resp, body, err := d.h.short(ctx, req, 64<<20)
		if err != nil {
			return err
		}
		if resp.StatusCode == 404 {
			return ErrNotFound
		}
		if resp.StatusCode != 207 {
			return statusErr(resp.StatusCode, what, "")
		}
		ms = multistatus{}
		if err := xml.Unmarshal(body, &ms); err != nil {
			return &Error{Msg: "the server's answer to a listing could not be read; is this a WebDAV address?"}
		}
		return nil
	})
	return &ms, err
}

// entries turns a listing into entries, leaving out u itself.
func (d *webdav) entries(ms *multistatus, u *url.URL, keepSelf bool) []Entry {
	self := strings.TrimSuffix(u.Path, "/")
	base := strings.TrimSuffix(d.base.Path, "/")
	var out []Entry
	for _, r := range ms.Responses {
		h, err := url.Parse(strings.TrimSpace(r.Href))
		if err != nil {
			continue
		}
		p := strings.TrimSuffix(h.Path, "/")
		if p == self && !keepSelf {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, base), "/")
		if rel == "" || (!strings.HasPrefix(p, base+"/") && base != "") {
			continue
		}
		e := Entry{Path: rel}
		for _, ps := range r.Props {
			if ps.Status != "" && !strings.Contains(ps.Status, " 200") {
				continue
			}
			pr := ps.Prop
			if pr.Type.Collection != nil {
				e.Dir = true
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(pr.Length), 10, 64); err == nil {
				e.Size = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(pr.Mod)); err == nil {
				e.Mod = t
			}
			if pr.ETag != "" {
				e.ETag = strings.Trim(strings.TrimPrefix(strings.TrimSpace(pr.ETag), "W/"), `"`)
			}
		}
		out = append(out, e)
	}
	return out
}

func freeOf(ms *multistatus) int64 {
	if len(ms.Responses) == 0 {
		return -1
	}
	for _, ps := range ms.Responses[0].Props {
		if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.Free), 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return -1
}

func (d *webdav) Check(ctx context.Context) (Info, error) {
	t0 := time.Now()
	ms, err := d.propfind(ctx, d.base, "1", "list the folder")
	in := Info{Free: -1, Ms: time.Since(t0).Milliseconds()}
	if errors.Is(err, ErrNotFound) {
		return in, &Error{Code: 404, Msg: "the folder was not found on the server; check the address and the folder"}
	}
	if err != nil {
		return in, err
	}
	in.Free = freeOf(ms)
	for _, e := range d.entries(ms, d.base, false) {
		if e.Dir {
			in.Folders++
		} else {
			in.Files++
		}
	}
	return in, nil
}

func (d *webdav) List(ctx context.Context, dir string, fn func(Entry) error) error {
	u, err := d.url(dir, true)
	if err != nil {
		return err
	}
	ms, err := d.propfind(ctx, u, "1", "list "+dir)
	if err != nil {
		return err
	}
	for _, e := range d.entries(ms, u, false) {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (d *webdav) Stat(ctx context.Context, path string) (Entry, error) {
	u, err := d.url(path, false)
	if err != nil {
		return Entry{}, err
	}
	ms, err := d.propfind(ctx, u, "0", "read "+path)
	if err != nil {
		return Entry{}, err
	}
	es := d.entries(ms, u, true) // a depth-0 listing holds only the item itself
	if len(es) == 0 {
		return Entry{}, ErrNotFound
	}
	return es[0], nil
}

func (d *webdav) Open(ctx context.Context, path string, off int64) (io.ReadCloser, error) {
	u, err := d.url(path, false)
	if err != nil {
		return nil, err
	}
	var body io.ReadCloser
	err = retry(ctx, d.h.o.Tries, func() error {
		req, err := d.req(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		if off > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		}
		resp, err := d.h.long(ctx, req)
		if err != nil {
			return err
		}
		body, err = openAnswer(resp, off, "read "+path, func(code int, what string, _ []byte) error { return statusErr(code, what, "") })
		return err
	})
	return body, err
}

func (d *webdav) Mkdir(ctx context.Context, dir string) error {
	dir, err := clean(dir)
	if err != nil || dir == "" {
		return err
	}
	segs := strings.Split(dir, "/")
	for i := range segs {
		p := strings.Join(segs[:i+1], "/")
		u, _ := d.url(p, true)
		err := retry(ctx, d.h.o.Tries, func() error {
			req, err := d.req(ctx, "MKCOL", u, nil)
			if err != nil {
				return err
			}
			resp, _, err := d.h.short(ctx, req, 64<<10)
			if err != nil {
				return err
			}
			switch resp.StatusCode {
			case 201, 405: // made, or already there
				return nil
			}
			return statusErr(resp.StatusCode, "make the folder "+p, "")
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Upload sends the whole file in one PUT. It refuses to replace a file that
// is there already; Shuttle uploads under a temporary name and renames once
// the copy is proven, so a half-sent file never has the real name.
func (d *webdav) Upload(ctx context.Context, path string, size int64, src io.ReaderAt, st *UploadState, save func(UploadState) error, done func(int64)) error {
	u, err := d.url(path, false)
	if err != nil {
		return err
	}
	if _, err := d.Stat(ctx, path); err == nil {
		return ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	*st = UploadState{Path: path, Size: size}
	first := true
	err = retry(ctx, d.h.o.Tries, func() error {
		cr := &countReader{r: io.NewSectionReader(src, 0, size), done: done}
		req, err := d.req(ctx, "PUT", u, io.NopCloser(cr))
		if err != nil {
			return err
		}
		req.ContentLength = size
		if first { // a later try may replace what the cut-off one left: the same name, checked empty just now
			req.Header.Set("If-None-Match", "*")
			first = false
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := d.h.long(ctx, req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		switch resp.StatusCode {
		case 200, 201, 204:
			return nil
		}
		return statusErr(resp.StatusCode, "store "+path, "")
	})
	if err != nil {
		return err
	}
	// The size the server holds must be the size sent.
	e, err := d.Stat(ctx, path)
	if err != nil {
		return err
	}
	if e.Size != size {
		return &Error{Msg: fmt.Sprintf("the server holds %d bytes of %d after the upload", e.Size, size), Temp: true}
	}
	return nil
}

// Abort: a WebDAV PUT leaves nothing unfinished on the server.
func (d *webdav) Abort(context.Context, UploadState) error { return nil }

func (d *webdav) Rename(ctx context.Context, from, to string) error {
	dir := false
	if !IsTemp(from) { // Files' Move may move a folder, which WebDAV names with a final slash
		e, err := d.Stat(ctx, from)
		if err != nil {
			return err
		}
		dir = e.Dir
	}
	fu, err := d.url(from, dir)
	if err != nil {
		return err
	}
	tu, err := d.url(to, dir)
	if err != nil {
		return err
	}
	return retry(ctx, d.h.o.Tries, func() error {
		req, err := d.req(ctx, "MOVE", fu, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Destination", tu.String())
		req.Header.Set("Overwrite", "F")
		resp, _, err := d.h.short(ctx, req, 64<<10)
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case 201, 204:
			return nil
		}
		return statusErr(resp.StatusCode, "rename "+from, "")
	})
}

// RemoveTemp deletes one of Shuttle's own unfinished uploads.
func (d *webdav) RemoveTemp(ctx context.Context, path string) error {
	if !IsTemp(path) {
		return errors.New("only Shuttle's own unfinished uploads are removed")
	}
	u, err := d.url(path, false)
	if err != nil {
		return err
	}
	return retry(ctx, d.h.o.Tries, func() error {
		req, err := d.req(ctx, "DELETE", u, nil)
		if err != nil {
			return err
		}
		resp, _, err := d.h.short(ctx, req, 64<<10)
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case 200, 204, 404:
			return nil
		}
		return statusErr(resp.StatusCode, "remove the unfinished upload "+path, "")
	})
}
