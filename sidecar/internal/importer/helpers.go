package importer

import (
	"regexp"
	"strconv"
)

// regexpMustCompile 包装 regexp.MustCompile，集中处理（编译期模式固定，出错属编程错误）。
func regexpMustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

// atoiSafe 安全地把字符串转 int，失败返回 -1。
func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
