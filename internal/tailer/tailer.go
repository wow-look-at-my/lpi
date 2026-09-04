// Package tailer follows a growing log file by
package tailer

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/wow-look-at-my/lpi/internal/linescan"
)

// DefaultInterval is the poll interval used when
const DefaultInterval = 150 * time.Millisecond

// Tailer follows file
type Tailer struct {
	Path      string
	FromStart bool          // read pre-existing content
	Interval  time.Duration // poll interval, default if
}

// Run follows the file until ctx is done (returning
func (t *Tailer) Run(ctx context.Context, lines chan<- string) error {
	defer close(lines)
	interval := t.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	// All read bytes are pumped through a pipe into a
	pr, pw := io.Pipe()
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := linescan.NewScanner(pr)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				pr.CloseWithError(context.Canceled) // unblock the writer
				return
			}
		}
	}()
	err := t.poll(ctx, interval, pw)
	pr.CloseWithError(context.Canceled) // stop the scanner without a final flush
	pw.CloseWithError(context.Canceled) // unblock a scanner mid-read
	<-scanDone
	return err
}

// poll is the file-watching loop; it writes
func (t *Tailer) poll(ctx context.Context, interval time.Duration, pw *io.PipeWriter) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var offset int64
	skipExisting := !t.FromStart // only for a file that exists at start
	buf := make([]byte, 64*1024)
	for {
		if f == nil {
			var err error
			f, offset, err = t.open(skipExisting)
			if err != nil {
				return err
			}
		}
		skipExisting = false // later (re)opens always read from
		if f != nil {
			if err := t.drain(f, &offset, buf, pw); err != nil {
				return err
			}
			reopen, err := t.checkRotation(f)
			if err != nil {
				return err
			}
			if reopen {
				f.Close()
				f = nil
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// open opens the tailed path
func (t *Tailer) open(skipExisting bool) (*os.File, int64, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	if skipExisting {
		offset, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return nil, 0, err
		}
		return f, offset, nil
	}
	return f, 0, nil
}

// drain reads everything currently available from f
func (t *Tailer) drain(f *os.File, offset *int64, buf []byte, pw *io.PipeWriter) error {
	for {
		n, err := f.Read(buf)
		if n > 0 {
			*offset += int64(n)
			if _, werr := pw.Write(buf[:n]); werr != nil {
				return nil // scanner stopped: ctx done, poll loop exits via
			}
		}
		if err == io.EOF {
			st, serr := f.Stat()
			if serr != nil {
				return serr
			}
			if st.Size() < *offset {
				// Truncated in place: start over from the top
				if _, serr := f.Seek(0, io.SeekStart); serr != nil {
					return serr
				}
				*offset = 0
				continue
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// checkRotation reports whether the path now refers
func (t *Tailer) checkRotation(f *os.File) (bool, error) {
	pathSt, err := os.Stat(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil // rotated away; wait for a new file
		}
		return false, err
	}
	fileSt, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(pathSt, fileSt) {
		return true, nil // renamed and recreated: follow the new file
	}
	return false, nil
}
