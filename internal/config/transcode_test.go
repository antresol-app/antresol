package config

import (
	"path/filepath"
	"testing"
)

func TestTranscodeDir(t *testing.T) {
	t.Setenv("VAULT_DATA_DIR", "/config")
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	t.Setenv("VAULT_TRANSCODE_DIR", "")
	t.Setenv("VAULT_TRANSCODE_MAX_BYTES", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TranscodeDir != filepath.Join("/config", "hls") || c.TranscodeMaxBytes != 20<<30 {
		t.Fatalf("defaults %q %d", c.TranscodeDir, c.TranscodeMaxBytes)
	}
	// On the media disk: relative paths are under the media root like the others.
	t.Setenv("VAULT_TRANSCODE_DIR", "tmp/vault-hls")
	t.Setenv("VAULT_TRANSCODE_MAX_BYTES", "1073741824")
	if c, err = Load(); err != nil || c.TranscodeDir != "/srv/media/tmp/vault-hls" || c.TranscodeMaxBytes != 1<<30 {
		t.Fatalf("%q %d %v", c.TranscodeDir, c.TranscodeMaxBytes, err)
	}
	t.Setenv("VAULT_TRANSCODE_DIR", "/srv/hls")
	if c, _ = Load(); c.TranscodeDir != "/srv/hls" {
		t.Fatalf("abs %q", c.TranscodeDir)
	}
	t.Setenv("VAULT_TRANSCODE_MAX_BYTES", "lots")
	if _, err := Load(); err == nil {
		t.Fatal("bad VAULT_TRANSCODE_MAX_BYTES accepted")
	}
}

func TestDeviceCopyDir(t *testing.T) {
	t.Setenv("VAULT_DATA_DIR", "/config")
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	t.Setenv("VAULT_TRANSCODE_DIR", "tmp/vault-hls")
	t.Setenv("VAULT_DEVICE_COPY_DIR", "")
	t.Setenv("VAULT_DEVICE_COPY_MAX_BYTES", "")
	c, err := Load()
	if err != nil || c.DeviceCopyDir != "/srv/media/tmp/vault-hls/device" || c.DeviceCopyMaxBytes != 50<<30 {
		t.Fatalf("defaults %q %d %v", c.DeviceCopyDir, c.DeviceCopyMaxBytes, err)
	}
	t.Setenv("VAULT_DEVICE_COPY_DIR", "tmp/vault-device")
	t.Setenv("VAULT_DEVICE_COPY_MAX_BYTES", "1073741824")
	if c, err = Load(); err != nil || c.DeviceCopyDir != "/srv/media/tmp/vault-device" || c.DeviceCopyMaxBytes != 1<<30 {
		t.Fatalf("%q %d %v", c.DeviceCopyDir, c.DeviceCopyMaxBytes, err)
	}
	t.Setenv("VAULT_DEVICE_COPY_DIR", "/srv/device")
	if c, _ = Load(); c.DeviceCopyDir != "/srv/device" {
		t.Fatalf("abs %q", c.DeviceCopyDir)
	}
	t.Setenv("VAULT_DEVICE_COPY_MAX_BYTES", "lots")
	if _, err := Load(); err == nil {
		t.Fatal("bad VAULT_DEVICE_COPY_MAX_BYTES accepted")
	}
}
