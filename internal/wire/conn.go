// Package wire provides newline-delimited JSON framing shared by the
// rendezvous protocol and the peer-to-peer transfer protocol.
package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// MaxLine bounds a single message.
const MaxLine = 64 * 1024

var ErrLineTooLong = errors.New("protocol message too long")

// Conn wraps a stream with line-delimited JSON framing.
type Conn struct {
	r   *bufio.Reader
	w   io.Writer
	max int
}

func NewConn(rw io.ReadWriter) *Conn { return NewConnMax(rw, MaxLine) }

// NewConnMax is NewConn for a stream whose messages may be up to max bytes,
// such as a transfer offer listing a whole folder.
func NewConnMax(rw io.ReadWriter, max int) *Conn {
	return &Conn{r: bufio.NewReaderSize(rw, 4096), w: rw, max: max}
}

// Reader exposes the buffered reader so callers that switch to raw bytes
// don't lose data that was already buffered.
func (c *Conn) Reader() *bufio.Reader { return c.r }

func (c *Conn) Send(m any) error { return Write(c.w, m) }

// Write sends a single message to w.
func Write(w io.Writer, m any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func (c *Conn) Recv(m any) error {
	line, err := ReadLineMax(c.r, c.max)
	if err != nil {
		return err
	}
	return json.Unmarshal(line, m)
}

// ReadLine reads one newline-terminated line of at most MaxLine bytes.
func ReadLine(r *bufio.Reader) ([]byte, error) { return ReadLineMax(r, MaxLine) }

// ReadLineMax reads one newline-terminated line of at most max bytes.
func ReadLineMax(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, ErrLineTooLong
		}
		if !isPrefix {
			return buf, nil
		}
	}
}
