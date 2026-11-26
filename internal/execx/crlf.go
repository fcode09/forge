package execx

import (
	"io"
	"sync"
)

// CRLF returns a writer that turns every bare "\n" into "\r\n". Docker running inside WSL writes its progress
// through wsl.exe, which can leave the Windows console without automatic carriage returns while the program
// runs: every line then starts where the previous one ended (a "staircase"). Sending "\r\n" is correct in
// either console mode. It is safe for concurrent use, so stdout and stderr of one process can share it.
func CRLF(w io.Writer) io.Writer { return &crlfWriter{w: w} }

type crlfWriter struct {
	mu   sync.Mutex
	w    io.Writer
	prev byte
}

func (c *crlfWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, 0, len(p)+len(p)/16+1)
	for _, b := range p {
		if b == '\n' && c.prev != '\r' {
			out = append(out, '\r')
		}
		out = append(out, b)
		c.prev = b
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
