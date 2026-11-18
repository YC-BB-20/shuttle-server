package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// s3 limits every compatible server keeps.
const (
	s3MinPart  = 5 << 20
	s3MaxParts = 10000
)

type s3 struct {
	c        Config
	h        *httpc
	endpoint *url.URL
	region   string
	now      func() time.Time
}

func newS3(c Config, h *httpc) (*s3, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.URL), "/"))
	if err != nil {
		return nil, err
	}
	r := c.Region
	if r == "" {
		r = "us-east-1"
	}
	c.Folder = strings.Trim(c.Folder, "/")
	return &s3{c: c, h: h, endpoint: u, region: r, now: time.Now}, nil
}

func (s *s3) Resumable() bool { return true }

func (s *s3) key(p string) (string, error) {
	p, err := clean(p)
	if err != nil {
		return "", err
	}
	return join(s.c.Folder, p), nil
}

// req builds a signed request. body is held whole (nil for none): S3 pieces
// are already in memory from the buffer budget, so the hash costs no extra read.
func (s *s3) req(ctx context.Context, method, key string, q url.Values, body []byte, hdr map[string]string) (*http.Request, error) {
	u := objectURL(s.endpoint, s.c.Bucket, key, s.c.Style)
	if q != nil {
		u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	}
	var rd io.Reader
	payload := emptySHA
	if body != nil {
		rd = bytes.NewReader(body)
		sum := sha256.Sum256(body)
		payload = hex.EncodeToString(sum[:])
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.URL = u // keep our own escaping, which is what was signed
	if body != nil {
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	sign(req, s.c.User, s.c.Secret, s.region, payload, s.now())
	return req, nil
}

// s3Err reads an S3 error document's code and message for the plain reason.
func s3Err(code int, what string, body []byte) error {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	xml.Unmarshal(body, &e)
	switch e.Code {
	case "NoSuchBucket":
		return &Error{Code: code, Msg: "the bucket was not found; check its name and the address"}
	case "InvalidAccessKeyId":
		return &Error{Code: code, Msg: "the server does not know this access key"}
	case "SignatureDoesNotMatch":
		return &Error{Code: code, Msg: "the server refused the secret key (or the region, or this Mac's clock is wrong)"}
	case "RequestTimeTooSkewed":
		return &Error{Code: code, Msg: "this Mac's clock is too far from the server's; set the time automatically in System Settings"}
	case "NoSuchUpload":
		return &Error{Code: code, Msg: "the server no longer has the unfinished upload"}
	case "AuthorizationHeaderMalformed":
		return &Error{Code: code, Msg: "the server refused the region; check the region"}
	case "PreconditionFailed":
		return ErrExists
	}
	return statusErr(code, what, e.Code)
}

func (s *s3) Check(ctx context.Context) (Info, error) {
	t0 := time.Now()
	in := Info{Free: -1}
	err := s.list(ctx, "", 1000, func(e Entry) error {
		if e.Dir {
			in.Folders++
		} else {
			in.Files++
		}
		return nil
	}, &in.More)
	in.Ms = time.Since(t0).Milliseconds()
	return in, err
}

func (s *s3) List(ctx context.Context, dir string, fn func(Entry) error) error {
	return s.list(ctx, dir, 0, fn, nil)
}

type listResult struct {
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
		ETag         string    `xml:"ETag"`
	} `xml:"Contents"`
	Prefixes    []struct{ Prefix string } `xml:"CommonPrefixes"`
	Truncated   bool                      `xml:"IsTruncated"`
	NextToken   string                    `xml:"NextContinuationToken"`
	KeyCount    int                       `xml:"KeyCount"`
	MaxKeysEcho int                       `xml:"MaxKeys"`
}

// list pages through one level. firstPage > 0 stops after one page of that
// size and reports in more whether there was another.
func (s *s3) list(ctx context.Context, dir string, firstPage int, fn func(Entry) error, more *bool) error {
	d, err := s.key(dir)
	if err != nil {
		return err
	}
	prefix := ""
	if d != "" {
		prefix = d + "/"
	}
	top := len(prefix)
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "delimiter": {"/"}, "prefix": {prefix}}
		if firstPage > 0 {
			q.Set("max-keys", strconv.Itoa(firstPage))
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := s.req(ctx, "GET", "", q, nil, nil)
		if err != nil {
			return err
		}
		var lr listResult
		err = retry(ctx, s.h.o.Tries, func() error {
			resp, body, err := s.h.short(ctx, req, 16<<20)
			if err != nil {
				return err
			}
			if resp.StatusCode != 200 {
				return s3Err(resp.StatusCode, "list the bucket", body)
			}
			lr = listResult{}
			if err := xml.Unmarshal(body, &lr); err != nil {
				return &Error{Msg: "the server's answer to a listing could not be read"}
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, p := range lr.Prefixes {
			name := strings.TrimSuffix(p.Prefix[min(top, len(p.Prefix)):], "/")
			if name == "" {
				continue
			}
			if err := fn(Entry{Path: s.rel(p.Prefix), Dir: true}); err != nil {
				return err
			}
		}
		for _, c := range lr.Contents {
			if c.Key == prefix || strings.HasSuffix(c.Key, "/") { // a folder marker
				continue
			}
			if err := fn(Entry{Path: s.rel(c.Key), Size: c.Size, Mod: c.LastModified, ETag: strings.Trim(c.ETag, `"`)}); err != nil {
				return err
			}
		}
		if firstPage > 0 {
			if more != nil {
				*more = lr.Truncated
			}
			return nil
		}
		if !lr.Truncated || lr.NextToken == "" {
			return nil
		}
		token = lr.NextToken
	}
}

// rel turns a key back into a path relative to the server's folder.
func (s *s3) rel(key string) string {
	key = strings.TrimSuffix(key, "/")
	if s.c.Folder != "" {
		key = strings.TrimPrefix(strings.TrimPrefix(key, s.c.Folder), "/")
	}
	return key
}

func (s *s3) Stat(ctx context.Context, path string) (Entry, error) {
	k, err := s.key(path)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	err = retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "HEAD", k, nil, nil, nil)
		if err != nil {
			return err
		}
		resp, _, err := s.h.short(ctx, req, 0)
		if err != nil {
			return err
		}
		if resp.StatusCode == 404 {
			return ErrNotFound
		}
		if resp.StatusCode != 200 {
			return statusErr(resp.StatusCode, "read "+path, "")
		}
		mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
		e = Entry{Path: s.rel(k), Size: resp.ContentLength, Mod: mod, ETag: strings.Trim(resp.Header.Get("ETag"), `"`)}
		return nil
	})
	return e, err
}

func (s *s3) Open(ctx context.Context, path string, off int64) (io.ReadCloser, error) {
	k, err := s.key(path)
	if err != nil {
		return nil, err
	}
	hdr := map[string]string{}
	if off > 0 {
		hdr["Range"] = fmt.Sprintf("bytes=%d-", off)
	}
	var body io.ReadCloser
	err = retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "GET", k, nil, nil, hdr)
		if err != nil {
			return err
		}
		resp, err := s.h.long(ctx, req)
		if err != nil {
			return err
		}
		body, err = openAnswer(resp, off, "read "+path, s3Err)
		return err
	})
	return body, err
}

// openAnswer checks a GET's answer and, when the server ignored the range,
// skips to off itself.
func openAnswer(resp *http.Response, off int64, what string, bad func(int, string, []byte) error) (io.ReadCloser, error) {
	switch {
	case resp.StatusCode == 206 || (resp.StatusCode == 200 && off == 0):
		return resp.Body, nil
	case resp.StatusCode == 200:
		if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
			resp.Body.Close()
			return nil, &Error{Msg: "the file on the server is shorter than expected", Temp: true}
		}
		return resp.Body, nil
	case resp.StatusCode == 404:
		resp.Body.Close()
		return nil, ErrNotFound
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return nil, bad(resp.StatusCode, what, body)
}

// Mkdir: S3 has no folders; a key's path makes them.
func (s *s3) Mkdir(context.Context, string) error { return nil }

// pieceFor picks the piece size for a file: the configured one, larger when
// the file would need more than S3's 10,000 pieces. A piece must fit the
// buffer budget twice over, so two files can move at once.
func (s *s3) pieceFor(size int64) (int64, error) {
	p := max(s.h.o.PieceSize, s3MinPart)
	if need := (size + s3MaxParts - 2) / (s3MaxParts - 1); need > p {
		p = (need + 1<<20 - 1) &^ (1<<20 - 1)
	}
	if b := s.h.o.Budget; b != nil && p > b.Total()/2 {
		return 0, &Error{Msg: fmt.Sprintf("the file is too large for Shuttle's memory budget: S3 needs pieces of %d MB", p>>20)}
	}
	return p, nil
}

func (s *s3) Upload(ctx context.Context, path string, size int64, src io.ReaderAt, st *UploadState, save func(UploadState) error, done func(int64)) error {
	k, err := s.key(path)
	if err != nil {
		return err
	}
	if s.h.o.Budget == nil {
		return errors.New("no buffer budget for uploads")
	}
	piece, err := s.pieceFor(size)
	if err != nil {
		return err
	}
	if size <= piece {
		return s.putSmall(ctx, k, size, src, done)
	}
	if st.UploadID != "" && (st.Path != path || st.Size != size || st.PieceSize != piece) {
		*st = UploadState{} // the file or the setting changed: that upload is not this one
	}
	if st.UploadID != "" {
		if err := s.confirmParts(ctx, k, st); err != nil {
			if errors.Is(err, ErrNotFound) {
				*st = UploadState{}
			} else {
				return err
			}
		}
	}
	if st.UploadID == "" {
		id, err := s.create(ctx, k)
		if err != nil {
			return err
		}
		*st = UploadState{Path: path, Size: size, PieceSize: piece, UploadID: id}
		if err := save(*st); err != nil {
			return err
		}
	}
	if err := s.sendParts(ctx, k, size, src, st, save, done); err != nil {
		return err
	}
	return s.complete(ctx, k, st)
}

// putSmall sends a file that fits in one piece with a single request.
// If-None-Match: * asks the server to refuse it if the name exists.
func (s *s3) putSmall(ctx context.Context, k string, size int64, src io.ReaderAt, done func(int64)) error {
	if _, err := s.Stat(ctx, s.rel(k)); err == nil {
		return ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	l, err := s.h.o.Budget.Acquire(ctx, max(size, 1))
	if err != nil {
		return err
	}
	defer s.h.o.Budget.Release(l)
	buf := l.B[:size]
	if err := readFull(src, buf, 0); err != nil {
		return err
	}
	err = retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "PUT", k, nil, buf, map[string]string{"If-None-Match": "*"})
		if err != nil {
			return err
		}
		resp, err := s.h.long(ctx, req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return s3Err(resp.StatusCode, "store "+s.rel(k), body)
		}
		return nil
	})
	if err == nil && done != nil {
		done(size)
	}
	return err
}

// readFull reads exactly len(buf) bytes at off. A short read is an error
// worth retrying, never a short piece (COMPATIBILITY.md).
func readFull(src io.ReaderAt, buf []byte, off int64) error {
	n, err := src.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil || err == io.EOF {
		return fmt.Errorf("the source gave %d of %d bytes; it may have changed", n, len(buf))
	}
	return err
}

func (s *s3) create(ctx context.Context, k string) (string, error) {
	if _, err := s.Stat(ctx, s.rel(k)); err == nil {
		return "", ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	var id string
	err := retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "POST", k, url.Values{"uploads": {""}}, nil, nil)
		if err != nil {
			return err
		}
		resp, body, err := s.h.short(ctx, req, 64<<10)
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			return s3Err(resp.StatusCode, "start an upload", body)
		}
		var r struct {
			UploadID string `xml:"UploadId"`
		}
		if xml.Unmarshal(body, &r) != nil || r.UploadID == "" {
			return &Error{Msg: "the server's answer to starting an upload could not be read"}
		}
		id = r.UploadID
		return nil
	})
	return id, err
}

// confirmParts keeps only the pieces the server really has, with the same
// size and tag; the others are sent again (DESIGN.md section 7, rung 2).
func (s *s3) confirmParts(ctx context.Context, k string, st *UploadState) error {
	have := map[int]Part{}
	marker := ""
	for {
		q := url.Values{"uploadId": {st.UploadID}}
		if marker != "" {
			q.Set("part-number-marker", marker)
		}
		var r struct {
			Parts []struct {
				N    int    `xml:"PartNumber"`
				ETag string `xml:"ETag"`
				Size int64  `xml:"Size"`
			} `xml:"Part"`
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextPartNumberMarker"`
		}
		err := retry(ctx, s.h.o.Tries, func() error {
			req, err := s.req(ctx, "GET", k, q, nil, nil)
			if err != nil {
				return err
			}
			resp, body, err := s.h.short(ctx, req, 8<<20)
			if err != nil {
				return err
			}
			if resp.StatusCode == 404 {
				return ErrNotFound
			}
			if resp.StatusCode != 200 {
				return s3Err(resp.StatusCode, "look up an unfinished upload", body)
			}
			return xml.Unmarshal(body, &r)
		})
		if err != nil {
			return err
		}
		for _, p := range r.Parts {
			have[p.N] = Part{N: p.N, ETag: strings.Trim(p.ETag, `"`), Size: p.Size}
		}
		if !r.Truncated || r.Next == "" {
			break
		}
		marker = r.Next
	}
	kept := st.Parts[:0]
	for _, p := range st.Parts {
		if h, ok := have[p.N]; ok && h.Size == p.Size && h.ETag == p.ETag {
			kept = append(kept, p)
		}
	}
	st.Parts = kept
	return nil
}

// sendParts sends the missing pieces, Parallel at a time, each with its own
// budget lease and retries. State is saved after every finished piece.
func (s *s3) sendParts(ctx context.Context, k string, size int64, src io.ReaderAt, st *UploadState, save func(UploadState) error, done func(int64)) error {
	n := int((size + st.PieceSize - 1) / st.PieceSize)
	have := map[int]bool{}
	for _, p := range st.Parts {
		have[p.N] = true
	}
	todo := make(chan int, n)
	for i := 1; i <= n; i++ {
		if !have[i] {
			todo <- i
		}
	}
	close(todo)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var mu sync.Mutex
	sent := st.Done()
	if done != nil && sent > 0 {
		done(sent)
	}
	var wg sync.WaitGroup
	for w := 0; w < min(s.h.o.Parallel, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range todo {
				if ctx.Err() != nil {
					return
				}
				off := int64(i-1) * st.PieceSize
				ln := min(st.PieceSize, size-off)
				etag, err := s.sendPart(ctx, k, st.UploadID, i, src, off, ln)
				if err != nil {
					cancel(err)
					return
				}
				mu.Lock()
				st.Parts = append(st.Parts, Part{N: i, ETag: etag, Size: ln})
				sent += ln
				err = save(*st)
				if done != nil {
					done(sent)
				}
				mu.Unlock()
				if err != nil {
					cancel(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil && err != context.Canceled {
		return err
	}
	return ctx.Err()
}

func (s *s3) sendPart(ctx context.Context, k, id string, n int, src io.ReaderAt, off, ln int64) (string, error) {
	l, err := s.h.o.Budget.Acquire(ctx, ln)
	if err != nil {
		return "", err
	}
	defer s.h.o.Budget.Release(l)
	buf := l.B[:ln]
	var etag string
	err = retry(ctx, s.h.o.Tries, func() error {
		if err := readFull(src, buf, off); err != nil {
			return &Error{Msg: err.Error(), Temp: true}
		}
		req, err := s.req(ctx, "PUT", k, url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {id}}, buf, nil)
		if err != nil {
			return err
		}
		resp, err := s.h.long(ctx, req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return s3Err(resp.StatusCode, "store a piece", body)
		}
		etag = strings.Trim(resp.Header.Get("ETag"), `"`)
		if etag == "" {
			return &Error{Msg: "the server stored a piece without saying so", Temp: true}
		}
		return nil
	})
	return etag, err
}

func (s *s3) complete(ctx context.Context, k string, st *UploadState) error {
	parts := append([]Part(nil), st.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].N < parts[j].N })
	var b bytes.Buffer
	b.WriteString("<CompleteMultipartUpload>")
	for _, p := range parts {
		fmt.Fprintf(&b, "<Part><PartNumber>%d</PartNumber><ETag>\"%s\"</ETag></Part>", p.N, xmlEsc(p.ETag))
	}
	b.WriteString("</CompleteMultipartUpload>")
	body := b.Bytes()
	return retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "POST", k, url.Values{"uploadId": {st.UploadID}}, body, map[string]string{"If-None-Match": "*"})
		if err != nil {
			return err
		}
		resp, err := s.h.long(ctx, req)
		if err != nil {
			return err
		}
		ans, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		// S3 can answer 200 with an error document when finishing fails late.
		if resp.StatusCode != 200 || bytes.Contains(ans, []byte("<Error>")) {
			code := resp.StatusCode
			if code == 200 {
				code = 500
			}
			return s3Err(code, "finish the upload", ans)
		}
		return nil
	})
}

func (s *s3) Abort(ctx context.Context, st UploadState) error {
	if st.UploadID == "" {
		return nil
	}
	k, err := s.key(st.Path)
	if err != nil {
		return err
	}
	return retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "DELETE", k, url.Values{"uploadId": {st.UploadID}}, nil, nil)
		if err != nil {
			return err
		}
		resp, body, err := s.h.short(ctx, req, 64<<10)
		if err != nil {
			return err
		}
		if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 404 {
			return s3Err(resp.StatusCode, "give up an unfinished upload", body)
		}
		return nil
	})
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// RemoveTemp deletes one of Shuttle's own unfinished uploads.
func (s *s3) RemoveTemp(ctx context.Context, path string) error {
	if !IsTemp(path) {
		return errors.New("only Shuttle's own unfinished uploads are removed")
	}
	k, err := s.key(path)
	if err != nil {
		return err
	}
	return retry(ctx, s.h.o.Tries, func() error {
		req, err := s.req(ctx, "DELETE", k, nil, nil, nil)
		if err != nil {
			return err
		}
		resp, body, err := s.h.short(ctx, req, 64<<10)
		if err != nil {
			return err
		}
		if resp.StatusCode == 204 || resp.StatusCode == 200 || resp.StatusCode == 404 {
			return nil
		}
		return s3Err(resp.StatusCode, "remove the unfinished upload "+path, body)
	})
}

// s3CopyMax is the largest object one server-side copy request may make;
// larger ones are copied in pieces (S3's limit is 5 GB).
var s3CopyMax int64 = 5 << 30

// Rename on S3 gives one of Shuttle's own uploads its final name once it is
// proven: the server copies the object to the new name (nothing passes
// through this computer), then the temporary object is removed. S3 has no
// real rename, so any other name is refused (ErrUnsupported) and an existing
// name is never replaced (ErrExists).
func (s *s3) Rename(ctx context.Context, from, to string) error {
	if !IsTemp(from) || IsTemp(to) {
		return ErrUnsupported
	}
	src, err := s.Stat(ctx, from)
	if err != nil {
		return err
	}
	fk, err := s.key(from)
	if err != nil {
		return err
	}
	tk, err := s.key(to)
	if err != nil {
		return err
	}
	if _, err := s.Stat(ctx, to); err == nil {
		return ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	segs := strings.Split(fk, "/")
	for i := range segs {
		segs[i] = awsEscape(segs[i])
	}
	source := "/" + s.c.Bucket + "/" + strings.Join(segs, "/")
	if src.Size <= s3CopyMax {
		err = retry(ctx, s.h.o.Tries, func() error {
			req, err := s.req(ctx, "PUT", tk, nil, nil, map[string]string{"x-amz-copy-source": source, "If-None-Match": "*"})
			if err != nil {
				return err
			}
			resp, err := s.h.long(ctx, req)
			if err != nil {
				return err
			}
			ans, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode != 200 || bytes.Contains(ans, []byte("<Error>")) {
				code := resp.StatusCode
				if code == 200 {
					code = 500
				}
				return s3Err(code, "give "+to+" its name", ans)
			}
			return nil
		})
	} else {
		err = s.copyParts(ctx, tk, source, src.Size)
	}
	if err != nil {
		return err
	}
	// The new name must hold as many bytes as the proven upload.
	if e, err := s.Stat(ctx, to); err != nil {
		return err
	} else if e.Size != src.Size {
		return &Error{Msg: fmt.Sprintf("the server's copy under the final name holds %d bytes of %d", e.Size, src.Size), Temp: true}
	}
	return s.RemoveTemp(ctx, from)
}

// copyParts copies a large object on the server in pieces of at least 1 GB.
func (s *s3) copyParts(ctx context.Context, tk, source string, size int64) error {
	piece := max(int64(1)<<30, (size+s3MaxParts-2)/(s3MaxParts-1))
	piece = min(piece, s3CopyMax)
	id, err := s.create(ctx, tk)
	if err != nil {
		return err
	}
	st := UploadState{Path: s.rel(tk), Size: size, PieceSize: piece, UploadID: id}
	for n, off := 1, int64(0); off < size; n, off = n+1, off+piece {
		end := min(off+piece, size) - 1
		var etag string
		err := retry(ctx, s.h.o.Tries, func() error {
			req, err := s.req(ctx, "PUT", tk, url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {id}}, nil,
				map[string]string{"x-amz-copy-source": source, "x-amz-copy-source-range": fmt.Sprintf("bytes=%d-%d", off, end)})
			if err != nil {
				return err
			}
			resp, err := s.h.long(ctx, req)
			if err != nil {
				return err
			}
			ans, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode != 200 || bytes.Contains(ans, []byte("<Error>")) {
				code := resp.StatusCode
				if code == 200 {
					code = 500
				}
				return s3Err(code, "copy a piece on the server", ans)
			}
			var r struct {
				ETag string `xml:"ETag"`
			}
			if xml.Unmarshal(ans, &r) != nil || r.ETag == "" {
				return &Error{Msg: "the server's answer to copying a piece could not be read", Temp: true}
			}
			etag = strings.Trim(r.ETag, `"`)
			return nil
		})
		if err != nil {
			s.Abort(context.WithoutCancel(ctx), st)
			return err
		}
		st.Parts = append(st.Parts, Part{N: n, ETag: etag, Size: end - off + 1})
	}
	return s.complete(ctx, tk, &st)
}
