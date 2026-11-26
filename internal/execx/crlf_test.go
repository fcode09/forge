package execx

import (
	"bytes"
	"sync"
	"testing"
)

func TestCRLFOnlyAddsMissingCarriageReturns(t *testing.T) {
	var b bytes.Buffer
	w := CRLF(&b)
	n, err := w.Write([]byte("a\nb\r\nc"))
	if err != nil || n != 6 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// A "\r" at the end of one write and "\n" at the start of the next is one CRLF, not two.
	_, _ = w.Write([]byte("\r"))
	_, _ = w.Write([]byte("\nd\n"))
	if got, want := b.String(), "a\r\nb\r\nc\r\nd\r\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCRLFIsSafeForConcurrentWriters(t *testing.T) {
	var b bytes.Buffer
	w := CRLF(&b)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = w.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	if got := bytes.Count(b.Bytes(), []byte("line\r\n")); got != 800 {
		t.Fatalf("complete lines = %d, want 800", got)
	}
}
