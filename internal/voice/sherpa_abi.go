package voice

// These follow c-api.h of sherpa-onnx v1.13.8 (commit 11afbd00), field for
// field: the engine reads the whole configuration, so every model's settings
// are declared although dictation fills in only the transducer's. Another
// engine version is checked against its own c-api.h before it is pinned.
type sherpaFeatureConfig struct {
	SampleRate int32
	FeatureDim int32
}

type sherpaHomophoneReplacerConfig struct {
	DictDir  *byte
	Lexicon  *byte
	RuleFsts *byte
}

type sherpaOfflineTransducerModelConfig struct {
	Encoder *byte
	Decoder *byte
	Joiner  *byte
}

type sherpaOfflineParaformerModelConfig struct {
	Model *byte
}

type sherpaOfflineNemoEncDecCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineWhisperModelConfig struct {
	Encoder                 *byte
	Decoder                 *byte
	Language                *byte
	Task                    *byte
	TailPaddings            int32
	EnableTokenTimestamps   int32
	EnableSegmentTimestamps int32
}

type sherpaOfflineCanaryModelConfig struct {
	Encoder *byte
	Decoder *byte
	SrcLang *byte
	TgtLang *byte
	UsePnc  int32
}

type sherpaOfflineCohereTranscribeModelConfig struct {
	Encoder  *byte
	Decoder  *byte
	Language *byte
	UsePunct int32
	UseItn   int32
}

type sherpaOfflineFireRedAsrModelConfig struct {
	Encoder *byte
	Decoder *byte
}

type sherpaOfflineFireRedAsrCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineMoonshineModelConfig struct {
	Preprocessor    *byte
	Encoder         *byte
	UncachedDecoder *byte
	CachedDecoder   *byte
	MergedDecoder   *byte
}

type sherpaOfflineTdnnModelConfig struct {
	Model *byte
}

type sherpaOfflineLMConfig struct {
	Model *byte
	Scale float32
}

type sherpaOfflineSenseVoiceModelConfig struct {
	Model    *byte
	Language *byte
	UseItn   int32
}

type sherpaOfflineDolphinModelConfig struct {
	Model *byte
}

type sherpaOfflineZipformerCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineWenetCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineOmnilingualAsrCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineFunASRNanoModelConfig struct {
	EncoderAdaptor *byte
	Llm            *byte
	Embedding      *byte
	Tokenizer      *byte
	SystemPrompt   *byte
	UserPrompt     *byte
	MaxNewTokens   int32
	Temperature    float32
	TopP           float32
	Seed           int32
	Language       *byte
	Itn            int32
	Hotwords       *byte
}

type sherpaOfflineQwen3ASRModelConfig struct {
	ConvFrontend *byte
	Encoder      *byte
	Decoder      *byte
	Tokenizer    *byte
	MaxTotalLen  int32
	MaxNewTokens int32
	Temperature  float32
	TopP         float32
	Seed         int32
	Hotwords     *byte
}

type sherpaOfflineMedAsrCtcModelConfig struct {
	Model *byte
}

type sherpaOfflineModelConfig struct {
	Transducer       sherpaOfflineTransducerModelConfig
	Paraformer       sherpaOfflineParaformerModelConfig
	NemoCtc          sherpaOfflineNemoEncDecCtcModelConfig
	Whisper          sherpaOfflineWhisperModelConfig
	Tdnn             sherpaOfflineTdnnModelConfig
	Tokens           *byte
	NumThreads       int32
	Debug            int32
	Provider         *byte
	ModelType        *byte
	ModelingUnit     *byte
	BpeVocab         *byte
	TelespeechCtc    *byte
	SenseVoice       sherpaOfflineSenseVoiceModelConfig
	Moonshine        sherpaOfflineMoonshineModelConfig
	FireRedAsr       sherpaOfflineFireRedAsrModelConfig
	Dolphin          sherpaOfflineDolphinModelConfig
	ZipformerCtc     sherpaOfflineZipformerCtcModelConfig
	Canary           sherpaOfflineCanaryModelConfig
	WenetCtc         sherpaOfflineWenetCtcModelConfig
	Omnilingual      sherpaOfflineOmnilingualAsrCtcModelConfig
	Medasr           sherpaOfflineMedAsrCtcModelConfig
	FunasrNano       sherpaOfflineFunASRNanoModelConfig
	FireRedAsrCtc    sherpaOfflineFireRedAsrCtcModelConfig
	Qwen3Asr         sherpaOfflineQwen3ASRModelConfig
	CohereTranscribe sherpaOfflineCohereTranscribeModelConfig
}

type sherpaOfflineRecognizerConfig struct {
	FeatConfig     sherpaFeatureConfig
	ModelConfig    sherpaOfflineModelConfig
	LmConfig       sherpaOfflineLMConfig
	DecodingMethod *byte
	MaxActivePaths int32
	HotwordsFile   *byte
	HotwordsScore  float32
	RuleFsts       *byte
	RuleFars       *byte
	BlankPenalty   float32
	Hr             sherpaHomophoneReplacerConfig
}

type sherpaWave struct {
	Samples    uintptr
	SampleRate int32
	NumSamples int32
}
