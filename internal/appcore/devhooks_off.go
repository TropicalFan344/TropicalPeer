//go:build !devhooks

package appcore

// Dev hooks do nothing in normal builds; see devhooks.go.
func (c *Core) runDevHooks() {}

func (c *Core) devAdjustNetwork(*NetworkDTO) {}
