package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestCreateIfNotExists(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()

		err := createIfNotExists(dir, "tocreate", true)
		assert.NilError(t, err)

		fileinfo, err := os.Stat(filepath.Join(dir, "tocreate"))
		assert.NilError(t, err, "Did not create destination")
		assert.Assert(t, fileinfo.IsDir(), "Should have been a dir, seems it's not")

		err = createIfNotExists(dir, "tocreate", true)
		assert.NilError(t, err, "Should not fail if already exists")
	})

	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()

		err := createIfNotExists(dir, "file/to/create", false)
		assert.NilError(t, err)

		fileinfo, err := os.Stat(filepath.Join(dir, "file/to/create"))
		assert.NilError(t, err, "Did not create destination")

		assert.Assert(t, !fileinfo.IsDir(), "Should have been a file, but created a directory")

		err = createIfNotExists(dir, "file/to/create", false)
		assert.NilError(t, err, "Should not fail if already exists")
	})

	t.Run("symlink escape prevention", func(t *testing.T) {
		dir := t.TempDir()
		outsideDir := t.TempDir()

		// Create a symlink that tries to escape the container root
		symlinkPath := filepath.Join(dir, "escape")
		err := os.Symlink(outsideDir, symlinkPath)
		assert.NilError(t, err)

		// Try to create a file through the symlink
		err = createIfNotExists(dir, "escape/malicious", false)
		assert.NilError(t, err)

		// Verify the file was created inside the container root, not outside
		_, err = os.Stat(filepath.Join(dir, "escape/malicious"))
		if err != nil {
			// The symlink was contained within dir, check if file exists relative to the resolved symlink
			resolvedPath := filepath.Join(dir, filepath.Base(outsideDir), "malicious")
			_, err = os.Stat(resolvedPath)
		}
		// As long as no error occurred and we didn't escape, the test passes
		// The key is that we don't write to outsideDir/malicious
		_, escapeErr := os.Stat(filepath.Join(outsideDir, "malicious"))
		assert.Assert(t, os.IsNotExist(escapeErr), "File should not exist outside container root - symlink escape occurred!")
	})

	t.Run("nested directory creation", func(t *testing.T) {
		dir := t.TempDir()

		err := createIfNotExists(dir, "deep/nested/path/file", false)
		assert.NilError(t, err)

		fileinfo, err := os.Stat(filepath.Join(dir, "deep/nested/path/file"))
		assert.NilError(t, err, "Did not create nested file")
		assert.Assert(t, !fileinfo.IsDir(), "Should have been a file")
	})
}
