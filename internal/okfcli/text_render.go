package okfcli

import "strconv"

// renderTextString is the only renderer for user/document-controlled strings
// in deterministic CLI text projections. QuoteToGraphic preserves readable
// Unicode while escaping line breaks, C0 controls, quotes, and backslashes.
func renderTextString(value string) string {
	return strconv.QuoteToGraphic(value)
}

func renderTextContent(value string) string {
	quoted := renderTextString(value)
	return quoted[1 : len(quoted)-1]
}

func renderOptionalTextString(value string) string {
	if value == "" {
		return "(absent)"
	}
	return renderTextString(value)
}
