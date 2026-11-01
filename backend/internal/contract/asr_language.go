package contract

// NormalizeASRLanguage preserves the legacy Chinese default. The small explicit
// set is the product contract, not a claim of sentence-level language detection.
func NormalizeASRLanguage(value string) (string, error) {
	switch value {
	case "", "zh":
		return "zh", nil
	case "en", "auto":
		return value, nil
	default:
		return "", InputInvalid("转写语言无效，请选择中文、英语或自动检测")
	}
}
