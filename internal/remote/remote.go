// Package remote reaches file servers over the network: S3-compatible
// object storage and WebDAV, both written on the standard library. It is the
// server side of copies between services (DESIGN.md section 19), first for
// CTERA, whose portal offers both.
//
// Rules kept here: every request has a time limit or a stall timer; file data
// held in memory comes from the shared buffer budget; a login never appears in
// an error, a log line or a reply; nothing on a server is overwritten.
package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

// Kinds of server.
const (
	KindS3     = "s3"
	KindWebDAV = "webdav"
)

// Config is everything needed to reach one server. It holds the login, so it
// is stored only in the secrets folder and never sent to the page.
type Config struct {
	ID     string `json:"id"`
	Name   string `json:"name"`             // the owner's label, for example "CTERA"
	Kind   string `json:"kind"`             // s3 or webdav
	URL    string `json:"url"`              // https://portal.example.com (S3 endpoint) or https://portal.example.com/webdav
	Bucket string `json:"bucket,omitempty"` // S3
	Region string `json:"region,omitempty"` // S3; us-east-1 when empty
	Style  string `json:"style,omitempty"`  // S3: "path" (default, endpoint/bucket/key) or "host" (bucket.endpoint/key)
	Folder string `json:"folder,omitempty"` // optional folder inside the server that Shuttle works in
	User   string `json:"user"`             // WebDAV user name, or S3 access key ID
	Secret string `json:"secret"`           // WebDAV password, or S3 secret key
}

// Entry is one file or folder on a server. Path is relative to the server's
// folder, with forward slashes and no leading slash.
type Entry struct {
	Path string    `json:"path"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod,omitzero"`
	Dir  bool      `json:"dir,omitempty"`
	ETag string    `json:"etag,omitempty"`
}

// Info is what a connection test found: counts only, never a list.
type Info struct {
	Folders int   `json:"folders"`
	Files   int   `json:"files"`
	More    bool  `json:"more,omitempty"` // the top level holds more than one page
	Free    int64 `json:"free"`           // bytes the server says are free; -1 when it does not say
	Ms      int64 `json:"ms"`             // how long the round trip took
}

// UploadState lets an upload continue after an interruption. It holds no
// secret: an S3 upload ID is useless without the login.
type UploadState struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	PieceSize int64  `json:"piece"`
	UploadID  string `json:"upload_id,omitempty"`
	Parts     []Part `json:"parts,omitempty"`
}

// Part is one finished piece of an S3 multipart upload.
type Part struct {
	N    int    `json:"n"`
	ETag string `json:"etag"`
	Size int64  `json:"size"`
}

// Done is how many bytes the state says are on the server already.
func (u UploadState) Done() int64 {
	var n int64
	for _, p := range u.Parts {
		n += p.Size
	}
	return n
}

// Remote is one server.
type Remote interface {
	// Check is a read-only round trip: it lists the top of the server's folder.
	Check(ctx context.Context) (Info, error)
	// List calls fn for each entry directly inside dir ("" is the top).
	List(ctx context.Context, dir string, fn func(Entry) error) error
	// Stat describes one file; ErrNotFound when it is not there.
	Stat(ctx context.Context, path string) (Entry, error)
	// Open reads a file from byte off to its end.
	Open(ctx context.Context, path string, off int64) (io.ReadCloser, error)
	// Mkdir makes dir and its parents; an existing folder is fine.
	Mkdir(ctx context.Context, dir string) error
	// Upload sends size bytes read from src to path, continuing from st when
	// it names an unfinished upload. save is called after each finished piece,
	// and done (if set) with the bytes sent so far. An existing file is never
	// replaced: ErrExists.
	Upload(ctx context.Context, path string, size int64, src io.ReaderAt, st *UploadState, save func(UploadState) error, done func(int64)) error
	// Abort gives up an unfinished upload, so the server frees its pieces.
	Abort(ctx context.Context, st UploadState) error
	// Rename gives a finished file its final name, failing with ErrExists
	// rather than replacing one. On WebDAV it also moves files and folders
	// (Files' Move). S3 has no rename: there it only gives one of Shuttle's
	// own uploads (IsTemp) its final name, by a copy on the server, and is
	// ErrUnsupported for anything else.
	Rename(ctx context.Context, from, to string) error
	// Resumable says whether a cut-off upload continues where it stopped.
	Resumable() bool
	// RemoveTemp removes one of Shuttle's own unfinished uploads. It refuses
	// any name that is not a temporary name (IsTemp): nothing else on a
	// server is ever removed by a copy.
	RemoveTemp(ctx context.Context, path string) error
}

// TempSuffix ends the name a file is uploaded under until it is proven.
const TempSuffix = ".shuttle-part"

// IsTemp reports Shuttle's own temporary name.
func IsTemp(p string) bool { return strings.HasSuffix(p, TempSuffix) }

// PlacePrefix starts the path of a place on an added server, as the page
// and copy jobs write it: "srv:ID" is the top of server ID, "srv:ID/a/b" a
// folder or file inside it.
const PlacePrefix = "srv:"

// SplitPlace splits a server place into the server's id and the path inside
// it; ok is false for anything else (a folder on this computer).
func SplitPlace(p string) (id, rel string, ok bool) {
	r, ok := strings.CutPrefix(p, PlacePrefix)
	if !ok {
		return "", "", false
	}
	id, rel, _ = strings.Cut(r, "/")
	rel, err := clean(rel)
	if id == "" || err != nil {
		return "", "", false
	}
	return id, rel, true
}

// Place is the path of rel on server id.
func Place(id, rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return PlacePrefix + id
	}
	return PlacePrefix + id + "/" + rel
}

// IsPlace reports whether p is a place on a server.
func IsPlace(p string) bool { return strings.HasPrefix(p, PlacePrefix) }

// Errors a caller acts on.
var (
	ErrNotFound    = errors.New("not on the server")
	ErrExists      = errors.New("a file with that name is already on the server")
	ErrUnsupported = errors.New("this server cannot do that")
)

// Error is a server problem in plain words. It never holds a login.
type Error struct {
	Code int    // HTTP status, 0 when the request never got an answer
	Msg  string // plain words for the owner
	Temp bool   // worth trying again
}

func (e *Error) Error() string { return e.Msg }

// Retryable says whether trying the same request again can help: timeouts,
// dropped connections, 429 and 5xx. A refused login or a missing folder cannot.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Temp
	}
	return false
}

// Options are the limits shared by every server.
type Options struct {
	Budget    *limits.Budget // piece memory; required for S3 uploads
	PieceSize int64          // S3 piece size; 0 means 64 MiB
	Parallel  int            // pieces of one file sent at once; 0 means 4
	Stall     time.Duration  // a transfer with no bytes moving this long is cut; 0 means 2 min
	Timeout   time.Duration  // a request without file data; 0 means 1 min
	Tries     int            // tries per piece (or per whole file on WebDAV); 0 means 5
	Transport http.RoundTripper
}

func (o Options) withDefaults() Options {
	if o.PieceSize <= 0 {
		o.PieceSize = 64 << 20
	}
	if o.Parallel <= 0 {
		o.Parallel = 4
	}
	if o.Stall <= 0 {
		o.Stall = 2 * time.Minute
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Minute
	}
	if o.Tries <= 0 {
		o.Tries = 5
	}
	return o
}

// New opens a server from its config.
func New(c Config, o Options) (Remote, error) {
	if err := c.Valid(); err != nil {
		return nil, err
	}
	o = o.withDefaults()
	h := &httpc{o: o, client: &http.Client{Transport: transport(o), CheckRedirect: noRedirect}}
	switch c.Kind {
	case KindS3:
		return newS3(c, h)
	case KindWebDAV:
		return newWebDAV(c, h)
	}
	return nil, fmt.Errorf("unknown kind of server %q", c.Kind)
}

// Valid checks a config without contacting the server.
func (c Config) Valid() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("give the server a name")
	}
	if c.Kind != KindS3 && c.Kind != KindWebDAV {
		return errors.New("choose S3 or WebDAV")
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("the address should look like https://portal.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("type the address without a user name, ? or #; the login goes in its own boxes")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return errors.New("the address must start with https://, so the login is never sent unencrypted")
	}
	if c.Kind == KindS3 {
		if strings.TrimSpace(c.Bucket) == "" {
			return errors.New("an S3 server needs the bucket name")
		}
		if c.Style != "" && c.Style != "path" && c.Style != "host" {
			return errors.New("unknown S3 address style")
		}
	}
	if c.User == "" || c.Secret == "" {
		if c.Kind == KindS3 {
			return errors.New("an S3 server needs the access key and the secret key")
		}
		return errors.New("a WebDAV server needs the user name and the password")
	}
	if strings.Contains(c.Folder, "..") {
		return errors.New("the folder cannot contain ..")
	}
	return nil
}

func loopback(h string) bool {
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

func noRedirect(*http.Request, []*http.Request) error {
	// A redirect could carry the login to another host; S3 and WebDAV
	// servers that need one are configured with the address they redirect to.
	return http.ErrUseLastResponse
}

func transport(o Options) http.RoundTripper {
	if o.Transport != nil {
		return o.Transport
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute, // finishing a big S3 upload can take minutes
		ExpectContinueTimeout: 5 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   16,
		MaxConnsPerHost:       16, // a fixed ceiling on connections to one server
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

// httpc is the shared HTTP side: time limits and plain-language errors.
type httpc struct {
	o      Options
	client *http.Client
}

// short runs a request that carries no file data, under the request timeout,
// and reads at most limit bytes of its answer.
func (h *httpc) short(ctx context.Context, req *http.Request, limit int64) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.o.Timeout)
	defer cancel()
	resp, err := h.client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, nil, netErr(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return resp, nil, netErr(ctx, err)
	}
	return resp, body, nil
}

// long runs a request whose body or answer is file data. Instead of a total
// time limit it has a stall timer: no bytes moving for Stall cuts it.
// The answer's body is wrapped so reading it also resets the timer; closing it
// ends the request.
func (h *httpc) long(ctx context.Context, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	st := &stall{cancel: cancel, d: h.o.Stall}
	st.arm()
	if req.Body != nil {
		req.Body = &stallBody{ReadCloser: req.Body, s: st}
	}
	resp, err := h.client.Do(req.WithContext(ctx))
	if err != nil {
		st.stop()
		cancel(nil)
		if st.fired.Load() {
			return nil, stalledErr()
		}
		return nil, netErr(ctx, err)
	}
	resp.Body = &stallBody{ReadCloser: resp.Body, s: st, end: func() { st.stop(); cancel(nil) }}
	return resp, nil
}

var errStalled = errors.New("stalled")

func stalledErr() error {
	return &Error{Msg: "the server stopped sending or receiving data", Temp: true}
}

// stall cancels a request when no bytes have moved for d.
type stall struct {
	cancel context.CancelCauseFunc
	d      time.Duration
	t      atomic.Pointer[time.Timer]
	fired  atomic.Bool
}

func (s *stall) arm() {
	t := time.AfterFunc(s.d, func() { s.fired.Store(true); s.cancel(errStalled) })
	s.t.Store(t)
}
func (s *stall) poke() {
	if t := s.t.Load(); t != nil {
		t.Reset(s.d)
	}
}
func (s *stall) stop() {
	if t := s.t.Load(); t != nil {
		t.Stop()
	}
}

type stallBody struct {
	io.ReadCloser
	s   *stall
	end func()
}

func (b *stallBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.s.poke()
	}
	if err != nil && err != io.EOF && b.s.fired.Load() {
		return n, stalledErr()
	}
	return n, err
}

func (b *stallBody) Close() error {
	err := b.ReadCloser.Close()
	if b.end != nil {
		b.end()
	}
	return err
}

// netErr turns a failed request into plain words without the address's
// query or any header.
func netErr(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil && context.Cause(ctx) == context.Canceled {
		return context.Canceled
	}
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var hostErr interface{ Timeout() bool }
	switch {
	case errors.As(err, &dns):
		if dns.IsNotFound {
			return &Error{Msg: "the server's name was not found; check the address, or the internet connection", Temp: true}
		}
		return &Error{Msg: "could not look up the server's name; the internet connection may be down", Temp: true}
	case errors.As(err, &cert):
		return &Error{Msg: "the server's certificate is not trusted by this Mac, so nothing was sent to it"}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Msg: "the server did not answer in time", Temp: true}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &Error{Msg: "the server refused the connection; check the address and port", Temp: true}
	case errors.As(err, &hostErr) && hostErr.Timeout():
		return &Error{Msg: "the server did not answer in time", Temp: true}
	}
	return &Error{Msg: "the connection to the server failed; the internet connection may have dropped", Temp: true}
}

// statusErr turns an HTTP answer into plain words. what names the action,
// detail is the server's own short reason when it gave one.
func statusErr(code int, what, detail string) error {
	e := &Error{Code: code}
	switch {
	case code == 401:
		e.Msg = "the server refused the login; check the user name or key and the password or secret"
	case code == 403:
		e.Msg = "the login works but is not allowed to " + what
	case code == 404:
		e.Msg = "not found on the server: " + what
	case code == 407:
		e.Msg = "a proxy on this network asked for a login"
	case code == 409:
		e.Msg = "the server refused to " + what + ": a folder above it is missing or in the way"
	case code == 412:
		return ErrExists
	case code == 413:
		e.Msg = "the server refused a piece as too large"
	case code == 429:
		e.Msg, e.Temp = "the server asked Shuttle to slow down", true
	case code == 507:
		e.Msg = "the server is full"
	case code >= 500:
		e.Msg, e.Temp = fmt.Sprintf("the server had a problem (HTTP %d)", code), true
	case code >= 300 && code < 400:
		e.Msg = "the server sent Shuttle to another address; type the address it uses"
	default:
		e.Msg = fmt.Sprintf("the server refused to %s (HTTP %d)", what, code)
	}
	if detail = strings.TrimSpace(detail); detail != "" && len(detail) < 200 {
		e.Msg += " (" + detail + ")"
	}
	return e
}

// retry runs f up to tries times while its error is worth retrying, waiting
// 1, 2, 4, 8… seconds (at most 30) in between.
func retry(ctx context.Context, tries int, f func() error) error {
	wait := time.Second
	var err error
	for i := 0; i < tries; i++ {
		if err = f(); err == nil || !Retryable(err) {
			return err
		}
		if i == tries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if wait *= 2; wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
	return err
}

// clean makes a relative path with forward slashes and no leading or
// trailing slash; it refuses .. so nothing reaches outside the folder.
func clean(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	for _, s := range strings.Split(p, "/") {
		if s == ".." || s == "." {
			return "", errors.New("a path cannot contain . or ..")
		}
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p, nil
}

// join puts the server's folder in front of a relative path.
func join(folder, p string) string {
	folder = strings.Trim(folder, "/")
	switch {
	case folder == "":
		return p
	case p == "":
		return folder
	}
	return folder + "/" + p
}

// countReader counts bytes read and reports them.
type countReader struct {
	r    io.Reader
	n    int64
	done func(int64)
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if n > 0 && c.done != nil {
		c.done(c.n)
	}
	return n, err
}
