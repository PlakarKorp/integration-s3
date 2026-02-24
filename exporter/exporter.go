/*
 * Copyright (c) 2023 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package exporter

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"path"
	"strings"

	sdk "github.com/PlakarKorp/go-kloset-sdk"
	"github.com/PlakarKorp/kloset/connectors"
	"github.com/PlakarKorp/kloset/connectors/exporter"
	"github.com/PlakarKorp/kloset/location"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/sync/errgroup"
)

type S3ExporterConfig struct {
	Location        string `json:"location"`
	AccessKey       string `json:"access_key"`
	SecretAccessKey string `json:"secret_access_key"`
	UseTLS          bool   `json:"use_tls"`
	TLSNoVerify     bool   `json:"tls_insecure_no_verify"`
}

type S3Exporter struct {
	opts        *connectors.Options
	minioClient *minio.Client
	rootDir     string
	host        string
	bucket      string
	restoreDir  string
}

func init() {
	exporter.Register("s3", 0, NewS3Exporter)
}

func connect(endpoint string, cfg S3ExporterConfig) (*minio.Client, error) {
	transport, err := minio.DefaultTransport(cfg.UseTLS)
	if err != nil {
		return nil, err
	}

	if cfg.TLSNoVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretAccessKey, ""),
		Secure:    cfg.UseTLS,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	client.SetAppInfo("plakar", "v1.1.0")

	return client, nil
}

//go:embed schema.json
var schema string

func NewS3Exporter(ctx context.Context, opts *connectors.Options, name string, config map[string]string) (exporter.Exporter, error) {
	var cfg S3ExporterConfig
	if err := sdk.DecodeConfig(schema, config, &cfg); err != nil {
		return nil, err
	}

	parsed, err := url.Parse(cfg.Location)
	if err != nil {
		return nil, err
	}

	var (
		atoms      = strings.Split(parsed.RequestURI()[1:], "/")
		bucket     = atoms[0]
		restoreDir = path.Clean("/" + strings.Join(atoms[1:], "/"))
	)

	conn, err := connect(parsed.Host, cfg)
	if err != nil {
		return nil, err
	}

	err = conn.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
			return nil, fmt.Errorf("failed to create bucket %s: %w", bucket, err)
		}
	}

	return &S3Exporter{
		opts:        opts,
		rootDir:     parsed.Path,
		minioClient: conn,
		host:        parsed.Host,
		bucket:      bucket,
		restoreDir:  restoreDir,
	}, nil
}

func (p *S3Exporter) Root() string          { return p.restoreDir }
func (p *S3Exporter) Origin() string        { return p.host + "/" + p.bucket }
func (p *S3Exporter) Type() string          { return "s3" }
func (p *S3Exporter) Flags() location.Flags { return 0 }

func (p *S3Exporter) Ping(ctx context.Context) error {
	ok, err := p.minioClient.BucketExists(ctx, p.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket does not exist")
	}
	return nil
}

func (p *S3Exporter) Export(ctx context.Context, records <-chan *connectors.Record, results chan<- *connectors.Result) error {
	defer close(results)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(p.opts.MaxConcurrency)

	for record := range records {
		if record.Err != nil || record.IsXattr || !record.FileInfo.Lmode.IsRegular() {
			results <- record.Ok()
			continue
		}

		g.Go(func() error {
			_, err := p.minioClient.PutObject(ctx, p.bucket, path.Join(p.restoreDir, record.Pathname),
				record.Reader, record.FileInfo.Lsize, minio.PutObjectOptions{})
			results <- record.Error(err)
			return nil
		})
	}

	return g.Wait()
}

func (p *S3Exporter) Close(ctx context.Context) error {
	return nil
}
