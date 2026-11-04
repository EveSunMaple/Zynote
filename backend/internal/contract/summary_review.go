package contract

func SummaryReviewLabel(state string) string {
	switch state {
	case "supported":
		return "【抽样复核支持】"
	case "unsupported":
		return "【复核不支持，仅供核对】"
	case "uncertain":
		return "【复核不确定，仅供核对】"
	case "not_reviewed":
		return "【未复核，仅供核对】"
	case "omitted":
		return "【整理省略，证据保留】"
	}
	return ""
}
