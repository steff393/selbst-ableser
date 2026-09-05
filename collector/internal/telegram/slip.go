package telegram

import "errors"

// Frame delimiter and escape bytes for the SLIP-like framing used on the
// serial link to the receiver.
const (
	slipEnd    = 0xC0
	slipEsc    = 0xDB
	slipEscEnd = 0xDC
	slipEscEsc = 0xDD
)

// ErrIncompleteFrame indicates the byte stream does not yet contain a full
// delimited frame; the caller should keep reading.
var ErrIncompleteFrame = errors.New("telegram: incomplete frame")

// ErrMalformedEscape indicates an escape byte was not followed by a valid
// escape code.
var ErrMalformedEscape = errors.New("telegram: malformed escape sequence")

// SplitFrame extracts the first delimited, unescaped frame from buf.
// It returns the unescaped frame content (without the surrounding 0xC0
// bytes) and the number of bytes of buf consumed, including any leading
// bytes before the frame and the trailing delimiter.
//
// Leading 0xC0 bytes (used as wake-up padding, see the receiver
// initialization sequence) are skipped without producing an empty frame.
func SplitFrame(buf []byte) (frame []byte, consumed int, err error) {
	start := -1
	for i, b := range buf {
		if b == slipEnd {
			if start == -1 {
				continue // skip leading END bytes (wake-up padding)
			}
			raw := buf[start:i]
			unescaped, uerr := unescape(raw)
			if uerr != nil {
				return nil, i + 1, uerr
			}
			return unescaped, i + 1, nil
		}
		if start == -1 {
			start = i
		}
	}
	return nil, 0, ErrIncompleteFrame
}

func unescape(raw []byte) ([]byte, error) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b != slipEsc {
			out = append(out, b)
			continue
		}
		i++
		if i >= len(raw) {
			return nil, ErrMalformedEscape
		}
		switch raw[i] {
		case slipEscEnd:
			out = append(out, slipEnd)
		case slipEscEsc:
			out = append(out, slipEsc)
		default:
			return nil, ErrMalformedEscape
		}
	}
	return out, nil
}

// EncodeFrame escapes content and wraps it with SLIP delimiters, producing
// the byte sequence as it would appear on the wire. It is primarily used by
// tests and by the file-based telegram source.
func EncodeFrame(content []byte) []byte {
	out := make([]byte, 0, len(content)+2)
	out = append(out, slipEnd)
	for _, b := range content {
		switch b {
		case slipEnd:
			out = append(out, slipEsc, slipEscEnd)
		case slipEsc:
			out = append(out, slipEsc, slipEscEsc)
		default:
			out = append(out, b)
		}
	}
	out = append(out, slipEnd)
	return out
}
