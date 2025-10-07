package main

import (
	"log"
	"os"
	"runtime"

	sdk "github.com/PlakarKorp/go-kloset-sdk"
	s3 "github.com/PlakarKorp/integration-s3"
	"github.com/PlakarKorp/integration-s3/storage"
)

func main() {
	// golang stdlib tries to open cert files at "well known"
	// locations.  On OpenBSD, we only really have
	// /etc/ssl/cert.pem, so that's a safe guess, but attempt to
	// respect SSL_CERT_FILE if set.
	if runtime.GOOS == "openbsd" {
		cert, ok := os.LookupEnv("SSL_CERT_FILE")
		if !ok {
			cert = "/etc/ssl/cert.pem"
			os.Setenv("SSL_CERT_FILE", cert)
		}

		if err := s3.Unveil(cert, "r"); err != nil {
			log.Fatalln("unveil /etc/ssl/cert.pem:", err)
		}
		if err := s3.Pledge("stdio rpath inet dns"); err != nil {
			log.Fatalln("pledge:", err)
		}
	}

	sdk.EntrypointStorage(os.Args, storage.NewStore)
}
