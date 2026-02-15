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

package importer

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/minio/minio-go/v7"

	"github.com/PlakarKorp/kloset/connectors"
	"github.com/PlakarKorp/kloset/connectors/importer"
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/objects"
)

type S3Importer struct {
	minioClient *minio.Client
	awsS3Client *s3.Client

	bucket  string
	host    string
	scanDir string
}

func init() {
	importer.Register("s3", 0, NewS3Importer)
}

func connect(location *url.URL, useSsl, insecure bool, accessKeyID, secretAccessKey string) (*s3.Client, error) {
	credsProvider := awscredentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(location.Host),
		awsconfig.WithCredentialsProvider(credsProvider),
	)

	// TODO: Add insecure and useSSL support

	if err != nil {
		return nil, err
	}

	svc := s3.NewFromConfig(cfg)
	return svc, nil
}

func NewS3Importer(ctx context.Context, opts *connectors.Options, name string, config map[string]string) (importer.Importer, error) {
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

	conn, err := connect(parsed, useSsl, insecure, accessKey, secretAccessKey)
	if err != nil {
		return nil, err
	}

	atoms := strings.Split(parsed.RequestURI()[1:], "/")
	bucket := atoms[0]
	scanDir := path.Clean("/" + strings.Join(atoms[1:], "/"))

	return &S3Importer{
		bucket:      bucket,
		scanDir:     scanDir,
		awsS3Client: conn,
		host:        parsed.Host,
	}, nil
}

func (p *S3Importer) Root() string          { return p.scanDir }
func (p *S3Importer) Origin() string        { return p.host + "/" + p.bucket }
func (p *S3Importer) Type() string          { return "s3" }
func (p *S3Importer) Flags() location.Flags { return 0 }

func (p *S3Importer) Ping(ctx context.Context) error {
	_, err := p.awsS3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &p.bucket,
	})
	if err != nil {
		return fmt.Errorf("ping bucket: %w", err)
	}
	return nil
}

func (p *S3Importer) Import(ctx context.Context, records chan<- *connectors.Record, results <-chan *connectors.Result) error {
	defer close(records)

	// racy, but ListObjects doesn't seem to signal failure
	// accessing the APIs.
	if err := p.Ping(ctx); err != nil {
		return err
	}

	listresults, err := p.awsS3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &p.bucket,
		Prefix: aws.String(strings.TrimPrefix(p.scanDir, "/")),
	})
	if err != nil {
		return fmt.Errorf("error during list objects: %w", err)
	}

	for _, object := range listresults.Contents {
		objkey := *object.Key
		if strings.HasSuffix(objkey, "/") {
			continue
		}

		fi := objects.FileInfo{
			Lname:    path.Base("/" + objkey),
			Lsize:    *object.Size,
			Lmode:    0700,
			LmodTime: *object.LastModified,
			Ldev:     1,
		}

		records <- connectors.NewRecord("/"+objkey, "", fi, nil, func() (io.ReadCloser, error) {
			object, err := p.awsS3Client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: &p.bucket,
				Key:    &objkey,
			})

			if err != nil {
				return nil, fmt.Errorf("error during get object: %w", err)
			}

			return object.Body, nil
		})
	}

	return nil
}

func (p *S3Importer) Close(ctx context.Context) error {
	return nil
}
