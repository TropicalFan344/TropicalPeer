package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// configName is the server's config file, looked for in the working
// directory and then next to the executable. It holds the API keys, so they
// needn't be typed on the command line or kept in the environment:
//
//	{
//	  "cloudflare_api_token": "...",
//	  "cloudflare_turn_key_id": "...",
//	  "cloudflare_turn_api_token": "..."
//	}
//
// A key given as a flag wins over its environment variable, which wins over
// the file. Keep the file private: anyone who reads it can use the keys.
const configName = "tropicalpeer-server.json"

type serverConfig struct {
	// Cloudflare API token that may edit the zone's DNS (-cloudflare-token).
	CloudflareAPIToken string `json:"cloudflare_api_token,omitempty"`
	// DuckDNS token (-duckdns-token).
	DuckDNSToken string `json:"duckdns_token,omitempty"`
	// Cloudflare Realtime TURN key: its ID and API token (-cloudflare-turn-key,
	// -cloudflare-turn-token).
	CloudflareTURNKeyID    string `json:"cloudflare_turn_key_id,omitempty"`
	CloudflareTURNAPIToken string `json:"cloudflare_turn_api_token,omitempty"`
}

// findConfig is the config file to use: path when given, else configName in
// the working directory or next to the executable. "" when there is none.
func findConfig(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	dirs := []string{"."}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, d := range dirs {
		p := filepath.Join(d, configName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// loadConfig reads path. Unknown keys are an error, so a misspelled one
// isn't silently ignored.
func loadConfig(path string) (serverConfig, error) {
	var c serverConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // Notepad's byte order mark
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// fill sets *flag from the file's value when neither the flag nor its
// environment variable gave one.
func fill(flag *string, fromFile string) {
	if *flag == "" {
		*flag = fromFile
	}
}
