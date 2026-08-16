package showcase

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"strings"
)

// RenderCSV renders CSV source as a scan-friendly table. The first record
// becomes the header row.
func RenderCSV(source []byte) (string, error) {
	reader := csv.NewReader(bytes.NewReader(source))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	var out strings.Builder
	out.WriteString(`<table class="csv">`)
	row := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("showcase: csv row %d: %w", row+1, err)
		}
		if row == 0 {
			out.WriteString("<thead><tr>")
			for _, field := range record {
				out.WriteString("<th>")
				out.WriteString(html.EscapeString(field))
				out.WriteString("</th>")
			}
			out.WriteString("</tr></thead><tbody>")
		} else {
			out.WriteString("<tr>")
			for _, field := range record {
				out.WriteString("<td>")
				out.WriteString(html.EscapeString(field))
				out.WriteString("</td>")
			}
			out.WriteString("</tr>")
		}
		row++
	}
	if row > 0 {
		out.WriteString("</tbody>")
	}
	out.WriteString("</table>")
	if row == 0 {
		return "", fmt.Errorf("showcase: csv is empty")
	}
	return out.String(), nil
}
