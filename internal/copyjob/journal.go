package copyjob

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"sync"
	"time"
)

// Rec is one journal line: the outcome of one step for one file.
type Rec struct {
	I   int    `json:"i"`
	St  string `json:"st"`          // verified, identical, strong, failed, s2ok, s2changed, s2unstable
	N   int64  `json:"n"`           // file size
	C   string `json:"c,omitempty"` // hash of the bytes read for the copy
	S1  string `json:"s1,omitempty"`
	D1  string `json:"d1,omitempty"`
	S2  string `json:"s2,omitempty"`
	P   string `json:"p,omitempty"` // path, kept only for failures and warnings
	Err string `json:"err,omitempty"`
	T   int64  `json:"t"`
	A   int    `json:"a,omitempty"`   // copy attempts it took (1 or 2); added in 0.2.7
	Ms  int64  `json:"ms,omitempty"`  // how long the file took, in milliseconds; added in 0.2.7
	X64 string `json:"x64,omitempty"` // XXH64 of the copy as read back, for other tools; added in 0.2.57
}

// Journal is append-only. Each line is "crc32 json". Records are written at
// once but flushed to the drive in groups (at most about once a second, or
// every 256 records), so a folder of tiny files does not flush per file.
// A record counts once it is flushed; after a crash, anything later is
// re-checked.
type Journal struct {
	mu      sync.Mutex
	f       *os.File
	w       *bufio.Writer
	pending int
	stop    chan struct{}
	done    chan struct{}
}

// FlushEvery and FlushRecords set the grouping.
var (
	FlushEvery   = time.Second
	FlushRecords = 256
)

// OpenJournal replays path through fn, cuts off a torn last line (a write the
// crash interrupted, never a flushed record), and opens it for appending.
func OpenJournal(path string, fn func(Rec)) (*Journal, int, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, 0, err
	}
	good, n, err := replay(f, fn)
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if fi, _ := f.Stat(); fi.Size() > good {
		if err := f.Truncate(good); err != nil {
			f.Close()
			return nil, 0, err
		}
	}
	if _, err := f.Seek(good, io.SeekStart); err != nil {
		f.Close()
		return nil, 0, err
	}
	j := &Journal{f: f, w: bufio.NewWriterSize(f, 64<<10), stop: make(chan struct{}), done: make(chan struct{})}
	go j.flusher()
	return j, n, nil
}

func replay(r io.Reader, fn func(Rec)) (good int64, n int, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if err == io.EOF {
			return good, n, nil // a partial line without newline is torn
		}
		if err != nil {
			return good, n, err
		}
		rec, ok := parseLine(line)
		if !ok {
			return good, n, nil // damaged: stop here, the rest is cut off
		}
		fn(rec)
		good += int64(len(line))
		n++
	}
}

func parseLine(line []byte) (Rec, bool) {
	var rec Rec
	if len(line) < 11 || line[8] != ' ' {
		return rec, false
	}
	sum, err := strconv.ParseUint(string(line[:8]), 16, 32)
	body := line[9 : len(line)-1]
	if err != nil || uint32(sum) != crc32.ChecksumIEEE(body) {
		return rec, false
	}
	return rec, json.Unmarshal(body, &rec) == nil
}

// ReadJournal streams records up to limit bytes without opening for write.
func ReadJournal(path string, limit int64, fn func(Rec) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return nil
		}
		rec, ok := parseLine(line)
		if !ok {
			return nil
		}
		if !fn(rec) {
			return nil
		}
	}
}

// Append adds a record; it reaches the drive with the next group flush.
func (j *Journal) Append(r Rec) error {
	r.T = time.Now().Unix()
	body, _ := json.Marshal(r)
	j.mu.Lock()
	defer j.mu.Unlock()
	fmt.Fprintf(j.w, "%08x %s\n", crc32.ChecksumIEEE(body), body)
	j.pending++
	if j.pending >= FlushRecords {
		return j.flushLocked()
	}
	return nil
}

// Flush writes and flushes everything appended so far.
func (j *Journal) Flush() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.flushLocked()
}

func (j *Journal) flushLocked() error {
	if j.pending == 0 {
		return nil
	}
	if err := j.w.Flush(); err != nil {
		return err
	}
	j.pending = 0
	return j.f.Sync()
}

// Size is the journal's flushed length.
func (j *Journal) Size() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	fi, err := j.f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (j *Journal) flusher() {
	defer close(j.done)
	t := time.NewTicker(FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-j.stop:
			return
		case <-t.C:
			j.Flush()
		}
	}
}

// Close flushes and closes.
func (j *Journal) Close() error {
	close(j.stop)
	<-j.done
	err := j.Flush()
	j.f.Close()
	return err
}
