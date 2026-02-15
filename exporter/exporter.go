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
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/PlakarKorp/kloset/connectors"
	"github.com/PlakarKorp/kloset/connectors/exporter"
	"github.com/PlakarKorp/kloset/location"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	plakarss3 "github.com/PlakarKorp/integration-s3/common"
)

type S3Exporter struct {
	awsS3Client *s3.Client
	rootDir     string
	host        string
	bucket      string
	restoreDir  string
}

func init() {
	exporter.Register("s3", 0, NewS3Exporter)
}

func connect(location *url.URL, useSsl, insecure bool, accessKeyID, secretAccessKey string) (*s3.Client, error) {
	conn, err := plakarss3.Connect(location, useSsl, insecure, accessKeyID, secretAccessKey)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func NewS3Exporter(ctx context.Context, opts *connectors.Options, name string, config map[string]string) (exporter.Exporter, error) {
	target := config["location"]
	var accessKey string
	if tmp, ok := config["access_key"]; !ok {
		return nil, fmt.Errorf("missing access_key")
	} else {
		accessKey = tmp
	}

	var secretAccessKey string
	if tmp, ok := config["secret_access_key"]; !ok {
		return nil, fmt.Errorf("missing secret_access_key")
	} else {
		secretAccessKey = tmp
	}

	useSsl := true
	if value, ok := config["use_tls"]; ok {
		tmp, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid use_tls value")
		}
		useSsl = tmp
	}

	insecure := false
	if value, ok := config["tls_insecure_no_verify"]; ok {
		tmp, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid tls_insecure_no_verify value")
		}
		insecure = tmp
	}

	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	var (
		atoms      = strings.Split(parsed.RequestURI()[1:], "/")
		bucket     = atoms[0]
		restoreDir = path.Clean("/" + strings.Join(atoms[1:], "/"))
	)

	conn, err := connect(parsed, useSsl, insecure, accessKey, secretAccessKey)
	if err != nil {
		return nil, err
	}

	_, err = conn.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: &bucket,
	})
	if err != nil {
		// TODO: Handle this error with aws sdk v2 error types
		// if minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
		// 	return nil, fmt.Errorf("failed to create bucket %s: %w", bucket, err)
		// }
	}

	return &S3Exporter{
		rootDir:     parsed.Path,
		awsS3Client: conn,
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
	ok, err := plakarss3.BucketExists(ctx, p.awsS3Client, p.bucket)
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

	for record := range records {
		if record.Err != nil || record.IsXattr || !record.FileInfo.Lmode.IsRegular() {
			results <- record.Ok()
			continue
		}
		// NOTE: Do we need to use MD5 PutObjectInput?
		_, err := p.awsS3Client.PutObject(ctx, plakarss3.NewPutObjectInput(p.bucket, path.Join(p.restoreDir, record.Pathname), record.Reader, record.FileInfo.Lsize, s3types.StorageClassStandard))
		results <- record.Error(err)
	}

	return nil
}

func (p *S3Exporter) Close(ctx context.Context) error {
	return nil
}
