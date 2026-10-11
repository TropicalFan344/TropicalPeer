// Package mobile is TropicalPeer's interface for phone apps, built into an
// Android library with
//
//	gomobile bind -target=android -androidapi 24 -javapkg app.tropicalpeer -o tropicalpeer.aar ./mobile
//
// gomobile only passes strings, numbers, []byte and interfaces, so the API is
// deliberately small and JSON-shaped:
//
//   - Start runs the device; events arrive on Listener.OnEvent(name, json),
//     with the same names and payloads the desktop frontend receives
//     ("transfer:update", "offer:new", ...).
//   - Call(method, argsJSON) runs one app method with its arguments as a JSON
//     array and returns the result as JSON. Methods and arguments mirror the
//     desktop App (app.go), so the Svelte frontend's api.ts maps onto it
//     one to one. Errors are already worded for people.
//
// Android specifics live in the Kotlin app: it opens picked documents
// (Documents.OpenFD), reports the device's addresses (SetLocalIPs) and
// network switches (NetworkChanged), and moves received files out of
// downloadDir into the user's Downloads.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TropicalPeer/internal/appcore"
	"TropicalPeer/internal/node"
)

// Listener receives events from the running device. OnEvent is called from
// Go goroutines; the app must hop to its UI thread itself. json is "null"
// for events without a payload.
type Listener interface {
	OnEvent(name string, json string)
}

// Documents opens files the user picked, which on Android are content://
// URIs rather than paths.
type Documents interface {
	// OpenFD opens uri for reading and returns a file descriptor the Go side
	// then owns and closes (ParcelFileDescriptor.detachFd() in Kotlin).
	OpenFD(uri string) (int, error)
}

var (
	mu     sync.Mutex
	core   *appcore.Core
	cancel context.CancelFunc
	docs   Documents
	dlDir  string
)

// Start runs this device until Stop. dataDir keeps its identity, peers and
// settings (Context.getFilesDir()); received files land in downloadDir
// (an app-specific folder such as getExternalFilesDir(DIRECTORY_DOWNLOADS)).
// deviceName names a new device ("Pixel 8"); a name the user set is kept.
func Start(dataDir, downloadDir, deviceName string, listener Listener, documents Documents) error {
	mu.Lock()
	defer mu.Unlock()
	if core != nil {
		return errors.New("already started")
	}
	if dataDir == "" || downloadDir == "" {
		return errors.New("dataDir and downloadDir are required")
	}
	c := appcore.New(dataDir, func(name string, data any) {
		b, err := json.Marshal(data)
		if err != nil {
			b = []byte("null")
		}
		listener.OnEvent(name, string(b))
	})
	// Updates download here; an installed one leaves its APK behind.
	updates := filepath.Join(dataDir, "updates")
	os.RemoveAll(updates)
	c.SetUpdater(androidUpdater{dir: updates, listener: listener})
	ctx, cancelFn := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		cancelFn()
		return err
	}
	n := c.Node()
	// Android owns where files go, so the download folder isn't a setting.
	if s := n.Settings(); s.DownloadDir != downloadDir {
		s.DownloadDir = downloadDir
		if err := n.UpdateSettings(s); err != nil {
			cancelFn()
			return err
		}
	}
	// A new identity is named after the hostname, which Android reports as
	// "localhost"; name the device after the phone instead.
	if deviceName = strings.TrimSpace(deviceName); deviceName != "" && isPlaceholderName(n.Identity.Name) {
		c.SetDeviceName(deviceName)
	}
	core, cancel, docs, dlDir = c, cancelFn, documents, downloadDir
	return nil
}

func isPlaceholderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "localhost", "android":
		return true
	}
	return false
}

// Stop disconnects and removes the router port forward. Start may be called
// again afterwards.
func Stop() {
	mu.Lock()
	c, cancelFn := core, cancel
	core, cancel, docs = nil, nil, nil
	mu.Unlock()
	if c == nil {
		return
	}
	c.Stop()
	cancelFn()
	// Give the node a moment to close its sockets so a quick restart can
	// bind the same port.
	time.Sleep(200 * time.Millisecond)
}

// SetLocalIPs reports this device's addresses, one per line, and its
// router's address (the default route's gateway; "" if unknown), from
// ConnectivityManager's LinkProperties. Call it at start and on every
// network change, before NetworkChanged. Android 11+ doesn't let Go look
// these up itself; without them devices on the same Wi-Fi can't find each
// other directly and the router can't be asked to forward a port.
func SetLocalIPs(lines, gateway string) {
	var ips []net.IP
	for _, f := range strings.Fields(lines) {
		if ip := net.ParseIP(strings.Trim(f, "[]")); ip != nil {
			ips = append(ips, ip)
		}
	}
	node.SetLocalIPs(ips, net.ParseIP(strings.TrimSpace(gateway)))
}

// NetworkChanged tells the device it moved to another network (Wi-Fi to
// mobile data, or a different Wi-Fi). It reconnects and rechecks.
func NetworkChanged() {
	mu.Lock()
	c := core
	mu.Unlock()
	if c != nil && c.Node() != nil {
		c.Node().NetworkChanged()
	}
}

// Text is the message for key (a locales key such as "android.readyTitle")
// in the UI's language, for text Android shows itself: notifications and
// plugin errors. paramsJSON is an object of {placeholder} values, or "".
func Text(key, paramsJSON string) string {
	mu.Lock()
	c := core
	mu.Unlock()
	var params map[string]any
	if paramsJSON != "" {
		json.Unmarshal([]byte(paramsJSON), &params)
	}
	var kv []any
	for k, v := range params {
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			v = int64(f) // JSON numbers: plural forms want whole counts
		}
		kv = append(kv, k, v)
	}
	if c == nil {
		return appcore.New("", nil).T(key, kv...)
	}
	return c.T(key, kv...)
}

// Call runs an app method. argsJSON is a JSON array of its arguments ("" or
// "[]" for none); the result is JSON ("null" for methods without one).
func Call(method, argsJSON string) (string, error) {
	mu.Lock()
	c := core
	mu.Unlock()
	if c == nil {
		return "", errors.New("TropicalPeer isn't running")
	}
	h, ok := methods[method]
	if !ok {
		return "", fmt.Errorf("unknown method %q", method)
	}
	var args []json.RawMessage
	if s := strings.TrimSpace(argsJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "", fmt.Errorf("%s: arguments must be a JSON array: %w", method, err)
		}
	}
	res, err := h(c, args)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
