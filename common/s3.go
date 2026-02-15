package common

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func Connect(location *url.URL, useSsl, insecure bool, accessKeyID, secretAccessKey string) (*s3.Client, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		// TODO: make configurable (And perhaps omittable)
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(cfg, buildOptions(location, useSsl, insecure, "Plakar-Integration/1.0")...), nil
}

func buildOptions(location *url.URL, useSsl, insecure bool, userAgent string) []func(*s3.Options) {
	return []func(*s3.Options){
		withEndpoint(location, useSsl),
		withHTTPClient(insecure),
		withUserAgent(userAgent),
		func(o *s3.Options) {
			// Make sure to use path style for S3 compatibility
			o.UsePathStyle = true
		},
	}
}

func withEndpoint(location *url.URL, useSsl bool) func(*s3.Options) {
	return func(o *s3.Options) {
		scheme := "http"
		if useSsl {
			scheme = "https"
		}

		if location.Host != "" {
			o.BaseEndpoint = aws.String(fmt.Sprintf("%s://%s", scheme, location.Host))
		}
	}
}

func withHTTPClient(insecure bool) func(*s3.Options) {
	return func(o *s3.Options) {
		if insecure {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			o.HTTPClient = &http.Client{Transport: tr}
		}
	}
}

func withUserAgent(ua string) func(*s3.Options) {
	return func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Build.Add(middleware.BuildMiddlewareFunc("AttachUserAgent",
				func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
					if req, ok := in.Request.(*smithyhttp.Request); ok {
						req.Header.Set("User-Agent", ua)
					}
					return next.HandleBuild(ctx, in)
				}), middleware.After)
		})
	}
}

func BucketExists(ctx context.Context, client *s3.Client, bucket string) (bool, error) {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &bucket,
	})
	if err != nil {
		var notFoundErr *types.NotFound
		if errors.As(err, &notFoundErr) {
			// The bucket does not exist, but an expected error
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func ObjectExists(ctx context.Context, client *s3.Client, bucket, key string) (bool, error) {
	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		var notFoundErr *types.NotFound
		if errors.As(err, &notFoundErr) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func MD5HashPutObjectInput(bucket, key string, body io.Reader, contentLength int64, storageClass types.StorageClass) *s3.PutObjectInput {
	hash := md5.New()
	_, err := io.Copy(hash, body)
	if err != nil {
		return nil
	}
	md5string := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	return &s3.PutObjectInput{
		Bucket:        &bucket,
		Key:           &key,
		Body:          body,
		ContentLength: &contentLength,
		StorageClass:  storageClass,
		ContentMD5:    &md5string,
	}
}

type S3SeekableFileReader struct {
	ctx    context.Context
	client *s3.Client
	bucket string
	key    string
}

func (s *S3SeekableFileReader) ReadAt(p []byte, off int64) (n int, err error) {
	limit := off + int64(len(p)) - 1
	rangeHeader := fmt.Sprintf("bytes=%d-%d", off, limit)

	out, err := s.client.GetObject(s.ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &s.key,
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return 0, err
	}
	defer out.Body.Close()

	return io.ReadFull(out.Body, p)
}

func (s *S3SeekableFileReader) Read(p []byte) (n int, err error) {
	return s.ReadAt(p, 0)
}

func (s *S3SeekableFileReader) Close() error {
	return nil
}

func NewS3SeekableFileReader(ctx context.Context, client *s3.Client, bucket, key string) (*S3SeekableFileReader, error) {
	return &S3SeekableFileReader{
		ctx:    ctx,
		client: client,
		bucket: bucket,
		key:    key,
	}, nil
}
