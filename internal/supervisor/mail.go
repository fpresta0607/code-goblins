package supervisor

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	// maxMailBody is the longest body a card's message carries, the bound of
	// a question's own text, and maxMailSubject the longest subject.
	maxMailBody    = 4000
	maxMailSubject = 200
	// maxMailFile bounds the message file read: its To and Subject lines, a
	// blank line and the body.
	maxMailFile = 8 << 10
)

// mailAddress is an email address as a card's message names where it goes.
var mailAddress = regexp.MustCompile(`^[^\s@<>"(),;:]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$`)

// Mail is a message a card gives the Overlord to send himself, such as an
// email (the Overlord, 2026-10-17: "copy email body or copy email that I
// could then send. Instead of having to go to like this file"): where it
// goes, an email address or an https page, its subject, and its body, which
// his card copies with one click. It is read once from the asker's file when
// the card is filed, and never carries anything shaped like a key.
type Mail struct {
	To      string `json:"to,omitempty"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// ReadMail reads a message file once: optional To: and Subject: lines, a
// blank line, then the body. A file whose first lines are not those headers
// is all body. It refuses what mailProblem refuses.
func ReadMail(path string) (*Mail, error) {
	f, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMailFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMailFile {
		return nil, fmt.Errorf("the message file is over %d KiB", maxMailFile>>10)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("the message file is not UTF-8 text")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	mail := Mail{}
	if head, body, found := strings.Cut(text, "\n\n"); found {
		headers := map[string]string{}
		for _, line := range strings.Split(head, "\n") {
			key, value, isHeader := strings.Cut(line, ":")
			key = strings.ToLower(strings.TrimSpace(key))
			if !isHeader || key != "to" && key != "subject" {
				headers = nil
				break
			}
			if _, twice := headers[key]; twice {
				return nil, fmt.Errorf("the message file gives %s: twice", key)
			}
			headers[key] = strings.TrimSpace(value)
		}
		if headers != nil {
			mail.To, mail.Subject, text = headers["to"], headers["subject"], body
		}
	}
	mail.Body = strings.TrimSpace(text)
	return &mail, mailProblem(&mail)
}

// mailProblem refuses a message without a body, one longer than its bounds,
// one that goes anywhere but an email address or a plain https page, and one
// holding a word shaped like a key. A word is split at an equals sign, so an
// assignment is judged by its value, and no refusal repeats what it refused.
func mailProblem(m *Mail) error {
	if m == nil {
		return nil
	}
	switch {
	case strings.TrimSpace(m.Body) == "" || len(m.Body) > maxMailBody || !utf8.ValidString(m.Body) || strings.ContainsFunc(m.Body, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }):
		return fmt.Errorf("a message's body is text of at most %d characters", maxMailBody)
	case len(m.Subject) > maxMailSubject || !utf8.ValidString(m.Subject) || strings.ContainsFunc(m.Subject, unicode.IsControl):
		return fmt.Errorf("a message's subject is one line of at most %d characters", maxMailSubject)
	case m.To != "" && !(len(m.To) <= 254 && mailAddress.MatchString(m.To)) && !(strings.HasPrefix(m.To, "https://") && validCredentialLink(m.To) == nil):
		return errors.New("a message goes to an email address or an https page with no credentials, query or fragment")
	}
	for _, part := range []struct{ name, text string }{{"subject", m.Subject}, {"body", m.Body}} {
		words := strings.FieldsFunc(part.text, func(r rune) bool { return unicode.IsSpace(r) || r == '=' })
		for index, word := range words {
			if shape := auth.SecretShape(word); shape != "" {
				return fmt.Errorf("word %d of the message's %s looks like a credential value: %s; a card never carries one", index+1, part.name, shape)
			}
		}
	}
	return nil
}

// sameMail reports whether two cards carry the same message, or neither does.
func sameMail(a, b *Mail) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
