//go:build openbsd

package s3

import "golang.org/x/sys/unix"

func Unveil(path, perm string) error {
	return unix.Unveil(path, perm)
}

func Pledge(promises string) error {
	return unix.PledgePromises(promises)
}
