package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// liveVoice is the engine and the model config/voice.json pins, downloaded
// once, about 51 MB, into the folder VOICE_LIVE_DIR names, where a second run
// finds them. The live tests are the ones here that use the network, so they
// run only when asked.
func liveVoice(t *testing.T) *Voice {
	t.Helper()
	dir := os.Getenv("VOICE_LIVE_DIR")
	if dir == "" {
		t.Skip("set VOICE_LIVE_DIR to a folder to download the pinned engine and model into")
	}
	settings, err := Load(filepath.Join("..", "..", "config", "voice.json"))
	if err != nil {
		t.Fatal(err)
	}
	voice := &Voice{Settings: settings, Dir: dir, Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil }}
	var arrived, pinned int64
	if err := voice.Fetch(context.Background(), func(done, total int64) { arrived, pinned = done, total }); err != nil {
		t.Fatal(err)
	}
	if arrived != pinned {
		t.Fatalf("the fetch ended at %d of the %d pinned bytes", arrived, pinned)
	}
	t.Logf("%s %s on %s %s in %s; downloaded now: %d bytes", settings.Model.Name, settings.Model.Version, settings.Engine.Name, settings.Engine.Version, dir, arrived)
	t.Cleanup(voice.Close)
	return voice
}

// The engine recognises a spoken line.
func TestThePinnedEngineRecognisesASpokenLine(t *testing.T) {
	voice := liveVoice(t)
	sound, err := os.ReadFile(filepath.Join("testdata", "open-the-pull-request.wav"))
	if err != nil {
		t.Fatal(err)
	}
	// The first dictation loads the engine and the second finds it loaded.
	var loaded *worker
	for _, dictation := range []string{"first", "second"} {
		started := time.Now()
		text, err := voice.Recognize(context.Background(), sound)
		if err != nil {
			t.Fatal(err)
		}
		words := strings.Join(regexp.MustCompile(`[a-z]+`).FindAllString(strings.ToLower(text), -1), " ")
		if words != "open the pull request" {
			t.Fatalf("heard %q", text)
		}
		t.Logf("%s dictation heard %q in %s", dictation, text, time.Since(started).Round(time.Millisecond))
		if loaded == nil {
			loaded = current(t, voice)
		} else if current(t, voice) != loaded {
			t.Fatal("the second dictation loaded the engine again")
		}
	}
}

// A long dictation is heard in full, every time: sentences said one after
// another for 15 seconds to 3 minutes, with the pauses a speaker leaves
// between phrases and to think, over a room's faint hiss, come back with
// every sentence in its place, nothing said twice and few words wrong. The
// pinned model answered nothing at all for each of them, as for every sound
// of 9.3 seconds or more. The sentences are synthesized voices
// (testdata/long-dictation, from data/cg-voice-long/bench/make-fixtures.py).
func TestThePinnedEngineHearsALongDictationInFull(t *testing.T) {
	voice := liveVoice(t)
	sentences := longDictationSentences(t)
	for _, seconds := range []int{15, 30, 60, 120, 180} {
		t.Run(fmt.Sprintf("%d seconds", seconds), func(t *testing.T) {
			sound, said := longDictation(sentences, seconds)
			if run := repeatedRun(words(strings.Join(said, " "))); run >= 4 {
				t.Fatalf("what is said repeats a run of %d words, so a run heard twice would prove nothing", run)
			}
			started := time.Now()
			text, err := voice.Recognize(context.Background(), sound)
			if err != nil {
				t.Fatal(err)
			}
			took := time.Since(started)
			score := scoreWords(said, text)
			t.Logf("%d s of speech, %d sentences: %d of %d words right, %d dropped, %d added, %.1f%% wrong, in %s", seconds, len(said), score.right, score.words, score.dropped, score.added, 100*score.wrong(), took.Round(time.Millisecond))
			for index, sentence := range said {
				if heard := score.sentenceHeard[index]; heard < 0.6 {
					t.Errorf("sentence %d, %q, was heard only %.0f%%", index+1, sentence, 100*heard)
				}
			}
			if float64(score.dropped) > 0.03*float64(score.words) || float64(score.added) > 0.02*float64(score.words) {
				t.Errorf("%d words were dropped and %d added, of %d", score.dropped, score.added, score.words)
			}
			if run := repeatedRun(words(text)); run >= 4 {
				t.Errorf("a run of %d words was heard twice in a row, which nothing said repeats", run)
			}
			if score.wrong() > 0.12 {
				t.Errorf("%.1f%% of the words were wrong", 100*score.wrong())
			}
			if t.Failed() {
				t.Logf("heard: %s", text)
			}
		})
	}
}

type spokenSentence struct {
	text    string
	samples []int16
}

func longDictationSentences(t *testing.T) []spokenSentence {
	t.Helper()
	folder := filepath.Join("testdata", "long-dictation")
	data, err := os.ReadFile(filepath.Join(folder, "sentences.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var sentences []spokenSentence
	for index, text := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n") {
		sound, err := os.ReadFile(filepath.Join(folder, fmt.Sprintf("%02d.wav", index+1)))
		if err != nil {
			t.Fatal(err)
		}
		samples, err := wavSamples(sound)
		if err != nil {
			t.Fatalf("%02d.wav: %v", index+1, err)
		}
		sentences = append(sentences, spokenSentence{text, samples})
	}
	return sentences
}

// wavSamples reads a 16 kHz mono 16-bit PCM WAV file's samples.
func wavSamples(sound []byte) ([]int16, error) {
	if len(sound) < 12 || string(sound[:4]) != "RIFF" || string(sound[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAV file")
	}
	var format []byte
	for at := 12; at+8 <= len(sound); {
		name, size := string(sound[at:at+4]), int(binary.LittleEndian.Uint32(sound[at+4:at+8]))
		body := sound[at+8 : min(len(sound), at+8+size)]
		switch name {
		case "fmt ":
			format = body
		case "data":
			if len(format) < 16 || binary.LittleEndian.Uint16(format[0:]) != 1 || binary.LittleEndian.Uint16(format[2:]) != 1 || binary.LittleEndian.Uint32(format[4:]) != testRate || binary.LittleEndian.Uint16(format[14:]) != 16 {
				return nil, fmt.Errorf("not 16 kHz mono 16-bit PCM")
			}
			samples := make([]int16, len(body)/2)
			return samples, binary.Read(bytes.NewReader(body[:len(samples)*2]), binary.LittleEndian, samples)
		}
		at += 8 + size + size%2
	}
	return nil, fmt.Errorf("no data")
}

// longDictation says every sentence once, in an order of its own each round,
// until at least seconds have passed, with a pause after each from a cycle of
// short gaps and the long ones a speaker leaves to think, and a room's hiss
// at -56 dB under all of it. It returns the WAV and the sentences said, in
// order.
func longDictation(sentences []spokenSentence, seconds int) ([]byte, []string) {
	pauses := []float64{0.5, 0.9, 0.4, 2.5, 0.7, 1.2, 0.5, 3.0, 0.6, 1.5}
	random := rand.New(rand.NewPCG(uint64(seconds), 20261008))
	var samples []int16
	var said []string
	var order []int
	for len(samples) < seconds*testRate {
		if len(order) == 0 {
			order = random.Perm(len(sentences))
			if len(said) > 0 && sentences[order[0]].text == said[len(said)-1] {
				order[0], order[1] = order[1], order[0]
			}
		}
		if len(said) > 0 {
			samples = append(samples, make([]int16, int(pauses[(len(said)-1)%len(pauses)]*testRate))...)
		}
		sentence := sentences[order[0]]
		order = order[1:]
		samples = append(samples, sentence.samples...)
		said = append(said, sentence.text)
	}
	var sound bytes.Buffer
	sound.WriteString("RIFF")
	_ = binary.Write(&sound, binary.LittleEndian, uint32(36+2*len(samples)))
	sound.WriteString("WAVEfmt ")
	for _, field := range []any{uint32(16), uint16(1), uint16(1), uint32(testRate), uint32(2 * testRate), uint16(2), uint16(16)} {
		_ = binary.Write(&sound, binary.LittleEndian, field)
	}
	sound.WriteString("data")
	_ = binary.Write(&sound, binary.LittleEndian, uint32(2*len(samples)))
	for _, sample := range samples {
		value := float64(sample) + random.NormFloat64()*0.0015*32768
		_ = binary.Write(&sound, binary.LittleEndian, int16(max(math.MinInt16, min(math.MaxInt16, math.Round(value)))))
	}
	return sound.Bytes(), said
}

func words(text string) []string {
	return regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(strings.ReplaceAll(text, "'", "")), -1)
}

// wordScore is how the words heard line up with those said: how many were
// right, said but not heard, and heard but not said, and for each sentence
// the share of its words heard in their place.
type wordScore struct {
	words, right, substituted, dropped, added int
	sentenceHeard                             []float64
}

func (s wordScore) wrong() float64 {
	return float64(s.substituted+s.dropped+s.added) / float64(s.words)
}

func scoreWords(said []string, heard string) wordScore {
	var want []string
	var sentenceOf []int
	for index, sentence := range said {
		for _, word := range words(sentence) {
			want = append(want, word)
			sentenceOf = append(sentenceOf, index)
		}
	}
	got := words(heard)
	cost := make([][]int, len(want)+1)
	for i := range cost {
		cost[i] = make([]int, len(got)+1)
		cost[i][0] = i
	}
	for j := range cost[0] {
		cost[0][j] = j
	}
	differ := func(i, j int) int {
		if want[i-1] == got[j-1] {
			return 0
		}
		return 1
	}
	for i := 1; i <= len(want); i++ {
		for j := 1; j <= len(got); j++ {
			cost[i][j] = min(cost[i-1][j]+1, cost[i][j-1]+1, cost[i-1][j-1]+differ(i, j))
		}
	}
	score := wordScore{words: len(want), sentenceHeard: make([]float64, len(said))}
	counts := make([]int, len(said))
	for i, j := len(want), len(got); i > 0 || j > 0; {
		switch {
		case i > 0 && j > 0 && cost[i][j] == cost[i-1][j-1]+differ(i, j):
			if differ(i, j) == 0 {
				score.right++
				score.sentenceHeard[sentenceOf[i-1]]++
			} else {
				score.substituted++
			}
			i, j = i-1, j-1
		case i > 0 && cost[i][j] == cost[i-1][j]+1:
			score.dropped++
			i--
		default:
			score.added++
			j--
		}
	}
	for _, index := range sentenceOf {
		counts[index]++
	}
	for index := range said {
		score.sentenceHeard[index] /= float64(max(1, counts[index]))
	}
	return score
}

// repeatedRun is the longest run of words heard again straight after itself.
func repeatedRun(heard []string) int {
	longest := 0
	for size := 1; 2*size <= len(heard); size++ {
		for at := 0; at+2*size <= len(heard); at++ {
			if slices.Equal(heard[at:at+size], heard[at+size:at+2*size]) {
				longest = size
				break
			}
		}
	}
	return longest
}
