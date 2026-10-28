package voice

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Moonshine tiny, the pinned model until 14 October 2026, answers nothing at
// all for a sound of 9.3 seconds or more (its decoder takes at most 384 of its encoder's
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

// leveled is a stretch of a test sound at a level: speech whose RMS is
// decibels of full scale, or, at 0, the room it is said in.
type leveled struct {
	decibels float64
	seconds  float64
}

// spokenAt builds a 16 kHz sound of stretches at their levels, in a quiet
// room whose hiss is at room decibels. It returns where each stretch of
// speech lies.
func spokenAt(room float64, parts ...leveled) (sound []float32, speech [][2]int) {
	random := rand.New(rand.NewPCG(20261014, 1))
	hissAmplitude := math.Sqrt(3) * math.Pow(10, room/20)
	phase := 0.0
	for _, part := range parts {
		count := int(part.seconds * testRate)
		if part.decibels != 0 {
			speech = append(speech, [2]int{len(sound), len(sound) + count})
		}
		amplitude := math.Sqrt2 * math.Pow(10, part.decibels/20)
		for index := range count {
			sample := (random.Float64()*2 - 1) * hissAmplitude
			if part.decibels != 0 {
				pitch := 160 + 90*math.Sin(float64(len(sound)+index)/testRate*1.7)
				phase += 2 * math.Pi * pitch / testRate
				sample += amplitude * math.Sin(phase)
			}
			sound = append(sound, float32(sample))
		}
	}
	return sound, speech
}

// A quiet microphone, or a word said softly, is speech all the same: on real
// recordings turned down by 25 dB, a microphone set low, the board lost 691
// words of 2252 that the model hears when handed the same sound whole,
// because only a part at -40 dB of full scale counted as speech. Every part
// of the speech reaches the model, and each piece still ends half a second
// after it.
func TestSpeechSofterThanFortyDecibelsReachesTheModel(t *testing.T) {
	for _, test := range []struct {
		name  string
		room  float64
		parts []leveled
	}{
		{"a quiet microphone whose loudest syllable just reaches -40 dB", -80, []leveled{{0, 0.3}, {-38, 0.2}, {-50, 2.8}, {0, 0.6}, {-52, 3}, {0, 0.7}, {-50, 3.5}, {0, 1.5}}},
		{"a soft last word after a loud phrase", -65, []leveled{{0, 0.3}, {-15, 3}, {-48, 0.9}, {0, 1.5}}},
		{"a soft first word before a loud phrase", -65, []leveled{{0, 1.2}, {-47, 0.8}, {-15, 3}, {0, 1}}},
		{"a long quiet dictation with one loud word", -78, []leveled{{0, 0.5}, {-48, 6}, {0, 0.6}, {-30, 0.4}, {-50, 7}, {0, 0.8}, {-49, 6}, {0, 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sound, speech := spokenAt(test.room, test.parts...)
			api, _ := recognizeSound(t, sound)
			covered := make([]bool, len(sound))
			for index, piece := range api.accepted {
				at, length := within(sound, piece, 0)
				if at < 0 {
					t.Fatalf("piece %d is not a stretch of the sound", index+1)
				}
				last := -1
				for _, run := range speech {
					if run[0] < at+length && at < run[1] {
						last = min(run[1], at+length) - at
					}
				}
				if after := float64(len(piece)-last) / testRate; last < 0 || after < 0.49 || after > 0.51 {
					t.Errorf("piece %d of %d goes on %.3f s after its last speech, want 0.5 s", index+1, len(api.accepted), after)
				}
				for sample := at; sample < at+length; sample++ {
					covered[sample] = true
				}
			}
			for _, run := range speech {
				for sample := run[0]; sample < run[1]; sample++ {
					if !covered[sample] {
						t.Fatalf("the speech at %.2f s was in no piece", float64(sample)/testRate)
					}
				}
			}
		})
	}
}

// Room noise is never speech, however loud the room or quiet the speaker:
// steady hiss alone, between two phrases said 20 dB over it, is left out.
func TestRoomNoiseAloneIsLeftOutAtAnyLevel(t *testing.T) {
	for _, room := range []float64{-75, -62, -50} {
		t.Run(fmt.Sprintf("a room at %.0f dB", room), func(t *testing.T) {
			sound, speech := spokenAt(room, leveled{room + 20, 3}, leveled{0, 12}, leveled{room + 20, 3})
			api, _ := recognizeSound(t, sound)
			for index, piece := range api.accepted {
				at, length := within(sound, piece, 0)
				heard := false
				for _, run := range speech {
					heard = heard || run[0] < at+length && at < run[1]
				}
				if !heard {
					t.Errorf("piece %d, %.2f s to %.2f s, is room noise alone", index+1, float64(at)/testRate, float64(at+length)/testRate)
				}
			}
		})
	}
}

// heldSilent builds a hold of seconds from a microphone that hears nothing:
// noise of a few of the 16-bit samples' steps, at -87 dB, as from a muted or
// unplugged input, and, when click is not 0, the 10 ms click of the keys let
// go at click decibels 0.1 s before its end. Two holds the board reported as
// speech the engine found no words in, issue #1456, were such sounds: the
// one kept had its loudest 10 ms, the click, at -70 dB, and the model heard
// nothing in it even made 63 dB louder.
func heldSilent(seconds, click float64) []float32 {
	random := rand.New(rand.NewPCG(20261027, 1456))
	sound := make([]float32, int(seconds*testRate))
	for index := range sound {
		sound[index] = float32(random.NormFloat64() * math.Pow(10, -87.0/20))
	}
	if click != 0 {
		at := len(sound) - testRate/10
		for index := range testRate / FRAMES_A_SECOND {
			sound[at+index] += float32(math.Sqrt2 * math.Pow(10, click/20) * math.Sin(float64(index)*0.9))
		}
	}
	return sound
}

// A hold whose microphone heard nothing but the keys holds no speech, so
// the board says nothing was heard rather than that the engine found no words
// in speech, and it is handed to the model whole, as it is.
func TestAHoldThatHeardOnlyTheKeysHoldsNoSpeech(t *testing.T) {
	for _, test := range []struct {
		name  string
		sound []float32
	}{
		{"a 3.6 s hold from a muted microphone, ended by the keys' click", heldSilent(3.6, -68)},
		{"a 2.0 s hold from a muted microphone, ended by the keys' click", heldSilent(2.0, -68)},
		{"a hold of a muted microphone alone", heldSilent(3.6, 0)},
		{"a click as loud as speech, alone in a hold", heldSilent(2.0, -20)},
		{"a hold of digital silence", make([]float32, 2*testRate)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isSpoken := Spoken(wavOf(test.sound, testRate, 1, 16))
			api, _ := recognizeSound(t, test.sound)

			// Assert
			if isSpoken {
				t.Error("Spoken = true for a hold with no speech in it")
			}
			if len(api.accepted) != 1 || len(api.accepted[0]) != len(test.sound) {
				t.Errorf("the model was handed %d pieces, not the hold whole", len(api.accepted))
			}
		})
	}
}

// A short line is speech however softly it is said, down to the -50 dB the
// pinned model still hears it at, and so is a single word of a tenth of a
// second; a sound loud for less is a click, not a word.
func TestAShortLineOrWordIsSpeech(t *testing.T) {
	line, err := os.ReadFile(filepath.Join("testdata", "open-the-pull-request.wav"))
	if err != nil {
		t.Fatal(err)
	}
	samples, rate := pcm16(line)
	softened := func(decibels float64) []byte {
		sound := make([]float32, len(samples))
		for index, sample := range samples {
			sound[index] = sample * float32(math.Pow(10, decibels/20))
		}
		return wavOf(sound, rate, 1, 16)
	}
	word := func(seconds float64) []byte {
		sound, _ := spokenAt(-80, leveled{0, 0.5}, leveled{-50, seconds}, leveled{0, 0.5})
		return wavOf(sound, testRate, 1, 16)
	}
	for _, test := range []struct {
		name  string
		sound []byte
		want  bool
	}{
		{"a 2.1 s line", line, true},
		{"the line 40 dB softer", softened(-40), true},
		{"the line 50 dB softer", softened(-50), true},
		{"a soft word of 0.1 s", word(0.1), true},
		{"a soft sound of 0.09 s", word(0.09), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Spoken(test.sound); got != test.want {
				t.Fatalf("Spoken = %v, want %v", got, test.want)
			}
		})
	}
}

// A piece whose only loud part is a click, in a dictation with speech
// elsewhere, is left out, as the model writes words of its own into it.
func TestAPieceWithOnlyAClickInItIsLeftOut(t *testing.T) {
	// Arrange
	sound, loud := spoken(soundPart{true, 3}, soundPart{false, 12})
	for index := range testRate / FRAMES_A_SECOND {
		sound[len(sound)-testRate+index] = float32(0.3 * math.Sin(float64(index)*0.9))
	}

	// Act
	api, _ := recognizeSound(t, sound)

	// Assert
	for index, piece := range api.accepted {
		at, length := within(sound, piece, 0)
		if at < 0 || at >= loud[0][1] || at+length <= loud[0][0] {
			t.Errorf("piece %d of %d holds no speech", index+1, len(api.accepted))
		}
	}
}

// wavOf writes sound as a WAV file of channels at rate with bits a sample,
// each channel the same sound, as the board's page writes one at 16 kHz,
// mono and 16 bits.
func wavOf(sound []float32, rate, channels, bits int) []byte {
	data := make([]byte, 0, len(sound)*channels*bits/8)
	for _, sample := range sound {
		for range channels {
			if bits == 8 {
				data = append(data, byte(128+int(math.Round(float64(sample)*127))))
				continue
			}
			data = binary.LittleEndian.AppendUint16(data, uint16(int16(math.Round(float64(sample)*32767))))
		}
	}
	header := []byte("RIFF")
	header = binary.LittleEndian.AppendUint32(header, uint32(36+len(data)))
	header = append(header, "WAVEfmt "...)
	header = binary.LittleEndian.AppendUint32(header, 16)
	header = binary.LittleEndian.AppendUint16(header, 1)
	header = binary.LittleEndian.AppendUint16(header, uint16(channels))
	header = binary.LittleEndian.AppendUint32(header, uint32(rate))
	header = binary.LittleEndian.AppendUint32(header, uint32(rate*channels*bits/8))
	header = binary.LittleEndian.AppendUint16(header, uint16(channels*bits/8))
	header = binary.LittleEndian.AppendUint16(header, uint16(bits))
	header = append(header, "data"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(len(data)))
	return append(header, data...)
}

// A WAV the board's page sent holds speech by the rule that cuts a sound
// into pieces: anything loud in it.
func TestSpokenJudgesTheBoardsSoundAsThePiecesDo(t *testing.T) {
	speech, _ := spoken(soundPart{false, 0.3}, soundPart{true, 0.4}, soundPart{false, 0.3})
	room, _ := spoken(soundPart{false, 1})
	soft, _ := spokenAt(-62, leveled{0, 0.3}, leveled{-45, 0.4}, leveled{0, 0.3})
	for _, test := range []struct {
		name  string
		sound []byte
		want  bool
	}{
		{"a second with a word in it", wavOf(speech, testRate, 1, 16), true},
		{"a word under -40 dB in a quiet room", wavOf(soft, testRate, 1, 16), true},
		{"a quiet room alone", wavOf(room, testRate, 1, 16), false},
		{"a word at another rate", wavOf(speech, 48000, 1, 16), true},
		{"a word in two channels", wavOf(speech, testRate, 2, 16), false},
		{"a word in 8-bit samples", wavOf(speech, testRate, 1, 8), false},
		{"no WAV at all", []byte("RIFF-sound"), false},
		{"a WAV with no samples", wavOf(nil, testRate, 1, 16), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Spoken(test.sound); got != test.want {
				t.Fatalf("Spoken = %v, want %v", got, test.want)
			}
		})
	}
}
