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
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

// The example from AWS's S3 documentation, "Example: GET Object".
func TestSignMatchesAWSExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	now, _ := time.Parse("20060102T150405Z", "20130524T000000Z")
	sign(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", emptySHA, now)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("signature:\n got %s\nwant %s", got, want)
	}
}

func TestObjectURLEscapesOnce(t *testing.T) {
	ep, _ := url.Parse("https://portal.example.com")
	u := objectURL(ep, "media", "A001/clip one+ä.mov", "path")
	if got := u.String(); got != "https://portal.example.com/media/A001/clip%20one%2B%C3%A4.mov" {
		t.Fatalf("path style: %s", got)
	}
	if got := canonPath(u); got != "/media/A001/clip%20one%2B%C3%A4.mov" {
		t.Fatalf("canonical: %s", got)
	}
	u = objectURL(ep, "media", "x.mov", "host")
	if got := u.String(); got != "https://media.portal.example.com/x.mov" {
		t.Fatalf("host style: %s", got)
	}
}

// fakeS3 is a small in-memory S3 that checks every body against its signed
// hash, and can refuse chosen requests to act out failures.
type fakeS3 struct {
	mu      sync.Mutex
	objs    map[string][]byte
	uploads map[string]map[int][]byte
	nextID  int
	fail    func(r *http.Request) int // a status to answer instead, 0 for none
	puts    int                       // pieces received
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objs: map[string][]byte{}, uploads: map[string]map[int][]byte{}}
}

func etagOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:8]) }

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Authorization"), "Credential=AK/") {
		w.WriteHeader(403)
		fmt.Fprint(w, "<Error><Code>InvalidAccessKeyId</Code></Error>")
		return
	}
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(body)
	if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) {
		w.WriteHeader(400)
		fmt.Fprint(w, "<Error><Code>XAmzContentSHA256Mismatch</Code></Error>")
		return
	}
	if f.fail != nil {
		if code := f.fail(r); code != 0 {
			w.WriteHeader(code)
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != "media" {
		w.WriteHeader(404)
		fmt.Fprint(w, "<Error><Code>NoSuchBucket</Code></Error>")
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == "GET" && key == "" && q.Get("list-type") == "2":
		f.list(w, q)
	case r.Method == "HEAD":
		b, ok := f.objs[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("ETag", `"`+etagOf(b)+`"`)
	case r.Method == "GET" && q.Has("uploadId"):
		parts, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, "<Error><Code>NoSuchUpload</Code></Error>")
			return
		}
		ns := []int{}
		for n := range parts {
			ns = append(ns, n)
		}
		sort.Ints(ns)
		fmt.Fprint(w, "<ListPartsResult>")
		for _, n := range ns {
			fmt.Fprintf(w, "<Part><PartNumber>%d</PartNumber><ETag>\"%s\"</ETag><Size>%d</Size></Part>", n, etagOf(parts[n]), len(parts[n]))
		}
		fmt.Fprint(w, "<IsTruncated>false</IsTruncated></ListPartsResult>")
	case r.Method == "GET":
		b, ok := f.objs[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			off, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.WriteHeader(206)
			w.Write(b[off:])
			return
		}
		w.Write(b)
	case r.Method == "PUT" && q.Has("partNumber"):
		parts, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		n, _ := strconv.Atoi(q.Get("partNumber"))
		if cs := r.Header.Get("X-Amz-Copy-Source"); cs != "" { // a piece copied on the server
			src, _ := url.PathUnescape(strings.TrimPrefix(cs, "/media/"))
			var a, b int
			fmt.Sscanf(r.Header.Get("X-Amz-Copy-Source-Range"), "bytes=%d-%d", &a, &b)
			parts[n] = append([]byte(nil), f.objs[src][a:b+1]...)
			fmt.Fprintf(w, "<CopyPartResult><ETag>\"%s\"</ETag></CopyPartResult>", etagOf(parts[n]))
			return
		}
		parts[n] = body
		f.puts++
		w.Header().Set("ETag", `"`+etagOf(body)+`"`)
	case r.Method == "PUT":
		if _, ok := f.objs[key]; ok && r.Header.Get("If-None-Match") == "*" {
			w.WriteHeader(412)
			return
		}
		if cs := r.Header.Get("X-Amz-Copy-Source"); cs != "" { // copied on the server
			src, _ := url.PathUnescape(strings.TrimPrefix(cs, "/media/"))
			b, ok := f.objs[src]
			if !ok {
				w.WriteHeader(404)
				return
			}
			f.objs[key] = append([]byte(nil), b...)
			fmt.Fprint(w, "<CopyObjectResult/>")
			return
		}
		f.objs[key] = body
	case r.Method == "POST" && q.Has("uploads"):
		f.nextID++
		id := fmt.Sprintf("up%d", f.nextID)
		f.uploads[id] = map[int][]byte{}
		fmt.Fprintf(w, "<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>", id)
	case r.Method == "POST" && q.Has("uploadId"):
		parts, ok := f.uploads[q.Get("uploadId")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		var c struct {
			Parts []struct {
				N    int    `xml:"PartNumber"`
				ETag string `xml:"ETag"`
			} `xml:"Part"`
		}
		xml.Unmarshal(body, &c)
		var all []byte
		for i, p := range c.Parts {
			if p.N != i+1 || strings.Trim(p.ETag, `"`) != etagOf(parts[p.N]) {
				w.WriteHeader(400)
				fmt.Fprint(w, "<Error><Code>InvalidPart</Code></Error>")
				return
			}
			all = append(all, parts[p.N]...)
		}
		f.objs[key] = all
		delete(f.uploads, q.Get("uploadId"))
		fmt.Fprint(w, "<CompleteMultipartUploadResult/>")
	case r.Method == "DELETE" && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(204)
	case r.Method == "DELETE":
		delete(f.objs, key)
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func (f *fakeS3) list(w http.ResponseWriter, q url.Values) {
	prefix := q.Get("prefix")
	max, _ := strconv.Atoi(q.Get("max-keys"))
	keys := []string{}
	for k := range f.objs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	var out bytes.Buffer
	n := 0
	trunc := false
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if max > 0 && n >= max {
			trunc = true
			break
		}
		rest := k[len(prefix):]
		if i := strings.Index(rest, "/"); i >= 0 {
			p := prefix + rest[:i+1]
			if !seen[p] {
				seen[p] = true
				fmt.Fprintf(&out, "<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes>", p)
				n++
			}
			continue
		}
		fmt.Fprintf(&out, "<Contents><Key>%s</Key><Size>%d</Size><ETag>\"%s\"</ETag><LastModified>2026-11-17T10:00:00.000Z</LastModified></Contents>", k, len(f.objs[k]), etagOf(f.objs[k]))
		n++
	}
	fmt.Fprintf(w, "<ListBucketResult><IsTruncated>%v</IsTruncated>%s</ListBucketResult>", trunc, out.String())
}

func s3Config(u string) Config {
	return Config{Name: "CTERA", Kind: KindS3, URL: u, Bucket: "media", User: "AK", Secret: "SK"}
}

func testOpts() Options {
	return Options{Budget: limits.NewBudget(64 << 20), PieceSize: 5 << 20, Parallel: 3, Tries: 3, Stall: 2 * time.Second, Timeout: 5 * time.Second}
}

func source(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(int64(n))).Read(b)
	return b
}

func TestS3CheckListStatOpen(t *testing.T) {
	f := newFakeS3()
	f.objs["A001/C001.mov"] = []byte("hello world")
	f.objs["A001/C002.mov"] = []byte("x")
	f.objs["notes.txt"] = []byte("n")
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, err := New(s3Config(ts.URL), testOpts())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in, err := r.Check(ctx)
	if err != nil || in.Folders != 1 || in.Files != 1 {
		t.Fatalf("check: %+v %v", in, err)
	}
	var got []string
	r.List(ctx, "A001", func(e Entry) error { got = append(got, fmt.Sprintf("%s:%d", e.Path, e.Size)); return nil })
	if strings.Join(got, ",") != "A001/C001.mov:11,A001/C002.mov:1" {
		t.Fatalf("list: %v", got)
	}
	if e, err := r.Stat(ctx, "A001/C001.mov"); err != nil || e.Size != 11 {
		t.Fatalf("stat: %+v %v", e, err)
	}
	if _, err := r.Stat(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	rc, err := r.Open(ctx, "A001/C001.mov", 6)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "world" {
		t.Fatalf("open from 6: %q", b)
	}
}

func TestS3PlainErrors(t *testing.T) {
	ts := httptest.NewServer(newFakeS3())
	defer ts.Close()
	c := s3Config(ts.URL)
	c.Bucket = "other"
	r, _ := New(c, testOpts())
	if _, err := r.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "bucket was not found") {
		t.Fatalf("missing bucket: %v", err)
	}
	c = s3Config(ts.URL)
	c.User = "WRONG"
	r, _ = New(c, testOpts())
	_, err := r.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not know this access key") || strings.Contains(err.Error(), "SK") {
		t.Fatalf("bad key: %v", err)
	}
	if Retryable(err) {
		t.Fatal("a refused key is not worth retrying")
	}
}

func TestS3SmallUploadNeverReplaces(t *testing.T) {
	f := newFakeS3()
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(s3Config(ts.URL), testOpts())
	src := source(1000)
	var st UploadState
	ctx := context.Background()
	if err := r.Upload(ctx, "a/small.bin", 1000, bytes.NewReader(src), &st, func(UploadState) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objs["a/small.bin"], src) {
		t.Fatal("stored bytes differ")
	}
	if err := r.Upload(ctx, "a/small.bin", 1000, bytes.NewReader(src), &st, func(UploadState) error { return nil }, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("second upload: %v", err)
	}
}

// A big file: pieces fail once with 500 (retried), then the upload is cut
// off after some pieces and continued from the saved state; only the missing
// pieces are sent again, and the stored file equals the source.
func TestS3MultipartRetriesAndResumes(t *testing.T) {
	f := newFakeS3()
	var mu sync.Mutex
	failed := map[string]bool{}
	f.fail = func(r *http.Request) int {
		mu.Lock()
		defer mu.Unlock()
		if n := r.URL.Query().Get("partNumber"); n == "2" && !failed[n] {
			failed[n] = true
			return 500
		}
		return 0
	}
	ts := httptest.NewServer(f)
	defer ts.Close()
	o := testOpts()
	o.Parallel = 1
	r, _ := New(s3Config(ts.URL), o)
	size := 5<<20*5 + 12345 // six pieces
	src := source(size)

	var saved UploadState
	ctx, cancel := context.WithCancel(context.Background())
	save := func(s UploadState) error {
		saved = s
		saved.Parts = append([]Part(nil), s.Parts...)
		if len(s.Parts) == 3 {
			cancel() // the line drops after three pieces
		}
		return nil
	}
	var st UploadState
	err := r.Upload(ctx, "big.mov", int64(size), bytes.NewReader(src), &st, save, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cut off: %v", err)
	}
	if len(saved.Parts) != 3 || saved.UploadID == "" {
		t.Fatalf("saved state: %+v", saved)
	}
	before := f.puts
	st = saved
	var last int64
	if err := r.Upload(context.Background(), "big.mov", int64(size), bytes.NewReader(src), &st, func(s UploadState) error { return nil }, func(n int64) { last = n }); err != nil {
		t.Fatal(err)
	}
	if sent := f.puts - before; sent != 3 {
		t.Fatalf("pieces sent after resuming: %d, want 3", sent)
	}
	if last != int64(size) {
		t.Fatalf("progress ended at %d", last)
	}
	if !bytes.Equal(f.objs["big.mov"], src) {
		t.Fatal("stored file differs from the source")
	}
	if len(f.uploads) != 0 {
		t.Fatal("unfinished upload left on the server")
	}
}

// A saved piece the server no longer has (or holds differently) is sent again.
func TestS3ResumeDropsPiecesTheServerLacks(t *testing.T) {
	f := newFakeS3()
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(s3Config(ts.URL), testOpts())
	size := 5<<20*3 + 1
	src := source(size)
	var st UploadState
	ctx, cancel := context.WithCancel(context.Background())
	r.Upload(ctx, "x.mov", int64(size), bytes.NewReader(src), &st, func(s UploadState) error {
		if len(s.Parts) == 2 {
			cancel()
		}
		return nil
	}, nil)
	f.mu.Lock()
	for _, p := range f.uploads {
		p[st.Parts[0].N] = []byte("damaged")
	}
	f.mu.Unlock()
	if err := r.Upload(context.Background(), "x.mov", int64(size), bytes.NewReader(src), &st, func(UploadState) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objs["x.mov"], src) {
		t.Fatal("stored file differs")
	}
}

// An upload the server forgot starts again rather than failing.
func TestS3ResumeAfterServerForgot(t *testing.T) {
	f := newFakeS3()
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(s3Config(ts.URL), testOpts())
	size := 5<<20*2 + 7
	src := source(size)
	st := UploadState{Path: "y.mov", Size: int64(size), PieceSize: 5 << 20, UploadID: "gone", Parts: []Part{{N: 1, ETag: "e", Size: 5 << 20}}}
	if err := r.Upload(context.Background(), "y.mov", int64(size), bytes.NewReader(src), &st, func(UploadState) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objs["y.mov"], src) {
		t.Fatal("stored file differs")
	}
}

func TestS3AbortFreesPieces(t *testing.T) {
	f := newFakeS3()
	ts := httptest.NewServer(f)
	defer ts.Close()
	r, _ := New(s3Config(ts.URL), testOpts())
	size := 5<<20*2 + 7
	var st UploadState
	ctx, cancel := context.WithCancel(context.Background())
	r.Upload(ctx, "z.mov", int64(size), bytes.NewReader(source(size)), &st, func(UploadState) error { cancel(); return nil }, nil)
	if len(f.uploads) != 1 {
		t.Fatal("expected an unfinished upload")
	}
	if err := r.Abort(context.Background(), st); err != nil || len(f.uploads) != 0 {
		t.Fatalf("abort: %v, %d left", err, len(f.uploads))
	}
}

func TestPieceSizeGrowsForHugeFiles(t *testing.T) {
	s := &s3{h: &httpc{o: Options{PieceSize: 64 << 20, Budget: limits.NewBudget(256 << 20)}.withDefaults()}}
	p, err := s.pieceFor(1 << 40) // 1 TiB
	if err != nil || p < (1<<40)/s3MaxParts || p%(1<<20) != 0 {
		t.Fatalf("1 TiB: %d %v", p, err)
	}
	if n := (int64(1<<40) + p - 1) / p; n >= s3MaxParts {
		t.Fatalf("pieces: %d", n)
	}
	if _, err := s.pieceFor(4 << 40); err == nil {
		t.Fatal("4 TiB pieces cannot fit a 256 MB budget twice")
	}
}

// Pieces in flight never take more than the budget.
func TestS3PiecesStayInsideBudget(t *testing.T) {
	f := newFakeS3()
	ts := httptest.NewServer(f)
	defer ts.Close()
	o := testOpts()
	o.Budget = limits.NewBudget(10 << 20) // two pieces of 5 MiB
	o.Parallel = 4
	r, _ := New(s3Config(ts.URL), o)
	size := 5<<20*6 + 3
	src := source(size)
	stop, ended := make(chan struct{}), make(chan struct{})
	peak := int64(0)
	go func() {
		defer close(ended)
		for {
			select {
			case <-stop:
				return
			default:
				if u := o.Budget.Stats().Used; u > peak {
					peak = u
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	var st UploadState
	err := r.Upload(context.Background(), "b.mov", int64(size), bytes.NewReader(src), &st, func(UploadState) error { return nil }, nil)
	close(stop)
	<-ended
	if err != nil || !bytes.Equal(f.objs["b.mov"], src) {
		t.Fatalf("upload: %v", err)
	}
	if peak > 10<<20 {
		t.Fatalf("budget exceeded: %d", peak)
	}
}

func TestS3RenameOnlyGivesOwnUploadsTheirName(t *testing.T) {
	f := newFakeS3()
	srv := httptest.NewServer(f)
	defer srv.Close()
	r, err := New(s3Config(srv.URL), testOpts())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	small, big := source(3<<20), source(12<<20)
	f.objs["Day 1/a.mov.shuttle-part"] = small
	f.objs["Day 1/b.mov.shuttle-part"] = big
	f.objs["Day 1/c.mov"] = []byte("there already")
	if err := r.Rename(ctx, "Day 1/a.mov.shuttle-part", "Day 1/a.mov"); err != nil {
		t.Fatal(err)
	}
	defer func(v int64) { s3CopyMax = v }(s3CopyMax)
	s3CopyMax = 5 << 20 // copied in pieces
	if err := r.Rename(ctx, "Day 1/b.mov.shuttle-part", "Day 1/b.mov"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objs["Day 1/a.mov"], small) || !bytes.Equal(f.objs["Day 1/b.mov"], big) {
		t.Fatal("the renamed objects differ from the uploads")
	}
	if _, ok := f.objs["Day 1/a.mov.shuttle-part"]; ok {
		t.Fatal("the temporary upload was left behind")
	}
	f.objs["Day 1/c.mov.shuttle-part"] = small
	if err := r.Rename(ctx, "Day 1/c.mov.shuttle-part", "Day 1/c.mov"); !errors.Is(err, ErrExists) {
		t.Fatalf("an existing name was not refused: %v", err)
	}
	if string(f.objs["Day 1/c.mov"]) != "there already" {
		t.Fatal("an existing object was replaced")
	}
	if err := r.Rename(ctx, "Day 1/c.mov", "Day 1/d.mov"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a rename of a real file on S3 was not refused: %v", err)
	}
	if err := r.RemoveTemp(ctx, "Day 1/c.mov"); err == nil {
		t.Fatal("RemoveTemp removed a file that is not Shuttle's own")
	}
}

func TestSplitPlace(t *testing.T) {
	for _, c := range []struct {
		in, id, rel string
		ok          bool
	}{
		{"srv:abc", "abc", "", true}, {"srv:abc/Day 1/a.mov", "abc", "Day 1/a.mov", true},
		{"srv:abc/../x", "", "", false}, {"/Volumes/T5", "", "", false}, {"srv:", "", "", false},
	} {
		id, rel, ok := SplitPlace(c.in)
		if id != c.id || rel != c.rel || ok != c.ok {
			t.Errorf("%q: got %q %q %v", c.in, id, rel, ok)
		}
	}
	if Place("abc", "") != "srv:abc" || Place("abc", "/a/b/") != "srv:abc/a/b" {
		t.Error("Place")
	}
}

// orderedReader records the offsets it is read at.
type orderedReader struct {
	b    []byte
	mu   sync.Mutex
	offs []int64
}

func (o *orderedReader) ReadAt(p []byte, off int64) (int, error) {
	o.mu.Lock()
	o.offs = append(o.offs, off)
	o.mu.Unlock()
	n := copy(p, o.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// v0.2.124: pieces are read in order (so the file can be hashed as it is
// sent), still sent several at once, and a piece sent again is not read again.
func TestS3ReadsPiecesInOrder(t *testing.T) {
	f := newFakeS3()
	var mu sync.Mutex
	failed := map[string]bool{}
	f.fail = func(r *http.Request) int {
		pn := r.URL.Query().Get("partNumber")
		if pn == "" {
			return 0
		}
		time.Sleep(time.Duration(len(pn)*7+int(pn[len(pn)-1]%3)*15) * time.Millisecond) // pieces finish out of order
		mu.Lock()
		defer mu.Unlock()
		if pn == "2" && !failed[pn] {
			failed[pn] = true
			return 503 // once: sent again from memory
		}
		return 0
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	r, _ := New(s3Config(srv.URL), testOpts())
	data := source(23 << 20) // 5 pieces of 5 MB
	src := &orderedReader{b: data}
	var st UploadState
	if err := r.Upload(context.Background(), "big.mov", int64(len(data)), src, &st, func(UploadState) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objs["big.mov"], data) {
		t.Fatal("the uploaded object differs")
	}
	if len(src.offs) != 5 {
		t.Fatalf("pieces read %d times, want 5: %v", len(src.offs), src.offs)
	}
	for i := 1; i < len(src.offs); i++ {
		if src.offs[i] <= src.offs[i-1] {
			t.Fatalf("pieces read out of order: %v", src.offs)
		}
	}
}

// v0.2.124: an address on this network may be typed bare and use plain http;
// one on the internet still needs https.
func TestLocalAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.8.1":           "http://192.168.8.1",
		"10.0.0.5:5005/dav":     "http://10.0.0.5:5005/dav",
		"nas.local/webdav":      "http://nas.local/webdav",
		"portal.example.com":    "https://portal.example.com",
		"https://192.168.8.1":   "https://192.168.8.1",
		" http://192.168.8.1/ ": "http://192.168.8.1/",
	} {
		if got := NormalizeURL(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	ok := Config{Name: "Home", Kind: KindWebDAV, URL: "http://192.168.8.1", User: "u", Secret: "p"}
	if err := ok.Valid(); err != nil {
		t.Errorf("a LAN address over http refused: %v", err)
	}
	bad := ok
	bad.URL = "http://8.8.8.8/dav"
	if err := bad.Valid(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("an internet address over http accepted: %v", err)
	}
}
