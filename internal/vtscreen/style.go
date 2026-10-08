package vtscreen

import (
	"strconv"
)

// The attributes of a style, each one SGR turns on and off.
const (
	bold uint16 = 1 << iota
	dim
	italic
	blink
	inverse
	invisible
	strikethrough
	overline
)

// attributeCodes are the SGR codes that turn each attribute on, in the
// order a style writes them.
var attributeCodes = []struct {
	attribute uint16
	code      string
}{{bold, "1"}, {dim, "2"}, {italic, "3"}, {blink, "5"}, {inverse, "7"}, {invisible, "8"}, {strikethrough, "9"}, {overline, "53"}}

// The kinds of colour a style holds, each written as SGR wrote it, since a
// terminal can draw the 16 base colours apart from the same entries of the
// 256 (such as bold in bright colours).
const (
	defaultColor uint8 = iota
	baseColor
	paletteColor
	rgbColor
)

// color is one colour of a style: the default, one of the 16 base colours or
// the 256 of the palette by index, or red, green and blue.
type color struct {
	kind  uint8
	value uint32
}

// style is how a cell is drawn: its attributes, underline and colours.
type style struct {
	attributes uint16
	// underline is 0 for none, else the SGR 4 sub-parameter of its kind: 1
	// single, 2 double, 3 curly, 4 dotted, 5 dashed.
	underline                          uint8
	foreground, background, underColor color
}

// apply changes the style as SGR with params does: each param is a code
// with its colon-separated sub-parameters.
func (s *style) apply(params [][]uint16) {
	for i := 0; i < len(params); i++ {
		param := params[i]
		switch code := param[0]; {
		case code == 0:
			*s = style{}
		case code == 1:
			s.attributes |= bold
		case code == 2:
			s.attributes |= dim
		case code == 3:
			s.attributes |= italic
		case code == 4:
			s.underline = 1
			if len(param) > 1 && param[1] <= 5 {
				s.underline = uint8(param[1])
			}
		case code == 5 || code == 6:
			s.attributes |= blink
		case code == 7:
			s.attributes |= inverse
		case code == 8:
			s.attributes |= invisible
		case code == 9:
			s.attributes |= strikethrough
		case code == 21:
			s.underline = 2
		case code == 22:
			s.attributes &^= bold | dim
		case code == 23:
			s.attributes &^= italic
		case code == 24:
			s.underline = 0
		case code == 25:
			s.attributes &^= blink
		case code == 27:
			s.attributes &^= inverse
		case code == 28:
			s.attributes &^= invisible
		case code == 29:
			s.attributes &^= strikethrough
		case code >= 30 && code <= 37:
			s.foreground = color{baseColor, uint32(code - 30)}
		case code == 38:
			s.foreground, i = extendedColor(params, i)
		case code == 39:
			s.foreground = color{}
		case code >= 40 && code <= 47:
			s.background = color{baseColor, uint32(code - 40)}
		case code == 48:
			s.background, i = extendedColor(params, i)
		case code == 49:
			s.background = color{}
		case code == 53:
			s.attributes |= overline
		case code == 55:
			s.attributes &^= overline
		case code == 58:
			s.underColor, i = extendedColor(params, i)
		case code == 59:
			s.underColor = color{}
		case code >= 90 && code <= 97:
			s.foreground = color{baseColor, uint32(code - 90 + 8)}
		case code >= 100 && code <= 107:
			s.background = color{baseColor, uint32(code - 100 + 8)}
		}
	}
}

// extendedColor reads the colour SGR 38, 48 or 58 at params[i] gives, in its
// colon form (38:5:n, 38:2::r:g:b or 38:2:r:g:b) or its semicolon form
// (38;5;n or 38;2;r;g;b), and returns it with the index of the last param it
// took.
func extendedColor(params [][]uint16, i int) (color, int) {
	if values := params[i][1:]; len(values) > 0 {
		switch {
		case values[0] == 5 && len(values) >= 2:
			return color{paletteColor, uint32(values[1] & 0xFF)}, i
		case values[0] == 2 && len(values) >= 5:
			return rgb(values[2], values[3], values[4]), i
		case values[0] == 2 && len(values) == 4:
			return rgb(values[1], values[2], values[3]), i
		}
		return color{}, i
	}
	if i+1 >= len(params) {
		return color{}, i
	}
	switch params[i+1][0] {
	case 5:
		if i+2 < len(params) {
			return color{paletteColor, uint32(params[i+2][0] & 0xFF)}, i + 2
		}
	case 2:
		if i+4 < len(params) {
			return rgb(params[i+2][0], params[i+3][0], params[i+4][0]), i + 4
		}
	}
	return color{}, len(params) - 1
}

func rgb(red, green, blue uint16) color {
	return color{rgbColor, uint32(red&0xFF)<<16 | uint32(green&0xFF)<<8 | uint32(blue&0xFF)}
}

// appendSGR appends the SGR sequence that sets a terminal's style to s from
// any other.
func (s style) appendSGR(out []byte) []byte {
	out = append(out, "\x1b[0"...)
	for _, each := range attributeCodes {
		if s.attributes&each.attribute != 0 {
			out = append(out, ';')
			out = append(out, each.code...)
		}
	}
	switch s.underline {
	case 0:
	case 1:
		out = append(out, ";4"...)
	default:
		out = append(out, ";4:"...)
		out = strconv.AppendUint(out, uint64(s.underline), 10)
	}
	out = s.foreground.appendSGR(out, 30, 90, 38)
	out = s.background.appendSGR(out, 40, 100, 48)
	out = s.underColor.appendSGR(out, 0, 0, 58)
	return append(out, 'm')
}

// appendSGR appends the SGR codes for c: base for the first 8 base colours,
// bright for the next 8, and extended for the palette and red, green and
// blue.
func (c color) appendSGR(out []byte, base, bright, extended int) []byte {
	switch {
	case c.kind == defaultColor:
		return out
	case c.kind == baseColor && base != 0 && c.value < 8:
		return strconv.AppendInt(append(out, ';'), int64(base+int(c.value)), 10)
	case c.kind == baseColor && base != 0:
		return strconv.AppendInt(append(out, ';'), int64(bright+int(c.value)-8), 10)
	case c.kind == rgbColor:
		out = strconv.AppendInt(append(out, ';'), int64(extended), 10)
		out = append(out, ";2;"...)
		out = strconv.AppendUint(out, uint64(c.value>>16), 10)
		out = strconv.AppendUint(append(out, ';'), uint64(c.value>>8&0xFF), 10)
		return strconv.AppendUint(append(out, ';'), uint64(c.value&0xFF), 10)
	}
	out = strconv.AppendInt(append(out, ';'), int64(extended), 10)
	out = append(out, ";5;"...)
	return strconv.AppendUint(out, uint64(c.value), 10)
}
