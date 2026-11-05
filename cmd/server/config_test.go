package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configName)
	// As Notepad saves it: with a byte order mark.
	os.WriteFile(path, []byte("\xef\xbb\xbf"+`{
  "cloudflare_api_token": "dns-from-file",
  "cloudflare_turn_key_id": "key-from-file",
  "cloudflare_turn_api_token": "turn-from-file"
}`), 0o600)

	t.Chdir(dir)
	found, err := findConfig("")
	if err != nil || found != configName {
		t.Fatalf("findConfig = %q, %v; want the one in the working directory", found, err)
	}
	cfg, err := loadConfig(found)
	if err != nil {
		t.Fatal(err)
	}

	// A flag (or environment variable) given wins over the file.
	for _, v := range []string{"CLOUDFLARE_API_TOKEN", "DUCKDNS_TOKEN", "CLOUDFLARE_TURN_KEY_ID", "CLOUDFLARE_TURN_API_TOKEN"} {
		t.Setenv(v, "")
	}
	old := flag.CommandLine
	defer func() { flag.CommandLine = old }()
	flag.CommandLine = flag.NewFlagSet("server", flag.ContinueOnError)
	var lf linkFlags
	lf.register()
	flag.CommandLine.Parse([]string{"-cloudflare-turn-key", "key-from-flag"})
	lf.applyConfig(cfg)
	if *lf.cfTURNKey != "key-from-flag" || *lf.cfTURNToken != "turn-from-file" || *lf.cf != "dns-from-file" {
		t.Fatalf("got key %q token %q dns %q", *lf.cfTURNKey, *lf.cfTURNToken, *lf.cf)
	}
}

func TestConfigFileTypo(t *testing.T) {
	path := filepath.Join(t.TempDir(), configName)
	os.WriteFile(path, []byte(`{"cloudflare_turn_keyid": "x"}`), 0o600)
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "cloudflare_turn_keyid") {
		t.Fatalf("misspelled key: %v", err)
	}
}

func TestNoConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if found, err := findConfig(""); err != nil || (found != "" && !strings.HasSuffix(found, configName)) {
		t.Fatalf("findConfig = %q, %v", found, err)
	}
	if _, err := findConfig("missing.json"); err == nil {
		t.Fatal("a -config file that doesn't exist should be an error")
	}
}
