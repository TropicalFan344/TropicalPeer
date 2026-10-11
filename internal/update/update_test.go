package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// fakeRelease serves a GitHub-like release whose files are given, signed
// with signer (nil: no signature files).
func fakeRelease(t *testing.T, files map[string][]byte, signer ed25519.PrivateKey, tamper string) *httptest.Server {
	t.Helper()
	sums := ""
	for name, data := range files {
		sum := sha256.Sum256(data)
		sums += fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	serve := map[string][]byte{}
	for name, data := range files {
		serve[name] = data
	}
	if tamper != "" {
		serve[tamper] = append([]byte("evil"), serve[tamper]...)
	}
	if signer != nil {
		serve[ChecksumsFile] = []byte(sums)
		serve[SignatureFile] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(signer, []byte(sums))))
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+Repo+"/releases/latest" {
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var assets []asset
			for name := range serve {
				assets = append(assets, asset{name, srv.URL + "/dl/" + name})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": assets})
			return
		}
		if data, ok := serve[r.URL.Path[len("/dl/"):]]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = "https://api.github.com" })
	return srv
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { PublicKey = old })
	return priv
}

func TestSignedReleaseDownloads(t *testing.T) {
	key := testKey(t)
	exe := []byte("new TropicalPeer")
	fakeRelease(t, map[string][]byte{"TropicalPeer-0.2.0.exe": exe}, key, "")

	r, err := Latest(context.Background(), http.DefaultClient)
	if err != nil || r.Version != "0.2.0" {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
	path, err := Download(context.Background(), http.DefaultClient, r, "TropicalPeer-0.2.0.exe", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(exe) {
		t.Fatalf("downloaded %q", got)
	}
}

func TestTamperedFileIsRefused(t *testing.T) {
	key := testKey(t)
	fakeRelease(t, map[string][]byte{"TropicalPeer-0.2.0.exe": []byte("genuine")}, key, "TropicalPeer-0.2.0.exe")
	r, err := Latest(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Download(context.Background(), http.DefaultClient, r, "TropicalPeer-0.2.0.exe", dir, nil); err == nil {
		t.Fatal("a file that doesn't match the signed checksum was accepted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}

func TestUnsignedOrWronglySignedIsRefused(t *testing.T) {
	testKey(t)
	fakeRelease(t, map[string][]byte{"TropicalPeer-0.2.0.exe": []byte("x")}, nil, "")
	if _, err := Latest(context.Background(), http.DefaultClient); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned release: %v", err)
	}
	_, someoneElse, _ := ed25519.GenerateKey(rand.Reader)
	fakeRelease(t, map[string][]byte{"TropicalPeer-0.2.0.exe": []byte("x")}, someoneElse, "")
	if _, err := Latest(context.Background(), http.DefaultClient); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("release signed with another key: %v", err)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.8", "0.1.7", true}, {"0.1.10", "0.1.9", true}, {"v1.0.0", "0.9.9", true},
		{"0.1.7", "0.1.7", false}, {"0.1.6", "0.1.7", false}, {"0.1.8", "dev", false}, {"garbage", "0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// An older release without a signature is reported (Latest returns it with
// ErrUnsigned), so the caller can tell "nothing new" from "refused".
func TestUnsignedReleaseStillTellsItsVersion(t *testing.T) {
	testKey(t)
	fakeRelease(t, map[string][]byte{"TropicalPeer-0.2.0.exe": []byte("x")}, nil, "")
	r, err := Latest(context.Background(), http.DefaultClient)
	if !errors.Is(err, ErrUnsigned) || r == nil || r.Version != "0.2.0" {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
}
