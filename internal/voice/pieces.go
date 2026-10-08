package voice

import "math"

// A dictation is handed to the model in pieces it hears well. Moonshine tiny,
// the model dictation pins, answers nothing at all for a sound of 9.3 seconds
// or more, so a longer one is cut where it is quietest into pieces of at most
// PIECE_MOST_FRAMES, each recognised alone and their words joined in order.
// Each piece ends half a second after its last loud part, as the model needs
// to hear its end, and a piece with nothing loud in it is left out when the
// sound has speech elsewhere, as the model writes words of its own into room
// noise.
const (
	// FRAMES_A_SECOND is how finely a sound's loudness is read: 10 ms frames.
	FRAMES_A_SECOND = 100
	// PIECE_MOST_FRAMES is the longest piece cut from a sound, 8 seconds,
	// which the half second a piece may be made up with keeps under the
	// model's 9.2.
	PIECE_MOST_FRAMES = 8 * FRAMES_A_SECOND
	// PIECE_LEAST_FRAMES is the shortest piece cut from a longer sound.
	PIECE_LEAST_FRAMES = FRAMES_A_SECOND
	// QUIET_SPAN_FRAMES is how long a stretch the quietest place to cut is
	// judged over, 200 ms: a pause between words or phrases, not a dip
	// inside a word.
	QUIET_SPAN_FRAMES = 20
	// LOUD_RMS is the loudness of a frame that holds speech, -40 dB of full
	// scale: above a room's hum, below a voice.
	LOUD_RMS = 0.01
	// EDGE_FRAMES is the quiet kept before a piece's first loud part and
	// after its last, half a second.
	EDGE_FRAMES = FRAMES_A_SECOND / 2
)

// pieces cuts a sound of samples at rate samples a second into the pieces
// the model is handed, in order.
func pieces(samples []float32, rate int) [][]float32 {
	if len(samples) == 0 {
		return nil
	}
	frame := max(1, rate/FRAMES_A_SECOND)
	power := make([]float64, (len(samples)+frame-1)/frame)
	isAnyLoud := false
	for index := range power {
		part := samples[index*frame : min((index+1)*frame, len(samples))]
		for _, sample := range part {
			power[index] += float64(sample) * float64(sample)
		}
		power[index] /= float64(len(part))
		isAnyLoud = isAnyLoud || isLoud(power[index])
	}
	cuts := cutPoints(power)
	var cut [][]float32
	for index := range len(cuts) - 1 {
		from, to := cuts[index], cuts[index+1]
		piece := samples[from*frame : min(to*frame, len(samples))]
		if !isAnyLoud {
			cut = append(cut, piece)
			continue
		}
		if shaped := shape(piece, power[from:to], frame); shaped != nil {
			cut = append(cut, shaped)
		}
	}
	return cut
}

func isLoud(power float64) bool {
	return math.Sqrt(power) >= LOUD_RMS
}

// cutPoints returns where a sound of frames of power is cut, as frame
// numbers from 0 to its end: each piece is at most PIECE_MOST_FRAMES long and
// is cut at the latest place between PIECE_LEAST_FRAMES and that length
// whose QUIET_SPAN_FRAMES around it are within twice the quietest there, so
// a cut falls in a pause rather than a word, and pieces stay long.
func cutPoints(power []float64) []int {
	sums := make([]float64, len(power)+1)
	for index, value := range power {
		sums[index+1] = sums[index] + value
	}
	quiet := func(at int) float64 {
		from, to := max(0, at-QUIET_SPAN_FRAMES/2), min(len(power), at+QUIET_SPAN_FRAMES/2)
		return (sums[to] - sums[from]) / float64(to-from)
	}
	cuts := []int{0}
	for start := 0; len(power)-start > PIECE_MOST_FRAMES; {
		low, high := start+PIECE_LEAST_FRAMES, start+PIECE_MOST_FRAMES
		quietest := math.Inf(1)
		for at := low; at <= high; at++ {
			quietest = min(quietest, quiet(at))
		}
		at := high
		for quiet(at) > 2*quietest {
			at--
		}
		cuts = append(cuts, at)
		start = at
	}
	return append(cuts, len(power))
}

// shape gives a piece EDGE_FRAMES of quiet before its first loud frame and
// after its last, cutting a longer quiet edge and making up a shorter end
// with silence, or nil when nothing in it is loud.
func shape(piece []float32, power []float64, frame int) []float32 {
	first, last := -1, -1
	for index, value := range power {
		if isLoud(value) {
			if first < 0 {
				first = index
			}
			last = index
		}
	}
	if first < 0 {
		return nil
	}
	begin := max(0, first-EDGE_FRAMES) * frame
	end := (last + 1 + EDGE_FRAMES) * frame
	if end <= len(piece) {
		return piece[begin:end]
	}
	shaped := make([]float32, end-begin)
	copy(shaped, piece[begin:])
	return shaped
}
