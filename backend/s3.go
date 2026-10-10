package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// s3Store is the bucket on AWS. The bucket must have versioning on
// (terraform/backend-aws): that is what stands in for GCS's generations and
// soft delete here.
//
//   - A Version is the object's ETag (without quotes). Write's conditions use
//     S3's conditional writes: If-None-Match "*" for "only if absent", If-Match
//     for "only if the current version is this one".
//   - Deleting puts a delete marker over the object's versions, which is the
//     soft delete. The trash lists the keys whose latest entry is a delete
//     marker, and identifies a deleted object by its newest real version's
//     VersionId; restoring removes the delete marker(s) above it.
type s3Store struct {
	c      *s3.Client
	bucket string
}

// shareTag marks a share-link manifest for terraform/backend-aws's lifecycle
// rule: S3 lifecycle rules cannot match a name suffix the way GCS's can, and
// the group id in front of the name varies, so the tag stands in for it.
const shareTag = "chamagon-expire=share"

func apiCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return ""
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	switch apiCode(err) {
	case "NoSuchKey", "NotFound", "NoSuchVersion":
		return true
	}
	return errors.As(err, &nsk) || errors.As(err, &nf)
}

func mapS3Err(err error) error {
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return ErrNotFound
	}
	switch apiCode(err) {
	// 409 means another conditional write on the same key is in flight: the
	// caller's read-modify-write retry loop is exactly the right answer.
	case "PreconditionFailed", "ConditionalRequestConflict":
		return ErrPrecondition
	}
	return err
}

func s3Version(etag *string) Version {
	return Version(strings.Trim(aws.ToString(etag), `"`))
}

func (s s3Store) Read(ctx context.Context, name string) ([]byte, Version, error) {
	out, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &name})
	if err != nil {
		return nil, "", mapS3Err(err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	return data, s3Version(out.ETag), err
}

func (s s3Store) Write(ctx context.Context, name string, data []byte, ifVersionMatch *Version) (Version, error) {
	in := &s3.PutObjectInput{Bucket: &s.bucket, Key: &name, Body: bytes.NewReader(data)}
	if strings.HasSuffix(name, ".json") {
		in.ContentType = aws.String("application/json")
	}
	if strings.HasSuffix(name, shareManifestSuffix) {
		in.Tagging = aws.String(shareTag)
	}
	if ifVersionMatch != nil {
		if *ifVersionMatch == createOnly {
			in.IfNoneMatch = aws.String("*")
		} else {
			in.IfMatch = aws.String(`"` + string(*ifVersionMatch) + `"`)
		}
	}
	out, err := s.c.PutObject(ctx, in)
	if err != nil {
		// A write that must match a version of an object that is gone is a
		// failed condition too (GCS answers 412 there, S3 answers 404).
		if ifVersionMatch != nil && *ifVersionMatch != createOnly && isNotFound(err) {
			return "", ErrPrecondition
		}
		return "", mapS3Err(err)
	}
	return s3Version(out.ETag), nil
}

// headConcurrency bounds the HeadObject calls List makes for metadata.
const headConcurrency = 16

func (s s3Store) List(ctx context.Context, prefix, delimiter string, withMetadata bool) (Listing, error) {
	var out Listing
	in := &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix}
	if delimiter != "" {
		in.Delimiter = &delimiter
	}
	for p := s3.NewListObjectsV2Paginator(s.c, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return Listing{}, mapS3Err(err)
		}
		for _, cp := range page.CommonPrefixes {
			out.Prefixes = append(out.Prefixes, aws.ToString(cp.Prefix))
		}
		for _, o := range page.Contents {
			out.Objects = append(out.Objects, ObjectInfo{
				Name: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), Updated: aws.ToTime(o.LastModified),
			})
		}
	}
	if withMetadata {
		// S3 does not return an object's metadata in a listing (GCS does), so
		// each costs a HeadObject. Only the event's item list asks for it.
		var mu sync.Mutex
		var firstErr error
		parallel(headConcurrency, out.Objects, func(i int, o ObjectInfo) {
			h, err := s.c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &o.Name})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case isNotFound(err): // deleted since the listing: keep it without metadata
			case err != nil:
				if firstErr == nil {
					firstErr = err
				}
			default:
				out.Objects[i].Metadata = h.Metadata
			}
		})
		if firstErr != nil {
			return Listing{}, mapS3Err(firstErr)
		}
	}
	return out, nil
}

func (s s3Store) Delete(ctx context.Context, name string) error {
	// Deleting a missing key is not an error on S3 either (it answers 204).
	_, err := s.c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &name})
	return mapS3Err(err)
}

// versionsOf lists every version and delete marker of exactly this key.
func (s s3Store) versionsOf(ctx context.Context, name string) ([]types.ObjectVersion, []types.DeleteMarkerEntry, error) {
	var vs []types.ObjectVersion
	var ms []types.DeleteMarkerEntry
	in := &s3.ListObjectVersionsInput{Bucket: &s.bucket, Prefix: &name}
	for p := s3.NewListObjectVersionsPaginator(s.c, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, nil, mapS3Err(err)
		}
		for _, v := range page.Versions {
			if aws.ToString(v.Key) == name {
				vs = append(vs, v)
			}
		}
		for _, m := range page.DeleteMarkers {
			if aws.ToString(m.Key) == name {
				ms = append(ms, m)
			}
		}
	}
	// Both lists keep S3's order, newest first within a key. Times are whole
	// seconds and can tie, so they are never used to put entries in order.
	return vs, ms, nil
}

func (s s3Store) ListDeleted(ctx context.Context, prefix string) ([]DeletedObject, error) {
	// An object is deleted when the newest entry for its key is a delete marker
	// (IsLatest); what the trash offers back is then the key's newest real
	// version, found by time so the listing's order does not matter.
	type deleted struct {
		markerAt  time.Time
		isDeleted bool
		versionID string
		haveVer   bool
	}
	keys := map[string]*deleted{}
	entry := func(k string) *deleted {
		if keys[k] == nil {
			keys[k] = &deleted{}
		}
		return keys[k]
	}
	in := &s3.ListObjectVersionsInput{Bucket: &s.bucket, Prefix: &prefix}
	for p := s3.NewListObjectVersionsPaginator(s.c, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, mapS3Err(err)
		}
		for _, m := range page.DeleteMarkers {
			if aws.ToBool(m.IsLatest) {
				d := entry(aws.ToString(m.Key))
				d.isDeleted, d.markerAt = true, aws.ToTime(m.LastModified)
			}
		}
		for _, v := range page.Versions {
			// S3 lists a key's versions newest first, so the first one seen is
			// the newest (times are whole seconds and can tie).
			if d := entry(aws.ToString(v.Key)); !d.haveVer {
				d.versionID, d.haveVer = aws.ToString(v.VersionId), true
			}
		}
	}
	var out []DeletedObject
	for k, d := range keys {
		if d.isDeleted && d.haveVer {
			out = append(out, DeletedObject{Name: k, Version: Version(d.versionID), DeletedAt: d.markerAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s s3Store) Restore(ctx context.Context, name string, version Version) error {
	vs, ms, err := s.versionsOf(ctx, name)
	if err != nil {
		return err
	}
	if len(vs) == 0 {
		return ErrNotFound
	}
	// The object is deleted when a delete marker is the latest entry; if not, a
	// live object is already there.
	deleted := false
	for _, m := range ms {
		deleted = deleted || aws.ToBool(m.IsLatest)
	}
	if !deleted {
		return ErrPrecondition
	}
	newest := vs[0]
	if aws.ToString(newest.VersionId) != string(version) {
		return ErrNotFound
	}
	// Removing the delete markers above the newest version brings it back. A
	// marker that shares the version's second is removed too: removing one
	// beneath it changes nothing, and a wrongly kept one would hide it.
	for _, m := range ms {
		if aws.ToTime(m.LastModified).Before(aws.ToTime(newest.LastModified)) {
			continue
		}
		if _, err := s.c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &name, VersionId: m.VersionId}); err != nil {
			return mapS3Err(err)
		}
	}
	return nil
}

func (s s3Store) ReadDeleted(ctx context.Context, name string, version Version) ([]byte, error) {
	out, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &name, VersionId: aws.String(string(version))})
	if err != nil {
		return nil, mapS3Err(err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	return b, mapS3Err(err)
}

// s3Signer signs SigV4 URLs with the credentials the backend runs as (the
// Lambda's execution role): no key file, like gcsSigner's signBlob.
//
// One limit differs from GCS: a presigned PUT cannot carry a size range
// (GCS's x-goog-content-length-range), only an exact Content-Length, and the
// request does not know the size. So SignRequest.MaxSize is NOT enforced here.
// The app still keeps to the limits it checks itself; a modified app could
// upload a larger file, which on S3 costs only storage until it is deleted.
type s3Signer struct {
	p      *s3.PresignClient
	bucket string
}

func (s s3Signer) SignURL(ctx context.Context, r SignRequest) (string, map[string]string, error) {
	expires := s3.WithPresignExpires(r.Expires)
	switch r.Method {
	case http.MethodGet:
		out, err := s.p.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &r.Object}, expires)
		if err != nil {
			return "", nil, err
		}
		return out.URL, nil, nil
	case http.MethodPut:
		in := &s3.PutObjectInput{Bucket: &s.bucket, Key: &r.Object}
		hdr := map[string]string{}
		if r.ContentType != "" {
			in.ContentType = &r.ContentType
			hdr["Content-Type"] = r.ContentType
		}
		if len(r.Metadata) > 0 {
			in.Metadata = map[string]string{}
			for k, v := range r.Metadata {
				in.Metadata[k] = v
				hdr["x-amz-meta-"+k] = v
			}
		}
		out, err := s.p.PresignPutObject(ctx, in, expires)
		if err != nil {
			return "", nil, err
		}
		return out.URL, hdr, nil
	}
	return "", nil, errors.New("s3Signer: unsupported method " + r.Method)
}
