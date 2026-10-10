package appcore

import "testing"

// Messages written by the app layer follow the UI's language, and an
// unknown language leaves the current one in place.
func TestMessagesFollowLanguage(t *testing.T) {
	c := New(t.TempDir(), nil)
	if err := c.SetDeviceName("  "); err == nil || err.Error() != "Give this device a name." {
		t.Fatalf("English: %v", err)
	}
	c.SetLanguage("zh-TW")
	if err := c.SetDeviceName(""); err == nil || err.Error() != "請替這台裝置取個名字。" {
		t.Fatalf("Traditional Chinese: %v", err)
	}
	c.SetLanguage("xx-unknown")
	if got := c.T("transfer.offline", "name", "Moon PC"); got != "Moon PC 目前離線。請確認那台裝置上的 TropicalPeer 已開啟。" {
		t.Fatalf("unknown language should keep zh-TW: %q", got)
	}
	c.SetLanguage("ru")
	if got := c.T("common.files", "count", 5); got != "5 файлов" {
		t.Fatalf("Russian plural: %q", got)
	}
}
