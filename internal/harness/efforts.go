package harness

func Efforts(kind Kind) []string {
	switch kind {
	case Claude, Codex, Pi:
		return []string{"low", "medium", "high", "xhigh", "max"}
	default:
		return nil
	}
}
