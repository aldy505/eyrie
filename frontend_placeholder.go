package main

import (
	"io"
	"io/fs"
	"strings"
	"time"
)

type placeholderFrontendFS struct{}

func (placeholderFrontendFS) Open(name string) (fs.File, error) {
	if name == "." || name == "index.html" {
		return &placeholderFrontendFile{
			reader: strings.NewReader(`<!doctype html><html><head><meta charset="utf-8"><title>Eyrie</title></head><body><div id="root">Frontend assets have not been built yet. Run the frontend build to embed the UI.</div></body></html>`),
		}, nil
	}
	return nil, fs.ErrNotExist
}

type placeholderFrontendFile struct {
	reader *strings.Reader
}

func (f *placeholderFrontendFile) Stat() (fs.FileInfo, error) { return placeholderFrontendInfo{}, nil }
func (f *placeholderFrontendFile) Read(p []byte) (int, error) { return f.reader.Read(p) }
func (f *placeholderFrontendFile) Close() error               { return nil }

type placeholderFrontendInfo struct{}

func (placeholderFrontendInfo) Name() string { return "index.html" }
func (placeholderFrontendInfo) Size() int64 {
	return int64(len(`<!doctype html><html><head><meta charset="utf-8"><title>Eyrie</title></head><body><div id="root">Frontend assets have not been built yet. Run the frontend build to embed the UI.</div></body></html>`))
}
func (placeholderFrontendInfo) Mode() fs.FileMode  { return 0o644 }
func (placeholderFrontendInfo) ModTime() time.Time { return time.Time{} }
func (placeholderFrontendInfo) IsDir() bool        { return false }
func (placeholderFrontendInfo) Sys() any           { return nil }

var _ fs.File = (*placeholderFrontendFile)(nil)
var _ io.Reader = (*strings.Reader)(nil)
