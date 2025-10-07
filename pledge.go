//go:build !openbsd

package s3

func Unveil(string, string) error { return nil }
func Pledge(string) error         { return nil }
