// Package update finds, downloads and verifies new versions of TropicalPeer
// from the project's GitHub releases.
//
// Every release carries checksums.txt (the SHA-256 of each file) and
// checksums.txt.sig, an Ed25519 signature over it made with the release
// signing key (cmd/signrelease). The app holds only the public key, below,
// and accepts a download only if its checksum is listed in a correctly
// signed checksums.txt. So a tampered release page, or anything between it
// and the app, can't get code installed: that would need the private key.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Repo is where releases are published.
const Repo = "TropicalFan344/TropicalPeer"

// PublicKey verifies release signatures (base64 Ed25519).
var PublicKey = "qH3M18YlWi9UwVNwOHEggy/QhVcHnwk6Ytn7qAdYkoE="

const (
	ChecksumsFile = "checksums.txt"
	SignatureFile = "checksums.txt.sig"
)

// APIBase is GitHub's API; tests point it elsewhere.
var APIBase = "https://api.github.com"

// ErrUnsigned means the latest release has no valid signature: it is not
// installed, whatever it contains.
var ErrUnsigned = errors.New("the new version isn't signed, so it won't be installed")

// Release is a published version and the verified checksums of its files.
type Release struct {
	Version   string // "0.1.8"
	Notes     string
	files     map[string]string // name -> download URL
	checksums map[string][]byte // name -> SHA-256, from the signed list
}

// Has reports whether the release offers file name with a signed checksum.
func (r *Release) Has(name string) bool {
	_, ok := r.files[name]
	return ok && r.checksums[name] != nil
}

// Latest fetches the newest release and checks its signature.
func Latest(ctx context.Context, client *http.Client) (*Release, error) {
	var gh struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, client, APIBase+"/repos/"+Repo+"/releases/latest", &gh); err != nil {
		return nil, err
	}
	r := &Release{Version: strings.TrimPrefix(gh.Tag, "v"), Notes: gh.Body, files: map[string]string{}}
	for _, a := range gh.Assets {
		r.files[a.Name] = a.URL
	}
	if r.files[ChecksumsFile] == "" || r.files[SignatureFile] == "" {
		return r, ErrUnsigned
	}
	sums, err := getSmall(ctx, client, r.files[ChecksumsFile])
	if err != nil {
		return nil, err
	}
	sig, err := getSmall(ctx, client, r.files[SignatureFile])
	if err != nil {
		return nil, err
	}
	if !Verify(sums, sig) {
		return r, ErrUnsigned
	}
	r.checksums = parseChecksums(sums)
	return r, nil
}

// Verify checks sig (base64) over checksums with the release public key.
func Verify(checksums, sig []byte) bool {
	pub, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, checksums, raw)
}

func parseChecksums(data []byte) map[string][]byte {
	out := map[string][]byte{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if b, err := hex.DecodeString(sum); ok && err == nil && len(b) == sha256.Size {
			out[name] = b
		}
	}
	return out
}

// Download fetches file name of the release into dir, reporting progress,
// and returns its path once its SHA-256 matches the signed checksum.
func Download(ctx context.Context, client *http.Client, r *Release, name, dir string, progress func(done, total int64)) (string, error) {
	if !r.Has(name) {
		return "", fmt.Errorf("the release has no signed %s", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, name)
	if ok, _ := matches(final, r.checksums[name]); ok {
		return final, nil // downloaded before
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.files[name], nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	part := final + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return "", err
	}
	if !bytes.Equal(h.Sum(nil), r.checksums[name]) {
		os.Remove(part)
		return "", errors.New("the download doesn't match the signed checksum")
	}
	os.Remove(final)
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return final, nil
}

func matches(path string, sum []byte) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return bytes.Equal(h.Sum(nil), sum), nil
}

// Newer reports whether version a is newer than b ("0.1.10" > "0.1.9").
// A development build ("dev") never updates itself.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	data, err := getSmall(ctx, client, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// getSmall fetches a small document (API answers, the checksum list).
func getSmall(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TropicalPeer-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update check failed: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
