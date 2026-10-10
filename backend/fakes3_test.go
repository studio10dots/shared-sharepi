package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3 is the part of the S3 REST API s3Store uses, with the behaviour that
// matters to it: a versioned bucket (a delete is a delete marker), conditional
// writes (If-Match / If-None-Match), metadata that a listing does not return,
// and the newest-first order of ListObjectVersions. Path-style addressing.
//
// With sameSecond set every entry gets the same time, as S3's one-second
// resolution can: s3Store must not depend on times to order versions.
type fakeS3 struct {
	t          *testing.T
	srv        *httptest.Server
	bucket     string
	sameSecond bool

	mu   sync.Mutex
	tick int
	objs map[string][]*fakeEntry // per key, newest first
	seq  int

	puts, heads int // calls seen, so a test can check what a listing costs
	tagging     map[string]string
}

type fakeEntry struct {
	versionID string
	marker    bool
	data      []byte
	etag      string
	meta      map[string]string
	at        time.Time
}

func newFakeS3(t *testing.T) *fakeS3 {
	f := &fakeS3{t: t, bucket: "test-bucket", objs: map[string][]*fakeEntry{}, tagging: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// client is an S3 client pointed at the fake. Checksums are only sent when
// required: the default adds a trailing checksum in aws-chunked framing, which
// the fake does not decode.
func (f *fakeS3) client() *s3.Client {
	return s3.New(s3.Options{
		Region:                     "us-east-1",
		BaseEndpoint:               aws.String(f.srv.URL),
		UsePathStyle:               true,
		Credentials:                credentials.NewStaticCredentialsProvider("AKIAEXAMPLE", "secret", ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	})
}

func (f *fakeS3) store() s3Store { return s3Store{c: f.client(), bucket: f.bucket} }

func (f *fakeS3) now() time.Time {
	if f.sameSecond {
		return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	}
	f.tick++
	return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(f.tick) * time.Second)
}

func (f *fakeS3) live(key string) *fakeEntry {
	es := f.objs[key]
	if len(es) == 0 || es[0].marker {
		return nil
	}
	return es[0]
}

func fakeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rest := strings.TrimPrefix(r.URL.Path, "/"+f.bucket)
	q := r.URL.Query()
	if rest == "" || rest == "/" {
		if _, ok := q["versions"]; ok {
			f.listVersions(w, q.Get("prefix"))
		} else {
			f.listObjects(w, q.Get("prefix"), q.Get("delimiter"))
		}
		return
	}
	key := strings.TrimPrefix(rest, "/")
	switch r.Method {
	case http.MethodPut:
		f.put(w, r, key)
	case http.MethodGet, http.MethodHead:
		f.get(w, r, key, q.Get("versionId"))
	case http.MethodDelete:
		f.del(w, key, q.Get("versionId"))
	default:
		fakeErr(w, 405, "MethodNotAllowed")
	}
}

func (f *fakeS3) put(w http.ResponseWriter, r *http.Request, key string) {
	f.puts++
	data, _ := io.ReadAll(r.Body)
	cur := f.live(key)
	if r.Header.Get("If-None-Match") == "*" && cur != nil {
		fakeErr(w, 412, "PreconditionFailed")
		return
	}
	if m := r.Header.Get("If-Match"); m != "" {
		if cur == nil {
			fakeErr(w, 404, "NoSuchKey")
			return
		}
		if strings.Trim(m, `"`) != cur.etag {
			fakeErr(w, 412, "PreconditionFailed")
			return
		}
	}
	sum := md5.Sum(data)
	e := &fakeEntry{data: data, etag: hex.EncodeToString(sum[:]), meta: map[string]string{}, at: f.now()}
	f.seq++
	e.versionID = fmt.Sprintf("v%04d", f.seq)
	for k, v := range r.Header {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-amz-meta-") {
			e.meta[strings.TrimPrefix(lk, "x-amz-meta-")] = v[0]
		}
	}
	if tg := r.Header.Get("X-Amz-Tagging"); tg != "" {
		f.tagging[key] = tg
	}
	f.objs[key] = append([]*fakeEntry{e}, f.objs[key]...)
	w.Header().Set("ETag", `"`+e.etag+`"`)
	w.Header().Set("x-amz-version-id", e.versionID)
	w.WriteHeader(200)
}

func (f *fakeS3) get(w http.ResponseWriter, r *http.Request, key, versionID string) {
	var e *fakeEntry
	if versionID != "" {
		for _, c := range f.objs[key] {
			if c.versionID == versionID && !c.marker {
				e = c
			}
		}
		if e == nil {
			fakeErr(w, 404, "NoSuchVersion")
			return
		}
	} else if e = f.live(key); e == nil {
		if r.Method == http.MethodHead {
			w.WriteHeader(404)
			return
		}
		fakeErr(w, 404, "NoSuchKey")
		return
	}
	if r.Method == http.MethodHead {
		f.heads++
	}
	w.Header().Set("ETag", `"`+e.etag+`"`)
	w.Header().Set("x-amz-version-id", e.versionID)
	w.Header().Set("Last-Modified", e.at.Format(http.TimeFormat))
	w.Header().Set("Content-Length", fmt.Sprint(len(e.data)))
	for k, v := range e.meta {
		w.Header().Set("x-amz-meta-"+k, v)
	}
	w.WriteHeader(200)
	if r.Method == http.MethodGet {
		_, _ = w.Write(e.data)
	}
}

func (f *fakeS3) del(w http.ResponseWriter, key, versionID string) {
	if versionID != "" {
		es := f.objs[key]
		for i, e := range es {
			if e.versionID == versionID {
				f.objs[key] = append(es[:i:i], es[i+1:]...)
				break
			}
		}
	} else {
		f.seq++
		m := &fakeEntry{marker: true, versionID: fmt.Sprintf("m%04d", f.seq), at: f.now()}
		f.objs[key] = append([]*fakeEntry{m}, f.objs[key]...)
	}
	w.WriteHeader(204)
}

func (f *fakeS3) sortedKeys(prefix string) []string {
	var ks []string
	for k := range f.objs {
		if strings.HasPrefix(k, prefix) {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	return ks
}

func iso(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }

func (f *fakeS3) listObjects(w http.ResponseWriter, prefix, delim string) {
	type content struct {
		Key          string
		LastModified string
		ETag         string
		Size         int
	}
	type common struct{ Prefix string }
	var res struct {
		XMLName        xml.Name `xml:"ListBucketResult"`
		Name           string
		Prefix         string
		IsTruncated    bool
		Contents       []content
		CommonPrefixes []common
	}
	res.Name, res.Prefix = f.bucket, prefix
	seen := map[string]bool{}
	for _, k := range f.sortedKeys(prefix) {
		e := f.live(k)
		if e == nil {
			continue
		}
		if delim != "" {
			if i := strings.Index(k[len(prefix):], delim); i >= 0 {
				p := prefix + k[len(prefix):][:i+len(delim)]
				if !seen[p] {
					seen[p] = true
					res.CommonPrefixes = append(res.CommonPrefixes, common{p})
				}
				continue
			}
		}
		res.Contents = append(res.Contents, content{k, iso(e.at), `"` + e.etag + `"`, len(e.data)})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}

func (f *fakeS3) listVersions(w http.ResponseWriter, prefix string) {
	type version struct {
		Key          string
		VersionId    string
		IsLatest     bool
		LastModified string
		ETag         string
		Size         int
	}
	type marker struct {
		Key          string
		VersionId    string
		IsLatest     bool
		LastModified string
	}
	var res struct {
		XMLName      xml.Name `xml:"ListVersionsResult"`
		Name         string
		Prefix       string
		IsTruncated  bool
		Version      []version
		DeleteMarker []marker
	}
	res.Name, res.Prefix = f.bucket, prefix
	for _, k := range f.sortedKeys(prefix) {
		for i, e := range f.objs[k] {
			if e.marker {
				res.DeleteMarker = append(res.DeleteMarker, marker{k, e.versionID, i == 0, iso(e.at)})
			} else {
				res.Version = append(res.Version, version{k, e.versionID, i == 0, iso(e.at), `"` + e.etag + `"`, len(e.data)})
			}
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}
