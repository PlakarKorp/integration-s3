/*
 * Copyright (c) 2021 Gilles Chehade <gilles@poolp.org>
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

package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/PlakarKorp/kloset/connectors/storage"
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/PlakarKorp/kloset/reading"

	plakarss3 "github.com/PlakarKorp/integration-s3/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/minio/minio-go/v7"
)

type Store struct {
	minioClient *minio.Client
	awsS3Client *s3.Client
	location    string
	host        string
	root        string
	bucket      string
	prefixDir   string

	useSsl          bool
	insecure        bool
	accessKey       string
	secretAccessKey string

	//storageClass string
	storageClass s3types.StorageClass

	bufPool sync.Pool

	putObjectOptions s3.PutObjectInput
}

func init() {
	storage.Register("s3", 0, NewStore)
}

func NewStore(ctx context.Context, proto string, storeConfig map[string]string) (storage.Store, error) {
	var accessKey string
	if value, ok := storeConfig["access_key"]; !ok {
		return nil, fmt.Errorf("missing access_key")
	} else {
		accessKey = value
	}

	var secretAccessKey string
	if value, ok := storeConfig["secret_access_key"]; !ok {
		return nil, fmt.Errorf("missing secret_access_key")
	} else {
		secretAccessKey = value
	}

	useSsl := true
	if value, ok := storeConfig["use_tls"]; ok {
		tmp, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid use_tls value")
		}
		useSsl = tmp
	}

	insecure := false
	if value, ok := storeConfig["tls_insecure_no_verify"]; ok {
		tmp, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid tls_insecure_no_verify value")
		}
		insecure = tmp
	}

	storageClass := "STANDARD"
	if value, ok := storeConfig["storage_class"]; ok {
		storageClass = strings.ToUpper(value)
		if storageClass != "STANDARD" && storageClass != "REDUCED_REDUNDANCY" && storageClass != "STANDARD_IA" && storageClass != "ONEZONE_IA" && storageClass != "INTELLIGENT_TIERING" && storageClass != "GLACIER" && storageClass != "GLACIER_IR" && storageClass != "DEEP_ARCHIVE" {
			return nil, fmt.Errorf("invalid storage_class value")
		}
	}

	u, err := url.Parse(storeConfig["location"])
	if err != nil {
		return nil, fmt.Errorf("parse location: %w", err)
	}

	return &Store{
		location:        storeConfig["location"],
		host:            u.Host,
		root:            u.Path,
		accessKey:       accessKey,
		secretAccessKey: secretAccessKey,
		useSsl:          useSsl,
		insecure:        insecure,
		storageClass:    s3types.StorageClass(storageClass),

		bufPool: sync.Pool{
			New: func() any {
				return &bytes.Buffer{}
			},
		},

		// putObjectOptions: s3.PutObjectInput{
		// 	// Some providers (eg. BlackBlaze) return the error
		// 	// "Unsupported header 'x-amz-checksum-algorithm'" if SendContentMd5
		// 	// is not set.
		// 	StorageClass:   storageClass,
		// 	SendContentMd5: true,
		// },
	}, nil
}

func (s *Store) realpath(path string) string {
	return s.prefixDir + path
}

func (s *Store) connect() error {
	useSSL := s.useSsl
	insecure := s.insecure

	parsed, err := url.Parse(s.location)
	if err != nil {
		return err
	}

	conn, err := plakarss3.Connect(parsed, useSSL, insecure, s.accessKey, s.secretAccessKey)
	if err != nil {
		return err
	}

	s.awsS3Client = conn
	return nil
}

func (s *Store) Create(ctx context.Context, config []byte) error {
	parsed, err := url.Parse(s.location)
	if err != nil {
		return fmt.Errorf("parse location: %w", err)
	}

	err = s.connect()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	s.bucket, s.prefixDir, _ = strings.Cut(parsed.RequestURI()[1:], "/")
	if s.prefixDir != "" && !strings.HasSuffix(s.prefixDir, "/") {
		s.prefixDir += "/"
	}

	exists, err := plakarss3.BucketExists(ctx, s.awsS3Client, s.bucket)
	if err != nil {
		return fmt.Errorf("check if bucket exists: %w", err)
	}
	if !exists {
		_, err = s.awsS3Client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &s.bucket,
			CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
				LocationConstraint: "eu-west-1", // NOTE: Should this be configurable?
			},
		})
		if err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}

	exists, err = plakarss3.ObjectExists(ctx, s.awsS3Client, s.bucket, s.realpath("CONFIG"))
	if err != nil {
		return fmt.Errorf("check if object exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket already initialized")
	}

	if s.mode()&storage.ModeRead == 0 {
		_, err = s.awsS3Client.PutObject(ctx, plakarss3.NewPutObjectInput(s.bucket, s.realpath("CONFIG.frozen"), bytes.NewReader(config), int64(len(config)), s.storageClass))
		if err != nil {
			return fmt.Errorf("put object CONFIG.frozen: %w", err)
		}
	}

	_, err = s.awsS3Client.PutObject(ctx, plakarss3.NewPutObjectInput(s.bucket, s.realpath("CONFIG"), bytes.NewReader(config), int64(len(config)), s.storageClass))

	if err != nil {
		return fmt.Errorf("put object CONFIG: %w", err)
	}

	return nil
}

func (s *Store) Open(ctx context.Context) ([]byte, error) {
	parsed, err := url.Parse(s.location)
	if err != nil {
		return nil, fmt.Errorf("parse location: %w", err)
	}

	err = s.connect()
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	s.bucket, s.prefixDir, _ = strings.Cut(parsed.RequestURI()[1:], "/")
	if s.prefixDir != "" && !strings.HasSuffix(s.prefixDir, "/") {
		s.prefixDir += "/"
	}

	exists, err := plakarss3.BucketExists(ctx, s.awsS3Client, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("error checking if bucket exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket does not exist")
	}

	//object, err := s.minioClient.GetObject(ctx, s.bucket, s.realpath("CONFIG"), minio.GetObjectOptions{})
	object, err := s.awsS3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(s.realpath("CONFIG")),
	})
	if err != nil {
		return nil, fmt.Errorf("error getting object: %w", err)
	}
	// TODO: Seems a minio only thing.. but have to investigate
	// stat, err := object.Stat()
	// if err != nil {
	// 	return nil, fmt.Errorf("error getting object stat: %w", err)
	// }

	data := make([]byte, *object.ContentLength)
	_, err = object.Body.Read(data)
	if err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("error reading object: %w", err)
		}
	}
	object.Body.Close()

	return data, nil
}

func (p *Store) Ping(ctx context.Context) error {
	ok, err := plakarss3.BucketExists(ctx, p.awsS3Client, p.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket does not exist")
	}
	return nil
}

func (s *Store) Origin() string        { return s.host }
func (s *Store) Root() string          { return s.root }
func (s *Store) Type() string          { return "s3" }
func (s *Store) Flags() location.Flags { return 0 }

func (s *Store) mode() storage.Mode {
	if s.storageClass == s3types.StorageClassGlacier || s.storageClass == s3types.StorageClassDeepArchive {
		return storage.ModeWrite
	}
	return storage.ModeRead | storage.ModeWrite
}

func (s *Store) Mode(ctx context.Context) (storage.Mode, error) {
	return s.mode(), nil
}

func (s *Store) Size(ctx context.Context) (int64, error) {
	return -1, nil
}

func (s *Store) List(ctx context.Context, res storage.StorageResource) ([]objects.MAC, error) {
	var prefix string
	var prefixSize int

	switch res {
	case storage.StorageResourcePackfile:
		prefix = s.realpath("packfiles/")
		prefixSize = len(prefix) + 3 // prefix + len(%02x/) encoded
	case storage.StorageResourceState:
		prefix = s.realpath("states/")
		prefixSize = len(prefix) + 3 // prefix + len(%02x/) encoded
	case storage.StorageResourceLock:
		prefix = s.realpath("locks/")
		prefixSize = len(prefix)
	default:
		return nil, errors.ErrUnsupported
	}

	ret := make([]objects.MAC, 0)
	listresults, err := s.awsS3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &s.bucket,
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("error listing objects: %w", err)
	}
	for _, object := range listresults.Contents {
		if strings.HasPrefix(*object.Key, prefix) && len(*object.Key) >= prefixSize {
			keyPrefix := (*object.Key)[:prefixSize]
			t, err := hex.DecodeString(keyPrefix)
			if err != nil {
				return nil, fmt.Errorf("decode %s key: %w", res, err)
			}
			if len(t) != 32 {
				continue
			}
			ret = append(ret, objects.MAC(t))
		}
	}
	return ret, nil
}

func (s *Store) Put(ctx context.Context, res storage.StorageResource, mac objects.MAC, rd io.Reader) (int64, error) {
	switch res {
	case storage.StorageResourcePackfile:
		buf := s.bufPool.Get().(*bytes.Buffer)
		copied, ioErr := io.Copy(buf, rd)
		if ioErr != nil {
			return 0, fmt.Errorf("read %s object: %w", res, ioErr)
		}

		putObjectInput := plakarss3.NewPutObjectInput(s.bucket, s.realpath(fmt.Sprintf("packfiles/%02x/%016x", mac[0], mac)), buf, copied, s.storageClass)
		_, err := s.awsS3Client.PutObject(ctx, putObjectInput)
		if err != nil {
			return 0, fmt.Errorf("put %s object: %w", res, err)
		}

		buf.Reset()
		s.bufPool.Put(buf)
		return copied, nil
	case storage.StorageResourceState:
		putObjectInput := plakarss3.NewPutObjectInput(s.bucket, s.realpath(fmt.Sprintf("states/%02x/%016x", mac[0], mac)), rd, -1, s.storageClass)
		_, err := s.awsS3Client.PutObject(ctx, putObjectInput)
		if err != nil {
			return 0, fmt.Errorf("put %s object: %w", res, err)
		}

		//return info.Size, nil
		return 0, nil
	case storage.StorageResourceLock:
		putObjectInput := plakarss3.NewPutObjectInput(s.bucket, s.realpath(fmt.Sprintf("locks/%016x", mac)), rd, -1, s.storageClass)
		_, err := s.awsS3Client.PutObject(ctx, putObjectInput)
		if err != nil {
			return 0, fmt.Errorf("put %s object: %w", res, err)
		}
		//return info.Size, nil
		return 0, nil
	}

	return -1, errors.ErrUnsupported
}

func (s *Store) Get(ctx context.Context, res storage.StorageResource, mac objects.MAC, rg *storage.Range) (io.ReadCloser, error) {
	var path string
	switch res {
	case storage.StorageResourcePackfile:
		path = s.realpath(fmt.Sprintf("packfiles/%02x/%016x", mac[0], mac))
	case storage.StorageResourceState:
		path = s.realpath(fmt.Sprintf("states/%02x/%016x", mac[0], mac))
	case storage.StorageResourceLock:
		path = s.realpath(fmt.Sprintf("locks/%016x", mac))
	default:
		return nil, errors.ErrUnsupported
	}

	//object, err := s.minioClient.GetObject(ctx, s.bucket, path, minio.GetObjectOptions{})
	seekableFileReader, err := plakarss3.NewS3SeekableFileReader(ctx, s.awsS3Client, s.bucket, path)
	if err != nil {
		return nil, fmt.Errorf("get %s object: %w", res, err)
	}

	if rg != nil {
		return reading.NewSectionReadCloser(seekableFileReader, int64(rg.Offset), int64(rg.Length)), nil
	}

	return seekableFileReader, nil
}

func (s *Store) Delete(ctx context.Context, res storage.StorageResource, mac objects.MAC) error {
	var path string
	switch res {
	case storage.StorageResourcePackfile:
		path = s.realpath(fmt.Sprintf("packfiles/%02x/%016x", mac[0], mac))
	case storage.StorageResourceState:
		path = s.realpath(fmt.Sprintf("states/%02x/%016x", mac[0], mac))
	case storage.StorageResourceLock:
		path = s.realpath(fmt.Sprintf("locks/%016x", mac))
	}

	_, err := s.awsS3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(path),
	})
	return err
}

func (s *Store) Close(ctx context.Context) error {
	return nil
}
