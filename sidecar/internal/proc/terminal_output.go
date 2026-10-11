package proc

// stripANSI converts terminal output to plain log text. Cursor movement is a
// text boundary, not just decoration: removing it must not glue a URL to the
// right-aligned clock. SGR color changes, however, must not split a URL.
func stripANSI(s string) string {
	out := make([]byte, 0, len(s))
	separator := func() {
		if len(out) > 0 {
			last := out[len(out)-1]
			if last != ' ' && last != '\t' && last != '\r' && last != '\n' {
				out = append(out, ' ')
			}
		}
	}
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			out = append(out, s[i])
			i++
			continue
		}
		i++
		if i == len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: final byte is in 0x40..0x7e, not only letters.
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < len(s) {
				switch s[i] {
				case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'f', 'd', '`':
					separator()
				}
				i++
			}
		case ']', 'P', '^', '_': // OSC/title or terminal control strings.
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // Other ESC sequences, with optional intermediate bytes.
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
				i++
			}
			if i < len(s) {
				i++
			}
		}
	}
	return string(out)
}
