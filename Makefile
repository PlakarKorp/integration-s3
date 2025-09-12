GO=go
EXT=

all: build

build:
	${GO} build -v -o s3Importer${EXT} ./plugin/importer
	${GO} build -v -o s3Exporter${EXT} ./plugin/exporter
	${GO} build -v -o s3Storage${EXT} ./plugin/storage

clean:
	rm -f s3Importer s3Exporter s3Storage s3-*.ptar
