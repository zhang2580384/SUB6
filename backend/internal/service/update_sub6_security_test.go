//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSUB6UpdateRejectsForeignReleaseAndMissingChecksum(t *testing.T) {
	valid := "https://github.com/zhang2580384/SUB6/releases/download/v0.1.0/sub2api_linux_amd64.tar.gz"
	require.NoError(t, validateDownloadURL(valid))
	for _, foreign := range []string{
		"https://github.com/ranxi2001/sub2api/releases/download/v2.21.1/sub2api_linux_amd64.tar.gz",
		"https://github.com/attacker/SUB6/releases/download/v0.1.0/sub2api_linux_amd64.tar.gz",
		"https://github.com.evil.example/zhang2580384/SUB6/releases/download/v0.1.0/server",
		"https://user@github.com/zhang2580384/SUB6/releases/download/v0.1.0/server",
		"https://github.com/zhang2580384/SUB6/releases/download/%2e%2e/server",
		valid + "?redirect=foreign",
	} {
		require.Error(t, validateDownloadURL(foreign), foreign)
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, &updateServiceGitHubClientStub{}, "0.1.0", "release")
	err := svc.applyReleaseAssets(context.Background(), []Asset{{Name: svc.getArchiveName(), DownloadURL: valid}})
	require.ErrorContains(t, err, "mandatory checksums.txt")
}
