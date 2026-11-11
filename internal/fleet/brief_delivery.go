package fleet

import "strings"

// BriefDelivery is what a brief's Delivery section names on lines of their
// own, such as "mode: direct-PR": each name with the first value it is given,
// read as ValidateBriefReadiness reads that section. A line under any other
// heading names nothing, so task text that quotes one sets no launch choice.
func BriefDelivery(content []byte) map[string]string {
	named := map[string]string{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(string(content)), "\r\n", "\n"), "\n") {
		if heading := levelTwoHeading.FindStringSubmatch(line); heading != nil {
			section = heading[1]
			continue
		}
		if section != "Delivery" {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if found && named[key] == "" && value != "" {
			named[key] = value
		}
	}
	return named
}
