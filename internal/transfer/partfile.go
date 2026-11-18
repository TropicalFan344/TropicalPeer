package transfer

import (
	"errors"
	"os"
)

// PartFile is one incoming file on its way to disk that doesn't arrive over
// a transfer between devices (a reply on a share link). It is written as
// ".part" and only gets its real name once Keep is called.
type PartFile struct {
	final, part string
	f           *os.File
}

// NewPartFile starts a file in dir. name comes from someone else, so only
// its SafeName is used.
func NewPartFile(dir, name string) (*PartFile, error) {
	safe := SafeName(name)
	if safe == "" {
		return nil, errors.New("invalid file name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	final, part, f, err := createUnique(dir, safe)
	if err != nil {
		return nil, err
	}
	return &PartFile{final: final, part: part, f: f}, nil
}

func (p *PartFile) Write(b []byte) (int, error) { return p.f.Write(b) }

// Keep finishes the file and returns where it was saved.
func (p *PartFile) Keep() (string, error) {
	if err := p.f.Close(); err != nil {
		os.Remove(p.part)
		return "", err
	}
	// A file of that name may have appeared since the part was started.
	if _, err := os.Stat(p.final); err == nil {
		p.final = freeName(p.final)
	}
	if err := os.Rename(p.part, p.final); err != nil {
		os.Remove(p.part)
		return "", err
	}
	markDownloaded(p.final)
	return p.final, nil
}

// Discard deletes what was written.
func (p *PartFile) Discard() {
	p.f.Close()
	os.Remove(p.part)
}
