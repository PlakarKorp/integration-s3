package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type mockHTTPClient struct {
	// Required for aws.HTTPClient interface
	DoFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.DoFunc(req)
}

func newTestS3Client(t *testing.T, responseFunc func(req *http.Request) (*http.Response, error)) *s3.Client {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "TOKEN")),
		config.WithHTTPClient(&mockHTTPClient{DoFunc: responseFunc}),
	)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return s3.NewFromConfig(cfg)
}

// TestBucketExists_Success tests the case where the bucket exists.
func TestBucketExists_Success(t *testing.T) {
	client := newTestS3Client(t, func(req *http.Request) (*http.Response, error) {
		// For a successful HeadBucket, AWS returns a 200 OK with an empty body.
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	exists, err := BucketExists(context.TODO(), client, "my-existing-bucket")

	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}
	if !exists {
		t.Errorf("expected bucket to exist, but it did not")
	}
}

func Test_BucketExists_Bucket_Not_Found(t *testing.T) {
	client := newTestS3Client(t, func(req *http.Request) (*http.Response, error) {
		// To trigger the SDK's *types.NotFound error, we must return a 404
		// with a specific XML error body that the SDK knows how to parse, according to S3 error response format.
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`<Error><Code>NotFound</Code></Error>`)),
		}, nil
	})

	exists, err := BucketExists(context.TODO(), client, "my-new-bucket")

	if err != nil {
		t.Fatalf("expected no error for a 'NotFound' case, but got: %v", err)
	}
	if exists {
		t.Errorf("expected bucket not to exist, but it did")
	}
}

func TestBucketExists_OtherError(t *testing.T) {
	client := newTestS3Client(t, func(req *http.Request) (*http.Response, error) {
		// Simulate a different error, like a 403 Forbidden.
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`<Error><Code>AccessDenied</Code></Error>`)),
		}, nil
	})

	exists, err := BucketExists(context.TODO(), client, "some-bucket")

	if err == nil {
		t.Fatalf("expected an error, but got nil")
	}
	if exists {
		t.Errorf("expected bucket not to exist on error, but it did")
	}
}

func Test_ObjectExists_Success(t *testing.T) {
	client := newTestS3Client(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	exists, err := ObjectExists(context.TODO(), client, "my-existing-bucket", "my-existing-object")

	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}
	if !exists {
		t.Errorf("expected object to exist, but it did not")
	}
}

func Test_ObjectExists_Object_Not_Found(t *testing.T) {
	client := newTestS3Client(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`<Error><Code>NotFound</Code></Error>`)),
		}, nil
	})

	exists, err := ObjectExists(context.TODO(), client, "my-existing-bucket", "my-existing-object")

	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}
	if exists {
		t.Errorf("expected object not to exist, but it did")
	}
}
