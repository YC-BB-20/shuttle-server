package remote

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// emptySHA is the SHA-256 of nothing, sent with requests that have no body.
const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign adds an AWS Signature Version 4 Authorization header for S3.
// payload is the hex SHA-256 of the body: the server checks the body
// against it, so a piece damaged on the way is refused, not stored.
func sign(req *http.Request, access, secret, region, payload string, now time.Time) {
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payload)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	// Signed: host, range, content-md5, content-type and every x-amz- header.
	names := []string{"host"}
	vals := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") || lk == "range" || lk == "content-md5" || lk == "content-type" || lk == "if-none-match" {
			names = append(names, lk)
			vals[lk] = strings.Join(trimAll(v), ",")
		}
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + vals[n] + "\n")
	}
	signed := strings.Join(names, ";")

	canon := strings.Join([]string{req.Method, canonPath(req.URL), canonQuery(req.URL.Query()), ch.String(), signed, payload}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canon))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])

	k := hmacSHA([]byte("AWS4"+secret), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+access+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func trimAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = strings.Join(strings.Fields(s), " ")
	}
	return out
}

// canonPath is the URI-encoded path; S3 encodes each segment once.
func canonPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		d, err := url.PathUnescape(s)
		if err != nil {
			d = s
		}
		segs[i] = awsEscape(d)
	}
	return strings.Join(segs, "/")
}

func canonQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape encodes everything except A-Z a-z 0-9 - _ . ~, as SigV4 asks.
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// objectURL builds the address of a key: endpoint/bucket/key (path style)
// or bucket.endpoint/key (host style). Each segment is escaped so that the
// signed path and the sent path agree.
func objectURL(endpoint *url.URL, bucket, key, style string) *url.URL {
	u := *endpoint
	base := strings.TrimRight(u.Path, "/")
	segs := []string{}
	if key != "" {
		segs = strings.Split(key, "/")
	}
	esc := make([]string, len(segs))
	for i, s := range segs {
		esc[i] = awsEscape(s)
	}
	var raw string
	if style == "host" {
		u.Host = bucket + "." + u.Host
		raw = base + "/" + strings.Join(esc, "/")
	} else {
		raw = base + "/" + awsEscape(bucket)
		if key != "" {
			raw += "/" + strings.Join(esc, "/")
		}
	}
	u.RawPath = raw
	p, _ := url.PathUnescape(raw)
	u.Path = p
	u.RawQuery = ""
	return &u
}
