package app

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID 生成短随机 ID（8 字节十六进制，碰撞概率足够低，且对人类友好）。
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewRunID 生成带时间前缀的 run ID，便于按时间排序。
func NewRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}
