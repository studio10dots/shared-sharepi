package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// storeContract is what the handlers rely on from a Store, whatever bucket is
// behind it. It runs on the in-memory bucket the other tests use and on s3Store
// (against fakeS3), so the two cannot drift apart unnoticed.
func storeContract(t *testing.T, st Store) {
	ctx := context.Background()

	t.Run("a missing object is ErrNotFound", func(t *testing.T) {
		if _, _, err := st.Read(ctx, "c/missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Read missing = %v", err)
		}
	})

	t.Run("create-only, then conditional rewrites", func(t *testing.T) {
		v1, err := st.Write(ctx, "c/roster.json", []byte("one"), gen(createOnly))
		if err != nil || v1 == "" || v1 == createOnly {
			t.Fatalf("create = %q, %v", v1, err)
		}
		if _, err := st.Write(ctx, "c/roster.json", []byte("again"), gen(createOnly)); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("second create-only = %v, want ErrPrecondition", err)
		}
		data, got, err := st.Read(ctx, "c/roster.json")
		if err != nil || string(data) != "one" || got != v1 {
			t.Fatalf("Read = %q %q %v, want one %q", data, got, err, v1)
		}
		v2, err := st.Write(ctx, "c/roster.json", []byte("two"), gen(v1))
		if err != nil || v2 == v1 {
			t.Fatalf("rewrite on v1 = %q, %v", v2, err)
		}
		// v1 is stale now: a second writer that read v1 must lose.
		if _, err := st.Write(ctx, "c/roster.json", []byte("three"), gen(v1)); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("stale rewrite = %v, want ErrPrecondition", err)
		}
		if data, _, _ := st.Read(ctx, "c/roster.json"); string(data) != "two" {
			t.Fatalf("after a lost race = %q, want two", data)
		}
	})

	t.Run("a conditional write to a missing object fails the condition", func(t *testing.T) {
		if _, err := st.Write(ctx, "c/nothing", []byte("x"), gen("12345")); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("= %v, want ErrPrecondition", err)
		}
	})

	t.Run("an unconditional write overwrites", func(t *testing.T) {
		if _, err := st.Write(ctx, "c/plain", []byte("a"), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Write(ctx, "c/plain", []byte("b"), nil); err != nil {
			t.Fatal(err)
		}
		if data, _, _ := st.Read(ctx, "c/plain"); string(data) != "b" {
			t.Fatalf("= %q", data)
		}
	})

	t.Run("listing one level", func(t *testing.T) {
		for _, n := range []string{"l/g1/a.txt", "l/g1/sub/b.txt", "l/g1/sub/c.txt", "l/g2/d.txt"} {
			if _, err := st.Write(ctx, n, []byte("x"), nil); err != nil {
				t.Fatal(err)
			}
		}
		l, err := st.List(ctx, "l/g1/", "/", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Objects) != 1 || l.Objects[0].Name != "l/g1/a.txt" || l.Objects[0].Size != 1 {
			t.Errorf("objects = %+v", l.Objects)
		}
		if len(l.Prefixes) != 1 || l.Prefixes[0] != "l/g1/sub/" {
			t.Errorf("prefixes = %v", l.Prefixes)
		}
		all, _ := st.List(ctx, "l/", "", false)
		if len(all.Objects) != 4 || len(all.Prefixes) != 0 {
			t.Errorf("flat listing = %+v", all)
		}
	})

	t.Run("delete is soft: listed, readable, restorable once", func(t *testing.T) {
		if _, err := st.Write(ctx, "d/photo", []byte("pixels"), nil); err != nil {
			t.Fatal(err)
		}
		// A missing object is not an error to delete.
		if err := st.Delete(ctx, "d/never-existed"); err != nil {
			t.Fatalf("delete missing = %v", err)
		}
		if err := st.Delete(ctx, "d/photo"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.Read(ctx, "d/photo"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Read after delete = %v", err)
		}
		if l, _ := st.List(ctx, "d/", "", false); len(l.Objects) != 0 {
			t.Fatalf("a deleted object is still listed: %+v", l.Objects)
		}
		dels, err := st.ListDeleted(ctx, "d/")
		if err != nil || len(dels) != 1 || dels[0].Name != "d/photo" || dels[0].Version == "" {
			t.Fatalf("ListDeleted = %+v, %v", dels, err)
		}
		if dels[0].DeletedAt.IsZero() {
			t.Error("DeletedAt is not set")
		}
		v := dels[0].Version
		if b, err := st.ReadDeleted(ctx, "d/photo", v); err != nil || string(b) != "pixels" {
			t.Fatalf("ReadDeleted = %q, %v", b, err)
		}
		if _, err := st.ReadDeleted(ctx, "d/photo", "no-such-version"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ReadDeleted of an unknown version = %v", err)
		}
		if err := st.Restore(ctx, "d/photo", "no-such-version"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Restore of an unknown version = %v", err)
		}
		if err := st.Restore(ctx, "d/photo", v); err != nil {
			t.Fatalf("Restore = %v", err)
		}
		if data, _, err := st.Read(ctx, "d/photo"); err != nil || string(data) != "pixels" {
			t.Fatalf("Read after restore = %q, %v", data, err)
		}
		// Alive again: a second restore finds a live object in the way.
		if err := st.Restore(ctx, "d/photo", v); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("Restore over a live object = %v, want ErrPrecondition", err)
		}
		if dels, _ := st.ListDeleted(ctx, "d/"); len(dels) != 0 {
			t.Errorf("a restored object is still in the trash: %+v", dels)
		}
	})

	t.Run("deleting twice still restores", func(t *testing.T) {
		if _, err := st.Write(ctx, "d/twice", []byte("keep"), nil); err != nil {
			t.Fatal(err)
		}
		_ = st.Delete(ctx, "d/twice")
		_ = st.Delete(ctx, "d/twice")
		dels, _ := st.ListDeleted(ctx, "d/twice")
		if len(dels) != 1 {
			t.Fatalf("ListDeleted = %+v", dels)
		}
		if err := st.Restore(ctx, "d/twice", dels[0].Version); err != nil {
			t.Fatalf("Restore = %v", err)
		}
		if data, _, err := st.Read(ctx, "d/twice"); err != nil || string(data) != "keep" {
			t.Fatalf("Read = %q, %v", data, err)
		}
	})
}

func TestMemStoreContract(t *testing.T) { storeContract(t, newMemStore()) }

func TestS3StoreContract(t *testing.T) {
	t.Run("times that all differ", func(t *testing.T) {
		storeContract(t, newFakeS3(t).store())
	})
	t.Run("every entry in the same second", func(t *testing.T) {
		f := newFakeS3(t)
		f.sameSecond = true
		storeContract(t, f.store())
	})
}

// A listing does not carry metadata on S3, so List asks for it per object, and
// only when told to.
func TestS3ListFetchesMetadataOnlyWhenAsked(t *testing.T) {
	f := newFakeS3(t)
	st := f.store()
	ctx := context.Background()
	put := func(name string, meta map[string]string) {
		req, _ := http.NewRequest(http.MethodPut, f.srv.URL+"/"+f.bucket+"/"+name, strings.NewReader("x"))
		for k, v := range meta {
			req.Header.Set("x-amz-meta-"+k, v)
		}
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	put("ev/original/a.jpg", map[string]string{"uploader": "u-alice", "taken-at": "2026-09-01T10:00:00Z"})
	put("ev/original/b.jpg", nil)

	f.heads = 0
	l, err := st.List(ctx, "ev/original/", "", false)
	if err != nil || len(l.Objects) != 2 || f.heads != 0 {
		t.Fatalf("without metadata: %+v, %v, %d HEADs", l.Objects, err, f.heads)
	}
	if l.Objects[0].Metadata != nil {
		t.Errorf("metadata came back unasked: %v", l.Objects[0].Metadata)
	}

	l, err = st.List(ctx, "ev/original/", "", true)
	if err != nil || f.heads != 2 {
		t.Fatalf("with metadata: %v, %d HEADs", err, f.heads)
	}
	if got := l.Objects[0].Metadata; got["uploader"] != "u-alice" || got["taken-at"] != "2026-09-01T10:00:00Z" {
		t.Errorf("a.jpg metadata = %v", got)
	}
	if len(l.Objects[1].Metadata) != 0 {
		t.Errorf("b.jpg metadata = %v", l.Objects[1].Metadata)
	}
}

// A share manifest is tagged so terraform/backend-aws's lifecycle rule (which
// S3 cannot write as "ends in .share.json") finds it.
func TestS3TagsShareManifests(t *testing.T) {
	f := newFakeS3(t)
	st := f.store()
	ctx := context.Background()
	if _, err := st.Write(ctx, "grp/shares/abc"+shareManifestSuffix, []byte("{}"), gen(createOnly)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(ctx, "grp/members.json", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.tagging["grp/shares/abc"+shareManifestSuffix]; got != shareTag {
		t.Errorf("manifest tagging = %q, want %q", got, shareTag)
	}
	if got, ok := f.tagging["grp/members.json"]; ok {
		t.Errorf("a roster was tagged: %q", got)
	}
}

func TestS3SignerPut(t *testing.T) {
	f := newFakeS3(t)
	sg := s3Signer{p: s3.NewPresignClient(f.client()), bucket: f.bucket}
	u, hdr, err := sg.SignURL(context.Background(), SignRequest{
		Object: "grp/ev/original/p.jpg", Method: http.MethodPut, Expires: 15 * time.Minute,
		ContentType: "image/jpeg", Metadata: map[string]string{"uploader": "u-alice"}, MaxSize: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// What the client must send, under S3's names.
	if hdr["Content-Type"] != "image/jpeg" || hdr["x-amz-meta-uploader"] != "u-alice" {
		t.Errorf("headers = %v", hdr)
	}
	for k := range hdr {
		if strings.HasPrefix(k, "x-goog-") {
			t.Errorf("a GCS header %q on an S3 URL", k)
		}
	}
	// ...and they are part of the signature, so they cannot be dropped or
	// changed (the uploader cannot be forged).
	pu, _ := url.Parse(u)
	signed := pu.Query().Get("X-Amz-SignedHeaders")
	for _, h := range []string{"content-type", "x-amz-meta-uploader", "host"} {
		if !strings.Contains(signed, h) {
			t.Errorf("SignedHeaders %q lacks %s", signed, h)
		}
	}
	if pu.Query().Get("X-Amz-Expires") != "900" {
		t.Errorf("X-Amz-Expires = %q", pu.Query().Get("X-Amz-Expires"))
	}
	if !strings.HasSuffix(pu.Path, "/grp/ev/original/p.jpg") {
		t.Errorf("path = %s", pu.Path)
	}
}

func TestS3SignerGet(t *testing.T) {
	f := newFakeS3(t)
	sg := s3Signer{p: s3.NewPresignClient(f.client()), bucket: f.bucket}
	u, hdr, err := sg.SignURL(context.Background(), SignRequest{Object: "grp/x.jpg", Method: http.MethodGet, Expires: time.Hour})
	if err != nil || len(hdr) != 0 {
		t.Fatalf("= %q %v %v", u, hdr, err)
	}
	pu, _ := url.Parse(u)
	if pu.Query().Get("X-Amz-Signature") == "" || pu.Query().Get("X-Amz-Expires") != "3600" {
		t.Errorf("url = %s", u)
	}
	// The URL really serves the object it names.
	if _, err := f.store().Write(context.Background(), "grp/x.jpg", []byte("img"), nil); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || string(b) != "img" {
		t.Errorf("GET = %d %q", resp.StatusCode, b)
	}
	if _, _, err := sg.SignURL(context.Background(), SignRequest{Object: "x", Method: "POST", Expires: time.Minute}); err == nil {
		t.Error("an unsupported method was signed")
	}
}
