// Package ingest reads batched JSONL events from an S3-compatible
// bucket for a specific time window and hands them to a per-event
// callback. Reuses the Hive-partition layout markup-svc and funnel-sim
// write into (dt=YYYY-MM-DD/hour=HH/env=<env>/...).
package ingest

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Reader is the MinIO-backed ingest surface. One reader instance is
// shared across all consumers (search-v1, booking-v1 partitions).
type Reader struct {
	client *minio.Client
}

func New(ctx context.Context, cfg Config) (*Reader, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: minio client: %w", err)
	}
	return &Reader{client: client}, nil
}

// PrefixFor returns the object-prefix an operator would list to fetch
// all events in `bucket` for one hour: bucket/prefix/dt=YYYY-MM-DD/hour=HH/env=<env>/...
func PrefixFor(prefix string, at time.Time, env string) string {
	return fmt.Sprintf("%sdt=%s/hour=%02d/env=%s/",
		prefix, at.UTC().Format("2006-01-02"), at.UTC().Hour(), env)
}

// EachEvent walks every object under a computed prefix within the
// window [from, to] and invokes fn per JSONL row. Objects whose
// LastModified is before `from` or after `to` are skipped. gzip decode
// runs inline; a single bad object is logged (returned error) but does
// not abort the walk.
//
// Callback fn receives raw JSON bytes; unmarshalling happens in the
// caller's typed decoder so this package stays schema-agnostic.
func (r *Reader) EachEvent(
	ctx context.Context,
	bucket, prefix string,
	env string,
	from, to time.Time,
	fn func(row []byte) error,
) (int, error) {
	// The Hive layout gives us a lower-cost prefix scope per hour. If
	// the window straddles hours we walk both.
	hours := hoursIn(from, to)
	total := 0
	for _, h := range hours {
		p := PrefixFor(prefix, h, env)
		objects := r.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
			Prefix:    p,
			Recursive: true,
		})
		for obj := range objects {
			if obj.Err != nil {
				return total, fmt.Errorf("list %s: %w", p, obj.Err)
			}
			// Skip objects clearly outside window (LastModified is a
			// coarse proxy; batches contain rows that all fell in ONE
			// flush interval so per-row filter would be redundant).
			if obj.LastModified.Before(from) || obj.LastModified.After(to) {
				continue
			}
			n, err := r.streamObject(ctx, bucket, obj.Key, fn)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

func (r *Reader) streamObject(ctx context.Context, bucket, key string, fn func([]byte) error) (int, error) {
	obj, err := r.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = obj.Close() }()

	var reader = bufio.NewReader(obj)
	if strings.HasSuffix(key, ".gz") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return 0, fmt.Errorf("gzip %s: %w", key, err)
		}
		defer func() { _ = gz.Close() }()
		return scanJSONL(gz, fn)
	}
	return scanJSONL(reader, fn)
}

func scanJSONL(rd interface {
	Read(p []byte) (n int, err error)
}, fn func([]byte) error) (int, error) {
	dec := json.NewDecoder(rd)
	dec.UseNumber()
	n := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return n, fmt.Errorf("jsonl decode row %d: %w", n, err)
		}
		if err := fn(raw); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// hoursIn returns every hour boundary between from and to inclusive,
// so a window straddling e.g. 09:55–10:05 walks both 09 and 10.
func hoursIn(from, to time.Time) []time.Time {
	from = from.UTC().Truncate(time.Hour)
	to = to.UTC().Truncate(time.Hour)
	var out []time.Time
	for t := from; !t.After(to); t = t.Add(time.Hour) {
		out = append(out, t)
	}
	return out
}
