// Package s3 uploads webinar recordings to an S3-compatible bucket.
package s3

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/vdao/webinar_mtx/internal/config"
)

// Client is a thin wrapper over minio-go tailored for recording uploads.
type Client struct {
	mc        *minio.Client
	bucket    string
	prefix    string
	publicURL string
}

// New creates a client; endpoint settings come from config.
func New(cfg config.S3Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("init S3 client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.Bucket, prefix: cfg.Prefix, publicURL: cfg.PublicURL}, nil
}

// ObjectKey builds the bucket key for a recording file.
func (c *Client) ObjectKey(recordingID, filename string) string {
	return c.prefix + "/" + recordingID + "/" + path.Base(filename)
}

// PublicURL builds the public download URL for a bucket key.
func (c *Client) PublicURL(key string) string {
	return c.publicURL + "/" + key
}

// UploadFile uploads one file under the given key with video/mp4 content type.
func (c *Client) UploadFile(ctx context.Context, key, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = c.mc.PutObject(ctx, c.bucket, key, f, fi.Size(), minio.PutObjectOptions{
		ContentType: "video/mp4",
	})
	if err != nil {
		return fmt.Errorf("put %s/%s: %w", c.bucket, key, err)
	}
	return nil
}
