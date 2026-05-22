package container // import "github.com/docker/docker/integration/container"

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/integration/internal/build"
	"github.com/docker/docker/integration/internal/container"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/testutil/daemon"
	"github.com/docker/docker/testutil/fakecontext"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/skip"
)

func TestCopyFromContainerPathDoesNotExist(t *testing.T) {
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()
	cid := container.Create(ctx, t, apiClient)

	_, _, err := apiClient.CopyFromContainer(ctx, cid, "/dne")
	assert.Check(t, is.ErrorType(err, errdefs.IsNotFound))
	assert.Check(t, is.ErrorContains(err, "Could not find the file /dne in container "+cid))
}

func TestCopyFromContainerPathIsNotDir(t *testing.T) {
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()
	cid := container.Create(ctx, t, apiClient)

	path := "/etc/passwd/"
	expected := "not a directory"
	if testEnv.DaemonInfo.OSType == "windows" {
		path = "c:/windows/system32/drivers/etc/hosts/"
		expected = "The filename, directory name, or volume label syntax is incorrect."
	}
	_, _, err := apiClient.CopyFromContainer(ctx, cid, path)
	assert.Assert(t, is.ErrorContains(err, expected))
}

func TestCopyToContainerPathDoesNotExist(t *testing.T) {
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()
	cid := container.Create(ctx, t, apiClient)

	err := apiClient.CopyToContainer(ctx, cid, "/dne", nil, types.CopyToContainerOptions{})
	assert.Check(t, is.ErrorType(err, errdefs.IsNotFound))
	assert.Check(t, is.ErrorContains(err, "Could not find the file /dne in container "+cid))
}

func TestCopyEmptyFile(t *testing.T) {
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()
	cid := container.Create(ctx, t, apiClient)

	// empty content
	dstDir, _ := makeEmptyArchive(t)
	err := apiClient.CopyToContainer(ctx, cid, dstDir, bytes.NewReader([]byte("")), types.CopyToContainerOptions{})
	assert.NilError(t, err)

	// tar with empty file
	dstDir, preparedArchive := makeEmptyArchive(t)
	err = apiClient.CopyToContainer(ctx, cid, dstDir, preparedArchive, types.CopyToContainerOptions{})
	assert.NilError(t, err)

	// tar with empty file archive mode
	dstDir, preparedArchive = makeEmptyArchive(t)
	err = apiClient.CopyToContainer(ctx, cid, dstDir, preparedArchive, types.CopyToContainerOptions{
		CopyUIDGID: true,
	})
	assert.NilError(t, err)

	// copy from empty file
	rdr, _, err := apiClient.CopyFromContainer(ctx, cid, dstDir)
	assert.NilError(t, err)
	defer rdr.Close()
}

func makeEmptyArchive(t *testing.T) (string, io.ReadCloser) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "empty-file.txt")
	err := os.WriteFile(srcPath, []byte(""), 0o400)
	assert.NilError(t, err)

	// TODO(thaJeztah) Add utilities to the client to make steps below less complicated.
	// Code below is taken from copyToContainer() in docker/cli.
	srcInfo, err := archive.CopyInfoSourcePath(srcPath, false)
	assert.NilError(t, err)

	srcArchive, err := archive.TarResource(srcInfo)
	assert.NilError(t, err)
	t.Cleanup(func() {
		srcArchive.Close()
	})

	ctrPath := "/empty-file.txt"
	dstInfo := archive.CopyInfo{Path: ctrPath}
	dstDir, preparedArchive, err := archive.PrepareArchiveCopy(srcArchive, srcInfo, dstInfo)
	assert.NilError(t, err)
	t.Cleanup(func() {
		preparedArchive.Close()
	})
	return dstDir, preparedArchive
}

func TestCopyToContainerPathIsNotDir(t *testing.T) {
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()
	cid := container.Create(ctx, t, apiClient)

	path := "/etc/passwd/"
	if testEnv.DaemonInfo.OSType == "windows" {
		path = "c:/windows/system32/drivers/etc/hosts/"
	}
	err := apiClient.CopyToContainer(ctx, cid, path, nil, types.CopyToContainerOptions{})
	assert.Check(t, is.ErrorContains(err, "not a directory"))
}

func TestCopyFromContainer(t *testing.T) {
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")
	ctx := setupTest(t)

	apiClient := testEnv.APIClient()

	dir, err := os.MkdirTemp("", t.Name())
	assert.NilError(t, err)
	defer os.RemoveAll(dir)

	buildCtx := fakecontext.New(t, dir, fakecontext.WithFile("foo", "hello"), fakecontext.WithFile("baz", "world"), fakecontext.WithDockerfile(`
		FROM busybox
		COPY foo /foo
		COPY baz /bar/quux/baz
		RUN ln -s notexist /bar/notarget && ln -s quux/baz /bar/filesymlink && ln -s quux /bar/dirsymlink && ln -s / /bar/root
		CMD /fake
	`))
	defer buildCtx.Close()

	resp, err := apiClient.ImageBuild(ctx, buildCtx.AsTarReader(t), types.ImageBuildOptions{})
	assert.NilError(t, err)
	defer resp.Body.Close()

	var imageID string
	err = jsonmessage.DisplayJSONMessagesStream(resp.Body, io.Discard, 0, false, func(msg jsonmessage.JSONMessage) {
		var r types.BuildResult
		assert.NilError(t, json.Unmarshal(*msg.Aux, &r))
		imageID = r.ID
	})
	assert.NilError(t, err)
	assert.Assert(t, imageID != "")

	cid := container.Create(ctx, t, apiClient, container.WithImage(imageID))

	for _, x := range []struct {
		src    string
		expect map[string]string
	}{
		{"/", map[string]string{"/": "", "/foo": "hello", "/bar/quux/baz": "world", "/bar/filesymlink": "", "/bar/dirsymlink": "", "/bar/notarget": ""}},
		{".", map[string]string{"./": "", "./foo": "hello", "./bar/quux/baz": "world", "./bar/filesymlink": "", "./bar/dirsymlink": "", "./bar/notarget": ""}},
		{"/.", map[string]string{"./": "", "./foo": "hello", "./bar/quux/baz": "world", "./bar/filesymlink": "", "./bar/dirsymlink": "", "./bar/notarget": ""}},
		{"./", map[string]string{"./": "", "./foo": "hello", "./bar/quux/baz": "world", "./bar/filesymlink": "", "./bar/dirsymlink": "", "./bar/notarget": ""}},
		{"/./", map[string]string{"./": "", "./foo": "hello", "./bar/quux/baz": "world", "./bar/filesymlink": "", "./bar/dirsymlink": "", "./bar/notarget": ""}},
		{"/bar/root", map[string]string{"root": ""}},
		{"/bar/root/", map[string]string{"root/": "", "root/foo": "hello", "root/bar/quux/baz": "world", "root/bar/filesymlink": "", "root/bar/dirsymlink": "", "root/bar/notarget": ""}},
		{"/bar/root/.", map[string]string{"./": "", "./foo": "hello", "./bar/quux/baz": "world", "./bar/filesymlink": "", "./bar/dirsymlink": "", "./bar/notarget": ""}},

		{"bar/quux", map[string]string{"quux/": "", "quux/baz": "world"}},
		{"bar/quux/", map[string]string{"quux/": "", "quux/baz": "world"}},
		{"bar/quux/.", map[string]string{"./": "", "./baz": "world"}},
		{"bar/quux/baz", map[string]string{"baz": "world"}},

		{"bar/filesymlink", map[string]string{"filesymlink": ""}},
		{"bar/dirsymlink", map[string]string{"dirsymlink": ""}},
		{"bar/dirsymlink/", map[string]string{"dirsymlink/": "", "dirsymlink/baz": "world"}},
		{"bar/dirsymlink/.", map[string]string{"./": "", "./baz": "world"}},
		{"bar/notarget", map[string]string{"notarget": ""}},
	} {
		t.Run(x.src, func(t *testing.T) {
			rdr, _, err := apiClient.CopyFromContainer(ctx, cid, x.src)
			assert.NilError(t, err)
			defer rdr.Close()

			found := make(map[string]bool, len(x.expect))
			var numFound int
			tr := tar.NewReader(rdr)
			for numFound < len(x.expect) {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				assert.NilError(t, err)

				expected, exists := x.expect[h.Name]
				if !exists {
					// this archive will have extra stuff in it since we are copying from root
					// and docker adds a bunch of stuff
					continue
				}

				numFound++
				found[h.Name] = true

				buf, err := io.ReadAll(tr)
				if err == nil {
					assert.Check(t, is.Equal(string(buf), expected))
				}
			}

			for f := range x.expect {
				assert.Check(t, found[f], f+" not found in archive")
			}
		})
	}
}

// TestCopyToContainerXZBinaryNotExecutedOnDaemon tests that when
// uploading an xz-compressed archive to a container via the
// PUT /containers/{id}/archive API, the daemon does NOT execute the xz
// binary found inside the container's filesystem.
// This is a regression test for
// https://github.com/moby/moby/security/advisories/GHSA-x86f-5xw2-fm2r
func TestCopyToContainerXZBinaryNotExecutedOnDaemon(t *testing.T) {
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")
	skip.If(t, testEnv.IsRemoteDaemon, "cannot start daemon on remote test run")
	ctx := setupTest(t)

	const tokenEnv = "MOBY_EXPLOIT_TEST_TOKEN"
	const tokenVal = "host-boundary-crossed"

	d := daemon.New(t, daemon.WithEnvVars(tokenEnv+"="+tokenVal))
	defer d.Cleanup(t)
	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false")
	defer d.Stop(t)

	apiClient := d.NewClientT(t)

	dir := t.TempDir()
	buildCtx := fakecontext.New(t, dir,
		// The fake xz writes the daemon's secret env var to a marker file.
		// A process inside the container would not have this env var.
		fakecontext.WithFile("fake-xz", "#!/bin/sh\necho $"+tokenEnv+" > /xz-was-executed\n"),
		fakecontext.WithDockerfile(`FROM busybox
COPY fake-xz /usr/bin/xz
RUN chmod +x /usr/bin/xz`),
	)
	defer buildCtx.Close()

	imageID := build.Do(ctx, t, apiClient, buildCtx)

	cID := container.Run(ctx, t, apiClient, container.WithImage(imageID))
	defer container.Remove(ctx, t, apiClient, cID, client.ContainerRemoveOptions{Force: true})

	// Craft a payload that starts with xz magic bytes so the daemon's
	// DecompressStream identifies it as xz-compressed. The payload is
	// deliberately invalid xz data; we only care whether the daemon
	// attempts to run the container's /usr/bin/xz to decompress it.
	xzMagic := []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}
	payload := append(xzMagic, []byte("not-real-xz-data")...)

	// CopyToContainer is expected to fail because the payload is not a
	// valid xz archive. We don't care about the error; we care about
	// whether the container's xz binary was invoked.
	_, _ = apiClient.CopyToContainer(ctx, cID, "/tmp", bytes.NewReader(payload), types.CopyToContainerOptions{})

	t.Run("binary not executed", func(t *testing.T) {
		// If the container's /usr/bin/xz was executed, the marker file
		// will exist, and this assertion will fail.
		res, err := container.Exec(ctx, apiClient, cID, []string{"test", "-f", "/xz-was-executed"})
		assert.NilError(t, err)
		assert.Check(t, is.Equal(res.ExitCode, 1),
			"container's xz binary was executed by the daemon during archive extraction; marker file /xz-was-executed was created")
	})

	t.Run("runs container", func(t *testing.T) {
		// If the binary ran, check that it ran in the daemon's process
		// context by looking for the daemon's secret env var in the
		// marker file. A container process would not have this env var.
		res, err := container.Exec(ctx, apiClient, cID, []string{"cat", "/xz-was-executed"})
		assert.NilError(t, err)
		assert.Check(t, !strings.Contains(res.Stdout(), tokenVal),
			"container's xz binary was executed in the daemon's process context: marker file contains the daemon's secret env var")
	})
}
