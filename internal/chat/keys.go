package chat

import "unicode/utf8"

// KeyKind is what a key press means to the editor.
type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyEnter
	KeyBackspace
	KeyDelete
	KeyTab
	KeyBackTab
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyCtrlC
	KeyCtrlD
	KeyCtrlU
	KeyCtrlW
	KeyCtrlK
	KeyCtrlL
	KeyUnknown
)

// Key is one decoded key press.
type Key struct {
	Kind KeyKind
	Rune rune
}

// Decode turns the bytes one terminal read returned into key presses. A read that is only ESC is
// the Esc key; ESC followed by more is an escape sequence (arrows, Home, End, Delete).
func Decode(b []byte) []Key {
	var keys []Key
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			if len(b) == 1 {
				keys = append(keys, Key{Kind: KeyEsc})
				b = b[1:]
				continue
			}
			k, n := escape(b)
			keys = append(keys, k)
			b = b[n:]
		case c == '\r' || c == '\n':
			keys = append(keys, Key{Kind: KeyEnter})
			b = b[1:]
			if c == '\r' && len(b) > 0 && b[0] == '\n' {
				b = b[1:]
			}
		case c == 0x7f || c == 0x08:
			keys = append(keys, Key{Kind: KeyBackspace})
			b = b[1:]
		case c == '\t':
			keys = append(keys, Key{Kind: KeyTab})
			b = b[1:]
		case c == 0x03:
			keys = append(keys, Key{Kind: KeyCtrlC})
			b = b[1:]
		case c == 0x04:
			keys = append(keys, Key{Kind: KeyCtrlD})
			b = b[1:]
		case c == 0x15:
			keys = append(keys, Key{Kind: KeyCtrlU})
			b = b[1:]
		case c == 0x17:
			keys = append(keys, Key{Kind: KeyCtrlW})
			b = b[1:]
		case c == 0x0b:
			keys = append(keys, Key{Kind: KeyCtrlK})
			b = b[1:]
		case c == 0x0c:
			keys = append(keys, Key{Kind: KeyCtrlL})
			b = b[1:]
		case c == 0x01:
			keys = append(keys, Key{Kind: KeyHome})
			b = b[1:]
		case c == 0x05:
			keys = append(keys, Key{Kind: KeyEnd})
			b = b[1:]
		case c < 0x20:
			keys = append(keys, Key{Kind: KeyUnknown})
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			keys = append(keys, Key{Kind: KeyRune, Rune: r})
			b = b[n:]
		}
	}
	return keys
}

// escape decodes "ESC [ …" and "ESC O …" sequences, returning the key and the bytes used.
func escape(b []byte) (Key, int) {
	if len(b) >= 3 && (b[1] == '[' || b[1] == 'O') {
		switch b[2] {
		case 'A':
			return Key{Kind: KeyUp}, 3
		case 'B':
			return Key{Kind: KeyDown}, 3
		case 'C':
			return Key{Kind: KeyRight}, 3
		case 'D':
			return Key{Kind: KeyLeft}, 3
		case 'H':
			return Key{Kind: KeyHome}, 3
		case 'F':
			return Key{Kind: KeyEnd}, 3
		case 'Z':
			return Key{Kind: KeyBackTab}, 3
		}
		if b[1] == '[' && b[2] >= '0' && b[2] <= '9' {
			n := 3
			for n < len(b) && (b[n] >= '0' && b[n] <= '9' || b[n] == ';') {
				n++
			}
			if n < len(b) {
				n++
			}
			switch {
			case b[2] == '3' && b[n-1] == '~':
				return Key{Kind: KeyDelete}, n
			case (b[2] == '1' || b[2] == '7') && b[n-1] == '~':
				return Key{Kind: KeyHome}, n
			case (b[2] == '4' || b[2] == '8') && b[n-1] == '~':
				return Key{Kind: KeyEnd}, n
			}
			return Key{Kind: KeyUnknown}, n
		}
	}
	// A lone ESC followed by something else (Alt+key): skip the ESC.
	return Key{Kind: KeyUnknown}, 1
}
