package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A card's message is read once from its file: To and Subject lines, a blank
// line, then the body, which his Copy email copies exactly.
func TestReadMailTakesItsHeadersAndExactBody(t *testing.T) {
	for name, c := range map[string]struct {
		file string
		want Mail
	}{
		"an email with its headers": {
			file: "\ufeffTo: support@iproyal.com\r\nSubject: Refund for order 4412\r\n\r\nHello,\r\n\r\nPlease refund order 4412.\r\n\r\nThanks,\r\nNick\r\n",
			want: Mail{To: "support@iproyal.com", Subject: "Refund for order 4412", Body: "Hello,\n\nPlease refund order 4412.\n\nThanks,\nNick"},
		},
		"a web form's text": {
			file: "to: https://iproyal.com/contact\n\nPlease refund order 4412.\n",
			want: Mail{To: "https://iproyal.com/contact", Body: "Please refund order 4412."},
		},
		"a body alone": {
			file: "Dear support,\n\nSubject to your terms, please refund order 4412.\n",
			want: Mail{Body: "Dear support,\n\nSubject to your terms, please refund order 4412."},
		},
		"an assignment judged by its value": {
			file: "Subject: Plan change\n\nPlease set plan=monthly on my account.\n",
			want: Mail{Subject: "Plan change", Body: "Please set plan=monthly on my account."},
		},
		"headers after blank lines, with spaces on the blank line": {
			file: "\n\nTo: support@iproyal.com\nSubject: Refund\n   \nPlease refund order 4412.\n",
			want: Mail{To: "support@iproyal.com", Subject: "Refund", Body: "Please refund order 4412."},
		},
		"an order's page in the body": {
			file: "Subject: Refund\n\nThe order is https://www.iproyal.com/en/orders/4412 and I want it refunded.\n",
			want: Mail{Subject: "Refund", Body: "The order is https://www.iproyal.com/en/orders/4412 and I want it refunded."},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "message.txt")
			if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			mail, err := ReadMail(path)

			// Assert
			if err != nil || mail == nil || *mail != c.want {
				t.Fatalf("ReadMail = %+v, %v; want %+v", mail, err, c.want)
			}
		})
	}
}

// A message holding a key, going anywhere but an address or a plain page, or
// with no body is refused, and the refusal never repeats the key.
func TestReadMailRefusesWithoutRepeatingAKey(t *testing.T) {
	key := "sk" + "_live_" + "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for name, c := range map[string]struct {
		file string
		says string
	}{
		"a key in the body":           {"Subject: Refund\n\nMy key is " + key + "\n", "word 4 of the message's body looks like a credential value"},
		"a key assigned in the body":  {"Please use STRIPE_KEY=" + key + " for it.\n", "word 4 of the message's body"},
		"a key in the subject":        {"Subject: " + key + "\n\nPlease refund it.\n", "word 1 of the message's subject"},
		"a page with a query":         {"To: https://iproyal.com/contact?token=abc\n\nPlease refund it.\n", "an email address or an https page"},
		"a plain http page":           {"To: http://iproyal.com/contact\n\nPlease refund it.\n", "an email address or an https page"},
		"no body":                     {"To: support@iproyal.com\nSubject: Refund\n\n  \n", "a message's body is text"},
		"two recipients":              {"To: a@iproyal.com\nTo: b@iproyal.com\n\nPlease refund it.\n", "gives to: twice"},
		"a body past its bound":       {strings.Repeat("Please refund it. ", 300), "at most 4000 characters"},
		"a control character in text": {"Please refund\x07 it.\n", "a message's body is text"},
		"a copy to someone else":      {"To: a@iproyal.com\nCc: b@iproyal.com\n\nPlease refund it.\n", "only To: and Subject: are read"},
		"a key in a page's path":      {"Subject: Refund\n\nSee https://example.com/keys/" + key + " for it.\n", "word 2 of the message's body looks like a credential value"},
		"a key in a page's query":     {"Subject: Refund\n\nSee https://example.com/keys?id=" + key + "&x=1 for it.\n", "looks like a credential value"},
		"a key as the recipient":      {"To: " + key + "@example.com\n\nPlease refund it.\n", "word 1 of the message's recipient"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "message.txt")
			if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := ReadMail(path)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("ReadMail = %v, want a refusal naming %q", err, c.says)
			}
			if strings.Contains(err.Error(), key) {
				t.Fatal("the refusal repeats the key")
			}
		})
	}
}
