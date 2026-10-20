package nativehook

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxInputDepth is the nesting json.Unmarshal allows, the bound the hook held
// before it streamed its input. A tool response nests a few levels deep.
const maxInputDepth = 10000

// hookInput reads a hook's JSON input a byte at a time. It holds the bytes of
// the one value it captures and nothing else, so a tool response of any size
// streams past without being held in memory.
type hookInput struct {
	r            *bufio.Reader
	isCapturing  bool
	captured     []byte
	isOverflowed bool
}

// readFields reads the JSON object in r and decodes into each target the value
// of the member whose name matches its key, as json.Unmarshal would. Every
// other value is checked and skipped as it streams past.
func readFields(r io.Reader, targets map[string]any) error {
	in := &hookInput{r: bufio.NewReader(r)}
	if err := in.expect('{'); err != nil {
		return err
	}
	err := in.members('}', func() error {
		key, err := in.capture(in.str)
		if err != nil {
			return err
		}
		name, target := in.target(key, targets)
		if err := in.expect(':'); err != nil {
			return err
		}
		if target == nil {
			return in.value(2)
		}
		raw, err := in.capture(func() error { return in.value(2) })
		if err != nil {
			return err
		}
		// A kept value is an identity Validate holds to 4096 bytes, which
		// even fully escaped fits an event record; one past it never spools.
		if in.isOverflowed {
			return fmt.Errorf("hook field %q exceeds %d bytes", name, MaxEventBytes)
		}
		return json.Unmarshal(raw, target)
	})
	if err != nil {
		return err
	}
	b, err := in.peek()
	if err == io.ErrUnexpectedEOF {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("invalid character %q after the object", b)
}

// target is the name and target the raw key matches, ignoring case as
// json.Unmarshal does, or a nil target when the member is not kept.
func (in *hookInput) target(key []byte, targets map[string]any) (string, any) {
	var name string
	if in.isOverflowed || json.Unmarshal(key, &name) != nil {
		return "", nil
	}
	for field, target := range targets {
		if strings.EqualFold(name, field) {
			return field, target
		}
	}
	return "", nil
}

// capture runs read and returns the bytes it consumed, at most MaxEventBytes
// of them; isOverflowed says when there were more.
func (in *hookInput) capture(read func() error) ([]byte, error) {
	in.isCapturing, in.captured, in.isOverflowed = true, in.captured[:0], false
	err := read()
	in.isCapturing = false
	return in.captured, err
}

func (in *hookInput) read() (byte, error) {
	b, err := in.r.ReadByte()
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return 0, err
	}
	if in.isCapturing {
		if len(in.captured) < MaxEventBytes {
			in.captured = append(in.captured, b)
		} else {
			in.isOverflowed = true
		}
	}
	return b, nil
}

// peek returns the next byte past any whitespace without consuming it.
func (in *hookInput) peek() (byte, error) {
	for {
		next, err := in.r.Peek(1)
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return 0, err
		}
		if next[0] != ' ' && next[0] != '\t' && next[0] != '\n' && next[0] != '\r' {
			return next[0], nil
		}
		if _, err := in.r.Discard(1); err != nil {
			return 0, err
		}
	}
}

// next consumes and returns the next byte past any whitespace.
func (in *hookInput) next() (byte, error) {
	if _, err := in.peek(); err != nil {
		return 0, err
	}
	return in.read()
}

func (in *hookInput) expect(want byte) error {
	b, err := in.next()
	if err != nil {
		return err
	}
	if b != want {
		return fmt.Errorf("invalid character %q, want %q", b, want)
	}
	return nil
}

// members reads the members of an array or object whose opening byte is
// consumed, up to and including its closer.
func (in *hookInput) members(closer byte, member func() error) error {
	b, err := in.peek()
	if err != nil {
		return err
	}
	if b == closer {
		_, err := in.read()
		return err
	}
	for {
		if err := member(); err != nil {
			return err
		}
		b, err := in.next()
		if err != nil {
			return err
		}
		if b == closer {
			return nil
		}
		if b != ',' {
			return fmt.Errorf("invalid character %q, want ',' or %q", b, closer)
		}
	}
}

// value reads one JSON value at the given nesting depth.
func (in *hookInput) value(depth int) error {
	b, err := in.next()
	if err != nil {
		return err
	}
	if (b == '{' || b == '[') && depth > maxInputDepth {
		return errors.New("hook input nests too deeply")
	}
	switch {
	case b == '{':
		return in.members('}', func() error {
			if err := in.str(); err != nil {
				return err
			}
			if err := in.expect(':'); err != nil {
				return err
			}
			return in.value(depth + 1)
		})
	case b == '[':
		return in.members(']', func() error { return in.value(depth + 1) })
	case b == '"':
		return in.strBody()
	case b == 't':
		return in.literal("rue")
	case b == 'f':
		return in.literal("alse")
	case b == 'n':
		return in.literal("ull")
	case b == '-' || isDigit(b):
		return in.number(b)
	}
	return fmt.Errorf("invalid character %q looking for a value", b)
}

func (in *hookInput) str() error {
	if err := in.expect('"'); err != nil {
		return err
	}
	return in.strBody()
}

// strBody reads a string whose opening quote is consumed.
func (in *hookInput) strBody() error {
	for {
		b, err := in.read()
		if err != nil {
			return err
		}
		switch {
		case b == '"':
			return nil
		case b < 0x20:
			return fmt.Errorf("invalid character %q in a string", b)
		case b != '\\':
			continue
		}
		escape, err := in.read()
		if err != nil {
			return err
		}
		if strings.IndexByte(`"\/bfnrt`, escape) >= 0 {
			continue
		}
		if escape != 'u' {
			return fmt.Errorf("invalid escape %q in a string", escape)
		}
		for range 4 {
			h, err := in.read()
			if err != nil {
				return err
			}
			if !isDigit(h) && (h|0x20 < 'a' || h|0x20 > 'f') {
				return fmt.Errorf("invalid character %q in a \\u escape", h)
			}
		}
	}
}

func (in *hookInput) literal(rest string) error {
	for i := range len(rest) {
		b, err := in.read()
		if err != nil {
			return err
		}
		if b != rest[i] {
			return fmt.Errorf("invalid character %q in a literal", b)
		}
	}
	return nil
}

// number reads a number whose first byte is consumed.
func (in *hookInput) number(first byte) error {
	if first == '-' {
		b, err := in.read()
		if err != nil {
			return err
		}
		if !isDigit(b) {
			return fmt.Errorf("invalid character %q in a number", b)
		}
		first = b
	}
	if first != '0' {
		in.digits()
	}
	if in.following('.') {
		if in.digits() == 0 {
			return errors.New("a number's fraction needs a digit")
		}
	}
	if in.following('e') || in.following('E') {
		if !in.following('+') {
			in.following('-')
		}
		if in.digits() == 0 {
			return errors.New("a number's exponent needs a digit")
		}
	}
	return nil
}

// following consumes the next byte when it is want.
func (in *hookInput) following(want byte) bool {
	next, err := in.r.Peek(1)
	if err != nil || next[0] != want {
		return false
	}
	_, err = in.read()
	return err == nil
}

// digits consumes the digits that follow and says how many there were.
func (in *hookInput) digits() int {
	count := 0
	for {
		next, err := in.r.Peek(1)
		if err != nil || !isDigit(next[0]) {
			return count
		}
		if _, err := in.read(); err != nil {
			return count
		}
		count++
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
