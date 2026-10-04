package main

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrPrecondition = errors.New("precondition failed")
)

// Version identifies one stored revision of an object. It is opaque and is
// only ever compared for equality: GCS uses its generation number
// (stringified), S3 uses its ETag. The zero value (empty string) is never a
// real version — GCS generations are positive integers, S3 ETags are always
// quoted hex — so it doubles as the "the object must not exist yet"
// precondition sentinel (see Write).
type Version string

// createOnly is the Write precondition meaning "only if the object does not
// exist yet" (Version's zero value; see its doc comment).
const createOnly Version = ""

type ObjectInfo struct {
	Name     string
	Size     int64
	Updated  time.Time
	Metadata map[string]string
}

// Listing is one level of a listing: with a delimiter, Prefixes holds the
// "folders" below the requested prefix and Objects the files directly in it.
type Listing struct {
	Objects  []ObjectInfo
	Prefixes []string
}

// Store is the bucket, as the backend needs it. It is an interface so tests need
// no cloud storage; the real implementations are gcs.go (GCP) and s3.go (AWS).
type Store interface {
	// Read returns the object and its version, or ErrNotFound.
	Read(ctx context.Context, name string) ([]byte, Version, error)
	// Write stores data. ifVersionMatch nil writes unconditionally, a pointer to
	// createOnly means "only if the object does not exist", any other value
	// means "only if the current version equals it". A failed condition
	// returns ErrPrecondition.
	Write(ctx context.Context, name string, data []byte, ifVersionMatch *Version) (Version, error)
	// List lists one prefix level. withMetadata asks for ObjectInfo.Metadata to
	// be populated too: GCS includes it in the listing call at no extra cost,
	// but S3 does not return object metadata from ListObjectsV2, so the S3
	// implementation pays one extra HeadObject per object when this is true.
	// Callers that only need names/prefixes pass false.
	List(ctx context.Context, prefix, delimiter string, withMetadata bool) (Listing, error)
	// Delete removes an object; a missing object is not an error. The bucket's
	// soft delete keeps it recoverable for the retention window.
	Delete(ctx context.Context, name string) error
	// ListDeleted lists soft-deleted versions whose name starts with prefix.
	ListDeleted(ctx context.Context, prefix string) ([]DeletedObject, error)
	// Restore brings one soft-deleted version back as a live object. If a live
	// object of that name already exists the call returns ErrPrecondition.
	Restore(ctx context.Context, name string, version Version) error
	// ReadDeleted returns the content of one soft-deleted version, or ErrNotFound.
	ReadDeleted(ctx context.Context, name string, version Version) ([]byte, error)
}

// DeletedObject is one soft-deleted version of an object.
type DeletedObject struct {
	Name      string
	Version   Version
	DeletedAt time.Time
}

func gen(v Version) *Version { return &v }
