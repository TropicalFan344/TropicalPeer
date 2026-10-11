package transfer

import "os"

// markDownloaded tags a received file the way browsers tag downloads (the
// "Mark of the Web"), so Windows warns before running it (SmartScreen) and
// Office opens it in Protected View. File systems without alternate data
// streams (FAT, exFAT) can't hold the mark; the file is kept either way.
func markDownloaded(path string) {
	os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o644)
}
