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
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/PlakarKorp/kloset/objects"
	"github.com/PlakarKorp/kloset/params"
	"github.com/PlakarKorp/kloset/snapshot/exporter"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Exporter struct {
	minioClient *minio.Client
	rootDir     string
}

func init() {
	exporter.Register("s3", 0, NewS3Exporter)
}

func connect(location *url.URL, useSsl, insecure bool, accessKeyID, secretAccessKey string) (*minio.Client, error) {
	endpoint := location.Host

	transport, err := minio.DefaultTransport(useSsl)
	if err != nil {
		return nil, err
	}

	if insecure {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	// Initialize minio client object.
	return minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure:    useSsl,
		Transport: transport,
	})
}

func NewS3Exporter(ctx context.Context, opts *exporter.Options, name string, config map[string]string) (exporter.Exporter, error) {
	var (
		location        *url.URL
		accessKey       string
		secretAccessKey string
		useTls          bool = true
		insecure        bool
	)

	p := params.New()
	p.Url("location", &location, params.Required)
	p.String("access_key", &accessKey, params.Required)
	p.String("secret_access_key", &secretAccessKey, params.Required)
	p.Bool("use_tls", &useTls, params.Optional)
	p.Bool("tls_insecure_no_verify", &insecure, params.Optional)

	if err := p.Parse(config); err != nil {
		return nil, err
	}

	conn, err := connect(location, useTls, insecure, accessKey, secretAccessKey)
	if err != nil {
		return nil, err
	}

	err = conn.MakeBucket(ctx, strings.TrimPrefix(location.Path, "/"), minio.MakeBucketOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
			return nil, err
		}
	}

	return &S3Exporter{
		rootDir:     location.Path,
		minioClient: conn,
	}, nil
}

func (p *S3Exporter) Root(ctx context.Context) (string, error) {
	return p.rootDir, nil
}

func (p *S3Exporter) CreateDirectory(ctx context.Context, pathname string) error {
	return nil
}

func (p *S3Exporter) StoreFile(ctx context.Context, pathname string, fp io.Reader, size int64) error {
	_, err := p.minioClient.PutObject(ctx,
		strings.TrimPrefix(p.rootDir, "/"),
		strings.TrimPrefix(pathname, p.rootDir+"/"),
		fp, size, minio.PutObjectOptions{})
	return err
}

func (p *S3Exporter) SetPermissions(ctx context.Context, pathname string, fileinfo *objects.FileInfo) error {
	return nil
}

func (p *S3Exporter) CreateLink(ctx context.Context, oldname string, newname string, ltype exporter.LinkType) error {
	return errors.ErrUnsupported
}

func (p *S3Exporter) Close(ctx context.Context) error {
	return nil
}
