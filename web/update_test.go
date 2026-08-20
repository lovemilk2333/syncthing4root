package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAPITag(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"tag_name":"v2.1.3","prerelease":false}`, "2.1.3"},
		{`{"tag_name":"v2.1.3"}`, "2.1.3"},
		{`{"message":"Not Found"}`, ""},
		{`<html>rate limited</html>`, ""},
		{``, ""},
	}
	for _, c := range cases {
		if got := parseAPITag([]byte(c.in)); got != c.want {
			t.Errorf("parseAPITag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseMirrorTag(t *testing.T) {
	in := `[{"tag_name":"v2.1.2","prerelease":false},
	       {"tag_name":"v2.1.3","prerelease":false},
	       {"tag_name":"v2.2.0-rc.1","prerelease":true}]`
	if got := parseMirrorTag([]byte(in)); got != "2.1.2" {
		t.Errorf("first stable should win, got %q", got)
	}
	if got := parseMirrorTag([]byte(`{"error":"boom"}`)); got != "" {
		t.Errorf("garbage should be empty, got %q", got)
	}
}

func TestParsePageTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<a href="/syncthing/syncthing/releases/tag/v2.1.3">2.1.3</a>`, "2.1.3"},
		{`releases/tag/v2.1.3`, "2.1.3"},
		{`<html>no tags here</html>`, ""},
	}
	for _, c := range cases {
		if got := parsePageTag([]byte(c.in)); got != c.want {
			t.Errorf("parsePageTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSyncArch(t *testing.T) {
	want := map[string]string{
		"arm64": "arm64", "amd64": "amd64",
		"arm": "arm", "386": "386", "riscv64": "riscv64",
	}
	for in, w := range want {
		if got := syncArchFor(in); got != w {
			t.Errorf("syncArchFor(%q) = %q, want %q", in, got, w)
		}
	}
}

func TestInstallBinaryPicksTopLevelSyncthing(t *testing.T) {
	// the release tarball contains decoy scripts named "syncthing" under
	// etc/freebsd-rc and etc/firewall-ufw before the real binary; install
	// must extract the top-level syncthing-linux-*/syncthing entry
	dir := t.TempDir()
	oldDir, oldBin := syncthingDir, syncthingBin
	syncthingDir = dir
	syncthingBin = filepath.Join(dir, "syncthing")
	defer func() { syncthingDir, syncthingBin = oldDir, oldBin }()

	tarball := filepath.Join(dir, "fake.tar.gz")
	f, err := os.Create(tarball)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	decoy := []byte("#!/bin/sh\necho decoy\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "syncthing-linux-amd64-v2.1.3/etc/freebsd-rc/syncthing",
		Mode: 0755, Size: int64(len(decoy)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(decoy); err != nil {
		t.Fatal(err)
	}

	real := bytes.Repeat([]byte{0xAA}, 1024)
	if err := tw.WriteHeader(&tar.Header{
		Name: "syncthing-linux-amd64-v2.1.3/syncthing",
		Mode: 0755, Size: int64(len(real)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(real); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	if err := installBinary(tarball); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(syncthingBin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != int64(len(real)) {
		t.Fatalf("extracted %d bytes, want %d (decoy entry picked?)", fi.Size(), len(real))
	}
	if fi.Mode()&0111 == 0 {
		t.Error("extracted binary not executable")
	}
}

func TestOrNone(t *testing.T) {
	if orNone("") != "none" {
		t.Error(`orNone("") should be "none"`)
	}
	if orNone("2.1.3") != "v2.1.3" {
		t.Error(`orNone("2.1.3") should be "v2.1.3"`)
	}
}
