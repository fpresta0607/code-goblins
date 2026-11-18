package pagecheck

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestACheckRefusesALinkThatIsNoScrawlSessionBeforeStartingAnyChrome(t *testing.T) {
	for _, link := range []string{"https://127.0.0.1:4387/session/k7", "http://127.0.0.1:4387/artifact/k7/index.html", "http://127.0.0.1:4387/session/", "not a link"} {
		t.Run(link, func(t *testing.T) {
			// Arrange
			out := filepath.Join(t.TempDir(), "check")

			// Act
			_, err := Check(context.Background(), "no-chrome-is-started.exe", link, out)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "is not a Scrawl session link") {
				t.Fatalf("err = %v, want the link refused", err)
			}
		})
	}
}
