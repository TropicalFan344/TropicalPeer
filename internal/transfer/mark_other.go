//go:build !windows && !darwin

package transfer

// markDownloaded does nothing here: Linux and Android have no standard
// "downloaded from elsewhere" flag that the system acts on.
func markDownloaded(string) {}
