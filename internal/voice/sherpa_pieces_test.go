package voice

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// The pinned model, Moonshine tiny, answers nothing at all for a sound of
// 9.3 seconds or more (its decoder takes at most 384 of its encoder's
// frames), which was every long dictation the Overlord made: "Nothing was
// heard." It also writes words of its own into a stretch of room noise with
// no speech in it. These tests hold the recognizer to handing the model only
// what it hears well.
const (
	testRate = 16000
	// modelMost is the longest sound the model was measured to answer.
	modelMost = 9 * testRate
)

// soundPart is a stretch of a test sound: speech when loud, else the faint
// hiss of a quiet room.
type soundPart struct {
	loud    bool
	seconds float64
}

// spoken builds a 16 kHz sound of parts. Speech is a tone whose pitch keeps
// moving, at -13 dB, and a room is a hiss at -59 dB, under the -40 dB a
// voice reaches; both carry noise, so no two stretches are alike and no
// sample of either is exactly zero.
func spoken(parts ...soundPart) (sound []float32, loud [][2]int) {
	random := rand.New(rand.NewPCG(20261008, 1))
	phase := 0.0
	for _, part := range parts {
		count := int(part.seconds * testRate)
		if part.loud {
			loud = append(loud, [2]int{len(sound), len(sound) + count})
		}
		for index := range count {
			hiss := float32(random.Float64()*0.004 - 0.002)
			if hiss == 0 {
				hiss = 0.001
			}
			if !part.loud {
				sound = append(sound, hiss)
				continue
			}
			pitch := 160 + 90*math.Sin(float64(len(sound)+index)/testRate*1.7)
			phase += 2 * math.Pi * pitch / testRate
			sound = append(sound, float32(0.3*math.Sin(phase))+hiss)
		}
	}
	return sound, loud
}

// within says where piece, less the silence padded onto its end, lies in
// sound at or after from: its start and how many samples of sound it holds,
// or -1 when it is no stretch of sound there.
func within(sound, piece []float32, from int) (at, length int) {
	length = len(piece)
	for length > 0 && piece[length-1] == 0 {
		length--
	}
	if length == 0 {
		return -1, 0
	}
	for at = from; at+length <= len(sound); at++ {
		if sound[at] != piece[0] {
			continue
		}
		same := true
		for index := 1; index < length && same; index++ {
			same = sound[at+index] == piece[index]
		}
		if same {
			return at, length
		}
	}
	return -1, 0
}

func recognizeSound(t *testing.T, sound []float32) (*fakeSherpaAPI, string) {
	t.Helper()
	options, err := ParseWorkerArguments(moonshineArguments(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	api := newFakeSherpaAPI(t, "")
	api.sound = sound
	for index := range 64 {
		api.heard = append(api.heard, fmt.Sprintf("words of piece %d.", index+1))
	}
	recognizer, err := newSherpaRecognizer(options, api)
	if err != nil {
		t.Fatal(err)
	}
	text, err := recognizer.Recognize([]byte("RIFF-sound"))
	if err != nil {
		t.Fatal(err)
	}
	return api, text
}

// A dictation of any length reaches the model in pieces it answers, in
// order, with every part of the speech in exactly one piece and no piece of
// room noise alone, and its words come back whole, in the order said.
func TestADictationOfAnyLengthReachesTheModelInPiecesItAnswersInOrder(t *testing.T) {
	phrases := func(count int, speech, pause float64) []soundPart {
		var parts []soundPart
		for range count {
			parts = append(parts, soundPart{true, speech}, soundPart{false, pause})
		}
		return parts
	}
	for _, test := range []struct {
		name  string
		parts []soundPart
	}{
		{"a short line", []soundPart{{false, 0.3}, {true, 4}, {false, 1}}},
		{"15 seconds with pauses", phrases(4, 3.2, 0.6)},
		{"30 seconds with a long pause to think", append(phrases(3, 3, 0.5), append([]soundPart{{false, 3}}, phrases(4, 4, 0.7)...)...)},
		{"a long silence between two phrases", []soundPart{{true, 3}, {false, 12}, {true, 3}}},
		{"20 seconds with no pause at all", []soundPart{{true, 20}}},
		{"three minutes with pauses", phrases(36, 4.2, 0.8)},
		{"over four minutes with pauses", phrases(52, 4.4, 0.6)},
	} {
		t.Run(test.name, func(t *testing.T) {
			sound, loud := spoken(test.parts...)
			api, text := recognizeSound(t, sound)
			if len(api.accepted) == 0 {
				t.Fatal("the model was handed nothing")
			}
			covered := make([]bool, len(sound))
			end := 0
			for index, piece := range api.accepted {
				if len(piece) > modelMost {
					t.Errorf("piece %d of %d is %.2f s, and the model answers nothing past 9.2 s", index+1, len(api.accepted), float64(len(piece))/testRate)
				}
				at, length := within(sound, piece, end)
				if at < 0 {
					at, length = within(sound, piece, 0)
				}
				if at < 0 {
					t.Fatalf("piece %d is not a stretch of the sound", index+1)
				}
				if at < end {
					t.Errorf("piece %d starts at %.2f s, inside or before piece %d, which ends at %.2f s", index+1, float64(at)/testRate, index, float64(end)/testRate)
				}
				end = at + length
				heard := false
				for _, run := range loud {
					heard = heard || run[0] < at+length && at < run[1]
				}
				if !heard {
					t.Errorf("piece %d, %.2f s to %.2f s, is room noise alone, which the model writes words of its own into", index+1, float64(at)/testRate, float64(at+length)/testRate)
				}
				for sample := at; sample < at+length; sample++ {
					covered[sample] = true
				}
			}
			for _, run := range loud {
				for sample := run[0]; sample < run[1]; sample++ {
					if !covered[sample] {
						t.Fatalf("the speech at %.2f s was in no piece", float64(sample)/testRate)
					}
				}
			}
			if want := strings.Join(api.heard[:len(api.accepted)], " "); text != want {
				t.Errorf("the words came back as %q, want %q", text, want)
			}
		})
	}
}

// Each piece ends half a second after its last loud part: a longer quiet end
// is cut, since the model answers nothing for some, and a piece cut while
// loud, as when the keys are let go mid-word or a long stretch has no pause,
// is made up with silence, since the model writes on past an end it does not
// hear, repeating itself.
func TestEachPieceEndsHalfASecondAfterItsLastLoudPart(t *testing.T) {
	for _, test := range []struct {
		name  string
		parts []soundPart
	}{
		{"a line with a long quiet end", []soundPart{{false, 0.2}, {true, 3}, {false, 3}}},
		{"a line cut mid-word", []soundPart{{false, 0.2}, {true, 2}}},
		{"talking with no pause", []soundPart{{true, 25}}},
		{"phrases with pauses", []soundPart{{true, 5}, {false, 0.6}, {true, 5}, {false, 2}, {true, 4}, {false, 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sound, loud := spoken(test.parts...)
			api, _ := recognizeSound(t, sound)
			for index, piece := range api.accepted {
				at, length := within(sound, piece, 0)
				last := -1
				for _, run := range loud {
					if run[0] < at+length && at < run[1] {
						last = min(run[1], at+length) - at
					}
				}
				after := float64(len(piece)-last) / testRate
				if last < 0 || after < 0.49 || after > 0.51 {
					t.Errorf("piece %d of %d goes on %.3f s after its last loud part, want 0.5 s", index+1, len(api.accepted), after)
				}
				for _, sample := range piece[length:] {
					if sample != 0 {
						t.Fatalf("piece %d is made up with something other than silence", index+1)
					}
				}
			}
		})
	}
}

// A sound with nothing loud in it, as from a quiet microphone, is handed to
// the model as it is.
func TestASoundWithNothingLoudInItIsHandedAsItIs(t *testing.T) {
	sound, _ := spoken(soundPart{false, 2})
	api, _ := recognizeSound(t, sound)
	if len(api.accepted) != 1 || len(api.accepted[0]) != len(sound) {
		t.Fatalf("the model was handed %d pieces for one quiet sound", len(api.accepted))
	}
}
