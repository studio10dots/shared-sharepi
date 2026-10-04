package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

type gcsStore struct{ b *storage.BucketHandle }

func mapErr(err error) error {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return ErrNotFound
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch ge.Code {
		case http.StatusPreconditionFailed:
			return ErrPrecondition
		case http.StatusNotFound:
			return ErrNotFound
		}
	}
	return err
}

func gcsVersion(generation int64) Version { return Version(strconv.FormatInt(generation, 10)) }

// gcsGeneration parses a Version this store itself produced; a value from
// another Store implementation (or a tampered one) fails closed as
// ErrNotFound rather than panicking or matching generation 0.
func gcsGeneration(v Version) (int64, error) {
	return strconv.ParseInt(string(v), 10, 64)
}

func (s gcsStore) Read(ctx context.Context, name string) ([]byte, Version, error) {
	rc, err := s.b.Object(name).NewReader(ctx)
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	return data, gcsVersion(rc.Attrs.Generation), err
}

func (s gcsStore) Write(ctx context.Context, name string, data []byte, ifVersionMatch *Version) (Version, error) {
	o := s.b.Object(name)
	if ifVersionMatch != nil {
		if *ifVersionMatch == createOnly {
			o = o.If(storage.Conditions{DoesNotExist: true})
		} else {
			n, err := gcsGeneration(*ifVersionMatch)
			if err != nil {
				return "", ErrPrecondition
			}
			o = o.If(storage.Conditions{GenerationMatch: n})
		}
	}
	w := o.NewWriter(ctx)
	if strings.HasSuffix(name, ".json") {
		w.ContentType = "application/json"
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return "", mapErr(err)
	}
	if err := w.Close(); err != nil {
		return "", mapErr(err)
	}
	return gcsVersion(w.Attrs().Generation), nil
}

// List's withMetadata is ignored here: GCS includes custom metadata in the
// listing call itself at no extra cost (see Store.List's doc comment).
func (s gcsStore) List(ctx context.Context, prefix, delimiter string, _ bool) (Listing, error) {
	it := s.b.Objects(ctx, &storage.Query{Prefix: prefix, Delimiter: delimiter})
	var out Listing
	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return Listing{}, mapErr(err)
		}
		if a.Prefix != "" {
			out.Prefixes = append(out.Prefixes, a.Prefix)
			continue
		}
		out.Objects = append(out.Objects, ObjectInfo{Name: a.Name, Size: a.Size, Updated: a.Updated, Metadata: a.Metadata})
	}
}

func (s gcsStore) Delete(ctx context.Context, name string) error {
	err := mapErr(s.b.Object(name).Delete(ctx))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s gcsStore) ListDeleted(ctx context.Context, prefix string) ([]DeletedObject, error) {
	it := s.b.Objects(ctx, &storage.Query{Prefix: prefix, SoftDeleted: true})
	var out []DeletedObject
	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, DeletedObject{Name: a.Name, Version: gcsVersion(a.Generation), DeletedAt: a.SoftDeleteTime})
	}
}

func (s gcsStore) Restore(ctx context.Context, name string, version Version) error {
	n, err := gcsGeneration(version)
	if err != nil {
		return ErrNotFound
	}
	_, err = s.b.Object(name).Generation(n).Restore(ctx, &storage.RestoreOptions{})
	return mapErr(err)
}

func (s gcsStore) ReadDeleted(ctx context.Context, name string, version Version) ([]byte, error) {
	n, err := gcsGeneration(version)
	if err != nil {
		return nil, ErrNotFound
	}
	r, err := s.b.Object(name).Generation(n).SoftDeleted().NewReader(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	return b, mapErr(err)
}

// gcsSigner signs V4 URLs as the service account through the IAM Credentials
// signBlob API (the storage client does this itself when no key file exists).
type gcsSigner struct {
	b        *storage.BucketHandle
	accessID string
}

func (s gcsSigner) SignURL(_ context.Context, r SignRequest) (string, map[string]string, error) {
	opts := &storage.SignedURLOptions{
		Scheme:         storage.SigningSchemeV4,
		Method:         r.Method,
		Expires:        time.Now().Add(r.Expires),
		GoogleAccessID: s.accessID,
	}
	hdr := map[string]string{}
	if r.ContentType != "" {
		opts.ContentType = r.ContentType
		hdr["Content-Type"] = r.ContentType
	}
	for k, v := range r.Metadata {
		h := "x-goog-meta-" + k
		hdr[h] = v
		opts.Headers = append(opts.Headers, h+":"+v)
	}
	if r.MaxSize > 0 {
		h := "x-goog-content-length-range"
		v := "0," + strconv.FormatInt(r.MaxSize, 10)
		hdr[h] = v
		opts.Headers = append(opts.Headers, h+":"+v)
	}
	u, err := s.b.SignedURL(r.Object, opts)
	if err != nil {
		return "", nil, err
	}
	return u, hdr, nil
}
