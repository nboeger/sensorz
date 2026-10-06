package ui

import "strings"

// A terminal cannot change font size, so "bigger" has to mean taller: a number
// drawn as a grid of blocks in a bitmap font, several rows high and wide,
// exactly the way a seven-segment or dot-matrix display renders a figure. This
// is the big readout at the top of each panel - the one number a user glances
// at - and it is why the rest of the panel can stay small.
//
// The glyphs are 3 pixels wide by 5 tall, which is the largest size that stays
// legible at the sizes a dashboard has to fit into. Each pixel is drawn as a
// horizontal run of cells, so doubling the scale widens the figure without
// making it taller.
var bigFont = map[rune][5]string{
	'0': {"111", "101", "101", "101", "111"},
	'1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"},
	'5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"},
	'7': {"111", "001", "001", "001", "001"},
	'8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"},

	// The decimal point sits on the baseline, in the middle of the glyph cell,
	// the way a dot matrix display draws one.
	'.': {"000", "000", "000", "000", "010"},
	'-': {"000", "000", "111", "000", "000"},
	'+': {"000", "010", "111", "010", "000"},
	'/': {"001", "001", "010", "100", "100"},
	' ': {"000", "000", "000", "000", "000"},

	// Units. Uppercase only: a 3x5 cell has no room for a lowercase letter
	// that is still readable.
	'°': {"010", "101", "010", "000", "000"},
	'C': {"011", "100", "100", "100", "011"},
	'R': {"110", "101", "110", "101", "101"},
	'P': {"110", "101", "110", "100", "100"},
	'M': {"101", "111", "111", "101", "101"},
	'x': {"000", "101", "010", "101", "000"},
}

// bigGlyphHeight is the number of rows every big glyph occupies.
const bigGlyphHeight = 5

// bigPixel is what a lit pixel is drawn as. A full block keeps the figure solid
// and joins up with its neighbours, which a thin mark would not.
const bigPixel = '█'

// The half-block glyphs each carry two pixel rows: the upper block is the top
// pixel, the lower block the bottom one, and the full block both.
const (
	bigUpper = '\u2580' // ▀
	bigLower = '\u2584' // ▄
)

// BigNumber renders text as a blocky display figure, returned as rows, one
// cell per pixel row.
//
// scale is how many cells wide each pixel is, so scale 2 draws every pixel as a
// two cell run and makes the figure twice as wide. Unknown runes render as
// blanks rather than as boxes, so an unexpected degree sign or multiplication
// sign cannot turn the headline number into a row of tofu.
func BigNumber(text string, scale int) []string {
	return bigNumber(text, scale, false)
}

// BigNumberHalf renders the same figure at half height.
//
// Two pixel rows are packed into one cell with the half-block glyphs, which is
// how a terminal gets a display figure small enough to sit above a graph
// without eating the panel. It is half the height of BigNumber at the same
// width and at the same scale, so a figure that is too tall gets shorter
// rather than smaller.
func BigNumberHalf(text string, scale int) []string {
	return bigNumber(text, scale, true)
}

func bigNumber(text string, scale int, half bool) []string {
	if scale < 1 {
		scale = 1
	}

	height := bigGlyphHeight
	if half {
		// Pixel rows come in pairs, and an odd row gets an empty partner.
		height = (bigGlyphHeight + 1) / 2
	}

	// Build the pixels first: one string per pixel row, then pack them into
	// cells. Working in pixels keeps both renderers down to the same loop.
	pixels := make([]strings.Builder, bigGlyphHeight)
	for i, r := range text {
		glyph, ok := bigFont[r]
		if !ok {
			glyph = bigFont[' ']
		}
		for y, line := range glyph {
			for _, px := range line {
				if px == '1' {
					pixels[y].WriteString(strings.Repeat(string(bigPixel), scale))
				} else {
					pixels[y].WriteString(strings.Repeat(" ", scale))
				}
			}
		}
		// A one cell gap between glyphs, so adjacent figures do not merge into
		// one block at a glance.
		if i < len(text)-1 {
			for y := range pixels {
				pixels[y].WriteByte(' ')
			}
		}
	}

	rows := make([]strings.Builder, height)
	for y := 0; y < height; y++ {
		if !half {
			rows[y].WriteString(pixels[y].String())
			continue
		}
		top, bottom := pixels[y*2].String(), ""
		if y*2+1 < bigGlyphHeight {
			bottom = pixels[y*2+1].String()
		}
		// Index by rune, not by byte: a lit pixel is a three byte block
		// character, so counting bytes would land in the middle of one and
		// read its second byte as an empty pixel.
		topRunes, bottomRunes := []rune(top), []rune(bottom)
		for c := 0; c < len(topRunes); c++ {
			hasTop := c < len(topRunes) && topRunes[c] == bigPixel
			hasBottom := c < len(bottomRunes) && bottomRunes[c] == bigPixel
			switch {
			case hasTop && hasBottom:
				rows[y].WriteRune(bigPixel)
			case hasTop:
				rows[y].WriteRune(bigUpper)
			case hasBottom:
				rows[y].WriteRune(bigLower)
			default:
				rows[y].WriteByte(' ')
			}
		}
	}

	out := make([]string, height)
	for i := range rows {
		out[i] = rows[i].String()
	}
	return out
}

// BigNumberWidth returns the width BigNumber will render text at, so a caller
// can centre it or check that it fits.
func BigNumberWidth(text string, scale int) int {
	if scale < 1 {
		scale = 1
	}
	// Each rune is 3 pixels wide plus one cell of spacing between glyphs.
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	return n*3*scale + (n - 1)
}
