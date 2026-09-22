package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Positions remembers how far each file has been read, across restarts.
type Positions struct {
	mu    sync.Mutex
	path  string
	Files map[string]Position `json:"files"`
}

type Position struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

func LoadPositions(dir string) *Positions {
	p := &Positions{path: filepath.Join(dir, "positions.json"), Files: map[string]Position{}}
	if raw, err := os.ReadFile(p.path); err == nil {
		_ = json.Unmarshal(raw, p)
	}
	return p
}

func (p *Positions) Get(file string) (Position, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos, ok := p.Files[file]
	return pos, ok
}

func (p *Positions) Set(file string, pos Position) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Files[file] = pos
}

func (p *Positions) Save() error {
	p.mu.Lock()
	raw, _ := json.Marshal(p)
	p.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// ReadNewLines reads the complete lines appended to a file since the last call.
// It follows rotation (new inode or truncated file) and never returns a
// half-written line: that stays for the next round.
func ReadNewLines(positions *Positions, file string, fromStart bool, emit func(line string)) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	inode := inodeOf(info)

	pos, known := positions.Get(file)
	switch {
	case !known && !fromStart:
		// First time: start from the end, history is not our business.
		positions.Set(file, Position{Inode: inode, Offset: info.Size()})
		return nil
	case !known, pos.Inode != inode, info.Size() < pos.Offset:
		pos = Position{Inode: inode, Offset: 0}
	}

	if _, err := f.Seek(pos.Offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(f, 256*1024)
	offset := pos.Offset
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			// Partial line (or EOF): read it again next round.
			break
		}
		offset += int64(len(line))
		emit(trimNewline(line))
	}

	positions.Set(file, Position{Inode: inode, Offset: offset})
	return nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func inodeOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
