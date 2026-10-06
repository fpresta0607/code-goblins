package voice

import (
	"reflect"
	"testing"
)

// These x64 declarations follow c-api.h at 11afbd009a7f8c08f4bcf2fc1b265d0df4670fbf.
// They do not establish compatibility with an uninspected DLL.
func TestSherpaDeclarationsMatchThePinnedX64Header(t *testing.T) {
	type field struct {
		name   string
		offset uintptr
		size   uintptr
	}
	layouts := []struct {
		declaration reflect.Type
		size        uintptr
		alignment   int
		fields      []field
	}{
		{reflect.TypeOf(sherpaFeatureConfig{}), 8, 4, []field{
			{"SampleRate", 0, 4},
			{"FeatureDim", 4, 4},
		}},
		{reflect.TypeOf(sherpaHomophoneReplacerConfig{}), 24, 8, []field{
			{"DictDir", 0, 8},
			{"Lexicon", 8, 8},
			{"RuleFsts", 16, 8},
		}},
		{reflect.TypeOf(sherpaOfflineCanaryModelConfig{}), 40, 8, []field{
			{"Encoder", 0, 8},
			{"Decoder", 8, 8},
			{"SrcLang", 16, 8},
			{"TgtLang", 24, 8},
			{"UsePnc", 32, 4},
		}},
		{reflect.TypeOf(sherpaOfflineCohereTranscribeModelConfig{}), 32, 8, []field{
			{"Encoder", 0, 8},
			{"Decoder", 8, 8},
			{"Language", 16, 8},
			{"UsePunct", 24, 4},
			{"UseItn", 28, 4},
		}},
		{reflect.TypeOf(sherpaOfflineDolphinModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineFireRedAsrCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineFireRedAsrModelConfig{}), 16, 8, []field{
			{"Encoder", 0, 8},
			{"Decoder", 8, 8},
		}},
		{reflect.TypeOf(sherpaOfflineFunASRNanoModelConfig{}), 88, 8, []field{
			{"EncoderAdaptor", 0, 8},
			{"Llm", 8, 8},
			{"Embedding", 16, 8},
			{"Tokenizer", 24, 8},
			{"SystemPrompt", 32, 8},
			{"UserPrompt", 40, 8},
			{"MaxNewTokens", 48, 4},
			{"Temperature", 52, 4},
			{"TopP", 56, 4},
			{"Seed", 60, 4},
			{"Language", 64, 8},
			{"Itn", 72, 4},
			{"Hotwords", 80, 8},
		}},
		{reflect.TypeOf(sherpaOfflineLMConfig{}), 16, 8, []field{
			{"Model", 0, 8},
			{"Scale", 8, 4},
		}},
		{reflect.TypeOf(sherpaOfflineMedAsrCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineModelConfig{}), 504, 8, []field{
			{"Transducer", 0, 24},
			{"Paraformer", 24, 8},
			{"NemoCtc", 32, 8},
			{"Whisper", 40, 48},
			{"Tdnn", 88, 8},
			{"Tokens", 96, 8},
			{"NumThreads", 104, 4},
			{"Debug", 108, 4},
			{"Provider", 112, 8},
			{"ModelType", 120, 8},
			{"ModelingUnit", 128, 8},
			{"BpeVocab", 136, 8},
			{"TelespeechCtc", 144, 8},
			{"SenseVoice", 152, 24},
			{"Moonshine", 176, 40},
			{"FireRedAsr", 216, 16},
			{"Dolphin", 232, 8},
			{"ZipformerCtc", 240, 8},
			{"Canary", 248, 40},
			{"WenetCtc", 288, 8},
			{"Omnilingual", 296, 8},
			{"Medasr", 304, 8},
			{"FunasrNano", 312, 88},
			{"FireRedAsrCtc", 400, 8},
			{"Qwen3Asr", 408, 64},
			{"CohereTranscribe", 472, 32},
		}},
		{reflect.TypeOf(sherpaOfflineMoonshineModelConfig{}), 40, 8, []field{
			{"Preprocessor", 0, 8},
			{"Encoder", 8, 8},
			{"UncachedDecoder", 16, 8},
			{"CachedDecoder", 24, 8},
			{"MergedDecoder", 32, 8},
		}},
		{reflect.TypeOf(sherpaOfflineNemoEncDecCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineOmnilingualAsrCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineParaformerModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineQwen3ASRModelConfig{}), 64, 8, []field{
			{"ConvFrontend", 0, 8},
			{"Encoder", 8, 8},
			{"Decoder", 16, 8},
			{"Tokenizer", 24, 8},
			{"MaxTotalLen", 32, 4},
			{"MaxNewTokens", 36, 4},
			{"Temperature", 40, 4},
			{"TopP", 44, 4},
			{"Seed", 48, 4},
			{"Hotwords", 56, 8},
		}},
		{reflect.TypeOf(sherpaOfflineRecognizerConfig{}), 608, 8, []field{
			{"FeatConfig", 0, 8},
			{"ModelConfig", 8, 504},
			{"LmConfig", 512, 16},
			{"DecodingMethod", 528, 8},
			{"MaxActivePaths", 536, 4},
			{"HotwordsFile", 544, 8},
			{"HotwordsScore", 552, 4},
			{"RuleFsts", 560, 8},
			{"RuleFars", 568, 8},
			{"BlankPenalty", 576, 4},
			{"Hr", 584, 24},
		}},
		{reflect.TypeOf(sherpaOfflineSenseVoiceModelConfig{}), 24, 8, []field{
			{"Model", 0, 8},
			{"Language", 8, 8},
			{"UseItn", 16, 4},
		}},
		{reflect.TypeOf(sherpaOfflineTdnnModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineTransducerModelConfig{}), 24, 8, []field{
			{"Encoder", 0, 8},
			{"Decoder", 8, 8},
			{"Joiner", 16, 8},
		}},
		{reflect.TypeOf(sherpaOfflineWenetCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaOfflineWhisperModelConfig{}), 48, 8, []field{
			{"Encoder", 0, 8},
			{"Decoder", 8, 8},
			{"Language", 16, 8},
			{"Task", 24, 8},
			{"TailPaddings", 32, 4},
			{"EnableTokenTimestamps", 36, 4},
			{"EnableSegmentTimestamps", 40, 4},
		}},
		{reflect.TypeOf(sherpaOfflineZipformerCtcModelConfig{}), 8, 8, []field{
			{"Model", 0, 8},
		}},
		{reflect.TypeOf(sherpaWave{}), 16, 8, []field{
			{"Samples", 0, 8},
			{"SampleRate", 8, 4},
			{"NumSamples", 12, 4},
		}},
	}
	for _, layout := range layouts {
		if layout.declaration.Size() != layout.size || layout.declaration.Align() != layout.alignment || layout.declaration.NumField() != len(layout.fields) {
			t.Fatalf("%s changed its pinned x64 size, alignment or field count", layout.declaration.Name())
		}
		for index, expected := range layout.fields {
			field := layout.declaration.Field(index)
			if field.Name != expected.name || field.Offset != expected.offset || field.Type.Size() != expected.size {
				t.Fatalf("%s.%s has offset %d and size %d, want %s at %d with size %d", layout.declaration.Name(), field.Name, field.Offset, field.Type.Size(), expected.name, expected.offset, expected.size)
			}
		}
	}
}
