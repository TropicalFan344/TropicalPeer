// Command signrelease signs a release so apps can trust its updates.
//
//	signrelease -keygen key.txt         make a signing key (prints the public key)
//	signrelease -key key.txt dist/      write dist/checksums.txt and its signature
//
// The key file holds the base64 Ed25519 seed; in CI it comes from the
// UPDATE_SIGNING_KEY secret instead (-key env:UPDATE_SIGNING_KEY). Apps
// carry the public key (internal/update) and install nothing whose
// checksums.txt isn't signed by it.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"TropicalPeer/internal/update"
)

func main() {
	keygen := flag.String("keygen", "", "write a new signing key to this file")
	keySrc := flag.String("key", "", "signing key file, or env:NAME")
	flag.Parse()

	if *keygen != "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		if _, err := os.Stat(*keygen); err == nil {
			fail("%s already exists; refusing to overwrite a signing key", *keygen)
		}
		check(os.WriteFile(*keygen, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600))
		fmt.Println("public key:", base64.StdEncoding.EncodeToString(pub))
		return
	}
	if *keySrc == "" || flag.NArg() != 1 {
		fail("usage: signrelease -key key.txt|env:NAME dir")
	}
	priv := loadKey(*keySrc)
	dir := flag.Arg(0)

	entries, err := os.ReadDir(dir)
	check(err)
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && e.Name() != update.ChecksumsFile && e.Name() != update.SignatureFile {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, name))
		check(err)
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		check(err)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), name)
	}
	sums := []byte(b.String())
	check(os.WriteFile(filepath.Join(dir, update.ChecksumsFile), sums, 0o644))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	check(os.WriteFile(filepath.Join(dir, update.SignatureFile), []byte(sig+"\n"), 0o644))
	fmt.Printf("signed %d files\n", len(names))
}

func loadKey(src string) ed25519.PrivateKey {
	var text string
	if name, ok := strings.CutPrefix(src, "env:"); ok {
		text = os.Getenv(name)
	} else {
		data, err := os.ReadFile(src)
		check(err)
		text = string(data)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(seed) != ed25519.SeedSize {
		fail("the signing key isn't a base64 Ed25519 seed")
	}
	return ed25519.NewKeyFromSeed(seed)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "signrelease: "+format+"\n", args...)
	os.Exit(1)
}
