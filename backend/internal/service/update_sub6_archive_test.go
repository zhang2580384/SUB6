//go:build unit

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func sub6RuntimeArchive(t *testing.T, binaryName string, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	// A release contains resources and notices as well as the binary.
	for _, file := range []struct {
		name string
		data []byte
	}{{"resources/README.txt", []byte("fixture resource")}, {binaryName, binary}} {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: 0755, Size: int64(len(file.data)), Typeflag: tar.TypeReg}))
		_, err := tarWriter.Write(file.data)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gz.Close())
	return buffer.Bytes()
}

func sub6PlatformBinaryName() string {
	name := "sub6-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func TestSUB6UpdateExtractsDistributionAndLegacyBinaryNames(t *testing.T) {
	for _, name := range []string{sub6PlatformBinaryName(), "sub6", "sub6.exe", "sub2api", "sub2api.exe"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "sub6_runtime.tar.gz")
			payload := []byte("fixture program for " + name)
			require.NoError(t, os.WriteFile(archive, sub6RuntimeArchive(t, name, payload), 0600))
			destination := filepath.Join(dir, "extracted-program")
			require.NoError(t, (&UpdateService{}).extractBinary(archive, destination))
			actual, err := os.ReadFile(destination)
			require.NoError(t, err)
			require.Equal(t, payload, actual)
			require.NoFileExists(t, filepath.Join(dir, "resources", "README.txt"))
		})
	}
}

func TestSUB6UpdateRejectsUnrelatedAndTraversalArchiveNames(t *testing.T) {
	for _, name := range []string{"sub6-otheros-otherarch", "unrelated-program", "../sub6"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "sub6_runtime.tar.gz")
			require.NoError(t, os.WriteFile(archive, sub6RuntimeArchive(t, name, []byte("fixture")), 0600))
			destination := filepath.Join(dir, "extracted-program")
			require.Error(t, (&UpdateService{}).extractBinary(archive, destination))
			require.NoFileExists(t, destination)
		})
	}
}

// Optional read-only acceptance against an operator-supplied published archive.
// Only extracts into t.TempDir; never installs, starts or replaces the program.
func TestSUB6UpdateExtractsPublishedRuntimeArchive(t *testing.T) {
	archive := os.Getenv("SUB6_TEST_RELEASE_ARCHIVE")
	if archive == "" {
		t.Skip("set SUB6_TEST_RELEASE_ARCHIVE and SUB6_TEST_BINARY_SHA256 for a published asset")
	}
	expected := os.Getenv("SUB6_TEST_BINARY_SHA256")
	require.Len(t, expected, sha256.Size*2)
	destination := filepath.Join(t.TempDir(), "extracted-program")
	require.NoError(t, (&UpdateService{}).extractBinary(archive, destination))
	binary, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, expected, fmt.Sprintf("%x", sha256.Sum256(binary)))
}
