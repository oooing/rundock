package publisher

import (
	"strings"
	"sync"
)

// Preserve both setup output and final compiler diagnostics without retaining
// an unbounded build log in memory.
type buildOutputBuffer struct {
	mu    sync.Mutex
	max   int
	total int64
	head  []byte
	tail  []byte
}

func (b *buildOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += int64(n)
	if b.max <= 0 {
		return n, nil
	}
	headLimit := b.max / 2
	if remaining := headLimit - len(b.head); remaining > 0 {
		take := min(remaining, len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	tailLimit := b.max - headLimit
	if len(p) >= tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-tailLimit:]...)
	} else {
		if overflow := len(b.tail) + len(p) - tailLimit; overflow > 0 {
			b.tail = b.tail[overflow:]
		}
		b.tail = append(b.tail, p...)
	}
	return n, nil
}

func (b *buildOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	separator := ""
	if b.total > int64(b.max) {
		separator = "\n…中间构建日志过长，已省略；保留开头和末尾…\n"
	}
	return strings.ToValidUTF8(string(b.head)+separator+string(b.tail), "�")
}
