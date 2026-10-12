package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// download fetches url to dest. It writes dest+".part" and renames it when
// complete, resumes a leftover .part when the server allows it, and returns
// the SHA-256 of the whole file. progress may be nil.
func download(ctx context.Context, url, dest string, progress func(format string, a ...any)) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	part := dest + ".part"
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	flags := os.O_WRONLY | os.O_CREATE
	switch resp.StatusCode {
	case http.StatusOK:
		have = 0 // no resume: start over
		flags |= os.O_TRUNC
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	default:
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	total := have + resp.ContentLength
	if resp.ContentLength < 0 {
		total = 0
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	n := have
	start := time.Now()
	buf := make([]byte, 256<<10)
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, werr := f.Write(buf[:k]); werr != nil {
				f.Close()
				return "", werr
			}
			n += int64(k)
			if progress != nil {
				rate := float64(n-have) / time.Since(start).Seconds() / 1e6
				if total > 0 {
					progress("%s: %.0f of %.0f MB (%.1f MB/s)", filepath.Base(dest), float64(n)/1e6, float64(total)/1e6, rate)
				} else {
					progress("%s: %.0f MB (%.1f MB/s)", filepath.Base(dest), float64(n)/1e6, rate)
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", rerr // the .part stays for a resume
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if total > 0 && n != total {
		return "", fmt.Errorf("download %s: got %d of %d bytes", url, n, total)
	}
	sum, err := fileSHA256(part)
	if err != nil {
		return "", err
	}
	return sum, os.Rename(part, dest)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// remoteSize asks the server how big url is (0 = unknown).
func remoteSize(ctx context.Context, url string) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	n, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return n
}
